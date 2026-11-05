// 运行期可编辑设置的持久化：system_configs 表存 JSON（PlatFarm 无状态容器纪律——
// 容器内写 config 文件不持久，重启/重建即失；所有 UI 可改设置统一落库）。
package database

import (
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"platfarm/svc-vision/internal/config"
	"platfarm/svc-vision/internal/models"
)

// 设置键名（value 为对应结构体的 JSON）
const (
	SettingStorageLocal   = "settings.storage_local"
	SettingWebdav         = "settings.webdav"
	SettingMinIO          = "settings.minio"
	SettingCameraSnapshot = "settings.camera_snapshot"
	SettingCameraDefaults = "settings.camera_defaults"
	SettingAlert          = "settings.alert"
	SettingServer         = "settings.server"
)

// SaveSetting 序列化并 upsert 一个设置项。
func SaveSetting(key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal setting %s: %w", key, err)
	}
	rec := models.SystemConfig{Key: key, Value: string(data), Desc: "runtime setting"}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&rec).Error
}

// LoadSetting 读取并反序列化一个设置项；不存在返回 found=false。
func LoadSetting(key string, out any) (bool, error) {
	var rec models.SystemConfig
	if err := DB.Where("key = ?", key).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal([]byte(rec.Value), out); err != nil {
		return false, fmt.Errorf("unmarshal setting %s: %w", key, err)
	}
	return true, nil
}

type SnapshotSetting struct {
	Enabled  bool `json:"enabled"`
	Interval int  `json:"interval"`
}

type ServerSetting struct {
	Name     string `json:"name"`
	LogLevel string `json:"log_level"`
}

// OverlaySettings 启动时把库里保存的设置覆盖到 cfg（env/默认值 → DB 覆盖 → 管理器启动）。
func OverlaySettings(cfg *config.Config) {
	warn := func(key string, err error) {
		if err != nil {
			fmt.Printf("overlay setting %s failed: %v\n", key, err)
		}
	}

	if _, err := LoadSetting(SettingStorageLocal, &cfg.Storage.Local); err != nil {
		warn(SettingStorageLocal, err)
	}
	if _, err := LoadSetting(SettingWebdav, &cfg.Storage.Webdav); err != nil {
		warn(SettingWebdav, err)
	}
	if _, err := LoadSetting(SettingMinIO, &cfg.Storage.MinIO); err != nil {
		warn(SettingMinIO, err)
	}
	if _, err := LoadSetting(SettingCameraDefaults, &cfg.Camera.Defaults); err != nil {
		warn(SettingCameraDefaults, err)
	}
	if _, err := LoadSetting(SettingAlert, &cfg.Alert); err != nil {
		warn(SettingAlert, err)
	}

	var snap SnapshotSetting
	if found, err := LoadSetting(SettingCameraSnapshot, &snap); err != nil {
		warn(SettingCameraSnapshot, err)
	} else if found {
		cfg.Camera.SnapshotEnabled = snap.Enabled
		cfg.Camera.SnapshotInterval = snap.Interval
	}

	var srv ServerSetting
	if found, err := LoadSetting(SettingServer, &srv); err != nil {
		warn(SettingServer, err)
	} else if found {
		if srv.Name != "" {
			cfg.Server.Name = srv.Name
		}
		if srv.LogLevel != "" {
			cfg.Logging.Level = srv.LogLevel
		}
	}
}
