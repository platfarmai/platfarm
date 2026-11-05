package api

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"platfarm/svc-vision/internal/config"
	"platfarm/svc-vision/pkg/minio"
	"platfarm/svc-vision/pkg/webdav"

	"github.com/gin-gonic/gin"
)

const (
	envOK   = "ok"
	envWarn = "warn"
	envFail = "fail"
)

type CheckItem struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
	Required bool   `json:"required"`
}

type EnvReport struct {
	OK         bool        `json:"ok"`
	CriticalOK bool        `json:"critical_ok"`
	Checks     []CheckItem `json:"checks"`
}

func (r *EnvReport) compute() {
	r.OK = true
	r.CriticalOK = true
	for _, c := range r.Checks {
		if c.Status == envFail {
			r.OK = false
			if c.Required {
				r.CriticalOK = false
			}
		} else if c.Status == envWarn {
			r.OK = false
		}
	}
}

func (s *Server) runEnvChecks() EnvReport {
	return s.runEnvChecksL(localeZH)
}

func (s *Server) runEnvChecksL(l string) EnvReport {
	var report EnvReport
	report.Checks = []CheckItem{}

	if v, err := runVersionCmd("ffmpeg"); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name: "ffmpeg", Status: envFail, Required: true,
			Detail: TEnv(l, "env.ffmpeg.fail"),
			Fix:    TEnv(l, "env.ffmpeg.fix"),
		})
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name: "ffmpeg", Status: envOK, Detail: v,
		})
	}

	if v, err := runVersionCmd("ffprobe"); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name: "ffprobe", Status: envFail, Required: true,
			Detail: TEnv(l, "env.ffprobe.fail"),
			Fix:    TEnv(l, "env.ffprobe.fix"),
		})
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name: "ffprobe", Status: envOK, Detail: v,
		})
	}

	recRoot := s.currentStorageSettings().RootPath
	if recRoot == "" {
		recRoot = "./recordings"
	}
	if err := os.MkdirAll(recRoot, 0755); err != nil || checkDirWritable(recRoot) != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name: TEnv(l, "env.recdir.name"), Status: envFail, Required: true,
			Detail: TEnv(l, "env.recdir.fail", recRoot),
			Fix:    TEnv(l, "env.recdir.fix", recRoot, recRoot),
		})
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name: TEnv(l, "env.recdir.name"), Status: envOK, Detail: recRoot,
		})
	}

	logPath := s.cfg.Logging.Output
	if logPath != "" {
		logDir := filepath.Dir(logPath)
		if err := os.MkdirAll(logDir, 0755); err != nil || checkDirWritable(logDir) != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.logdir.name"), Status: envFail, Required: true,
				Detail: TEnv(l, "env.logdir.fail", logDir),
				Fix:    TEnv(l, "env.logdir.fix", logDir, logDir),
			})
		} else {
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.logdir.name"), Status: envOK, Detail: logDir,
			})
		}
	}

	if free, err := diskFreeBytes(recRoot); err == nil {
		const gb = 1024 * 1024 * 1024
		switch {
		case free < 500*1024*1024:
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.disk.name"), Status: envFail, Required: true,
				Detail: TEnv(l, "env.disk.fail", formatBytes(free)),
				Fix:    TEnv(l, "env.disk.failfix"),
			})
		case free < 2*gb:
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.disk.name"), Status: envWarn,
				Detail: TEnv(l, "env.disk.warn", formatBytes(free)),
				Fix:    TEnv(l, "env.disk.warnfix"),
			})
		default:
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.disk.name"), Status: envOK, Detail: TEnv(l, "env.disk.ok", formatBytes(free)),
			})
		}
	}

	if wd := s.currentWebdavSettings(); wd.Enabled && wd.URL != "" {
		if err := checkWebdavReachable(wd.URL, wd.Username, wd.Password, wd.BasePath); err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.webdav.name"), Status: envWarn,
				Detail: err.Error(),
				Fix:    TEnv(l, "env.webdav.fix"),
			})
		} else {
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.webdav.name"), Status: envOK, Detail: wd.URL,
			})
		}
	}

	if mn := s.currentMinIOSettings(); mn.Enabled && mn.Endpoint != "" {
		if err := checkMinioReachable(mn); err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.minio.name"), Status: envWarn,
				Detail: err.Error(),
				Fix:    TEnv(l, "env.minio.fix"),
			})
		} else {
			report.Checks = append(report.Checks, CheckItem{
				Name: TEnv(l, "env.minio.name"), Status: envOK, Detail: mn.Endpoint + " / " + mn.Bucket,
			})
		}
	}

	report.compute()
	return report
}

func checkWebdavReachable(url, username, password, basePath string) error {
	c := webdav.NewClient(url, username, password)
	return c.CheckWithTimeout(basePath, 5*time.Second)
}

func checkMinioReachable(mnCfg config.MinIOConfig) error {
	client, err := minio.NewClient(mnCfg.Endpoint, mnCfg.AccessKey, mnCfg.SecretKey, mnCfg.Bucket, mnCfg.UseSSL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.EnsureBucket(ctx)
}

func (s *Server) getEnvCheck(c *gin.Context) {
	c.JSON(200, s.runEnvChecksL(LocaleFromContext(c)))
}

func runVersionCmd(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, "-version").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line), nil
		}
	}
	return "", fmt.Errorf("cannot parse version output")
}

func checkDirWritable(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("directory does not exist: %w", err)
	}
	f, err := os.CreateTemp(dir, ".envcheck_*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
