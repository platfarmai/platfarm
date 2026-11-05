package models

import (
	"time"

	"gorm.io/gorm"
)

type Camera struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Name        string `gorm:"size:100;not null;index" json:"name" validate:"required,max=100"`
	Description string `gorm:"size:500" json:"description"`

	Protocol string `gorm:"size:20;not null;default:'rtsp'" json:"protocol" validate:"oneof=rtsp onvif gb28181"`
	IP       string `gorm:"size:45;not null" json:"ip" validate:"required,ip"`
	Port     int    `gorm:"default:554" json:"port" validate:"min=1,max=65535"`
	Username string `gorm:"size:50" json:"username"`
	Password string `gorm:"size:100" json:"-"`
	Path     string `gorm:"size:200" json:"path"`

	OnvifAddress        string     `gorm:"size:200" json:"onvif_address"`
	OnvifProfileToken   string     `gorm:"size:100" json:"onvif_profile_token"`
	DiscoveredStreamUri string     `gorm:"size:500" json:"discovered_stream_uri"`
	StreamUriUpdatedAt  *time.Time `json:"stream_uri_updated_at"`
	DeviceID            *string    `gorm:"size:50;uniqueIndex" json:"device_id"`

	Manufacturer string `gorm:"size:100" json:"manufacturer"`
	Model        string `gorm:"size:100" json:"model"`
	Firmware     string `gorm:"size:50"  json:"firmware"`
	SerialNumber string `gorm:"size:100" json:"serial_number"`

	Status     string     `gorm:"size:20;default:'offline'" json:"status"`
	LastOnline *time.Time `json:"last_online"`
	ErrorMsg   string     `gorm:"size:500" json:"error_msg"`

	RecordEnabled  bool   `gorm:"default:true" json:"record_enabled"`
	RecordSchedule string `gorm:"size:100;default:'0-23'" json:"record_schedule"`
	RecordType     string `gorm:"size:20;default:'continuous'" json:"record_type"`

	Width   int    `gorm:"default:1920" json:"width"`
	Height  int    `gorm:"default:1080" json:"height"`
	FPS     int    `gorm:"default:25" json:"fps"`
	Bitrate int    `gorm:"default:4096" json:"bitrate"`
	Codec   string `gorm:"size:20;default:'h264'" json:"codec"`

	PTZEnabled bool `gorm:"default:false" json:"ptz_enabled"`

	PTZSupported *bool `gorm:"-" json:"ptz_supported"`

	PreviewDefault string `gorm:"-" json:"preview_default"`

	Recordings []Recording `gorm:"foreignKey:CameraID" json:"-"`
	Alerts     []Alert     `gorm:"foreignKey:CameraID" json:"-"`
	Snapshots  []Snapshot  `gorm:"foreignKey:CameraID" json:"-"`
}

type Recording struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	CameraID uint   `gorm:"not null;index" json:"camera_id"`
	Camera   Camera `gorm:"foreignKey:CameraID" json:"-"`

	StartTime time.Time `gorm:"not null;index" json:"start_time"`
	EndTime   time.Time `gorm:"not null;index" json:"end_time"`
	Duration  int       `json:"duration"`

	FilePath     string `gorm:"size:500;not null" json:"file_path"`
	FileSize     int64  `json:"file_size"`
	SegmentIndex int    `json:"segment_index"`

	RecordType string `gorm:"size:20" json:"record_type"`
	Status     string `gorm:"size:20;default:'completed'" json:"status"`

	IndexPath string `gorm:"size:500" json:"index_path"`

	StorageType string `gorm:"size:20;default:'local'" json:"storage_type"`
	StoragePath string `gorm:"size:500" json:"storage_path"`
}

type Snapshot struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	CameraID uint   `gorm:"not null;index" json:"camera_id"`
	Camera   Camera `gorm:"foreignKey:CameraID" json:"-"`

	Timestamp   time.Time `gorm:"not null;index" json:"timestamp"`
	FilePath    string    `gorm:"size:500;not null" json:"file_path"`
	FileSize    int64     `json:"file_size"`
	Type        string    `gorm:"size:20;default:'schedule'" json:"type"`
	StorageType string    `gorm:"size:20;default:'local'" json:"storage_type"`
}

type Alert struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	CameraID uint   `gorm:"not null;index" json:"camera_id"`
	Camera   Camera `gorm:"foreignKey:CameraID" json:"-"`

	Type    string `gorm:"size:30;not null;index" json:"type"`
	Level   string `gorm:"size:10;default:'medium'" json:"level"`
	Message string `gorm:"size:500" json:"message"`
	Details string `gorm:"type:text" json:"details"`

	SnapshotPath string `gorm:"size:500" json:"snapshot_path"`
	VideoPath    string `gorm:"size:500" json:"video_path"`

	Status     string     `gorm:"size:20;default:'new'" json:"status"`
	AckedBy    uint       `json:"acked_by"`
	AckedAt    *time.Time `json:"acked_at"`
	ResolvedBy uint       `json:"resolved_by"`
	ResolvedAt *time.Time `json:"resolved_at"`

	Notified bool       `gorm:"default:false" json:"notified"`
	NotifyAt *time.Time `json:"notify_at"`
}

type SystemConfig struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Key   string `gorm:"size:100;uniqueIndex;not null" json:"key"`
	Value string `gorm:"type:text" json:"value"`
	Desc  string `gorm:"size:200" json:"desc"`
}

func (Camera) TableName() string       { return "cameras" }
func (Recording) TableName() string    { return "recordings" }
func (Snapshot) TableName() string     { return "snapshots" }
func (Alert) TableName() string        { return "alerts" }
func (SystemConfig) TableName() string { return "system_configs" }

const (
	CameraStatusOnline  = "online"
	CameraStatusOffline = "offline"
	CameraStatusError   = "error"
)

const (
	RecordTypeContinuous = "continuous"
	RecordTypeMotion     = "motion"
	RecordTypeSchedule   = "schedule"
	RecordTypeManual     = "manual"
)

const (
	AlertTypeMotion       = "motion"
	AlertTypeIntrusion    = "intrusion"
	AlertTypeLineCross    = "line_cross"
	AlertTypeObjectDetect = "object_detect"
	AlertTypeOffline      = "offline"
	AlertTypeStorageFull  = "storage_full"
	AlertTypeError        = "error"
)

const (
	AlertLevelLow      = "low"
	AlertLevelMedium   = "medium"
	AlertLevelHigh     = "high"
	AlertLevelCritical = "critical"
)

const (
	AlertStatusNew          = "new"
	AlertStatusAcknowledged = "acknowledged"
	AlertStatusResolved     = "resolved"
)
