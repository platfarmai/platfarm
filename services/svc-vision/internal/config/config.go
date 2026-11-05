package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `mapstructure:"server" yaml:"server"`
	Storage StorageConfig `mapstructure:"storage" yaml:"storage"`
	Camera  CameraConfig  `mapstructure:"camera" yaml:"camera"`
	Alert   AlertConfig   `mapstructure:"alert" yaml:"alert"`
	GB28181 GB28181Config `mapstructure:"gb28181" yaml:"gb28181"`
	Logging LoggingConfig `mapstructure:"logging" yaml:"logging"`
}

type ServerConfig struct {
	Name     string `mapstructure:"name" yaml:"name"`
	Host     string `mapstructure:"host" yaml:"host"`
	HTTPPort int    `mapstructure:"http_port" yaml:"http_port"`
	WSPort   int    `mapstructure:"ws_port" yaml:"ws_port"`
	GRPCPort int    `mapstructure:"grpc_port" yaml:"grpc_port"`
	Mode     string `mapstructure:"mode" yaml:"mode"`
}

type StorageConfig struct {
	Local  LocalStorageConfig `mapstructure:"local" yaml:"local"`
	MinIO  MinIOConfig        `mapstructure:"minio" yaml:"minio"`
	Webdav WebdavConfig       `mapstructure:"webdav" yaml:"webdav"`
}

type LocalStorageConfig struct {
	Enabled         bool   `mapstructure:"enabled" yaml:"enabled"`
	RootPath        string `mapstructure:"root_path" yaml:"root_path"`
	SegmentDuration int    `mapstructure:"segment_duration" yaml:"segment_duration"`
	MaxDays         int    `mapstructure:"max_days" yaml:"max_days"`

	MaxStorageGB    float64 `mapstructure:"max_storage_gb" yaml:"max_storage_gb"`
	CleanupInterval int     `mapstructure:"cleanup_interval" yaml:"cleanup_interval"`
}

type WebdavConfig struct {
	Enabled  bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	URL      string `mapstructure:"url" yaml:"url" json:"url"`
	Username string `mapstructure:"username" yaml:"username" json:"username"`
	Password string `mapstructure:"password" yaml:"password" json:"password"`
	BasePath string `mapstructure:"base_path" yaml:"base_path" json:"base_path"`

	MaxDays int `mapstructure:"max_days" yaml:"max_days" json:"max_days"`

	MaxStorageGB float64 `mapstructure:"max_storage_gb" yaml:"max_storage_gb" json:"max_storage_gb"`

	Only bool `mapstructure:"only" yaml:"only" json:"only"`
}

type MinIOConfig struct {
	Enabled   bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Endpoint  string `mapstructure:"endpoint" yaml:"endpoint" json:"endpoint"`
	AccessKey string `mapstructure:"access_key" yaml:"access_key" json:"access_key"`
	SecretKey string `mapstructure:"secret_key" yaml:"secret_key" json:"secret_key"`
	Bucket    string `mapstructure:"bucket" yaml:"bucket" json:"bucket"`
	UseSSL    bool   `mapstructure:"use_ssl" yaml:"use_ssl" json:"use_ssl"`

	BasePath string `mapstructure:"base_path" yaml:"base_path" json:"base_path"`

	MaxDays int `mapstructure:"max_days" yaml:"max_days" json:"max_days"`

	MaxStorageGB float64 `mapstructure:"max_storage_gb" yaml:"max_storage_gb" json:"max_storage_gb"`

	Only bool `mapstructure:"only" yaml:"only" json:"only"`
}

type CameraConfig struct {
	DiscoveryTimeout  int  `mapstructure:"discovery_timeout" yaml:"discovery_timeout"`
	StreamTimeout     int  `mapstructure:"stream_timeout" yaml:"stream_timeout"`
	ReconnectInterval int  `mapstructure:"reconnect_interval" yaml:"reconnect_interval"`
	MaxReconnect      int  `mapstructure:"max_reconnect" yaml:"max_reconnect"`
	SnapshotEnabled   bool `mapstructure:"snapshot_enabled" yaml:"snapshot_enabled"`
	SnapshotInterval  int  `mapstructure:"snapshot_interval" yaml:"snapshot_interval"`

	OnvifEvent OnvifEventConfig `mapstructure:"onvif_event" yaml:"onvif_event"`

	MotionRecord MotionRecordConfig `mapstructure:"motion_record" yaml:"motion_record"`

	PreviewStream string `mapstructure:"preview_stream" yaml:"preview_stream"`

	// ScanCIDRs：逗号分隔的扫描网段（VISION_SCAN_CIDRS）。容器内网卡枚举不到宿主机
	// 可达网段，由部署方显式指定（如 192.168.110.0/24）。
	ScanCIDRs string `mapstructure:"scan_cidrs" yaml:"scan_cidrs" json:"scan_cidrs"`

	// Defaults：新建摄像头时省略字段的默认值（UI 可改，落库持久化）。
	Defaults CameraDefaults `mapstructure:"defaults" yaml:"defaults" json:"defaults"`
}

type CameraDefaults struct {
	RecordEnabled bool   `mapstructure:"record_enabled" yaml:"record_enabled" json:"record_enabled"`
	RecordType    string `mapstructure:"record_type" yaml:"record_type" json:"record_type"`
	Width         int    `mapstructure:"width" yaml:"width" json:"width"`
	Height        int    `mapstructure:"height" yaml:"height" json:"height"`
	FPS           int    `mapstructure:"fps" yaml:"fps" json:"fps"`
	Bitrate       int    `mapstructure:"bitrate" yaml:"bitrate" json:"bitrate"`
	Codec         string `mapstructure:"codec" yaml:"codec" json:"codec"`
}

type MotionRecordConfig struct {
	Duration int `mapstructure:"duration" yaml:"duration"`

	PreRecord int `mapstructure:"pre_record" yaml:"pre_record"`

	Cooldown int `mapstructure:"cooldown" yaml:"cooldown"`
}

type OnvifEventConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`

	PollInterval int `mapstructure:"poll_interval" yaml:"poll_interval"`

	SubscriptionTimeout int `mapstructure:"subscription_timeout" yaml:"subscription_timeout"`
}

type AlertConfig struct {
	Enabled  bool          `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Channels AlertChannels `mapstructure:"channels" yaml:"channels" json:"channels"`
}

type AlertChannels struct {
	Webhook WebhookAlertConfig `mapstructure:"webhook" yaml:"webhook" json:"webhook"`
	Email   EmailAlertConfig   `mapstructure:"email" yaml:"email" json:"email"`
	SMS     SMSAlertConfig     `mapstructure:"sms" yaml:"sms" json:"sms"`
}

type WebhookAlertConfig struct {
	Enabled bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	URL     string `mapstructure:"url" yaml:"url" json:"url"`

	Type string `mapstructure:"type" yaml:"type" json:"type"`
}

type EmailAlertConfig struct {
	Enabled  bool     `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	SMTPHost string   `mapstructure:"smtp_host" yaml:"smtp_host" json:"smtp_host"`
	SMTPPort int      `mapstructure:"smtp_port" yaml:"smtp_port" json:"smtp_port"`
	Username string   `mapstructure:"username" yaml:"username" json:"username"`
	Password string   `mapstructure:"password" yaml:"password" json:"password"`
	From     string   `mapstructure:"from" yaml:"from" json:"from"`
	To       []string `mapstructure:"to" yaml:"to" json:"to"`
}

type SMSAlertConfig struct {
	Enabled      bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Provider     string `mapstructure:"provider" yaml:"provider" json:"provider"`
	AccessKey    string `mapstructure:"access_key" yaml:"access_key" json:"access_key"`
	SecretKey    string `mapstructure:"secret_key" yaml:"secret_key" json:"secret_key"`
	SignName     string `mapstructure:"sign_name" yaml:"sign_name" json:"sign_name"`
	TemplateCode string `mapstructure:"template_code" yaml:"template_code" json:"template_code"`
}

type GB28181Config struct {
	Enabled     bool   `mapstructure:"enabled" yaml:"enabled"`
	SIPID       string `mapstructure:"sip_id" yaml:"sip_id"`
	SIPDomain   string `mapstructure:"sip_domain" yaml:"sip_domain"`
	SIPPassword string `mapstructure:"sip_password" yaml:"sip_password"`
	SIPPort     int    `mapstructure:"sip_port" yaml:"sip_port"`
	MediaPort   int    `mapstructure:"media_port" yaml:"media_port"`
	Expires     int    `mapstructure:"expires" yaml:"expires"`
}

type LoggingConfig struct {
	Level      string `mapstructure:"level" yaml:"level"`
	Format     string `mapstructure:"format" yaml:"format"`
	Output     string `mapstructure:"output" yaml:"output"`
	MaxSize    int    `mapstructure:"max_size" yaml:"max_size"`
	MaxBackups int    `mapstructure:"max_backups" yaml:"max_backups"`
	MaxAge     int    `mapstructure:"max_age" yaml:"max_age"`
	Compress   bool   `mapstructure:"compress" yaml:"compress"`
}

var GlobalConfig *Config

func Load(configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	setDefaults(v)

	// 配置文件可选：PlatFarm 下仅靠环境变量即可运行。文件存在时作为覆盖层读取。
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			v.SetConfigFile(configPath)
			if err := v.ReadInConfig(); err != nil {
				return nil, err
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	cfg.Storage.Local.RootPath = expandPath(cfg.Storage.Local.RootPath)
	cfg.Logging.Output = expandPath(cfg.Logging.Output)

	seedMinIOFromEnv(&cfg)

	// 扫描网段：容器部署下由环境变量显式指定宿主机可达网段
	if cidrs := os.Getenv("VISION_SCAN_CIDRS"); cidrs != "" {
		cfg.Camera.ScanCIDRs = cidrs
	}

	GlobalConfig = &cfg
	return &cfg, nil
}

// setDefaults 提供无配置文件时的运行默认值（server/storage/camera/alert/logging）。
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.http_port", 8080)
	v.SetDefault("server.ws_port", 8081)
	v.SetDefault("server.mode", "release")

	v.SetDefault("storage.local.enabled", true)
	v.SetDefault("storage.local.root_path", "./recordings")
	v.SetDefault("storage.local.segment_duration", 180)
	v.SetDefault("storage.local.max_days", 7)
	v.SetDefault("storage.local.cleanup_interval", 3600)

	v.SetDefault("camera.discovery_timeout", 10)
	v.SetDefault("camera.stream_timeout", 30)
	v.SetDefault("camera.reconnect_interval", 5)
	v.SetDefault("camera.max_reconnect", 10)
	v.SetDefault("camera.snapshot_interval", 300)
	v.SetDefault("camera.preview_stream", "main")
	v.SetDefault("camera.defaults.record_enabled", true)
	v.SetDefault("camera.defaults.record_type", "continuous")
	v.SetDefault("camera.defaults.width", 1920)
	v.SetDefault("camera.defaults.height", 1080)
	v.SetDefault("camera.defaults.fps", 25)
	v.SetDefault("camera.defaults.bitrate", 4096)
	v.SetDefault("camera.defaults.codec", "h264")
	v.SetDefault("camera.onvif_event.enabled", true)
	v.SetDefault("camera.onvif_event.poll_interval", 10)
	v.SetDefault("camera.onvif_event.subscription_timeout", 60)
	v.SetDefault("camera.motion_record.duration", 60)
	v.SetDefault("camera.motion_record.cooldown", 60)

	v.SetDefault("alert.enabled", true)

	// 日志默认仅 stdout（output 为空 → 不启用文件/lumberjack）。
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "")
}

// seedMinIOFromEnv 用 S3_* / VISION_S3_BUCKET 环境变量填充 MinIO 远端存储，
// 作为录像的默认远端后端（仅远端：only=true，满足容器无状态纪律）。UI 可运行时覆盖。
func seedMinIOFromEnv(cfg *Config) {
	endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	if endpoint == "" {
		return
	}
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	endpoint = strings.Trim(endpoint, "/")
	cfg.Storage.MinIO.Endpoint = endpoint
	cfg.Storage.MinIO.AccessKey = os.Getenv("S3_ACCESS_KEY")
	cfg.Storage.MinIO.SecretKey = os.Getenv("S3_SECRET_KEY")
	if b := strings.TrimSpace(os.Getenv("VISION_S3_BUCKET")); b != "" {
		cfg.Storage.MinIO.Bucket = b
	}
	cfg.Storage.MinIO.UseSSL = false
	cfg.Storage.MinIO.Enabled = true
	cfg.Storage.MinIO.Only = true
}

func expandPath(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[1:])
	}
	return path
}

func GetConfig() *Config {
	return GlobalConfig
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
