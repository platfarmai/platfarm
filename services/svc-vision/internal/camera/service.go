package camera

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"platfarm/svc-vision/internal/config"
	"platfarm/svc-vision/internal/database"
	"platfarm/svc-vision/internal/models"
	"platfarm/svc-vision/internal/storage"
	"platfarm/svc-vision/pkg/ffmpeg"
	"platfarm/svc-vision/pkg/minio"
	"platfarm/svc-vision/pkg/onvif"
	"platfarm/svc-vision/pkg/webdav"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type CameraManager struct {
	cfg            *config.Config
	db             *gorm.DB
	cameras        map[uint]*CameraInstance
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	ffmpegMgr      *ffmpeg.Manager
	onvifClient    *onvif.Client
	runtimeStorage *storage.RuntimeStorage

	motionRecordMu   sync.Mutex
	motionRecordLast map[uint]time.Time

	snapMu       sync.RWMutex
	snapEnabled  bool
	snapInterval int
	snapChanged  chan struct{}
}

type CameraInstance struct {
	Model        *models.Camera
	Stream       *ffmpeg.Stream
	Preview      *ffmpeg.PreviewStream
	OnvifClient  *onvif.Client
	PTZSupported bool
	Status       string
	LastError    string
	ReconnectCnt int
	StopChan     chan struct{}
	loopDone     chan struct{}

	previewLastActive time.Time
	previewMu         sync.Mutex

	RecordRTSPURL  string
	PreviewRTSPURL string
	mu             sync.Mutex

	running bool
}

func NewCameraManager(cfg *config.Config, ffmpegMgr *ffmpeg.Manager) *CameraManager {
	ctx, cancel := context.WithCancel(context.Background())

	snapInterval := cfg.Camera.SnapshotInterval
	if snapInterval <= 0 {
		snapInterval = 300
	}

	return &CameraManager{
		cfg:              cfg,
		db:               database.GetDB(),
		cameras:          make(map[uint]*CameraInstance),
		ctx:              ctx,
		cancel:           cancel,
		ffmpegMgr:        ffmpegMgr,
		onvifClient:      onvif.NewClient(cfg.Camera.DiscoveryTimeout),
		snapEnabled:      cfg.Camera.SnapshotEnabled,
		snapInterval:     snapInterval,
		snapChanged:      make(chan struct{}, 1),
		motionRecordLast: make(map[uint]time.Time),
	}
}

func (m *CameraManager) SetRuntimeStorage(r *storage.RuntimeStorage) {
	m.runtimeStorage = r
}

func (m *CameraManager) GetSnapshotSettings() (enabled bool, interval int) {
	m.snapMu.RLock()
	defer m.snapMu.RUnlock()
	return m.snapEnabled, m.snapInterval
}

func (m *CameraManager) SetSnapshotSettings(enabled bool, interval int) {
	if interval <= 0 {
		interval = 300
	}
	m.snapMu.Lock()
	m.snapEnabled = enabled
	m.snapInterval = interval
	m.snapMu.Unlock()

	select {
	case m.snapChanged <- struct{}{}:
	default:
	}
}

func (m *CameraManager) localStorage() config.LocalStorageConfig {
	if m.runtimeStorage != nil {
		return m.runtimeStorage.GetLocal()
	}
	return m.cfg.Storage.Local
}

func (m *CameraManager) webdavConfig() config.WebdavConfig {
	if m.runtimeStorage != nil {
		return m.runtimeStorage.GetWebdav()
	}
	return m.cfg.Storage.Webdav
}

func (m *CameraManager) minioConfig() config.MinIOConfig {
	if m.runtimeStorage != nil {
		return m.runtimeStorage.GetMinIO()
	}
	return m.cfg.Storage.MinIO
}

func (m *CameraManager) Start() error {

	if err := m.loadCameras(); err != nil {
		return err
	}

	for id, inst := range m.cameras {
		if inst.Model.RecordEnabled {
			m.wg.Add(1)
			go m.runCamera(id)
		}
	}

	m.wg.Add(1)
	go m.healthCheckLoop()
	m.wg.Add(1)
	go m.snapshotLoop()
	m.wg.Add(1)
	go m.previewIdleLoop()

	logrus.Info("camera manager started")
	return nil
}

func (m *CameraManager) Stop() error {
	m.cancel()
	m.wg.Wait()

	m.mu.Lock()
	var streams []*ffmpeg.Stream
	for _, inst := range m.cameras {
		if s, p := inst.stop(); s != nil {
			streams = append(streams, s)
			if p != nil {
				p.Stop()
			}
		}
	}
	m.mu.Unlock()

	for _, s := range streams {
		s.Stop()
	}

	logrus.Info("camera manager stopped")
	return nil
}

func (m *CameraManager) loadCameras() error {
	var cameras []models.Camera
	if err := m.db.Where("deleted_at IS NULL").Find(&cameras).Error; err != nil {
		return err
	}

	for _, cam := range cameras {
		cam := cam
		m.cameras[cam.ID] = &CameraInstance{
			Model:    &cam,
			Status:   models.CameraStatusOffline,
			StopChan: make(chan struct{}),
			loopDone: make(chan struct{}),
		}
	}
	logrus.Infof("loaded %d cameras", len(cameras))
	return nil
}

func (m *CameraManager) runCamera(id uint) {
	defer m.wg.Done()

	inst := m.cameras[id]
	cam := inst.Model

	inst.mu.Lock()
	inst.running = true
	inst.mu.Unlock()
	defer func() {
		inst.mu.Lock()
		inst.running = false
		inst.mu.Unlock()
	}()

	loopDone := inst.loopDone
	defer func() {
		if loopDone != nil {
			close(loopDone)
		}
	}()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-inst.StopChan:
			return
		default:
			if err := m.connectCamera(inst); err != nil {
				m.updateCameraStatus(cam.ID, models.CameraStatusError, conciseStreamError(err))
				inst.ReconnectCnt++

				if inst.ReconnectCnt >= m.cfg.Camera.MaxReconnect {
					logrus.Errorf("camera %s reached max reconnect attempts, giving up", cam.Name)
					m.updateCameraStatus(cam.ID, models.CameraStatusError, conciseStreamError(err))
					return
				}

				logrus.Warnf("camera %s connection failed: %v, retrying in %d s (%d/%d)",
					cam.Name, err, m.cfg.Camera.ReconnectInterval, inst.ReconnectCnt, m.cfg.Camera.MaxReconnect)

				select {
				case <-m.ctx.Done():
					return
				case <-inst.StopChan:
					return
				case <-time.After(time.Duration(m.cfg.Camera.ReconnectInterval) * time.Second):
					continue
				}
			}

			inst.ReconnectCnt = 0
			m.updateCameraStatus(cam.ID, models.CameraStatusOnline, "")

			select {
			case <-m.ctx.Done():
				return
			case <-inst.StopChan:
				return
			case <-inst.streamDone():
				logrus.Infof("camera %s stream ended", cam.Name)
			}
		}
	}
}

func (inst *CameraInstance) streamDone() <-chan struct{} {
	if inst.Stream == nil {
		return nil
	}
	return inst.Stream.Done()
}

func (m *CameraManager) connectCamera(inst *CameraInstance) error {
	cam := inst.Model

	rtspURL := BuildRTSPURL(cam)

	if cam.Protocol == "onvif" && (cam.Path == "" || cam.Path == "/") {
		if cam.OnvifAddress != "" {
			logrus.Infof("camera %s attempting to obtain stream URL via ONVIF", cam.Name)

			client := onvif.NewClient(m.cfg.Camera.DiscoveryTimeout)
			if cam.Username != "" && cam.Password != "" {
				client.SetCredentials(cam.Username, cam.Password)
			}
			inst.OnvifClient = client

			if devInfo, derr := client.GetDeviceInfo(cam.OnvifAddress); derr == nil && devInfo != nil {
				updates := map[string]interface{}{}
				if devInfo.Manufacturer != "" {
					updates["manufacturer"] = devInfo.Manufacturer
				}
				if devInfo.Model != "" {
					updates["model"] = devInfo.Model
				}
				if devInfo.Firmware != "" {
					updates["firmware"] = devInfo.Firmware
				}
				if devInfo.SerialNumber != "" {
					updates["serial_number"] = devInfo.SerialNumber
				}
				if len(updates) > 0 {
					if res := m.db.Model(cam).Updates(updates); res.Error != nil {
						logrus.Warnf("camera %s failed to save ONVIF device info: %v", cam.Name, res.Error)
					} else {
						logrus.Infof("camera %s ONVIF device info: %s %s (firmware %s, serial %s)",
							cam.Name, devInfo.Manufacturer, devInfo.Model, devInfo.Firmware, devInfo.SerialNumber)
					}
				}
			} else if derr != nil {
				logrus.Debugf("camera %s failed to get ONVIF device info: %v", cam.Name, derr)
			}

			inst.PTZSupported = false
			if caps, cerr := client.GetCapabilities(cam.OnvifAddress); cerr == nil && caps != nil && caps.PTZXAddr != "" {
				inst.PTZSupported = true
				logrus.Infof("camera %s ONVIF supports PTZ service", cam.Name)
			} else {
				logrus.Debugf("camera %s ONVIF does not provide PTZ service: %v", cam.Name, cerr)
			}

			cacheValid := false
			if cam.DiscoveredStreamUri != "" && cam.StreamUriUpdatedAt != nil {
				if time.Since(*cam.StreamUriUpdatedAt) < 24*time.Hour && false {
					rtspURL = injectRTSPAuth(cam.DiscoveredStreamUri, cam.Username, cam.Password)
					cacheValid = true
					logrus.Infof("camera %s using cached stream URL: %s", cam.Name, rtspURL)
				}
			}

			if !cacheValid {

				logrus.Infof("camera %s cached stream URL invalid/expired/profile changed, rediscovering", cam.Name)

				mediaAddr := client.ResolveMediaXAddr(cam.OnvifAddress)

				profiles, err := client.GetProfiles(mediaAddr)
				if (err != nil || len(profiles) == 0) && mediaAddr != cam.OnvifAddress {

					profiles, err = client.GetProfiles(cam.OnvifAddress)
				}
				if err != nil {
					logrus.Warnf("camera %s failed to fetch ONVIF profiles: %v", cam.Name, err)
				} else if len(profiles) > 0 {
					logrus.Infof("camera %s found %d profiles", cam.Name, len(profiles))

					var selectedProfile onvif.Profile
					if cam.OnvifProfileToken != "" {

						found := false
						for _, p := range profiles {
							if p.Token == cam.OnvifProfileToken {
								selectedProfile = p
								found = true
								break
							}
						}
						if !found {
							logrus.Warnf("camera %s specified profile token %s not found, falling back to auto selection", cam.Name, cam.OnvifProfileToken)
						} else {
							logrus.Infof("camera %s using user-specified profile: %s", cam.Name, selectedProfile.Name)
						}
					}

					if selectedProfile.Token == "" {

						selectedProfile = selectBestProfile(profiles)
						logrus.Infof("camera %s auto-selected profile: %s (%dx%d)", cam.Name, selectedProfile.Name, selectedProfile.Width, selectedProfile.Height)
					}

					streamUri, usedProfile, err := client.GetStreamUriWithRetry(
						mediaAddr,
						profiles,
						selectedProfile.Token,
						onvif.TransportTCP,
						3,
					)
					if err != nil {
						logrus.Warnf("camera %s TCP fetch failed: %v, trying UDP", cam.Name, err)
						streamUri, usedProfile, err = client.GetStreamUriWithRetry(
							mediaAddr,
							profiles,
							selectedProfile.Token,
							onvif.TransportUDP,
							3,
						)
						if err != nil {
							logrus.Warnf("camera %s UDP also failed: %v, using constructed URL", cam.Name, err)
						}
					}

					if err == nil && streamUri != "" {
						rtspURL = injectRTSPAuth(streamUri, cam.Username, cam.Password)

						inst.RecordRTSPURL = rtspURL

						now := time.Now()
						cam.DiscoveredStreamUri = streamUri
						cam.StreamUriUpdatedAt = &now

						m.db.Model(cam).Updates(map[string]interface{}{
							"discovered_stream_uri": streamUri,
							"stream_uri_updated_at": now,
						})

						usedName := ""
						if usedProfile != nil {
							usedName = usedProfile.Name
						}
						logrus.Infof("camera %s ONVIF stream URL obtained (Profile: %s): %s, cached",
							cam.Name, usedName, rtspURL)

						if len(profiles) > 1 {
							subProfile := selectSubProfile(profiles)
							if subProfile.Token != "" && subProfile.Token != selectedProfile.Token {
								if subUri, _, serr := client.GetStreamUriWithRetry(
									mediaAddr, profiles, subProfile.Token, onvif.TransportTCP, 2,
								); serr == nil && subUri != "" {
									inst.PreviewRTSPURL = injectRTSPAuth(subUri, cam.Username, cam.Password)
									logrus.Infof("camera %s sub-stream preview URL (Profile: %s %dx%d): %s",
										cam.Name, subProfile.Name, subProfile.Width, subProfile.Height, inst.PreviewRTSPURL)
								}
							}
						}
					}
				}
			}
		} else {
			logrus.Warnf("camera %s has no ONVIF address configured, cannot auto-discover stream", cam.Name)
		}
	}

	logrus.Infof("connecting to camera %s: %s", cam.Name, rtspURL)

	if inst.RecordRTSPURL == "" {
		inst.RecordRTSPURL = rtspURL
	}
	if inst.PreviewRTSPURL == "" {
		inst.PreviewRTSPURL = rtspURL
	}

	motionMode := cam.RecordType == models.RecordTypeMotion

	if motionMode {
		inst.setError("")
		logrus.Infof("camera %s ready (event-based recording mode: preview on demand, recording only on motion trigger)", cam.Name)
		return nil
	}

	stream, err := m.ffmpegMgr.CreateStream(inst.RecordRTSPURL, ffmpeg.StreamOptions{
		CameraID:        cam.ID,
		SegmentDuration: m.localStorage().SegmentDuration,
		OutputDir:       m.getCameraStoragePath(cam.ID),
		OnSegment:       m.onSegmentComplete,
		// 拉流失败（流源不可达/中断）：把 error 状态持久化到 DB，列表状态实时显示"异常"
		OnError: func(err error) {
			m.updateCameraStatus(cam.ID, models.CameraStatusError, conciseStreamError(err))
		},
		// 新片段确认正常运行（真正连上流源）：恢复 online
		OnSegmentStart: func() {
			m.updateCameraStatus(cam.ID, models.CameraStatusOnline, "")
		},
		RecordOnly: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create recording stream: %w", err)
	}

	inst.Stream = stream
	inst.setError("")

	if err := stream.Start(); err != nil {
		return fmt.Errorf("failed to start recording stream: %w", err)
	}

	logrus.Infof("camera %s recording stream started (main stream -c copy, preview on demand, source stream per preview_stream config)", cam.Name)

	return nil
}

func (m *CameraManager) TriggerMotionRecording(cameraID uint) {
	m.mu.RLock()
	inst, ok := m.cameras[cameraID]
	m.mu.RUnlock()
	if !ok || inst == nil || inst.Model.RecordType != models.RecordTypeMotion {
		return
	}

	duration := m.cfg.Camera.MotionRecord.Duration
	if duration <= 0 {
		duration = 30
	}
	cooldown := m.cfg.Camera.MotionRecord.Cooldown
	if cooldown <= 0 {
		cooldown = 30
	}

	now := time.Now()
	m.motionRecordMu.Lock()
	if last, exists := m.motionRecordLast[cameraID]; exists && now.Sub(last) < time.Duration(cooldown)*time.Second {
		m.motionRecordMu.Unlock()
		logrus.Debugf("camera %s motion recording in cooldown, skipping trigger", inst.Model.Name)
		return
	}
	m.motionRecordLast[cameraID] = now
	m.motionRecordMu.Unlock()

	rtspURL := inst.RecordRTSPURL
	if rtspURL == "" {
		rtspURL = BuildRTSPURL(inst.Model)
	}

	go m.runMotionRecording(inst, rtspURL, duration)
}

func (m *CameraManager) runMotionRecording(inst *CameraInstance, rtspURL string, duration int) {
	cam := inst.Model
	outDir := m.getCameraStoragePath(cam.ID)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		logrus.Errorf("failed to create recording directory camera=%d: %v", cam.ID, err)
		return
	}

	startTime := time.Now()
	filename := fmt.Sprintf("motion_%s.mp4", startTime.Format("20060102_150405"))
	outPath := filepath.Join(outDir, filename)

	args := []string{
		"-y",
		"-rtsp_transport", "tcp",

		"-i", rtspURL,
		"-c", "copy",
		"-movflags", "+faststart",
		"-t", strconv.Itoa(duration),
		outPath,
	}

	logrus.Infof("camera %s motion detection triggered recording (%d s): %s", cam.Name, duration, outPath)

	cmd := exec.CommandContext(m.ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil && m.ctx.Err() == nil {
		logrus.Errorf("camera %s motion recording ffmpeg failed: %v (output: %s)", cam.Name, err, tailString(string(output), 300))
		return
	}

	fi, err := os.Stat(outPath)
	if err != nil {
		logrus.Errorf("camera %s motion recording file missing: %v", cam.Name, err)
		return
	}

	endTime := time.Now()
	recording := &models.Recording{
		CameraID:    cam.ID,
		StartTime:   startTime,
		EndTime:     endTime,
		Duration:    int(endTime.Sub(startTime).Seconds()),
		FilePath:    outPath,
		FileSize:    fi.Size(),
		RecordType:  models.RecordTypeMotion,
		Status:      "completed",
		StorageType: "local",
		StoragePath: outPath,
	}

	if err := m.db.Create(recording).Error; err != nil {
		logrus.Errorf("failed to save motion recording record camera=%d: %v", cam.ID, err)
		return
	}
	logrus.Infof("motion detection recording completed: Camera=%d, File=%s, Size=%d", cam.ID, outPath, fi.Size())

	if wd := m.webdavConfig(); wd.Enabled && wd.URL != "" {
		remoteRel := fmt.Sprintf("camera_%d/%s", cam.ID, filename)
		go m.uploadSegmentToWebdav(recording.ID, cam.ID, outPath, wd, remoteRel)
	}

	if mn := m.minioConfig(); mn.Enabled && mn.Endpoint != "" && mn.Bucket != "" {
		remoteRel := fmt.Sprintf("camera_%d/%s", cam.ID, filename)
		go m.uploadSegmentToMinio(recording.ID, cam.ID, outPath, mn, remoteRel)
	}
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func (m *CameraManager) onSegmentComplete(cameraID uint, segment *ffmpeg.SegmentInfo) {

	filePath := segment.FilePath
	timeStamp := segment.StartTime.Format("20060102_150405")
	newPath := filepath.Join(filepath.Dir(filePath), "segment_"+timeStamp+".mp4")
	if _, err := os.Stat(newPath); err == nil {

		for i := 1; i <= 999; i++ {
			cand := filepath.Join(filepath.Dir(filePath), fmt.Sprintf("segment_%s_%d.mp4", timeStamp, i))
			if _, err := os.Stat(cand); os.IsNotExist(err) {
				newPath = cand
				break
			}
			if i == 999 {
				newPath = filePath
			}
		}
	}
	if newPath != filePath {
		if err := os.Rename(filePath, newPath); err != nil {
			logrus.Warnf("failed to rename recording file (keeping original name): %v", err)
			newPath = filePath
		}
	}
	segment.FilePath = newPath

	recording := &models.Recording{
		CameraID:     cameraID,
		StartTime:    segment.StartTime,
		EndTime:      segment.EndTime,
		Duration:     int(segment.EndTime.Sub(segment.StartTime).Seconds()),
		FilePath:     segment.FilePath,
		FileSize:     segment.FileSize,
		SegmentIndex: segment.Index,
		RecordType:   models.RecordTypeContinuous,
		Status:       "completed",
		StorageType:  "local",
		StoragePath:  segment.FilePath,
		IndexPath:    segment.IndexPath,
	}

	if err := m.db.Create(recording).Error; err != nil {
		logrus.Errorf("failed to save recording record: %v", err)
		return
	}
	logrus.Infof("recording segment completed: Camera=%d, File=%s, Size=%d", cameraID, segment.FilePath, segment.FileSize)

	if wd := m.webdavConfig(); wd.Enabled && wd.URL != "" {
		remoteRel := fmt.Sprintf("camera_%d/%s", cameraID, filepath.Base(segment.FilePath))
		go m.uploadSegmentToWebdav(recording.ID, cameraID, segment.FilePath, wd, remoteRel)
	}

	if mn := m.minioConfig(); mn.Enabled && mn.Endpoint != "" && mn.Bucket != "" {
		remoteRel := fmt.Sprintf("camera_%d/%s", cameraID, filepath.Base(segment.FilePath))
		go m.uploadSegmentToMinio(recording.ID, cameraID, segment.FilePath, mn, remoteRel)
	}
}

func (m *CameraManager) uploadSegmentToWebdav(recordingID uint, cameraID uint, localPath string, wd config.WebdavConfig, remoteRel string) {
	client := webdav.NewClient(wd.URL, wd.Username, wd.Password)
	remotePath := wd.BasePath
	if remotePath != "" {
		remotePath += "/" + remoteRel
	} else {
		remotePath = remoteRel
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := client.Upload(localPath, remotePath); err != nil {
			lastErr = err
			logrus.Warnf("WebDAV upload retry %d/3: camera=%d file=%s err=%v", attempt, cameraID, filepath.Base(localPath), err)
			time.Sleep(time.Duration(attempt*10) * time.Second)
			continue
		}

		m.db.Model(&models.Recording{}).Where("id = ?", recordingID).
			Updates(map[string]any{"storage_path": remotePath, "storage_type": "webdav"})
		logrus.Infof("WebDAV upload succeeded: camera=%d -> %s", cameraID, remotePath)

		if wd.Only {
			if err := os.Remove(localPath); err == nil {
				logrus.Infof("WebDAV exclusive mode: local copy %s deleted", localPath)
			} else if !os.IsNotExist(err) {
				logrus.Warnf("WebDAV exclusive mode: failed to delete local copy %s: %v", localPath, err)
			}
		}
		return
	}
	logrus.Errorf("WebDAV upload failed (retried 3 times): camera=%d file=%s err=%v", cameraID, filepath.Base(localPath), lastErr)
}

func (m *CameraManager) uploadSegmentToMinio(recordingID uint, cameraID uint, localPath string, mn config.MinIOConfig, remoteRel string) {
	client, err := minio.NewClient(mn.Endpoint, mn.AccessKey, mn.SecretKey, mn.Bucket, mn.UseSSL)
	if err != nil {
		logrus.Errorf("MinIO client creation failed: camera=%d err=%v", cameraID, err)
		return
	}
	objectKey := mn.BasePath
	if objectKey != "" {
		objectKey = strings.Trim(objectKey, "/") + "/" + remoteRel
	} else {
		objectKey = remoteRel
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := client.Upload(localPath, objectKey); err != nil {
			lastErr = err
			logrus.Warnf("MinIO upload retry %d/3: camera=%d file=%s err=%v", attempt, cameraID, filepath.Base(localPath), err)
			time.Sleep(time.Duration(attempt*10) * time.Second)
			continue
		}

		res := m.db.Model(&models.Recording{}).
			Where("id = ? AND storage_path = ?", recordingID, localPath).
			Updates(map[string]any{"storage_path": objectKey, "storage_type": "minio"})
		if res.Error != nil {
			logrus.Warnf("MinIO storage_path write-back failed id=%d: %v", recordingID, res.Error)
		} else {
			logrus.Infof("MinIO upload succeeded: camera=%d -> %s/%s", cameraID, mn.Bucket, objectKey)
		}

		if mn.Only {
			wd := m.webdavConfig()
			if !(wd.Enabled && wd.Only) {
				if err := os.Remove(localPath); err == nil {
					logrus.Infof("MinIO exclusive mode: local copy %s deleted", localPath)
				} else if !os.IsNotExist(err) {
					logrus.Warnf("MinIO exclusive mode: failed to delete local copy %s: %v", localPath, err)
				}
			}
		}
		return
	}
	logrus.Errorf("MinIO upload failed (retried 3 times): camera=%d file=%s err=%v", cameraID, filepath.Base(localPath), lastErr)
}

func (m *CameraManager) getCameraStoragePath(cameraID uint) string {
	return fmt.Sprintf("%s/camera_%d", m.localStorage().RootPath, cameraID)
}

func (m *CameraManager) Snapshot(cameraID uint) (string, error) {
	m.mu.RLock()
	inst, ok := m.cameras[cameraID]
	m.mu.RUnlock()
	if !ok || inst == nil {
		return "", fmt.Errorf("camera not found")
	}

	if inst.Stream != nil && inst.Stream.IsRunning() {
		return inst.Stream.Snapshot()
	}

	rtspURL := inst.RecordRTSPURL
	if rtspURL == "" {
		rtspURL = inst.PreviewRTSPURL
	}
	if rtspURL == "" {
		rtspURL = BuildRTSPURL(inst.Model)
	}
	outDir := m.getCameraStoragePath(cameraID)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	snapshotPath := filepath.Join(outDir, fmt.Sprintf("snapshot_%d_%d.jpg", cameraID, time.Now().Unix()))
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-rtsp_transport", "tcp", "-i", rtspURL, "-vframes", "1", "-q:v", "2", snapshotPath)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("snapshot failed: %w", err)
	}
	return snapshotPath, nil
}

const previewIdleTimeout = 30 * time.Second

func (m *CameraManager) EnsurePreview(cameraID uint, src string) error {
	m.mu.RLock()
	inst, ok := m.cameras[cameraID]
	m.mu.RUnlock()
	if !ok || inst == nil {
		return fmt.Errorf("camera not found")
	}
	if !inst.Model.RecordEnabled {
		return fmt.Errorf("camera is not enabled")
	}

	src = m.NormalizePreviewSrc(src)

	inst.previewMu.Lock()
	defer inst.previewMu.Unlock()

	if inst.Preview != nil && inst.Preview.IsRunning() {
		if inst.Preview.Src == src {
			inst.previewLastActive = time.Now()
			return nil
		}
		old := inst.Preview
		inst.Preview = nil

		old.Stop()
		logrus.Infof("camera %s preview stream switched: %s -> %s", inst.Model.Name, old.Src, src)
	}

	var rtspURL string
	if src == "sub" {
		rtspURL = inst.PreviewRTSPURL
		if rtspURL == "" {
			rtspURL = inst.RecordRTSPURL
		}
	} else {
		rtspURL = inst.RecordRTSPURL
		if rtspURL == "" {
			rtspURL = inst.PreviewRTSPURL
		}
	}
	if rtspURL == "" {
		rtspURL = BuildRTSPURL(inst.Model)
	}

	outDir := m.getCameraStoragePath(cameraID)
	inst.Preview = ffmpeg.NewPreviewStream(cameraID, rtspURL, outDir)
	inst.Preview.Src = src
	if err := inst.Preview.Start(); err != nil {
		inst.Preview = nil
		return fmt.Errorf("failed to start preview stream: %w", err)
	}
	inst.previewLastActive = time.Now()
	logrus.Infof("camera %s preview stream started on demand (%s: %s)", inst.Model.Name, src, rtspURL)
	return nil
}

func (m *CameraManager) NormalizePreviewSrc(src string) string {
	switch strings.ToLower(src) {
	case "main", "sub":
		return strings.ToLower(src)
	default:
		if strings.EqualFold(m.cfg.Camera.PreviewStream, "sub") {
			return "sub"
		}
		return "main"
	}
}

func (m *CameraManager) TouchPreview(cameraID uint) {
	m.mu.RLock()
	inst, ok := m.cameras[cameraID]
	m.mu.RUnlock()
	if !ok || inst == nil {
		return
	}
	inst.previewMu.Lock()
	if inst.Preview != nil && inst.Preview.IsRunning() {
		inst.previewLastActive = time.Now()
	}
	inst.previewMu.Unlock()
}

func (m *CameraManager) stopIdlePreviews() {
	m.mu.RLock()
	instances := make([]*CameraInstance, 0, len(m.cameras))
	for _, inst := range m.cameras {
		instances = append(instances, inst)
	}
	m.mu.RUnlock()

	now := time.Now()
	for _, inst := range instances {
		inst.previewMu.Lock()
		if inst.Preview != nil && inst.Preview.IsRunning() && now.Sub(inst.previewLastActive) > previewIdleTimeout {
			logrus.Infof("camera %s preview stream idle %s, stopping automatically to reclaim memory", inst.Model.Name, previewIdleTimeout)
			p := inst.Preview
			inst.Preview = nil
			p.Stop()
		}
		inst.previewMu.Unlock()
	}
}

func (m *CameraManager) previewIdleLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.stopIdlePreviews()
		}
	}
}

func (m *CameraManager) healthCheckLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkCamerasHealth()
		}
	}
}

func (m *CameraManager) checkCamerasHealth() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id, inst := range m.cameras {
		if inst.Stream != nil && !inst.Stream.IsHealthy() {
			logrus.Warnf("camera %d stream unhealthy (data stream interrupted), triggering reconnect", id)

			inst.Stream.Restart()
		}
	}
}

func (m *CameraManager) snapshotLoop() {
	defer m.wg.Done()

	for {
		enabled, interval := m.GetSnapshotSettings()
		if interval <= 0 {
			interval = 300
		}
		timer := time.NewTimer(time.Duration(interval) * time.Second)
		select {
		case <-m.ctx.Done():
			timer.Stop()
			return
		case <-m.snapChanged:

			timer.Stop()
			continue
		case <-timer.C:
		}

		if !enabled {
			continue
		}
		m.takeSnapshots()
	}
}

func (m *CameraManager) takeSnapshots() {
	m.mu.RLock()
	instances := make([]*CameraInstance, 0, len(m.cameras))
	for _, inst := range m.cameras {
		if inst.Model.RecordEnabled {
			instances = append(instances, inst)
		}
	}
	m.mu.RUnlock()

	for _, inst := range instances {
		go func(inst *CameraInstance) {
			path, err := m.Snapshot(inst.Model.ID)
			if err != nil {
				logrus.Errorf("camera %d snapshot failed: %v", inst.Model.ID, err)
				return
			}

			snapshot := &models.Snapshot{
				CameraID:    inst.Model.ID,
				Timestamp:   time.Now(),
				FilePath:    path,
				FileSize:    getFileSize(path),
				Type:        "schedule",
				StorageType: "local",
			}
			if err := m.db.Create(snapshot).Error; err != nil {
				logrus.Errorf("failed to save snapshot record: %v", err)
			}
		}(inst)
	}
}

func (m *CameraManager) GetCamera(id uint) (*models.Camera, error) {
	var cam models.Camera
	if err := m.db.First(&cam, id).Error; err != nil {
		return nil, err
	}
	return &cam, nil
}

func (m *CameraManager) ListCameras() ([]models.Camera, error) {
	var cameras []models.Camera
	err := m.db.Where("deleted_at IS NULL").Order("id ASC").Find(&cameras).Error
	return cameras, err
}

func (m *CameraManager) SaveSnapshot(cameraID uint, path string, fileType string) error {
	snapshot := &models.Snapshot{
		CameraID:    cameraID,
		Timestamp:   time.Now(),
		FilePath:    path,
		FileSize:    getFileSize(path),
		Type:        fileType,
		StorageType: "local",
	}
	return m.db.Create(snapshot).Error
}

func (m *CameraManager) ListSnapshots(cameraID uint, page, pageSize int) ([]models.Snapshot, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 12
	}
	var total int64
	q := m.db.Model(&models.Snapshot{}).Where("camera_id = ?", cameraID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var snaps []models.Snapshot
	err := q.Order("timestamp DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&snaps).Error
	return snaps, total, err
}

func cloneCamera(cam *models.Camera) *models.Camera {
	c := *cam
	if cam.DeviceID != nil {
		d := *cam.DeviceID
		c.DeviceID = &d
	}
	if cam.StreamUriUpdatedAt != nil {
		t := *cam.StreamUriUpdatedAt
		c.StreamUriUpdatedAt = &t
	}
	if cam.LastOnline != nil {
		t := *cam.LastOnline
		c.LastOnline = &t
	}
	c.Recordings = nil
	c.Alerts = nil
	c.Snapshots = nil
	return &c
}

func (m *CameraManager) CreateCamera(cam *models.Camera) error {
	if err := m.db.Create(cam).Error; err != nil {
		return err
	}

	inst := &CameraInstance{
		Model:    cloneCamera(cam),
		Status:   models.CameraStatusOffline,
		StopChan: make(chan struct{}),
		loopDone: make(chan struct{}),
	}
	m.mu.Lock()
	m.cameras[cam.ID] = inst
	m.mu.Unlock()

	if cam.RecordEnabled {
		m.wg.Add(1)
		go m.runCamera(cam.ID)
	}
	return nil
}

func (m *CameraManager) UpdateCamera(cam *models.Camera) error {
	if err := m.db.Save(cam).Error; err != nil {
		return err
	}

	m.mu.Lock()
	inst, ok := m.cameras[cam.ID]
	var needStart, needStop bool
	if ok {

		inst.Model = cloneCamera(cam)
		streamRunning := inst.running
		needStop = streamRunning && !cam.RecordEnabled
		needStart = !streamRunning && cam.RecordEnabled
	}
	m.mu.Unlock()

	if !ok {
		return nil
	}
	if needStop {
		if err := m.StopCamera(cam.ID); err != nil {
			logrus.Warnf("failed to stop camera %d stream: %v", cam.ID, err)
		}
	} else if needStart {
		if err := m.StartCamera(cam.ID); err != nil {
			logrus.Warnf("failed to start camera %d stream: %v", cam.ID, err)
		}
	}
	return nil
}

func (m *CameraManager) DeleteCamera(id uint) error {
	m.mu.Lock()
	inst, ok := m.cameras[id]
	var stream *ffmpeg.Stream
	if ok {
		stream, _ = inst.stop()
		delete(m.cameras, id)
	}
	m.mu.Unlock()

	if stream != nil {
		stream.Stop()
	}

	return m.db.Delete(&models.Camera{}, id).Error
}

func (m *CameraManager) GetCameraStatus(id uint) (*CameraInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.cameras[id]
	return inst, ok
}

func (m *CameraManager) StartCamera(id uint) error {
	cam, err := m.GetCamera(id)
	if err != nil {
		return fmt.Errorf("camera not found")
	}
	m.mu.Lock()
	inst, ok := m.cameras[id]
	if ok {
		inst.Model = cloneCamera(cam)

		inst.StopChan = make(chan struct{})
		inst.loopDone = make(chan struct{})
		inst.ReconnectCnt = 0
	} else {
		inst = &CameraInstance{
			Model:    cloneCamera(cam),
			Status:   models.CameraStatusOffline,
			StopChan: make(chan struct{}),
			loopDone: make(chan struct{}),
		}
		m.cameras[id] = inst
	}
	m.mu.Unlock()

	m.wg.Add(1)
	go m.runCamera(id)
	logrus.Infof("starting stream for camera %d", id)
	return nil
}

func (m *CameraManager) StopCamera(id uint) error {
	m.mu.Lock()
	inst, ok := m.cameras[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("camera not found")
	}
	loopDone := inst.loopDone
	stream, preview := inst.stop()
	m.mu.Unlock()

	if stream != nil {
		stream.Stop()
	}
	if preview != nil {
		preview.Stop()
	}

	if loopDone != nil {
		select {
		case <-loopDone:
		case <-time.After(15 * time.Second):
			logrus.Warnf("camera %d run loop did not exit within 15 s", id)
		}
	}
	logrus.Infof("stopped stream for camera %d", id)
	return nil
}

func (m *CameraManager) RestartCamera(id uint) error {
	if err := m.StopCamera(id); err != nil {
		return err
	}
	return m.StartCamera(id)
}

func (m *CameraManager) DiscoverONVIFCameras(network string) ([]*onvif.DeviceInfo, error) {
	return m.onvifClient.Discover(network)
}

func (m *CameraManager) ProbeONVIFCamera(ip, username, password string) (*onvif.DeviceInfo, error) {
	client := onvif.NewClient(10)
	if username != "" && password != "" {
		client.SetCredentials(username, password)
	}
	device, authRequired := client.ProbeSingleEx(ip)
	if device != nil {
		return device, nil
	}
	if authRequired {
		return nil, fmt.Errorf("ONVIF device detected, but authentication is required: please provide the correct username/password (add credentials if missing)")
	}
	return nil, fmt.Errorf("no ONVIF device found (the device may be offline, the ONVIF service may be disabled, or the IP address may be incorrect)")
}

// ScanCIDRList 解析配置的扫描网段（逗号分隔），空配置返回 nil。
func (m *CameraManager) ScanCIDRList() []string {
	raw := strings.TrimSpace(m.cfg.Camera.ScanCIDRs)
	if raw == "" {
		return nil
	}
	var cidrs []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			cidrs = append(cidrs, p)
		}
	}
	return cidrs
}

func (m *CameraManager) DiscoverLAN(timeoutSec int) ([]*onvif.DeviceInfo, error) {
	client := onvif.NewClient(timeoutSec)

	wsWindow := timeoutSec * 2 / 5
	if wsWindow <= 0 {
		wsWindow = 2
	}
	if wsWindow > 5 {
		wsWindow = 5
	}
	if devices, err := client.WSDiscover(wsWindow); err == nil && len(devices) > 0 {
		return devices, nil
	}

	sweepWindow := time.Duration(timeoutSec) * time.Second
	if sweepWindow < 5*time.Second {
		sweepWindow = 5 * time.Second
	}
	// 配置了扫描网段（VISION_SCAN_CIDRS）时优先使用：容器内枚举网卡拿不到宿主机可达网段
	if cidrs := m.ScanCIDRList(); len(cidrs) > 0 {
		return client.SweepCIDRList(cidrs, sweepWindow)
	}
	devices, err := client.SweepLocalSubnets(sweepWindow)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("no ONVIF device found on the LAN: make sure the camera has its ONVIF service enabled and is on the same network segment as this host")
	}
	return devices, nil
}

var (
	ErrCameraNotFound  = errors.New("camera not found")
	ErrCameraOffline   = errors.New("camera not connected")
	ErrPTZNotSupported = errors.New("camera does not support PTZ")
)

func (m *CameraManager) PTZControl(cameraID uint, command string, speed float64) error {
	m.mu.RLock()
	inst, ok := m.cameras[cameraID]
	m.mu.RUnlock()

	if !ok {
		return ErrCameraNotFound
	}
	if !inst.Model.PTZEnabled {
		return ErrPTZNotSupported
	}
	if inst.Status != models.CameraStatusOnline {
		return ErrCameraOffline
	}

	if inst.OnvifClient != nil {
		return inst.OnvifClient.PTZControl(inst.Model.OnvifAddress, command, speed)
	}
	return m.onvifClient.PTZControl(inst.Model.OnvifAddress, command, speed)
}

func (m *CameraManager) PTZCapability(cameraID uint) *bool {
	m.mu.RLock()
	inst, ok := m.cameras[cameraID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	supported := inst.PTZSupported
	return &supported
}

func (m *CameraManager) updateCameraStatus(id uint, status, errMsg string) {
	updates := map[string]interface{}{
		"status":    status,
		"error_msg": errMsg,
	}
	if status == models.CameraStatusOnline {
		updates["last_online"] = time.Now()
	}
	m.db.Model(&models.Camera{}).Where("id = ?", id).Updates(updates)

	m.mu.RLock()
	if inst, ok := m.cameras[id]; ok {
		inst.Status = status
		inst.LastError = errMsg
	}
	m.mu.RUnlock()
}

// conciseStreamError 提取简短的流错误信息：ffmpeg 错误输出最后一条非空行通常是致命错误
// （如 "rtsp://...: Connection refused"），避免把整段 stderr 尾巴塞进 DB / API。
// 错误格式为 "ffmpeg exited abnormally: exit status 1 (stderr tail: ...)"，
// 取 stderr 尾部（剥掉包装括号）的最后一行。
func conciseStreamError(err error) string {
	msg := err.Error()
	const marker = "(stderr tail: "
	if i := strings.Index(msg, marker); i >= 0 {
		tail := strings.TrimSuffix(msg[i+len(marker):], ")")
		lines := strings.Split(tail, "\n")
		for j := len(lines) - 1; j >= 0; j-- {
			if t := strings.TrimSpace(lines[j]); t != "" {
				if len(t) > 200 {
					t = t[len(t)-200:]
				}
				return t
			}
		}
	}
	lines := strings.Split(msg, "\n")
	for j := len(lines) - 1; j >= 0; j-- {
		if t := strings.TrimSpace(lines[j]); t != "" {
			if len(t) > 200 {
				t = t[len(t)-200:]
			}
			return t
		}
	}
	return msg
}

func (inst *CameraInstance) stop() (*ffmpeg.Stream, *ffmpeg.PreviewStream) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	select {
	case <-inst.StopChan:

	default:
		close(inst.StopChan)
	}

	stream := inst.Stream
	inst.Stream = nil

	preview := inst.Preview
	inst.Preview = nil

	inst.Status = models.CameraStatusOffline
	return stream, preview
}

func (inst *CameraInstance) setError(err string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.LastError = err
	if err != "" {
		inst.Status = models.CameraStatusError
	}
}

func selectBestProfile(profiles []onvif.Profile) onvif.Profile {
	if len(profiles) == 1 {
		return profiles[0]
	}

	bestIdx := 0
	bestScore := -1

	for i, p := range profiles {
		score := 0

		score += p.Width * p.Height / 10000

		name := strings.ToLower(p.Name)
		if strings.Contains(name, "main") || strings.Contains(name, "primary") || strings.Contains(name, "high") {
			score += 1000
		}
		if strings.Contains(name, "sub") || strings.Contains(name, "secondary") || strings.Contains(name, "low") {
			score -= 500
		}

		if p.Codec == "h264" || p.Codec == "h265" {
			score += 100
		}

		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	return profiles[bestIdx]
}

func selectSubProfile(profiles []onvif.Profile) onvif.Profile {
	if len(profiles) == 0 {
		return onvif.Profile{}
	}
	if len(profiles) == 1 {
		return profiles[0]
	}

	bestIdx := 0
	bestScore := int(^uint(0) >> 1)

	for i, p := range profiles {
		score := p.Width * p.Height

		name := strings.ToLower(p.Name)
		if strings.Contains(name, "sub") || strings.Contains(name, "secondary") || strings.Contains(name, "low") {
			score -= 1000000
		}
		if strings.Contains(name, "main") || strings.Contains(name, "primary") || strings.Contains(name, "high") {
			continue
		}

		if score < bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	return profiles[bestIdx]
}

func injectRTSPAuth(rtspURL, username, password string) string {
	if username == "" || rtspURL == "" {
		return rtspURL
	}

	if idx := strings.Index(rtspURL, "://"); idx > 0 {
		rest := rtspURL[idx+3:]
		slash := strings.Index(rest, "/")
		host := rest
		if slash > 0 {
			host = rest[:slash]
		}
		if strings.Contains(host, "@") {
			return rtspURL
		}
	} else {
		return rtspURL
	}

	auth := username
	if password != "" {
		auth += ":" + password
	}
	idx := strings.Index(rtspURL, "://")
	return rtspURL[:idx+3] + auth + "@" + rtspURL[idx+3:]
}

func BuildRTSPURL(c *models.Camera) string {
	auth := ""
	if c.Username != "" && c.Password != "" {

		auth = url.UserPassword(c.Username, c.Password).String() + "@"
	}
	path := c.Path
	if path == "" {
		path = "/stream1"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("rtsp://%s%s:%d%s", auth, c.IP, c.Port, path)
}

func getFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
