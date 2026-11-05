// svc-vision — Platfarm 视频监控服务（cineye 移植；接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5）。
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/platfarmai/sdk/go/pfauth"

	"platfarm/svc-vision/internal/alert"
	"platfarm/svc-vision/internal/api"
	"platfarm/svc-vision/internal/camera"
	"platfarm/svc-vision/internal/config"
	"platfarm/svc-vision/internal/database"
	"platfarm/svc-vision/internal/models"
	"platfarm/svc-vision/internal/onvifevent"
	"platfarm/svc-vision/internal/storage"
	"platfarm/svc-vision/pkg/ffmpeg"
	"platfarm/svc-vision/pkg/minio"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

var startTime = time.Now()

func main() {
	// 验签唯一实现在 sdk/go/pfauth（RS256，读 /pf/jwt.pub，含 kid 轮换）。
	pfauth.Load()

	configPath := resolveConfigPath(os.Args)

	cfg, err := config.Load(configPath)
	if err != nil {
		logrus.Fatalf("failed to load config: %v", err)
	}

	initLogging(cfg)

	logrus.Infof("starting svc-vision v%s (build: %s, commit: %s)", Version, BuildTime, GitCommit)

	if err := database.Init(cfg); err != nil {
		logrus.Fatalf("failed to initialize database: %v", err)
	}
	defer database.Close()

	sqlDB, err := database.GetDB().DB()
	if err != nil {
		logrus.Fatalf("failed to acquire sql.DB for migrations: %v", err)
	}
	if err := runMigrations(sqlDB); err != nil {
		logrus.Fatalf("database migration failed: %v", err)
	}

	// DB 中保存的运行期设置覆盖 env/默认值（无状态容器：设置统一落库）
	database.OverlaySettings(cfg)

	ffmpegMgr := ffmpeg.NewManager()

	storageRT := storage.NewRuntimeStorage(cfg)

	// MinIO 启用时确保 bucket 存在（远端存储依赖；失败仅告警，不阻塞启动）
	if mn := cfg.Storage.MinIO; mn.Enabled {
		if mc, err := minio.NewClient(mn.Endpoint, mn.AccessKey, mn.SecretKey, mn.Bucket, mn.UseSSL); err != nil {
			logrus.Warnf("MinIO client init failed: %v", err)
		} else if err := mc.EnsureBucket(context.Background()); err != nil {
			logrus.Warnf("MinIO ensure bucket %s failed: %v", mn.Bucket, err)
		} else {
			logrus.Infof("MinIO bucket %s ready", mn.Bucket)
		}
	}

	cameraMgr := camera.NewCameraManager(cfg, ffmpegMgr)
	cameraMgr.SetRuntimeStorage(storageRT)
	storageMgr := storage.NewManager(cfg)
	storageMgr.SetRuntimeStorage(storageRT)
	alertMgr := alert.NewManager(cfg)

	onvifEventMgr := onvifevent.NewManager(cfg, func(a *models.Alert) {
		alertMgr.OnAlert(a)
		if a.Type == models.AlertTypeMotion {
			cameraMgr.TriggerMotionRecording(a.CameraID)
		}
	})

	if err := cameraMgr.Start(); err != nil {
		logrus.Fatalf("failed to start camera manager: %v", err)
	}
	if err := storageMgr.Start(); err != nil {
		logrus.Fatalf("failed to start storage manager: %v", err)
	}
	if err := alertMgr.Start(); err != nil {
		logrus.Fatalf("failed to start alert manager: %v", err)
	}
	if err := onvifEventMgr.Start(); err != nil {
		logrus.Fatalf("failed to start ONVIF event subscription manager: %v", err)
	}

	gin.SetMode(cfg.Server.Mode)

	dist, derr := consoleDist()
	if derr != nil {
		logrus.Warnf("embedded console assets unavailable: %v", derr)
	}

	apiServer := api.NewServer(cfg, cameraMgr, storageMgr, alertMgr, dist)
	apiServer.SetRuntimeStorage(storageRT)
	apiServer.SetConfigPath(configPath)
	apiServer.SetVersionInfo(Version, BuildTime, GitCommit, startTime)

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.HTTPPort),
		Handler: apiServer.Router(),
	}

	go func() {
		logrus.Infof("HTTP server listening on %s:%d", cfg.Server.Host, cfg.Server.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Fatalf("HTTP server failed to start: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logrus.Info("shutting down server...")
	apiServer.Drain()

	drainSeconds := 25
	if v := os.Getenv("PF_DRAIN_SECONDS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			drainSeconds = p
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(drainSeconds)*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logrus.Errorf("HTTP server shutdown failed: %v", err)
	}
	cameraMgr.Stop()
	storageMgr.Stop()
	alertMgr.Stop()
	onvifEventMgr.Stop()
	ffmpegMgr.StopAll()
	database.Close()
	logrus.Info("server stopped")
}

func initLogging(cfg *config.Config) {
	level, err := logrus.ParseLevel(cfg.Logging.Level)
	if err != nil {
		level = logrus.InfoLevel
	}
	logrus.SetLevel(level)

	if cfg.Logging.Format == "json" {
		logrus.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339,
		})
	} else {
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	}

	// PlatFarm 服务日志仅走 stdout，由平台采集（不写本地文件 / lumberjack）。
	logrus.SetOutput(os.Stdout)
}

// resolveConfigPath 返回可选配置文件路径；找不到时返回空串（纯环境变量运行）。
func resolveConfigPath(args []string) string {
	if len(args) > 1 && args[1] != "" {
		return args[1]
	}
	for _, p := range []string{"config.yaml", "configs/config.yaml"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
