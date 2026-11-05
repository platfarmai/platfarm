package onvifevent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"platfarm/svc-vision/internal/config"
	"platfarm/svc-vision/internal/database"
	"platfarm/svc-vision/internal/models"
	"platfarm/svc-vision/pkg/onvif"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Manager struct {
	cfg    *config.Config
	db     *gorm.DB
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	onAlert func(*models.Alert)

	mu         sync.Mutex
	lastRecord map[uint]map[string]time.Time
}

func NewManager(cfg *config.Config, onAlert func(*models.Alert)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg:        cfg,
		db:         database.GetDB(),
		ctx:        ctx,
		cancel:     cancel,
		onAlert:    onAlert,
		lastRecord: make(map[uint]map[string]time.Time),
	}
}

func (m *Manager) pollInterval() int {
	if m.cfg.Camera.OnvifEvent.PollInterval > 0 {
		return m.cfg.Camera.OnvifEvent.PollInterval
	}
	return 10
}

func (m *Manager) Start() error {
	if !m.cfg.Camera.OnvifEvent.Enabled {
		logrus.Info("ONVIF event subscription disabled, skipping start")
		return nil
	}

	var cams []models.Camera
	if err := m.db.Where("protocol = ? AND deleted_at IS NULL", "onvif").Find(&cams).Error; err != nil {
		return fmt.Errorf("failed to query ONVIF camera: %w", err)
	}

	if len(cams) == 0 {
		logrus.Info("no ONVIF protocol cameras found, skipping event subscription")
		return nil
	}

	for _, cam := range cams {
		if cam.OnvifAddress == "" {
			logrus.Warnf("camera %s has no ONVIF address configured, skipping event subscription", cam.Name)
			continue
		}
		cam := cam
		m.wg.Add(1)
		go m.subscribeLoop(cam)
	}
	logrus.Infof("ONVIF event subscription started, %d cameras in total", len(cams))
	return nil
}

func (m *Manager) Stop() error {
	m.cancel()
	m.wg.Wait()
	logrus.Info("ONVIF event subscription manager stopped")
	return nil
}

func (m *Manager) subscribeLoop(cam models.Camera) {
	defer m.wg.Done()

	client := onvif.NewClient(m.cfg.Camera.DiscoveryTimeout)
	if cam.Username != "" && cam.Password != "" {
		client.SetCredentials(cam.Username, cam.Password)
	}

	for {
		if m.ctx.Err() != nil {
			return
		}

		sub, err := client.CreatePullPointSubscription(cam.OnvifAddress)
		if err != nil {

			if eventsAddr := client.ResolveEventsXAddr(cam.OnvifAddress); eventsAddr != cam.OnvifAddress {
				sub, err = client.CreatePullPointSubscription(eventsAddr)
			}
		}
		if err != nil {
			logrus.Warnf("camera %s event subscription failed: %v, retrying in %d s", cam.Name, err, m.pollInterval())
			if !sleepCtx(m.ctx, time.Duration(m.pollInterval())*time.Second) {
				return
			}
			continue
		}

		logrus.Infof("camera %s event subscription successful: %s", cam.Name, sub.Address)

		active := m.pullLoop(client, cam, sub)

		_ = client.Unsubscribe(sub.Address)

		if !active {

			return
		}

		if !sleepCtx(m.ctx, time.Duration(m.pollInterval())*time.Second) {
			return
		}
	}
}

func (m *Manager) pullLoop(client *onvif.Client, cam models.Camera, sub *onvif.EventSubscription) bool {
	poll := m.pollInterval()

	var deadline time.Time
	if !sub.TerminationTime.IsZero() {
		deadline = sub.TerminationTime
	} else if m.cfg.Camera.OnvifEvent.SubscriptionTimeout > 0 {
		deadline = time.Now().Add(time.Duration(m.cfg.Camera.OnvifEvent.SubscriptionTimeout) * time.Minute)
	}

	for {
		if m.ctx.Err() != nil {
			return false
		}

		events, err := client.PullMessages(sub.Address, poll)
		if err != nil {
			if m.ctx.Err() != nil {
				return false
			}
			logrus.Warnf("camera %s failed to pull events: %v", cam.Name, err)
			return true
		}

		for _, ev := range events {
			m.handleEvent(cam, ev)
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			logrus.Infof("camera %s event subscription expired, resubscribing", cam.Name)
			return true
		}
	}
}

func (m *Manager) handleEvent(cam models.Camera, ev onvif.OnvifEvent) {
	alertType, level, isAlarm := mapTopicToAlert(ev.Topic)

	if !isAlarm {
		logrus.Debugf("ignoring non-alarm ONVIF event: Camera=%s topic=%s", cam.Name, ev.Topic)
		return
	}

	if m.duplicated(cam.ID, alertType) {
		return
	}

	msg := buildMessage(alertType, ev)
	details := buildDetails(ev)

	alert := &models.Alert{
		CameraID: cam.ID,
		Type:     alertType,
		Level:    level,
		Message:  msg,
		Details:  details,
		Status:   models.AlertStatusNew,
	}

	if err := m.db.Create(alert).Error; err != nil {
		logrus.Errorf("failed to save ONVIF alert camera=%d: %v", cam.ID, err)
		return
	}
	logrus.Infof("ONVIF alert: Camera=%s(%d), Type=%s, Level=%s, Topic=%s", cam.Name, cam.ID, alertType, level, ev.Topic)

	if m.onAlert != nil {
		m.onAlert(alert)
	}
}

func (m *Manager) duplicated(cameraID uint, alertType string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	byType, ok := m.lastRecord[cameraID]
	if !ok {
		byType = make(map[string]time.Time)
		m.lastRecord[cameraID] = byType
	}
	last, exists := byType[alertType]
	now := time.Now()
	if exists && now.Sub(last) < 30*time.Second {
		return true
	}
	byType[alertType] = now
	return false
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func mapTopicToAlert(topic string) (alertType string, level string, isAlarm bool) {
	t := strings.ToLower(topic)

	if strings.HasPrefix(t, "monitoring/") ||
		strings.Contains(t, "processorusage") ||
		strings.Contains(t, "memoryusage") ||
		strings.Contains(t, "device/io") ||
		strings.Contains(t, "systemlog") {
		return "", "", false
	}

	switch {
	case strings.Contains(t, "linecross"), strings.Contains(t, "linedetector"), strings.Contains(t, "crossed"):
		return models.AlertTypeLineCross, models.AlertLevelHigh, true
	case strings.Contains(t, "intrusion"), strings.Contains(t, "objectsinside"),
		strings.Contains(t, "fielddetector"), strings.Contains(t, "enter"), strings.Contains(t, "invasion"):
		return models.AlertTypeIntrusion, models.AlertLevelHigh, true
	case strings.Contains(t, "tamper"), strings.Contains(t, "spray"), strings.Contains(t, "covered"):
		return models.AlertTypeObjectDetect, models.AlertLevelHigh, true
	case strings.Contains(t, "motion"):

		return models.AlertTypeMotion, models.AlertLevelMedium, true
	case strings.Contains(t, "digitalinput"), strings.Contains(t, "trigger"):
		return models.AlertTypeIntrusion, models.AlertLevelMedium, true
	case strings.Contains(t, "disconnect"), strings.Contains(t, "removed"), strings.Contains(t, "networklost"):
		return models.AlertTypeOffline, models.AlertLevelCritical, true
	default:

		return "", "", false
	}
}

func buildMessage(alertType string, ev onvif.OnvifEvent) string {
	if ev.Topic != "" {
		return fmt.Sprintf("摄像头上报事件: %s", ev.Topic)
	}
	switch alertType {
	case models.AlertTypeMotion:
		return "摄像头上报：检测到移动"
	case models.AlertTypeLineCross:
		return "摄像头上报：越线报警"
	case models.AlertTypeIntrusion:
		return "摄像头上报：区域入侵报警"
	case models.AlertTypeObjectDetect:
		return "摄像头上报：设备破坏/遮挡报警"
	case models.AlertTypeOffline:
		return "摄像头上报：设备断开报警"
	default:
		return "摄像头上报报警事件"
	}
}

func buildDetails(ev onvif.OnvifEvent) string {
	if len(ev.Items) == 0 {
		return fmt.Sprintf(`{"topic": %q}`, ev.Topic)
	}
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, `{"topic": %q, "items": {`, ev.Topic)
	i := 0
	for k, v := range ev.Items {
		if i > 0 {
			sb.WriteByte(',')
		}
		_, _ = fmt.Fprintf(&sb, "%q: %q", k, v)
		i++
	}
	sb.WriteString(`}}`)
	return sb.String()
}
