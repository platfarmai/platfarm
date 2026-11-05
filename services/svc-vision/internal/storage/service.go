package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"platfarm/svc-vision/internal/config"
	"platfarm/svc-vision/internal/database"
	"platfarm/svc-vision/internal/models"
	"platfarm/svc-vision/pkg/minio"
	"platfarm/svc-vision/pkg/webdav"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Manager struct {
	cfg       *config.Config
	db        *gorm.DB
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	stats     StorageStats
	runtime   *RuntimeStorage
	cleanupMu sync.Mutex
}

type StorageStats struct {
	TotalSpace     int64                      `json:"total_space"`
	UsedSpace      int64                      `json:"used_space"`
	FreeSpace      int64                      `json:"free_space"`
	RecordingCount int64                      `json:"recording_count"`
	SnapshotCount  int64                      `json:"snapshot_count"`
	CameraStats    map[uint]CameraStorageStat `json:"camera_stats"`
}

type CameraStorageStat struct {
	CameraID        uint       `json:"camera_id"`
	CameraName      string     `json:"camera_name"`
	RecordingCount  int64      `json:"recording_count"`
	TotalSize       int64      `json:"total_size"`
	OldestRecording *time.Time `json:"oldest_recording"`
	LatestRecording *time.Time `json:"latest_recording"`
}

func NewManager(cfg *config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg:    cfg,
		db:     database.GetDB(),
		ctx:    ctx,
		cancel: cancel,
		stats:  StorageStats{CameraStats: make(map[uint]CameraStorageStat)},
	}
}

func (m *Manager) SetRuntimeStorage(r *RuntimeStorage) {
	m.runtime = r
}

func (m *Manager) local() config.LocalStorageConfig {
	if m.runtime != nil {
		return m.runtime.GetLocal()
	}
	return m.cfg.Storage.Local
}

func (m *Manager) webdav() config.WebdavConfig {
	if m.runtime != nil {
		return m.runtime.GetWebdav()
	}
	return m.cfg.Storage.Webdav
}

func (m *Manager) minio() config.MinIOConfig {
	if m.runtime != nil {
		return m.runtime.GetMinIO()
	}
	return m.cfg.Storage.MinIO
}

func (m *Manager) Start() error {
	if !m.cfg.Storage.Local.Enabled {
		logrus.Info("local storage not enabled, skipping storage manager start")
		return nil
	}

	if err := os.MkdirAll(m.cfg.Storage.Local.RootPath, 0755); err != nil {
		return fmt.Errorf("failed to create storage root directory: %w", err)
	}

	m.wg.Add(1)
	go m.cleanupLoop()

	m.wg.Add(1)
	go m.statsLoop()

	logrus.Info("storage manager started")
	return nil
}

func (m *Manager) Stop() error {
	m.cancel()
	m.wg.Wait()
	logrus.Info("storage manager stopped")
	return nil
}

func (m *Manager) cleanupLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(time.Duration(m.cfg.Storage.Local.CleanupInterval) * time.Second)
	defer ticker.Stop()

	m.cleanup()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanup()
		}
	}
}

func (m *Manager) cleanup() {
	m.cleanupMu.Lock()
	defer m.cleanupMu.Unlock()
	m.doCleanup()
}

func (m *Manager) TriggerCleanup() {
	m.cleanup()
}

func (m *Manager) doCleanup() {
	loc := m.local()
	if loc.Enabled {
		m.doCleanupLocal(loc)
	}

	m.doCleanupWebdav()

	m.doCleanupMinio()
}

func (m *Manager) doCleanupLocal(loc config.LocalStorageConfig) {
	cutoff := time.Now().AddDate(0, 0, -loc.MaxDays)

	var expiredRecordings []models.Recording
	if err := m.db.Where("end_time < ? AND storage_type = 'local' AND deleted_at IS NULL", cutoff).
		Find(&expiredRecordings).Error; err != nil {
		logrus.Errorf("failed to query expired recordings: %v", err)
		return
	}

	deletedCount := 0
	deletedSize := int64(0)

	for _, rec := range expiredRecordings {
		if size := m.deleteRecordingLocalFiles(&rec); size > 0 {
			deletedSize += size
		}

		if err := m.db.Delete(&rec).Error; err != nil {
			logrus.Errorf("failed to delete recording record: %v", err)
		} else {
			deletedCount++
		}
	}

	m.cleanupOrphanFiles()

	var expiredSnapshots []models.Snapshot
	if err := m.db.Where("timestamp < ? AND storage_type = 'local' AND deleted_at IS NULL", cutoff).
		Find(&expiredSnapshots).Error; err != nil {
		logrus.Errorf("failed to query expired snapshots: %v", err)
	} else {
		for _, snap := range expiredSnapshots {
			if err := os.Remove(snap.FilePath); err != nil && !os.IsNotExist(err) {
				logrus.Warnf("failed to delete snapshot file %s: %v", snap.FilePath, err)
			}
			m.db.Delete(&snap)
		}
	}

	if deletedCount > 0 {
		logrus.Infof("storage cleanup complete: deleted %d recording segments, freed %s", deletedCount, formatBytes(deletedSize))
	}

	if loc.MaxStorageGB > 0 {
		m.enforceMaxStorage(loc)
	}
}

func (m *Manager) doCleanupWebdav() {
	wd := m.webdav()
	if !wd.Enabled || wd.URL == "" {
		return
	}
	if wd.MaxDays <= 0 && wd.MaxStorageGB <= 0 {
		return
	}

	client := webdav.NewClient(wd.URL, wd.Username, wd.Password)
	base := strings.Trim(strings.TrimSpace(wd.BasePath), "/")

	type remoteFile struct {
		path    string
		size    int64
		modTime time.Time
	}

	var files []remoteFile
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 6 {
			return
		}
		entries, err := client.List(dir)
		if err != nil {
			logrus.Warnf("WebDAV cleanup: failed to list directory %s: %v", dir, err)
			return
		}
		for _, e := range entries {
			p := dir
			if p == "" {
				p = e.Name
			} else {
				p = p + "/" + e.Name
			}
			if e.IsDir {
				walk(p, depth+1)
				continue
			}
			files = append(files, remoteFile{path: p, size: e.Size, modTime: e.ModTime})
		}
	}
	walk(base, 0)
	if len(files) == 0 {
		return
	}

	now := time.Now()

	protectWindow := 10 * time.Minute

	deleted := 0
	var released int64
	deletedPaths := make(map[string]bool)
	if wd.MaxDays > 0 {
		cutoff := now.AddDate(0, 0, -wd.MaxDays)
		for _, f := range files {
			if !f.modTime.IsZero() && f.modTime.Before(cutoff) && now.Sub(f.modTime) > protectWindow {
				if err := client.Delete(f.path); err == nil {
					deleted++
					released += f.size
					deletedPaths[f.path] = true
				} else {
					logrus.Warnf("WebDAV cleanup: failed to delete expired file %s: %v", f.path, err)
				}
			}
		}
	}

	if wd.MaxStorageGB > 0 {
		var total int64
		for _, f := range files {
			total += f.size
		}
		limit := int64(wd.MaxStorageGB * 1024 * 1024 * 1024)
		if total-released > limit {
			sort.SliceStable(files, func(i, j int) bool {
				return files[i].modTime.Before(files[j].modTime)
			})
			remaining := total - released
			for _, f := range files {
				if remaining <= limit {
					break
				}
				if deletedPaths[f.path] {
					continue
				}
				if now.Sub(f.modTime) <= protectWindow {
					continue
				}
				if err := client.Delete(f.path); err == nil {
					deleted++
					released += f.size
					remaining -= f.size
					logrus.Debugf("WebDAV capacity cleanup: deleted %s (%s)", f.path, formatBytes(f.size))
				} else {
					logrus.Warnf("WebDAV capacity cleanup: delete failed %s: %v", f.path, err)
				}
			}
		}
	}

	if deleted > 0 {
		logrus.Infof("WebDAV cleanup complete: deleted %d remote files, freed approx. %s", deleted, formatBytes(released))
	}
}

func (m *Manager) doCleanupMinio() {
	mn := m.minio()
	if !mn.Enabled || mn.Endpoint == "" || mn.Bucket == "" {
		return
	}
	if mn.MaxDays <= 0 && mn.MaxStorageGB <= 0 {
		return
	}

	client, err := minio.NewClient(mn.Endpoint, mn.AccessKey, mn.SecretKey, mn.Bucket, mn.UseSSL)
	if err != nil {
		logrus.Warnf("MinIO cleanup: failed to create client: %v", err)
		return
	}

	base := strings.Trim(strings.TrimSpace(mn.BasePath), "/")
	entries, err := client.List(base)
	if err != nil {
		logrus.Warnf("MinIO cleanup: failed to list objects: %v", err)
		return
	}
	if len(entries) == 0 {
		return
	}

	now := time.Now()

	protectWindow := 10 * time.Minute

	deleted := 0
	var released int64
	deletedKeys := make(map[string]bool)
	if mn.MaxDays > 0 {
		cutoff := now.AddDate(0, 0, -mn.MaxDays)
		for _, f := range entries {
			if !f.ModTime.IsZero() && f.ModTime.Before(cutoff) && now.Sub(f.ModTime) > protectWindow {
				if err := client.Delete(f.Key); err == nil {
					deleted++
					released += f.Size
					deletedKeys[f.Key] = true
				} else {
					logrus.Warnf("MinIO cleanup: failed to delete expired object %s: %v", f.Key, err)
				}
			}
		}
	}

	if mn.MaxStorageGB > 0 {
		var total int64
		for _, f := range entries {
			total += f.Size
		}
		limit := int64(mn.MaxStorageGB * 1024 * 1024 * 1024)
		if total-released > limit {
			sort.SliceStable(entries, func(i, j int) bool {
				return entries[i].ModTime.Before(entries[j].ModTime)
			})
			remaining := total - released
			for _, f := range entries {
				if remaining <= limit {
					break
				}
				if deletedKeys[f.Key] || now.Sub(f.ModTime) <= protectWindow {
					continue
				}
				if err := client.Delete(f.Key); err == nil {
					deleted++
					released += f.Size
					remaining -= f.Size
					logrus.Debugf("MinIO capacity cleanup: deleted %s (%s)", f.Key, formatBytes(f.Size))
				} else {
					logrus.Warnf("MinIO capacity cleanup: delete failed %s: %v", f.Key, err)
				}
			}
		}
	}

	if deleted > 0 {
		logrus.Infof("MinIO cleanup complete: deleted %d remote objects, freed approx. %s", deleted, formatBytes(released))
	}
}

func (m *Manager) deleteRecordingLocalFiles(rec *models.Recording) int64 {
	size := int64(0)
	if rec.FilePath != "" {
		if err := os.Remove(rec.FilePath); err == nil {
			size = rec.FileSize
		} else if !os.IsNotExist(err) {
			logrus.Warnf("failed to delete file %s: %v", rec.FilePath, err)
		}
	}
	if rec.IndexPath != "" {
		if err := os.Remove(rec.IndexPath); err != nil && !os.IsNotExist(err) {
			logrus.Warnf("failed to delete file %s: %v", rec.IndexPath, err)
		}
	}
	return size
}

func (m *Manager) enforceMaxStorage(loc config.LocalStorageConfig) {
	limitBytes := int64(loc.MaxStorageGB * 1024 * 1024 * 1024)
	if limitBytes <= 0 {
		return
	}

	used := dirSize(loc.RootPath)
	if used <= limitBytes {
		return
	}

	logrus.Warnf("storage usage %s exceeds limit %s, starting capacity cleanup (deleting from the oldest recordings)",
		formatBytes(used), formatBytes(limitBytes))

	var recordings []models.Recording
	if err := m.db.Where("storage_type = 'local' AND status = 'completed' AND deleted_at IS NULL").
		Order("start_time ASC").Find(&recordings).Error; err != nil {
		logrus.Errorf("capacity cleanup: failed to query recordings: %v", err)
		return
	}

	released := int64(0)
	count := 0
	for i := range recordings {
		if used-released <= limitBytes {
			break
		}
		rec := &recordings[i]
		if size := m.deleteRecordingLocalFiles(rec); size > 0 {
			released += size
		}
		if err := m.db.Delete(rec).Error; err != nil {
			logrus.Errorf("capacity cleanup: failed to delete recording record id=%d: %v", rec.ID, err)
			continue
		}
		count++
	}

	if used-released > limitBytes {
		if orphanReleased, orphanCount := m.cleanupOrphanSegments(loc.RootPath, 5*time.Minute); orphanCount > 0 {
			released += orphanReleased
			count += orphanCount
		}
	}

	logrus.Infof("capacity cleanup complete: deleted %d recording segments, freed approx. %s", count, formatBytes(released))
}

func (m *Manager) cleanupOrphanSegments(root string, protectWindow time.Duration) (int64, int) {
	var released int64
	count := 0
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		name := filepath.Base(path)
		if !strings.HasSuffix(name, ".mp4") && !strings.HasSuffix(name, ".idx") {
			return nil
		}
		if time.Since(info.ModTime()) < protectWindow {
			return nil
		}
		var n int64
		m.db.Model(&models.Recording{}).Where("file_path = ?", filepath.Clean(path)).Count(&n)
		if n > 0 {
			return nil
		}
		if err := os.Remove(path); err == nil {
			released += info.Size()
			count++
			logrus.Debugf("capacity cleanup: deleted orphan segment file %s", path)
		}
		return nil
	})
	return released, count
}

func dirSize(dir string) int64 {
	var total int64
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func (m *Manager) cleanupOrphanFiles() {
	loc := m.local()
	root := loc.RootPath
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		cameraDir := filepath.Join(root, entry.Name())
		files, _ := os.ReadDir(cameraDir)

		for _, f := range files {
			if f.IsDir() {
				continue
			}

			filePath := filepath.Join(cameraDir, f.Name())

			var count int64
			m.db.Model(&models.Recording{}).Where("file_path = ?", filePath).Count(&count)
			if count == 0 {
				m.db.Model(&models.Snapshot{}).Where("file_path = ?", filePath).Count(&count)
			}

			if count == 0 {

				info, _ := f.Info()
				if time.Since(info.ModTime()) > time.Duration(loc.MaxDays)*24*time.Hour {
					os.Remove(filePath)
					logrus.Debugf("deleted orphan file: %s", filePath)
				}
			}
		}
	}
}

func (m *Manager) statsLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	m.updateStats()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.updateStats()
		}
	}
}

func (m *Manager) updateStats() {
	m.mu.Lock()
	defer m.mu.Unlock()

	total, free, err := diskUsage(m.local().RootPath)
	if err != nil {
		logrus.Warnf("failed to get disk space: %v", err)
	} else {
		m.stats.TotalSpace = int64(total)
		m.stats.FreeSpace = int64(free)
		m.stats.UsedSpace = m.stats.TotalSpace - m.stats.FreeSpace
	}

	var recordings []models.Recording
	m.db.Where("storage_type = 'local' AND deleted_at IS NULL").Find(&recordings)
	m.stats.RecordingCount = int64(len(recordings))

	var snapshots []models.Snapshot
	m.db.Where("storage_type = 'local' AND deleted_at IS NULL").Find(&snapshots)
	m.stats.SnapshotCount = int64(len(snapshots))

	cameraStats := make(map[uint]CameraStorageStat)
	for _, rec := range recordings {
		stat := cameraStats[rec.CameraID]
		stat.CameraID = rec.CameraID
		stat.RecordingCount++
		stat.TotalSize += rec.FileSize

		if stat.OldestRecording == nil || rec.StartTime.Before(*stat.OldestRecording) {
			t := rec.StartTime
			stat.OldestRecording = &t
		}
		if stat.LatestRecording == nil || rec.EndTime.After(*stat.LatestRecording) {
			t := rec.EndTime
			stat.LatestRecording = &t
		}
		cameraStats[rec.CameraID] = stat
	}

	var cameras []models.Camera
	m.db.Find(&cameras)
	for _, cam := range cameras {
		if stat, ok := cameraStats[cam.ID]; ok {
			stat.CameraName = cam.Name
			cameraStats[cam.ID] = stat
		}
	}

	m.stats.CameraStats = cameraStats
}

func (m *Manager) GetStats() StorageStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

type RecordingManager struct {
	db *gorm.DB
}

func NewRecordingManager() *RecordingManager {
	return &RecordingManager{db: database.GetDB()}
}

func (rm *RecordingManager) QueryRecordings(cameraID uint, start, end time.Time, recordType string, page, pageSize int) ([]models.Recording, int64, error) {
	query := rm.db.Model(&models.Recording{})
	if cameraID != 0 {
		query = query.Where("camera_id = ?", cameraID)
	}

	if !start.IsZero() {
		query = query.Where("end_time >= ?", start)
	}
	if !end.IsZero() {
		query = query.Where("start_time <= ?", end)
	}
	if recordType != "" {
		query = query.Where("record_type = ?", recordType)
	}

	var total int64
	query.Count(&total)

	var recordings []models.Recording
	offset := (page - 1) * pageSize
	err := query.Order("start_time DESC").Offset(offset).Limit(pageSize).Find(&recordings).Error

	return recordings, total, err
}

func (rm *RecordingManager) GetRecordingByID(id uint) (*models.Recording, error) {
	var rec models.Recording
	err := rm.db.First(&rec, id).Error
	return &rec, err
}

func (rm *RecordingManager) DeleteRecording(id uint) error {
	return rm.db.Delete(&models.Recording{}, id).Error
}

func (rm *RecordingManager) GetRecordingSegments(cameraID uint, start, end time.Time) ([]models.Recording, error) {
	var recordings []models.Recording
	err := rm.db.Where("camera_id = ? AND start_time <= ? AND end_time >= ? AND status = 'completed' AND deleted_at IS NULL",
		cameraID, end, start).
		Order("start_time ASC").
		Find(&recordings).Error
	return recordings, err
}

func (rm *RecordingManager) GetLatestRecording(cameraID uint) (*models.Recording, error) {
	var rec models.Recording
	err := rm.db.Where("camera_id = ? AND status = 'completed' AND deleted_at IS NULL", cameraID).
		Order("start_time DESC").First(&rec).Error
	return &rec, err
}

type SnapshotManager struct {
	db *gorm.DB
}

func NewSnapshotManager() *SnapshotManager {
	return &SnapshotManager{db: database.GetDB()}
}

func (sm *SnapshotManager) QuerySnapshots(cameraID uint, start, end time.Time, snapType string, page, pageSize int) ([]models.Snapshot, int64, error) {
	query := sm.db.Model(&models.Snapshot{}).Where("camera_id = ? AND deleted_at IS NULL", cameraID)

	if !start.IsZero() {
		query = query.Where("timestamp >= ?", start)
	}
	if !end.IsZero() {
		query = query.Where("timestamp <= ?", end)
	}
	if snapType != "" {
		query = query.Where("type = ?", snapType)
	}

	var total int64
	query.Count(&total)

	var snapshots []models.Snapshot
	offset := (page - 1) * pageSize
	err := query.Order("timestamp DESC").Offset(offset).Limit(pageSize).Find(&snapshots).Error

	return snapshots, total, err
}

func (sm *SnapshotManager) GetSnapshotByID(id uint) (*models.Snapshot, error) {
	var snap models.Snapshot
	err := sm.db.First(&snap, id).Error
	return &snap, err
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
