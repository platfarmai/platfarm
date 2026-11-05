package database

import (
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"platfarm/svc-vision/internal/config"

	"github.com/sirupsen/logrus"
)

var DB *gorm.DB

// Init 连接 PlatFarm 分配的独立 Postgres 库（DSN 由环境变量 VISION_DATABASE_URL 提供）。
// schema 由 specs/015 迁移运行器（根包 migrate.go）在启动时自应用，此处不做 AutoMigrate。
func Init(cfg *config.Config) error {
	dsn := os.Getenv("VISION_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("VISION_DATABASE_URL is required")
	}

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:      logger.Default.LogMode(logLevel(cfg.Logging.Level)),
		PrepareStmt: true,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying connection: %w", err)
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)

	logrus.Info("database connection established")
	return nil
}

func logLevel(level string) logger.LogLevel {
	switch level {
	case "debug":
		return logger.Info
	case "info":
		return logger.Warn
	case "warn":
		return logger.Warn
	case "error":
		return logger.Error
	default:
		return logger.Warn
	}
}

func GetDB() *gorm.DB {
	return DB
}

func Close() error {
	if DB != nil {
		sqlDB, err := DB.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}
