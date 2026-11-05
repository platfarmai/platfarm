package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"platfarm/svc-vision/internal/config"
	"platfarm/svc-vision/internal/models"
	"github.com/sirupsen/logrus"
)

type Manager struct {
	cfg       *config.Config
	alertCh   chan *models.Alert
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	templates map[string]string
}

type WebhookPayload struct {
	Alert     *models.Alert `json:"alert"`
	Camera    *CameraInfo   `json:"camera,omitempty"`
	Timestamp string        `json:"timestamp"`
}

type CameraInfo struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`
}

func NewManager(cfg *config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		cfg:     cfg,
		alertCh: make(chan *models.Alert, 1000),
		ctx:     ctx,
		cancel:  cancel,
		templates: map[string]string{
			"motion":        "🚨 Motion detection alert",
			"intrusion":     "🚨 Zone intrusion alert",
			"line_cross":    "🚨 Line crossing alert",
			"object_detect": "🚨 Object detection alert",
			"offline":       "⚠️ Camera offline",
			"storage_full":  "💾 Insufficient storage",
			"error":         "❌ System error",
		},
	}

	return m
}

func (m *Manager) Start() error {
	if !m.cfg.Alert.Enabled {
		logrus.Info("alert push disabled, skipping start")
		return nil
	}

	m.wg.Add(1)
	go m.processLoop()

	logrus.Info("alert manager started")
	return nil
}

func (m *Manager) Stop() error {
	m.cancel()
	m.wg.Wait()
	logrus.Info("alert manager stopped")
	return nil
}

func (m *Manager) OnAlert(alert *models.Alert) {
	select {
	case m.alertCh <- alert:
	default:
		logrus.Warn("alert queue is full, dropping alert")
	}
}

func (m *Manager) processLoop() {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return
		case alert := <-m.alertCh:
			m.sendAlert(alert)
		}
	}
}

func (m *Manager) sendAlert(alert *models.Alert) {
	var wg sync.WaitGroup

	if m.cfg.Alert.Channels.Webhook.Enabled && m.cfg.Alert.Channels.Webhook.URL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.sendWebhook(alert, &m.cfg.Alert.Channels.Webhook); err != nil {
				logrus.Errorf("Webhook push failed: %v", err)
			}
		}()
	}

	if m.cfg.Alert.Channels.Email.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.sendEmail(alert, &m.cfg.Alert.Channels.Email); err != nil {
				logrus.Errorf("email push failed: %v", err)
			}
		}()
	}

	if m.cfg.Alert.Channels.SMS.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.sendSMS(alert, &m.cfg.Alert.Channels.SMS); err != nil {
				logrus.Errorf("SMS push failed: %v", err)
			}
		}()
	}

	wg.Wait()

}

func (m *Manager) sendWebhook(alert *models.Alert, wh *config.WebhookAlertConfig) error {
	if wh.URL == "" {
		return fmt.Errorf("Webhook URL not configured")
	}
	var data []byte
	var err error

	if strings.EqualFold(wh.Type, "gotify") {
		data, err = m.buildGotifyPayload(alert)
	} else {

		payload := WebhookPayload{
			Alert:     alert,
			Timestamp: time.Now().Format(time.RFC3339),
		}
		data, err = json.Marshal(payload)
	}
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", wh.URL, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned error status code: %d", resp.StatusCode)
	}

	return nil
}

type gotifyMessage struct {
	Title    string `json:"title,omitempty"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
}

func (m *Manager) buildGotifyPayload(alert *models.Alert) ([]byte, error) {
	title := m.templates[alert.Type]
	if title == "" {
		title = "🚨 Surveillance alert"
	}

	msg := strings.TrimSpace(alert.Message)
	if msg == "" {
		msg = fmt.Sprintf("camera %d triggered a %s alert", alert.CameraID, alert.Type)
	}

	detail := fmt.Sprintf("\nTime: %s", time.Now().Format("2006-01-02 15:04:05"))
	if alert.CameraID > 0 {
		detail = fmt.Sprintf("\nCamera ID: %d%s", alert.CameraID, detail)
	}

	body := gotifyMessage{
		Title:    title,
		Message:  msg + detail,
		Priority: levelToGotifyPriority(alert.Level),
	}
	return json.Marshal(body)
}

func levelToGotifyPriority(level string) int {
	switch level {
	case models.AlertLevelLow:
		return 1
	case models.AlertLevelMedium:
		return 3
	case models.AlertLevelHigh:
		return 5
	case models.AlertLevelCritical:
		return 8
	default:
		return 3
	}
}

func (m *Manager) sendEmail(alert *models.Alert, em *config.EmailAlertConfig) error {
	cfg := em
	if len(cfg.To) == 0 {
		return fmt.Errorf("recipients not configured")
	}

	subject := fmt.Sprintf("[Surveillance Alert] %s - %s", m.templates[alert.Type], alert.Message)
	body := m.formatEmailBody(alert)

	auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort)

	msg := []byte(fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\n"+
		"\r\n"+
		"%s", cfg.From, strings.Join(cfg.To, ","), subject, body))

	return smtp.SendMail(addr, auth, cfg.From, cfg.To, msg)
}

func (m *Manager) formatEmailBody(alert *models.Alert) string {
	levelColor := map[string]string{
		models.AlertLevelLow:      "#28a745",
		models.AlertLevelMedium:   "#ffc107",
		models.AlertLevelHigh:     "#fd7e14",
		models.AlertLevelCritical: "#dc3545",
	}

	color := levelColor[alert.Level]

	return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <div style="background: %s; color: white; padding: 20px; border-radius: 8px 8px 0 0;">
            <h2 style="margin: 0;">%s</h2>
        </div>
        <div style="background: #f8f9fa; padding: 20px; border-radius: 0 0 8px 8px; border: 1px solid #dee2e6; border-top: none;">
            <table style="width: 100%%; border-collapse: collapse;">
                <tr>
                    <td style="padding: 8px 0; font-weight: bold; width: 120px;">Alert type:</td>
                    <td style="padding: 8px 0;">%s</td>
                </tr>
                <tr>
                    <td style="padding: 8px 0; font-weight: bold;">Alert level:</td>
                    <td style="padding: 8px 0;"><span style="background: %s; color: white; padding: 2px 8px; border-radius: 4px;">%s</span></td>
                </tr>
                <tr>
                    <td style="padding: 8px 0; font-weight: bold;">Camera ID:</td>
                    <td style="padding: 8px 0;">%d</td>
                </tr>
                <tr>
                    <td style="padding: 8px 0; font-weight: bold;">Alert time:</td>
                    <td style="padding: 8px 0;">%s</td>
                </tr>
                <tr>
                    <td style="padding: 8px 0; font-weight: bold;">Details:</td>
                    <td style="padding: 8px 0;">%s</td>
                </tr>
            </table>
            %s
        </div>
    </div>
</body>
</html>
`, color, m.templates[alert.Type], alert.Type, color, strings.ToUpper(alert.Level), alert.CameraID, alert.CreatedAt.Format("2006-01-02 15:04:05"), alert.Message, m.getSnapshotHTML(alert))
}

func (m *Manager) getSnapshotHTML(alert *models.Alert) string {
	if alert.SnapshotPath != "" {
		return fmt.Sprintf(`<div style="margin-top: 20px;">
            <p style="font-weight: bold;">Scene snapshot:</p>
            <img src="cid:snapshot" alt="alert snapshot" style="max-width: 100%%; border: 1px solid #dee2e6; border-radius: 4px;">
        </div>`)
	}
	return ""
}

func (m *Manager) sendSMS(alert *models.Alert, sm *config.SMSAlertConfig) error {
	cfg := sm

	logrus.Infof("SMS push (simulated): to %s, content: %s", cfg.SignName, alert.Message)

	return nil
}

func (m *Manager) SendTestAlert(channel string, override *config.AlertConfig) error {
	testAlert := &models.Alert{
		CameraID: 0,
		Type:     "test",
		Level:    models.AlertLevelLow,
		Message:  "This is a test alert message",
		Details:  `{"test": true}`,
	}

	src := m.cfg.Alert
	if override != nil {
		src = *override
	}

	switch channel {
	case "webhook":
		return m.sendWebhook(testAlert, &src.Channels.Webhook)
	case "email":
		return m.sendEmail(testAlert, &src.Channels.Email)
	case "sms":
		return m.sendSMS(testAlert, &src.Channels.SMS)
	default:
		return fmt.Errorf("unknown channel: %s", channel)
	}
}

func (m *Manager) SendBatch(alerts []*models.Alert) error {
	if len(alerts) == 0 {
		return nil
	}

	_ = fmt.Sprintf("surveillance system alert summary (%d alerts)", len(alerts))
	body := "<h3>Alert details:</h3><ul>"
	for _, a := range alerts {
		body += fmt.Sprintf("<li>[%s] %s - %s</li>", a.Level, a.Type, a.Message)
	}
	body += "</ul>"

	return nil
}
