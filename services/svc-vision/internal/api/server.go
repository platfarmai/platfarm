package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/platfarmai/sdk/go/pfauth"

	"platfarm/svc-vision/internal/alert"
	"platfarm/svc-vision/internal/camera"
	"platfarm/svc-vision/internal/config"
	"platfarm/svc-vision/internal/database"
	"platfarm/svc-vision/internal/models"
	"platfarm/svc-vision/internal/storage"
	"platfarm/svc-vision/pkg/minio"
	"platfarm/svc-vision/pkg/webdav"
)

const mount = "/api/vision"

type Server struct {
	cfg            *config.Config
	cameraMgr      *camera.CameraManager
	storageMgr     *storage.Manager
	alertMgr       *alert.Manager
	router         *gin.Engine
	wsUpgrader     websocket.Upgrader
	runtimeStorage *storage.RuntimeStorage
	cfgPath        string

	version   string
	buildTime string
	gitCommit string
	startTime time.Time

	dist        fs.FS
	mediaSecret []byte
	draining    atomic.Bool
}

func (s *Server) SetVersionInfo(version, buildTime, gitCommit string, startTime time.Time) {
	s.version = version
	s.buildTime = buildTime
	s.gitCommit = gitCommit
	s.startTime = startTime
}

func (s *Server) SetRuntimeStorage(r *storage.RuntimeStorage) {
	s.runtimeStorage = r
}

func (s *Server) SetConfigPath(p string) {
	s.cfgPath = p
}

// Drain 标记服务进入排空状态，/readyz 随即返回 503，供平台滚动发布摘流。
func (s *Server) Drain() {
	s.draining.Store(true)
}

func NewServer(cfg *config.Config, cameraMgr *camera.CameraManager, storageMgr *storage.Manager, alertMgr *alert.Manager, dist fs.FS) *Server {
	s := &Server{
		cfg:         cfg,
		cameraMgr:   cameraMgr,
		storageMgr:  storageMgr,
		alertMgr:    alertMgr,
		dist:        dist,
		mediaSecret: []byte(os.Getenv("VISION_MEDIA_SECRET")),
		wsUpgrader: websocket.Upgrader{
			CheckOrigin:     func(r *http.Request) bool { return true },
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
	s.setupRoutes()
	return s
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

func (s *Server) setupRoutes() {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.loggerMiddleware())
	r.Use(s.corsMiddleware())

	// 健康检查在挂载点之外（平台直连容器 :8080 探活）。
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.GET("/readyz", func(c *gin.Context) {
		if s.draining.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "draining"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	g := r.Group(mount)

	// 公开：无需鉴权（service.yaml public_routes: GET /public/*）。
	g.GET("/public/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pong": true, "service": "svc-vision", "auth": "not required"})
	})

	// 身份自省：自行校验 JWT（契约测试断言 body.username）。
	g.GET("/me", s.handleMe)

	// 版本信息（网关已对非公开路由强制 JWT）。
	g.GET("/version", s.getVersion)

	// 内嵌控制台 SPA（service.yaml public_routes: GET /console/*）。
	s.mountConsole(g)

	secured := g.Group("")
	secured.Use(s.authMiddleware(), s.writeGuard())
	{
		secured.GET("/media-token", s.issueMediaToken)

		cameras := secured.Group("/cameras")
		{
			cameras.GET("", s.listCameras)
			cameras.POST("", s.createCamera)
			cameras.GET("/:id", s.getCamera)
			cameras.PUT("/:id", s.updateCamera)
			cameras.DELETE("/:id", s.deleteCamera)
			cameras.GET("/:id/status", s.getCameraStatus)
			cameras.POST("/:id/start", s.startCamera)
			cameras.POST("/:id/stop", s.stopCamera)
			cameras.POST("/:id/restart", s.restartCamera)
			cameras.POST("/:id/snapshot", s.takeSnapshot)
			cameras.POST("/:id/ptz", s.ptzControl)
			cameras.GET("/:id/snapshots", s.listSnapshots)
			cameras.GET("/discover", s.discoverCameras)
			cameras.POST("/discover/lan", s.discoverLAN)
			cameras.GET("/probe", s.probeONVIFCamera)
		}

		settings := secured.Group("/settings")
		{
			settings.GET("/storage", s.getStorageSettings)
			settings.PUT("/storage", s.updateStorageSettings)
			settings.GET("/camera", s.getCameraSettings)
			settings.PUT("/camera", s.updateCameraSettings)
			settings.GET("/defaults", s.getCameraDefaults)
			settings.PUT("/defaults", s.updateCameraDefaults)
			settings.POST("/webdav/test", s.testWebdav)
			settings.POST("/minio/test", s.testMinio)
		}

		recordings := secured.Group("/recordings")
		{
			recordings.GET("", s.listRecordings)
			recordings.GET("/:id", s.getRecording)
			recordings.DELETE("/:id", s.deleteRecording)
			recordings.GET("/camera/:cameraId", s.listCameraRecordings)
			recordings.GET("/camera/:cameraId/segments", s.getRecordingSegments)
		}

		snapshots := secured.Group("/snapshots")
		{
			snapshots.GET("", s.listAllSnapshots)
			snapshots.DELETE("", s.clearSnapshots)
			snapshots.DELETE("/:id", s.deleteSnapshot)
		}

		analytics := secured.Group("/analytics")
		{
			analytics.GET("/alerts", s.listAlerts)
			analytics.DELETE("/alerts", s.clearAlerts)
			analytics.GET("/alerts/:id", s.getAlert)
			analytics.PUT("/alerts/:id/ack", s.acknowledgeAlert)
			analytics.PUT("/alerts/:id/resolve", s.resolveAlert)
			analytics.DELETE("/alerts/:id", s.deleteAlert)
		}

		storageGroup := secured.Group("/storage")
		{
			storageGroup.GET("/stats", s.getStorageStats)
			storageGroup.POST("/cleanup", s.triggerCleanup)
		}

		alerts := secured.Group("/alerts")
		{
			alerts.GET("/config", s.getAlertConfig)
			alerts.PUT("/config", s.updateAlertConfig)
			alerts.POST("/test", s.sendTestAlert)
		}

		system := secured.Group("/system")
		{
			system.GET("/config", s.getSystemConfig)
			system.PUT("/config", s.updateSystemConfig)
			system.GET("/info", s.getSystemInfo)
			system.GET("/env", s.getEnvCheck)
		}
	}

	// 媒体段：公开挂载（service.yaml public_routes: GET /media/*），服务自校验短时签名 token。
	media := g.Group("/media")
	media.Use(s.mediaAuthMiddleware())
	{
		media.GET("/recordings/:id/file", s.getRecordingFile)
		media.HEAD("/recordings/:id/file", s.getRecordingFile)
		media.GET("/recordings/:id/download", s.downloadRecording)

		media.GET("/webdav/list", s.listWebdavFiles)
		media.GET("/webdav/file", s.streamWebdavFile)

		media.GET("/minio/list", s.listMinIOFiles)
		media.GET("/minio/file", s.streamMinIOFile)

		stream := media.Group("/stream")
		{
			stream.GET("/camera/:cameraId/hls", s.getHLSPlaylist)
			stream.GET("/camera/:cameraId/hls/*file", s.getHLSSegment)
			stream.GET("/camera/:cameraId/mp4", s.getMP4Segment)
			stream.GET("/camera/:cameraId/snapshot", s.getLatestSnapshot)
			stream.GET("/camera/:cameraId/snapshots/*file", s.getSnapshotFile)
			stream.GET("/camera/:cameraId/recordings/:recordingId/hls", s.getRecordingHLS)
		}

		media.GET("/ws", s.handleWebSocket)
		media.GET("/ws/camera/:cameraId", s.handleCameraWS)
	}

	s.router = r
}

// mountConsole 将内嵌 web/dist 以 SPA 方式挂到 /api/vision/console/*filepath（index.html 回退）。
func (s *Server) mountConsole(g *gin.RouterGroup) {
	if s.dist == nil {
		return
	}
	fileServer := http.FileServer(http.FS(s.dist))
	serveIndex := func(c *gin.Context) {
		data, err := fs.ReadFile(s.dist, "index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "index missing")
			return
		}
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}
	serve := func(c *gin.Context) {
		rel := strings.TrimPrefix(c.Param("filepath"), "/")
		if rel == "" {
			serveIndex(c)
			return
		}
		if st, err := fs.Stat(s.dist, rel); err != nil || st.IsDir() {
			serveIndex(c)
			return
		}
		req := c.Request.Clone(c.Request.Context())
		req.URL.Path = "/" + rel
		fileServer.ServeHTTP(c.Writer, req)
	}
	g.GET("/console", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, mount+"/console/") })
	g.GET("/console/*filepath", serve)
}

func (s *Server) loggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		logrus.WithFields(logrus.Fields{
			"method":   c.Request.Method,
			"path":     c.Request.URL.Path,
			"status":   c.Writer.Status(),
			"latency":  latency,
			"clientIP": c.ClientIP(),
		}).Info("HTTP Request")
	}
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

// mapRole 将 PlatFarm 平台角色收敛为服务内部角色：admin 保留，其余一律 viewer（只读 + 媒体回放）。
func mapRole(platformRole string) string {
	if platformRole == "admin" {
		return "admin"
	}
	return "viewer"
}

// authMiddleware 通过 vendored pfauth（RS256）校验网关转发的 JWT，并把身份写入 context。
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := pfauth.Identity(c.GetHeader("Authorization"), c.GetHeader("X-PF-User-Token"))
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}
		c.Set("username", claims.Username)
		c.Set("role", mapRole(claims.Role))
		c.Set("userId", claims.UserId)
		c.Next()
	}
}

// writeGuard 读写分级：GET/HEAD 对所有已登录用户放行；其余写/控制操作要求 admin。
func (s *Server) writeGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
			c.Next()
			return
		}
		if role, _ := c.Get("role"); role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin role required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) handleMe(c *gin.Context) {
	claims, err := pfauth.Identity(c.GetHeader("Authorization"), c.GetHeader("X-PF-User-Token"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service":  "svc-vision",
		"userId":   claims.UserId,
		"username": claims.Username,
		"role":     claims.Role,
		"tenantId": claims.TenantId,
		"svc":      claims.Svc,
	})
}

const mediaTokenTTL = 10 * time.Minute

// makeMediaToken 生成媒体短时签名：base64url(username|role|exp) + "." + hex(HMAC-SHA256(payload, VISION_MEDIA_SECRET))。
func (s *Server) makeMediaToken(username, role string, exp int64) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s|%s|%d", username, role, exp)))
	mac := hmac.New(sha256.New, s.mediaSecret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

func (s *Server) parseMediaToken(token string) (string, string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid token format")
	}
	mac := hmac.New(sha256.New, s.mediaSecret)
	mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) != 1 {
		return "", "", fmt.Errorf("invalid token signature")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("failed to decode token")
	}
	segs := strings.Split(string(payloadBytes), "|")
	if len(segs) != 3 {
		return "", "", fmt.Errorf("invalid token content")
	}
	exp, err := strconv.ParseInt(segs[2], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", fmt.Errorf("token expired")
	}
	return segs[0], segs[1], nil
}

func (s *Server) issueMediaToken(c *gin.Context) {
	username, _ := c.Get("username")
	role, _ := c.Get("role")
	exp := time.Now().Add(mediaTokenTTL).Unix()
	token := s.makeMediaToken(fmt.Sprint(username), fmt.Sprint(role), exp)
	c.JSON(http.StatusOK, gin.H{"token": token, "expiresAt": exp})
}

// mediaAuthMiddleware 校验媒体访问：优先 ?token= 的短时签名；回退接受完整 pfauth JWT（Authorization/query）。
func (s *Server) mediaAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Query("token")
		if token == "" {
			if h := c.GetHeader("Authorization"); h != "" {
				token = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing media token"})
			c.Abort()
			return
		}
		if username, role, err := s.parseMediaToken(token); err == nil {
			c.Set("username", username)
			c.Set("role", role)
			c.Next()
			return
		}
		if claims, err := pfauth.Identity("Bearer "+token, c.GetHeader("X-PF-User-Token")); err == nil {
			c.Set("username", claims.Username)
			c.Set("role", mapRole(claims.Role))
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired media token"})
		c.Abort()
	}
}

func (s *Server) getVersion(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":   s.version,
		"buildTime": s.buildTime,
		"gitCommit": s.gitCommit,
		"name":      s.cfg.Server.Name,
	})
}

func (s *Server) listCameras(c *gin.Context) {
	cameras, err := s.cameraMgr.ListCameras()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	previewDefault := s.cameraMgr.NormalizePreviewSrc("")
	for i := range cameras {
		cameras[i].Password = ""
		cameras[i].PTZSupported = s.cameraMgr.PTZCapability(cameras[i].ID)
		cameras[i].PreviewDefault = previewDefault
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  cameras,
		"total": len(cameras),
	})
}

type cameraRequest struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	Protocol          string `json:"protocol"`
	IP                string `json:"ip"`
	Port              int    `json:"port"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	Path              string `json:"path"`
	OnvifAddress      string `json:"onvif_address"`
	OnvifProfileToken string `json:"onvif_profile_token"`

	Manufacturer string  `json:"manufacturer"`
	Model        string  `json:"model"`
	Firmware     string  `json:"firmware"`
	SerialNumber string  `json:"serial_number"`
	DeviceID     *string `json:"device_id"`

	RecordEnabled  *bool  `json:"record_enabled"`
	RecordSchedule string `json:"record_schedule"`
	RecordType     string `json:"record_type"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	FPS            int    `json:"fps"`
	Bitrate        int    `json:"bitrate"`
	Codec          string `json:"codec"`
	PTZEnabled     *bool  `json:"ptz_enabled"`
}

func (r *cameraRequest) toModel() *models.Camera {
	rec := false
	if r.RecordEnabled != nil {
		rec = *r.RecordEnabled
	}
	ptz := false
	if r.PTZEnabled != nil {
		ptz = *r.PTZEnabled
	}
	return &models.Camera{
		Name:              r.Name,
		Description:       r.Description,
		Protocol:          r.Protocol,
		IP:                r.IP,
		Port:              r.Port,
		Username:          r.Username,
		Password:          r.Password,
		Path:              r.Path,
		OnvifAddress:      r.OnvifAddress,
		OnvifProfileToken: r.OnvifProfileToken,
		Manufacturer:      r.Manufacturer,
		Model:             r.Model,
		Firmware:          r.Firmware,
		SerialNumber:      r.SerialNumber,
		DeviceID:          r.DeviceID,
		RecordEnabled:     rec,
		RecordSchedule:    r.RecordSchedule,
		RecordType:        r.RecordType,
		Width:             r.Width,
		Height:            r.Height,
		FPS:               r.FPS,
		Bitrate:           r.Bitrate,
		Codec:             r.Codec,
		PTZEnabled:        ptz,
	}
}

// applyCameraDefaults 请求省略的字段用配置的默认值补齐（settings/defaults，DB 持久化）。
func (s *Server) applyCameraDefaults(req *cameraRequest, cam *models.Camera) {
	d := s.cfg.Camera.Defaults
	if req.RecordEnabled == nil {
		cam.RecordEnabled = d.RecordEnabled
	}
	if req.RecordType == "" && d.RecordType != "" {
		cam.RecordType = d.RecordType
	}
	if req.Width == 0 && d.Width > 0 {
		cam.Width = d.Width
	}
	if req.Height == 0 && d.Height > 0 {
		cam.Height = d.Height
	}
	if req.FPS == 0 && d.FPS > 0 {
		cam.FPS = d.FPS
	}
	if req.Bitrate == 0 && d.Bitrate > 0 {
		cam.Bitrate = d.Bitrate
	}
	if req.Codec == "" && d.Codec != "" {
		cam.Codec = d.Codec
	}
}

func (s *Server) createCamera(c *gin.Context) {
	var req cameraRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cam := req.toModel()
	s.applyCameraDefaults(&req, cam)

	if cam.Protocol == "rtsp" && cam.RecordType == "motion" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "RTSP cameras do not support motion-detection recording (it relies on ONVIF event reporting); please choose continuous or scheduled recording"})
		return
	}

	if cam.Protocol != "gb28181" && (cam.DeviceID == nil || *cam.DeviceID == "") {
		cam.DeviceID = nil
	}

	if cam.Protocol == "rtsp" {
		rtspURL := camera.BuildRTSPURL(cam)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "ffprobe",
			"-v", "error",
			"-select_streams", "v:0",
			"-show_entries", "stream=codec_name,width,height,r_frame_rate",
			"-of", "json",
			"-rtsp_transport", "tcp",

			rtspURL,
		)

		output, err := cmd.Output()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("camera connection test failed: %v, please check the IP/port/path/credentials are correct", err),
			})
			return
		}

		var probeResult map[string]interface{}
		if err := json.Unmarshal(output, &probeResult); err != nil || len(probeResult) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "camera connection test failed: unable to parse video stream information"})
			return
		}
	}

	if err := s.cameraMgr.CreateCamera(cam); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cam.Password = ""
	c.JSON(http.StatusCreated, cam)
}

func (s *Server) getCamera(c *gin.Context) {
	id := c.Param("id")
	cam, err := s.cameraMgr.GetCamera(parseUint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "camera not found"})
		return
	}
	cam.Password = ""
	cam.PTZSupported = s.cameraMgr.PTZCapability(parseUint(id))
	cam.PreviewDefault = s.cameraMgr.NormalizePreviewSrc("")
	c.JSON(http.StatusOK, cam)
}

func (s *Server) updateCamera(c *gin.Context) {
	id := c.Param("id")
	var req cameraRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	camID := parseUint(id)

	existing, err := s.cameraMgr.GetCamera(camID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "camera not found"})
		return
	}

	cam := req.toModel()
	cam.ID = existing.ID
	cam.CreatedAt = existing.CreatedAt
	cam.UpdatedAt = existing.UpdatedAt
	cam.Status = existing.Status
	cam.LastOnline = existing.LastOnline
	cam.ErrorMsg = existing.ErrorMsg

	if cam.Name == "" {
		cam.Name = existing.Name
	}
	if cam.Protocol == "" {
		cam.Protocol = existing.Protocol
	}
	if cam.Username == "" {
		cam.Username = existing.Username
	}
	if cam.IP == "" {
		cam.IP = existing.IP
	}
	if cam.Port == 0 {
		cam.Port = existing.Port
	}
	if cam.Path == "" {
		cam.Path = existing.Path
	}
	if req.OnvifAddress == "" {
		cam.OnvifAddress = existing.OnvifAddress
	}
	if req.OnvifProfileToken == "" {
		cam.OnvifProfileToken = existing.OnvifProfileToken
	}

	if req.Manufacturer == "" {
		cam.Manufacturer = existing.Manufacturer
	}
	if req.Model == "" {
		cam.Model = existing.Model
	}
	if req.Firmware == "" {
		cam.Firmware = existing.Firmware
	}
	if req.SerialNumber == "" {
		cam.SerialNumber = existing.SerialNumber
	}
	if cam.RecordSchedule == "" {
		cam.RecordSchedule = existing.RecordSchedule
	}
	if cam.RecordType == "" {
		cam.RecordType = existing.RecordType
	}
	if cam.Codec == "" {
		cam.Codec = existing.Codec
	}
	if cam.Width == 0 {
		cam.Width = existing.Width
	}
	if cam.Height == 0 {
		cam.Height = existing.Height
	}
	if cam.FPS == 0 {
		cam.FPS = existing.FPS
	}
	if cam.Bitrate == 0 {
		cam.Bitrate = existing.Bitrate
	}

	if req.RecordEnabled == nil {
		cam.RecordEnabled = existing.RecordEnabled
	}
	if req.PTZEnabled == nil {
		cam.PTZEnabled = existing.PTZEnabled
	}

	cam.DiscoveredStreamUri = existing.DiscoveredStreamUri
	cam.StreamUriUpdatedAt = existing.StreamUriUpdatedAt
	if cam.OnvifProfileToken == existing.OnvifProfileToken {
		cam.DiscoveredStreamUri = existing.DiscoveredStreamUri
	} else {

		cam.DiscoveredStreamUri = ""
		cam.StreamUriUpdatedAt = nil
	}

	if req.Password == "" {
		cam.Password = existing.Password
	}

	if err := s.cameraMgr.UpdateCamera(cam); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cam.Password = ""
	c.JSON(http.StatusOK, cam)
}

func (s *Server) deleteCamera(c *gin.Context) {
	id := c.Param("id")
	if err := s.cameraMgr.DeleteCamera(parseUint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

func (s *Server) getCameraStatus(c *gin.Context) {
	id := c.Param("id")
	inst, ok := s.cameraMgr.GetCameraStatus(parseUint(id))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "camera not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"camera_id":       inst.Model.ID,
		"name":            inst.Model.Name,
		"status":          inst.Status,
		"last_error":      inst.LastError,
		"reconnect_count": inst.ReconnectCnt,
		"is_streaming":    inst.Stream != nil && inst.Stream.IsLive(),
	})
}

func (s *Server) startCamera(c *gin.Context) {
	id := parseUint(c.Param("id"))
	cam, err := s.cameraMgr.GetCamera(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "camera not found"})
		return
	}
	cam.RecordEnabled = true
	if err := s.cameraMgr.UpdateCamera(cam); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "started successfully"})
}

func (s *Server) stopCamera(c *gin.Context) {
	id := parseUint(c.Param("id"))
	cam, err := s.cameraMgr.GetCamera(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "camera not found"})
		return
	}
	cam.RecordEnabled = false
	if err := s.cameraMgr.UpdateCamera(cam); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "stopped successfully"})
}

func (s *Server) restartCamera(c *gin.Context) {
	id := parseUint(c.Param("id"))
	if err := s.cameraMgr.RestartCamera(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "restarted successfully"})
}

func (s *Server) takeSnapshot(c *gin.Context) {
	id := parseUint(c.Param("id"))

	path, err := s.cameraMgr.Snapshot(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := s.cameraMgr.SaveSnapshot(id, path, "manual"); err != nil {
		logrus.Warnf("failed to save snapshot record: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{"path": path})
}

func (s *Server) getSnapshotFile(c *gin.Context) {
	cameraID := c.Param("cameraId")
	file := strings.TrimPrefix(c.Param("file"), "/")

	if file == "" || strings.Contains(file, "..") ||
		!strings.HasPrefix(file, "snapshot_") || !strings.HasSuffix(file, ".jpg") {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Content-Type", "image/jpeg")
	c.File("./recordings/camera_" + cameraID + "/" + file)
}

type storageSettingsRequest struct {
	RootPath        string               `json:"root_path"`
	SegmentDuration int                  `json:"segment_duration"`
	MaxDays         int                  `json:"max_days"`
	MaxStorageGB    *float64             `json:"max_storage_gb"`
	CleanupInterval int                  `json:"cleanup_interval"`
	Webdav          *config.WebdavConfig `json:"webdav"`
	MinIO           *config.MinIOConfig  `json:"minio"`
}

type webdavTestRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	BasePath string `json:"base_path"`
}

type minioTestRequest struct {
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Bucket    string `json:"bucket"`
	UseSSL    bool   `json:"use_ssl"`
	BasePath  string `json:"base_path"`
}

func (s *Server) currentStorageSettings() config.LocalStorageConfig {
	if s.runtimeStorage != nil {
		return s.runtimeStorage.GetLocal()
	}
	return s.cfg.Storage.Local
}

func (s *Server) currentWebdavSettings() config.WebdavConfig {
	if s.runtimeStorage != nil {
		return s.runtimeStorage.GetWebdav()
	}
	return s.cfg.Storage.Webdav
}

func (s *Server) currentMinIOSettings() config.MinIOConfig {
	if s.runtimeStorage != nil {
		return s.runtimeStorage.GetMinIO()
	}
	return s.cfg.Storage.MinIO
}

func (s *Server) listWebdavFiles(c *gin.Context) {
	wd := s.currentWebdavSettings()
	if !wd.Enabled || wd.URL == "" {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "files": []interface{}{}})
		return
	}

	cameraID := strings.TrimSpace(c.Query("camera_id"))
	rel := strings.Trim(strings.TrimSpace(wd.BasePath), "/")
	if cameraID != "" {

		rel = "camera_" + cameraID
		if rel2 := strings.Trim(wd.BasePath, "/"); rel2 != "" {
			rel = rel2 + "/" + rel
		}
	}

	client := webdav.NewClient(wd.URL, wd.Username, wd.Password)
	entries, err := client.List(rel)
	if err != nil {

		if strings.Contains(err.Error(), "404") {
			c.JSON(http.StatusOK, gin.H{"enabled": true, "camera_id": cameraID, "files": []interface{}{}})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read WebDAV directory: " + err.Error()})
		return
	}

	type webdavFileItem struct {
		Name    string    `json:"name"`
		Path    string    `json:"path"`
		Size    int64     `json:"size"`
		ModTime time.Time `json:"mod_time"`
	}
	var files []webdavFileItem
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(strings.ToLower(e.Name), ".mp4") {
			continue
		}
		full := e.Name
		if base := strings.Trim(wd.BasePath, "/"); base != "" {
			if cameraID != "" {
				full = base + "/camera_" + cameraID + "/" + e.Name
			} else {
				full = base + "/" + e.Name
			}
		}
		files = append(files, webdavFileItem{Name: e.Name, Path: full, Size: e.Size, ModTime: e.ModTime})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
	if files == nil {
		files = []webdavFileItem{}
	}
	c.JSON(http.StatusOK, gin.H{"enabled": true, "camera_id": cameraID, "files": files})
}

func isMinioNoSuchKey(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NoSuchKey")
}

func validateWebdavRelPath(basePath, rel string) error {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return fmt.Errorf("path must not be empty")
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return fmt.Errorf("invalid path")
		}
	}
	base := strings.Trim(strings.TrimSpace(basePath), "/")
	if base == "" {
		return nil
	}
	if rel != base && !strings.HasPrefix(rel, base+"/") {
		return fmt.Errorf("path must be within the WebDAV base directory")
	}
	return nil
}

func (s *Server) streamWebdavFile(c *gin.Context) {
	wd := s.currentWebdavSettings()
	if !wd.Enabled || wd.URL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "WebDAV is not enabled"})
		return
	}
	rel := c.Query("path")
	if err := validateWebdavRelPath(wd.BasePath, rel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client := webdav.NewClient(wd.URL, wd.Username, wd.Password)
	resp, err := client.Get(c.Request.Context(), rel, c.GetHeader("Range"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read WebDAV file: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "file does not exist on WebDAV"})
		return
	}
	c.Status(resp.StatusCode)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	} else {
		c.Header("Content-Type", "video/mp4")
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		c.Header("Content-Range", cr)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		c.Header("Content-Length", cl)
	}
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", filepath.Base(rel)))
	io.Copy(c.Writer, resp.Body)
}

func (s *Server) getStorageSettings(c *gin.Context) {
	loc := s.currentStorageSettings()
	wd := s.currentWebdavSettings()
	mn := s.currentMinIOSettings()

	wdSet := wd
	if wd.Password != "" {
		wdSet.Password = "********"
	}
	mnSet := mn
	if mn.SecretKey != "" {
		mnSet.SecretKey = "********"
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"root_path":        loc.RootPath,
			"segment_duration": loc.SegmentDuration,
			"max_days":         loc.MaxDays,
			"max_storage_gb":   loc.MaxStorageGB,
			"cleanup_interval": loc.CleanupInterval,
			"webdav":           wdSet,
			"minio":            mnSet,
		},
	})
}

func (s *Server) updateStorageSettings(c *gin.Context) {
	var req storageSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters: " + err.Error()})
		return
	}

	loc := s.currentStorageSettings()
	wd := s.currentWebdavSettings()
	mio := s.currentMinIOSettings()

	if req.RootPath != "" {
		if strings.Contains(req.RootPath, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage path"})
			return
		}
		loc.RootPath = req.RootPath
	}
	if req.SegmentDuration >= 30 && req.SegmentDuration <= 86400 {
		loc.SegmentDuration = req.SegmentDuration
	}
	if req.MaxDays >= 1 && req.MaxDays <= 365 {
		loc.MaxDays = req.MaxDays
	}
	if req.MaxStorageGB != nil {
		if *req.MaxStorageGB < 0 || *req.MaxStorageGB > 100000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "max storage usage must be between 0 and 100000 GB (0 means unlimited)"})
			return
		}
		loc.MaxStorageGB = *req.MaxStorageGB
	}
	if req.CleanupInterval >= 300 {
		loc.CleanupInterval = req.CleanupInterval
	}

	if req.Webdav != nil {
		w := *req.Webdav

		if w.Password == "" || w.Password == "********" {
			w.Password = wd.Password
		}

		if w.MaxDays < 0 || w.MaxDays > 3650 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "WebDAV remote retention days must be between 0 and 3650 (0 means no time-based auto deletion)"})
			return
		}
		if w.MaxStorageGB < 0 || w.MaxStorageGB > 100000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "WebDAV remote max usage must be between 0 and 100000 GB (0 means unlimited)"})
			return
		}
		wd = w
	}

	if req.MinIO != nil {
		mn := *req.MinIO

		if mn.SecretKey == "" || mn.SecretKey == "********" {
			mn.SecretKey = mio.SecretKey
		}

		mn.Endpoint = strings.TrimPrefix(strings.TrimPrefix(mn.Endpoint, "http://"), "https://")
		mn.Endpoint = strings.Trim(mn.Endpoint, "/")

		if mn.MaxDays < 0 || mn.MaxDays > 3650 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "MinIO remote retention days must be between 0 and 3650 (0 means no time-based auto deletion)"})
			return
		}
		if mn.MaxStorageGB < 0 || mn.MaxStorageGB > 100000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "MinIO remote max usage must be between 0 and 100000 GB (0 means unlimited)"})
			return
		}
		mio = mn
	}

	if s.runtimeStorage != nil {
		s.runtimeStorage.SetLocal(loc)
		s.runtimeStorage.SetWebdav(wd)
		s.runtimeStorage.SetMinIO(mio)
	}
	s.cfg.Storage.Local = loc
	s.cfg.Storage.Webdav = wd
	s.cfg.Storage.MinIO = mio

	for key, v := range map[string]any{
		database.SettingStorageLocal: loc,
		database.SettingWebdav:       wd,
		database.SettingMinIO:        mio,
	} {
		if err := database.SaveSetting(key, v); err != nil {
			logrus.Warnf("failed to persist %s to DB: %v", key, err)
		}
	}

	if err := s.persistConfig(); err != nil {
		logrus.Warnf("failed to persist settings to config.yaml: %v (this change is only in effect in memory and will be reverted after restart)", err)
	}

	if err := os.MkdirAll(loc.RootPath, 0755); err != nil {
		logrus.Warnf("failed to create storage root directory %s: %v", loc.RootPath, err)
	}

	logrus.Infof("storage settings updated: root=%s segment=%ds maxdays=%d maxstorage=%vGB webdav.enabled=%v",
		loc.RootPath, loc.SegmentDuration, loc.MaxDays, loc.MaxStorageGB, wd.Enabled)

	c.JSON(http.StatusOK, gin.H{"message": "saved successfully (segment duration takes effect for new connections/reconnections)"})
}

func (s *Server) getCameraSettings(c *gin.Context) {
	enabled, interval := s.cameraMgr.GetSnapshotSettings()
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"snapshot_enabled":  enabled,
			"snapshot_interval": interval,
		},
	})
}

func (s *Server) updateCameraSettings(c *gin.Context) {
	var req struct {
		SnapshotEnabled  *bool `json:"snapshot_enabled"`
		SnapshotInterval *int  `json:"snapshot_interval"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters: " + err.Error()})
		return
	}

	enabled, interval := s.cameraMgr.GetSnapshotSettings()
	if req.SnapshotEnabled != nil {
		enabled = *req.SnapshotEnabled
	}
	if req.SnapshotInterval != nil {
		if *req.SnapshotInterval < 30 || *req.SnapshotInterval > 86400 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "snapshot interval must be between 30 and 86400 seconds"})
			return
		}
		interval = *req.SnapshotInterval
	}

	s.cameraMgr.SetSnapshotSettings(enabled, interval)
	s.cfg.Camera.SnapshotEnabled = enabled
	s.cfg.Camera.SnapshotInterval = interval
	if err := database.SaveSetting(database.SettingCameraSnapshot,
		database.SnapshotSetting{Enabled: enabled, Interval: interval}); err != nil {
		logrus.Warnf("failed to persist snapshot settings to DB: %v", err)
	}
	if err := s.persistConfig(); err != nil {
		logrus.Debugf("config.yaml write skipped (container mode): %v", err)
	}

	logrus.Infof("scheduled snapshot settings updated: enabled=%v interval=%ds", enabled, interval)
	c.JSON(http.StatusOK, gin.H{"message": "saved successfully (scheduled snapshots take effect immediately)"})
}

// getCameraDefaults 新建摄像头默认值（UI 可改，DB 持久化）。
func (s *Server) getCameraDefaults(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": s.cfg.Camera.Defaults})
}

func (s *Server) updateCameraDefaults(c *gin.Context) {
	var req struct {
		RecordEnabled *bool   `json:"record_enabled"`
		RecordType    *string `json:"record_type"`
		Width         *int    `json:"width"`
		Height        *int    `json:"height"`
		FPS           *int    `json:"fps"`
		Bitrate       *int    `json:"bitrate"`
		Codec         *string `json:"codec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	d := &s.cfg.Camera.Defaults
	if req.RecordEnabled != nil {
		d.RecordEnabled = *req.RecordEnabled
	}
	if req.RecordType != nil {
		switch *req.RecordType {
		case "continuous", "motion", "schedule":
			d.RecordType = *req.RecordType
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid record type"})
			return
		}
	}
	if req.Width != nil && *req.Width >= 320 && *req.Width <= 8192 {
		d.Width = *req.Width
	}
	if req.Height != nil && *req.Height >= 240 && *req.Height <= 8192 {
		d.Height = *req.Height
	}
	if req.FPS != nil && *req.FPS >= 1 && *req.FPS <= 60 {
		d.FPS = *req.FPS
	}
	if req.Bitrate != nil && *req.Bitrate >= 512 && *req.Bitrate <= 20480 {
		d.Bitrate = *req.Bitrate
	}
	if req.Codec != nil && (*req.Codec == "h264" || *req.Codec == "h265") {
		d.Codec = *req.Codec
	}

	if err := database.SaveSetting(database.SettingCameraDefaults, *d); err != nil {
		logrus.Warnf("failed to persist camera defaults to DB: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist settings"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "saved", "data": *d})
}

func (s *Server) testWebdav(c *gin.Context) {
	var req webdavTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}
	client := webdav.NewClient(req.URL, req.Username, req.Password)
	if err := client.TestAndUpload(req.BasePath); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "WebDAV connection successful, read and write work normally"})
}

func (s *Server) testMinio(c *gin.Context) {
	var req minioTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}
	endpoint := strings.TrimPrefix(strings.TrimPrefix(req.Endpoint, "http://"), "https://")
	endpoint = strings.Trim(endpoint, "/")
	if endpoint == "" || req.Bucket == "" {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": "endpoint and bucket must not be empty"})
		return
	}
	client, err := minio.NewClient(endpoint, req.AccessKey, req.SecretKey, req.Bucket, req.UseSSL)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if err := client.TestAndUpload(req.BasePath); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "MinIO connection successful, bucket is readable and writable"})
}

func (s *Server) listMinIOFiles(c *gin.Context) {
	mn := s.currentMinIOSettings()
	if !mn.Enabled || mn.Endpoint == "" || mn.Bucket == "" {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "files": []interface{}{}})
		return
	}

	cameraID := strings.TrimSpace(c.Query("camera_id"))
	prefix := strings.Trim(strings.TrimSpace(mn.BasePath), "/")
	if cameraID != "" {

		prefix = "camera_" + cameraID
		if b := strings.Trim(mn.BasePath, "/"); b != "" {
			prefix = b + "/" + prefix
		}
	}
	if prefix != "" {
		prefix += "/"
	}

	client, err := minio.NewClient(mn.Endpoint, mn.AccessKey, mn.SecretKey, mn.Bucket, mn.UseSSL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create MinIO client: " + err.Error()})
		return
	}
	entries, err := client.List(prefix)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to list MinIO objects: " + err.Error()})
		return
	}

	type minioFileItem struct {
		Name    string    `json:"name"`
		Path    string    `json:"path"`
		Size    int64     `json:"size"`
		ModTime time.Time `json:"mod_time"`
	}
	var files []minioFileItem
	for _, e := range entries {
		if !strings.HasSuffix(strings.ToLower(e.Key), ".mp4") {
			continue
		}
		files = append(files, minioFileItem{
			Name:    filepath.Base(e.Key),
			Path:    e.Key,
			Size:    e.Size,
			ModTime: e.ModTime,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
	if files == nil {
		files = []minioFileItem{}
	}
	c.JSON(http.StatusOK, gin.H{"enabled": true, "camera_id": cameraID, "files": files})
}

func (s *Server) streamMinIOFile(c *gin.Context) {
	mn := s.currentMinIOSettings()
	if !mn.Enabled || mn.Endpoint == "" || mn.Bucket == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MinIO is not enabled"})
		return
	}
	key := c.Query("path")
	if err := validateWebdavRelPath(mn.BasePath, key); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := minio.NewClient(mn.Endpoint, mn.AccessKey, mn.SecretKey, mn.Bucket, mn.UseSSL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create MinIO client: " + err.Error()})
		return
	}
	res, err := client.Get(c.Request.Context(), key, c.GetHeader("Range"))
	if err != nil {
		if isMinioNoSuchKey(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file does not exist on MinIO"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read MinIO file: " + err.Error()})
		return
	}
	defer res.Body.Close()

	c.Status(http.StatusOK)
	if res.Start > 0 || res.End < res.TotalSize-1 {
		c.Status(http.StatusPartialContent)
		c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", res.Start, res.End, res.TotalSize))
	}
	c.Header("Content-Type", "video/mp4")
	c.Header("Content-Length", strconv.FormatInt(res.End-res.Start+1, 10))
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", filepath.Base(key)))
	io.Copy(c.Writer, res.Body)
}

func (s *Server) persistConfig() error {
	if s.cfgPath == "" {
		return fmt.Errorf("config path not configured")
	}
	data, err := yaml.Marshal(s.cfg)
	if err != nil {
		return err
	}
	tmp := s.cfgPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.cfgPath)
}

func (s *Server) ptzControl(c *gin.Context) {
	id := parseUint(c.Param("id"))
	var req struct {
		Command string  `json:"command" binding:"required"`
		Speed   float64 `json:"speed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.cameraMgr.PTZControl(id, req.Command, req.Speed); err != nil {

		switch {
		case errors.Is(err, camera.ErrCameraNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, camera.ErrPTZNotSupported):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, camera.ErrCameraOffline):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusBadGateway, gin.H{"error": "PTZ control failed: " + err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "PTZ command sent"})
}

func (s *Server) listSnapshots(c *gin.Context) {
	id := parseUint(c.Param("id"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "12"))

	snaps, total, err := s.cameraMgr.ListSnapshots(id, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if snaps == nil {
		snaps = []models.Snapshot{}
	}
	c.JSON(http.StatusOK, gin.H{"data": snaps, "total": total})
}

func (s *Server) listAllSnapshots(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}

	db := database.GetDB().Model(&models.Snapshot{}).Preload("Camera")
	if cameraID := c.Query("camera_id"); cameraID != "" {
		if id, err := strconv.ParseUint(cameraID, 10, 64); err == nil {
			db = db.Where("camera_id = ?", id)
		}
	}
	if start := c.Query("start"); start != "" {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", start, time.Local); err == nil {
			db = db.Where("timestamp >= ?", t)
		}
	}
	if end := c.Query("end"); end != "" {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", end, time.Local); err == nil {
			db = db.Where("timestamp <= ?", t)
		}
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var snaps []models.Snapshot
	if err := db.Order("timestamp DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&snaps).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type snapshotListItem struct {
		models.Snapshot
		CameraName string `json:"camera_name"`
	}
	items := make([]snapshotListItem, 0, len(snaps))
	for _, sn := range snaps {
		name := sn.Camera.Name
		items = append(items, snapshotListItem{Snapshot: sn, CameraName: name})
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "total": total})
}

func (s *Server) deleteSnapshot(c *gin.Context) {
	id := parseUint(c.Param("id"))
	var snap models.Snapshot
	if err := database.GetDB().First(&snap, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
		return
	}
	if err := database.GetDB().Delete(&models.Snapshot{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	full := snap.FilePath
	if !filepath.IsAbs(full) {
		full = "./" + full
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		logrus.Warnf("failed to delete snapshot file %s: %v", full, err)
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (s *Server) clearSnapshots(c *gin.Context) {
	var snaps []models.Snapshot
	if err := database.GetDB().Unscoped().Where("id > 0").Find(&snaps).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	deleted := 0
	for _, snap := range snaps {
		full := snap.FilePath
		if !filepath.IsAbs(full) {
			full = "./" + full
		}
		if err := os.Remove(full); err == nil {
			deleted++
		} else if !os.IsNotExist(err) {
			logrus.Warnf("failed to delete snapshot file %s: %v", full, err)
		}
	}
	if err := database.GetDB().Unscoped().Where("id > 0").Delete(&models.Snapshot{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	logrus.Infof("all snapshots cleared: %d records, %d files deleted", len(snaps), deleted)
	c.JSON(http.StatusOK, gin.H{"message": "snapshots cleared", "deleted": len(snaps)})
}

func (s *Server) discoverCameras(c *gin.Context) {
	network := c.Query("network")
	if network == "" {
		// 未指定网段时优先用部署配置的扫描网段（VISION_SCAN_CIDRS）
		if cidrs := s.cameraMgr.ScanCIDRList(); len(cidrs) > 0 {
			network = cidrs[0]
		} else {
			network = "192.168.1.0/24"
		}
	}
	devices, err := s.cameraMgr.DiscoverONVIFCameras(network)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": devices})
}

func (s *Server) discoverLAN(c *gin.Context) {
	var req struct {
		Timeout int `json:"timeout"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {

		req.Timeout = 10
	}
	if req.Timeout <= 0 {
		req.Timeout = 10
	}
	if req.Timeout > 30 {
		req.Timeout = 30
	}

	devices, err := s.cameraMgr.DiscoverLAN(req.Timeout)
	if err != nil {
		// 容器桥接网络通常收不到 WS-Discovery 多播应答：降级为空结果（前端提示"未发现设备"），
		// 用户可改用「按 IP 探测 ONVIF」（unicast，容器内可用）或手动填写 RTSP 地址。
		logrus.Warnf("LAN discovery unavailable (likely container network without multicast): %v", err)
		c.JSON(http.StatusOK, gin.H{"data": []any{}, "warning": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": devices})
}

func (s *Server) probeONVIFCamera(c *gin.Context) {
	ip := c.Query("ip")
	if ip == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing ip parameter"})
		return
	}

	username := c.Query("username")
	password := c.Query("password")

	device, err := s.cameraMgr.ProbeONVIFCamera(ip, username, password)
	if err != nil {

		authRequired := strings.Contains(err.Error(), "authentication is required")
		c.JSON(http.StatusOK, gin.H{"device": nil, "auth_required": authRequired, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"device": device, "auth_required": false, "error": ""})
}

func parseTimeParam(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", time.RFC3339, "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (s *Server) listRecordings(c *gin.Context) {
	cameraID, _ := strconv.ParseUint(c.Query("camera_id"), 10, 32)
	recordType := c.Query("record_type")
	start, _ := parseTimeParam(c.Query("start"))
	end, _ := parseTimeParam(c.Query("end"))

	recs, total, err := storage.NewRecordingManager().QueryRecordings(uint(cameraID), start, end, recordType, 1, 5000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": recs, "total": total})
}

func (s *Server) getRecording(c *gin.Context) {
	id := parseUint(c.Param("id"))
	rec, err := storage.NewRecordingManager().GetRecordingByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recording not found"})
		return
	}
	c.JSON(http.StatusOK, rec)
}

func (s *Server) downloadRecording(c *gin.Context) {
	id := parseUint(c.Param("id"))
	rec, err := storage.NewRecordingManager().GetRecordingByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recording not found"})
		return
	}
	full := rec.FilePath
	if !filepath.IsAbs(full) {
		full = "./" + full
	}
	if _, err := os.Stat(full); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recording file does not exist"})
		return
	}
	c.FileAttachment(full, filepath.Base(full))
}

func (s *Server) getRecordingFile(c *gin.Context) {
	id := parseUint(c.Param("id"))
	rec, err := storage.NewRecordingManager().GetRecordingByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recording not found"})
		return
	}
	full := rec.FilePath
	if !filepath.IsAbs(full) {
		full = "./" + full
	}
	if _, err := os.Stat(full); err == nil {

		c.Header("Accept-Ranges", "bytes")
		c.File(full)
		return
	}

	if rec.StoragePath != "" {
		if wd := s.currentWebdavSettings(); wd.Enabled && wd.URL != "" {
			if err := validateWebdavRelPath(wd.BasePath, rec.StoragePath); err == nil {
				client := webdav.NewClient(wd.URL, wd.Username, wd.Password)
				resp, gerr := client.Get(c.Request.Context(), rec.StoragePath, c.GetHeader("Range"))
				if gerr == nil {
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
						c.Status(resp.StatusCode)
						if ct := resp.Header.Get("Content-Type"); ct != "" {
							c.Header("Content-Type", ct)
						} else {
							c.Header("Content-Type", "video/mp4")
						}
						if cr := resp.Header.Get("Content-Range"); cr != "" {
							c.Header("Content-Range", cr)
						}
						if cl := resp.Header.Get("Content-Length"); cl != "" {
							c.Header("Content-Length", cl)
						}
						c.Header("Accept-Ranges", "bytes")
						io.Copy(c.Writer, resp.Body)
						return
					}
					if resp.StatusCode != http.StatusNotFound {
						c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read recording from WebDAV"})
						return
					}
				} else {
					logrus.Warnf("WebDAV fallback playback failed id=%d: %v", id, gerr)
				}
			}
		}

		if mn := s.currentMinIOSettings(); mn.Enabled && mn.Endpoint != "" && mn.Bucket != "" {
			if err := validateWebdavRelPath(mn.BasePath, rec.StoragePath); err == nil {
				mc, merr := minio.NewClient(mn.Endpoint, mn.AccessKey, mn.SecretKey, mn.Bucket, mn.UseSSL)
				if merr == nil {
					res, gerr := mc.Get(c.Request.Context(), rec.StoragePath, c.GetHeader("Range"))
					if gerr == nil {
						defer res.Body.Close()
						if res.Start > 0 || res.End < res.TotalSize-1 {
							c.Status(http.StatusPartialContent)
							c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", res.Start, res.End, res.TotalSize))
						} else {
							c.Status(http.StatusOK)
						}
						c.Header("Content-Type", "video/mp4")
						c.Header("Content-Length", strconv.FormatInt(res.End-res.Start+1, 10))
						c.Header("Accept-Ranges", "bytes")
						io.Copy(c.Writer, res.Body)
						return
					}
					if !isMinioNoSuchKey(gerr) {
						c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read recording from MinIO"})
						return
					}
				} else {
					logrus.Warnf("MinIO fallback playback failed id=%d: %v", id, merr)
				}
			}
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "recording file does not exist"})
}

func (s *Server) deleteRecording(c *gin.Context) {
	id := parseUint(c.Param("id"))
	rm := storage.NewRecordingManager()
	rec, err := rm.GetRecordingByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recording not found"})
		return
	}

	full := rec.FilePath
	if !filepath.IsAbs(full) {
		full = "./" + full
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		logrus.Warnf("failed to delete recording file %s: %v", full, err)
	}

	if rec.StoragePath != "" {
		if wd := s.runtimeStorage.GetWebdav(); wd.Enabled && wd.URL != "" {
			remotePath := filepath.Join(wd.BasePath, rec.StoragePath)
			if err := webdav.NewClient(wd.URL, wd.Username, wd.Password).Delete(remotePath); err == nil {
				logrus.Infof("deleted WebDAV remote recording: %s", remotePath)
			}
		}
	}

	if err := rm.DeleteRecording(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

func (s *Server) listCameraRecordings(c *gin.Context) {
	cameraID := parseUint(c.Param("cameraId"))
	recordType := c.Query("record_type")
	start, _ := parseTimeParam(c.Query("start"))
	end, _ := parseTimeParam(c.Query("end"))

	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}

	recs, total, err := storage.NewRecordingManager().QueryRecordings(cameraID, start, end, recordType, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": recs, "total": total})
}

func (s *Server) getRecordingSegments(c *gin.Context) {
	cameraID := parseUint(c.Param("cameraId"))
	start, _ := parseTimeParam(c.Query("start"))
	end, _ := parseTimeParam(c.Query("end"))

	recs, err := storage.NewRecordingManager().GetRecordingSegments(cameraID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": recs})
}

func (s *Server) getHLSPlaylist(c *gin.Context) {
	cameraID := c.Param("cameraId")

	stream := c.Query("stream")
	if err := s.cameraMgr.EnsurePreview(parseUint(cameraID), stream); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to start preview stream: " + err.Error()})
		return
	}
	file := "./recordings/camera_" + cameraID + "/index.m3u8"

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(file); err == nil {
			break
		}
		if time.Now().After(deadline) {
			c.JSON(http.StatusNotFound, gin.H{"error": "playlist does not exist, preview stream start timed out"})
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "playlist does not exist, preview stream may not be ready"})
		return
	}

	token := c.Query("token")
	if token == "" {
		if h := c.GetHeader("Authorization"); h != "" {
			token = strings.TrimPrefix(h, "Bearer ")
		}
	}
	tokenSuffix := ""
	if token != "" {
		tokenSuffix = "?token=" + url.QueryEscape(token)
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if !strings.HasPrefix(trimmed, "hls/") {
				trimmed = "hls/" + trimmed
			}
			lines[i] = trimmed + tokenSuffix
		}
	}
	c.Header("Content-Type", "application/vnd.apple.mpegurl")
	c.Data(http.StatusOK, "application/vnd.apple.mpegurl", []byte(strings.Join(lines, "\n")))
}

func (s *Server) getHLSSegment(c *gin.Context) {
	cameraID := c.Param("cameraId")

	s.cameraMgr.TouchPreview(parseUint(cameraID))
	file := strings.TrimPrefix(c.Param("file"), "/")
	if file == "" || strings.Contains(file, "..") {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Content-Type", "video/mp2t")
	c.File("./recordings/camera_" + cameraID + "/" + file)
}

func (s *Server) getMP4Segment(c *gin.Context) {
	cameraID := c.Param("cameraId")

	c.Header("Content-Type", "video/mp4")
	c.File("./recordings/camera_" + cameraID + "/segment_latest.mp4")
}

func (s *Server) getLatestSnapshot(c *gin.Context) {
	cameraID := c.Param("cameraId")

	matches, _ := filepath.Glob("./recordings/camera_" + cameraID + "/snapshot_*.jpg")
	if len(matches) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no snapshots available"})
		return
	}

	sort.Strings(matches)
	c.Header("Content-Type", "image/jpeg")
	c.File(matches[len(matches)-1])
}

func (s *Server) getRecordingHLS(c *gin.Context) {

	c.Header("Content-Type", "application/vnd.apple.mpegurl")
	c.String(http.StatusOK, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-ENDLIST\n")
}

func (s *Server) listAlerts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}

	query := database.GetDB().Model(&models.Alert{})

	if v := c.Query("camera_id"); v != "" {
		if id, err := strconv.ParseUint(v, 10, 64); err == nil {
			query = query.Where("camera_id = ?", id)
		}
	}
	if v := c.Query("type"); v != "" {
		query = query.Where("type = ?", v)
	}
	if v := c.Query("level"); v != "" {
		query = query.Where("level = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	start, hasStart := parseTimeParam(c.Query("start"))
	end, hasEnd := parseTimeParam(c.Query("end"))
	if hasStart {
		query = query.Where("created_at >= ?", start)
	}
	if hasEnd {
		query = query.Where("created_at <= ?", end)
	}

	var total int64
	query.Count(&total)

	var alerts []models.Alert
	if err := query.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&alerts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if alerts == nil {
		alerts = []models.Alert{}
	}
	al := LocaleFromContext(c)
	for i := range alerts {
		alerts[i].Message = LocalizeAlert(al, alerts[i].Message)
	}

	c.JSON(http.StatusOK, gin.H{"data": alerts, "total": total})
}

func (s *Server) getAlert(c *gin.Context) {
	l := LocaleFromContext(c)
	id := parseUint(c.Param("id"))
	var alert models.Alert
	if err := database.GetDB().First(&alert, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": TEnv(l, "alert.notfound")})
		return
	}
	alert.Message = LocalizeAlert(l, alert.Message)
	c.JSON(http.StatusOK, alert)
}

func (s *Server) acknowledgeAlert(c *gin.Context) {
	l := LocaleFromContext(c)
	id := parseUint(c.Param("id"))
	var alert models.Alert
	if err := database.GetDB().First(&alert, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": TEnv(l, "alert.notfound")})
		return
	}
	now := time.Now()

	usernameVal, _ := c.Get("username")
	username, _ := usernameVal.(string)
	_ = username

	updates := map[string]interface{}{
		"status":   models.AlertStatusAcknowledged,
		"acked_at": &now,
	}
	if err := database.GetDB().Model(&alert).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": TEnv(l, "alert.acked")})
}

func (s *Server) resolveAlert(c *gin.Context) {
	l := LocaleFromContext(c)
	id := parseUint(c.Param("id"))
	var alert models.Alert
	if err := database.GetDB().First(&alert, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": TEnv(l, "alert.notfound")})
		return
	}
	now := time.Now()
	updates := map[string]interface{}{
		"status":      models.AlertStatusResolved,
		"resolved_at": &now,
	}
	if err := database.GetDB().Model(&alert).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": TEnv(l, "alert.resolved")})
}

func (s *Server) deleteAlert(c *gin.Context) {
	l := LocaleFromContext(c)
	id := parseUint(c.Param("id"))
	if err := database.GetDB().Delete(&models.Alert{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": TEnv(l, "alert.deleted")})
}

func (s *Server) clearAlerts(c *gin.Context) {
	l := LocaleFromContext(c)
	result := database.GetDB().Unscoped().Where("id > 0").Delete(&models.Alert{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": TEnv(l, "alert.cleared"), "deleted": result.RowsAffected})
}

func (s *Server) getStorageStats(c *gin.Context) {
	stats := s.storageMgr.GetStats()
	c.JSON(http.StatusOK, stats)
}

func (s *Server) triggerCleanup(c *gin.Context) {

	s.storageMgr.TriggerCleanup()
	c.JSON(http.StatusOK, gin.H{"message": "cleanup completed (checked by retention days and max storage usage)"})
}

func (s *Server) getAlertConfig(c *gin.Context) {
	c.JSON(http.StatusOK, s.cfg.Alert)
}

func (s *Server) updateAlertConfig(c *gin.Context) {

	var req struct {
		Enabled  *bool `json:"enabled"`
		Channels *struct {
			Webhook *config.WebhookAlertConfig `json:"webhook"`
			Email   *config.EmailAlertConfig   `json:"email"`
			SMS     *config.SMSAlertConfig     `json:"sms"`
		} `json:"channels"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Enabled != nil {
		s.cfg.Alert.Enabled = *req.Enabled
	}
	if req.Channels != nil {
		if req.Channels.Webhook != nil {
			s.cfg.Alert.Channels.Webhook = *req.Channels.Webhook
		}
		if req.Channels.Email != nil {
			s.cfg.Alert.Channels.Email = *req.Channels.Email
		}
		if req.Channels.SMS != nil {
			s.cfg.Alert.Channels.SMS = *req.Channels.SMS
		}
	}

	if err := database.SaveSetting(database.SettingAlert, s.cfg.Alert); err != nil {
		logrus.Warnf("failed to persist alert config to DB: %v", err)
	}

	if err := s.persistConfig(); err != nil {
		logrus.Warnf("failed to persist alert config: %v (this change is only in effect in memory and will be reverted after restart)", err)
	}

	c.JSON(http.StatusOK, gin.H{"message": "alert config updated"})
}

func (s *Server) sendTestAlert(c *gin.Context) {

	var req struct {
		Channel string                     `json:"channel" binding:"required"`
		Webhook *config.WebhookAlertConfig `json:"webhook"`
		Email   *config.EmailAlertConfig   `json:"email"`
		SMS     *config.SMSAlertConfig     `json:"sms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var override *config.AlertConfig
	if req.Webhook != nil || req.Email != nil || req.SMS != nil {
		base := s.cfg.Alert
		if req.Webhook != nil {
			base.Channels.Webhook = *req.Webhook
		}
		if req.Email != nil {
			base.Channels.Email = *req.Email
		}
		if req.SMS != nil {
			base.Channels.SMS = *req.SMS
		}
		override = &base
	}

	if err := s.alertMgr.SendTestAlert(req.Channel, override); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "test alert sent"})
}

func (s *Server) getSystemConfig(c *gin.Context) {

	c.JSON(http.StatusOK, gin.H{
		"server": gin.H{
			"name":      s.cfg.Server.Name,
			"host":      s.cfg.Server.Host,
			"http_port": s.cfg.Server.HTTPPort,
			"ws_port":   s.cfg.Server.WSPort,
			"mode":      s.cfg.Server.Mode,
			"log_level": s.cfg.Logging.Level,
		},
		"storage": s.cfg.Storage,
		"camera":  s.cfg.Camera,
	})
}

func (s *Server) updateSystemConfig(c *gin.Context) {
	var req struct {
		Server struct {
			Name     string `json:"name"`
			HTTPPort int    `json:"http_port"`
			WSPort   int    `json:"ws_port"`
			Mode     string `json:"mode"`
			LogLevel string `json:"log_level"`
		} `json:"server"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	srv := req.Server
	if srv.HTTPPort < 1 || srv.HTTPPort > 65535 || srv.WSPort < 1 || srv.WSPort > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid port number"})
		return
	}
	if srv.Mode != "" && srv.Mode != "debug" && srv.Mode != "release" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run mode"})
		return
	}
	if lvl := srv.LogLevel; lvl != "" {
		if _, err := logrus.ParseLevel(lvl); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid log level"})
			return
		}
	}

	restartRequired := false
	if srv.Name != s.cfg.Server.Name {
		s.cfg.Server.Name = srv.Name
	}
	if srv.HTTPPort != s.cfg.Server.HTTPPort || srv.WSPort != s.cfg.Server.WSPort || srv.Mode != s.cfg.Server.Mode {
		s.cfg.Server.HTTPPort = srv.HTTPPort
		s.cfg.Server.WSPort = srv.WSPort
		s.cfg.Server.Mode = srv.Mode
		restartRequired = true
	}
	if srv.LogLevel != s.cfg.Logging.Level {
		s.cfg.Logging.Level = srv.LogLevel
		if lvl, err := logrus.ParseLevel(srv.LogLevel); err == nil {
			logrus.SetLevel(lvl)
		}
	}

	if err := database.SaveSetting(database.SettingServer,
		database.ServerSetting{Name: s.cfg.Server.Name, LogLevel: s.cfg.Logging.Level}); err != nil {
		logrus.Warnf("failed to persist server settings to DB: %v", err)
	}
	if s.cfgPath != "" {
		if err := s.cfg.Save(s.cfgPath); err != nil {
			logrus.Debugf("config.yaml write skipped (container mode): %v", err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "config updated", "restart_required": restartRequired})
}

func (s *Server) getSystemInfo(c *gin.Context) {

	var memTotalKB, memAvailKB int64
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			var v int64
			fmt.Sscanf(fields[1], "%d", &v)
			switch fields[0] {
			case "MemTotal:":
				memTotalKB = v
			case "MemAvailable:":
				memAvailKB = v
			}
		}
	}

	diskPath := s.cfg.Storage.Local.RootPath
	if diskPath == "" {
		diskPath = "."
	}
	diskTotalMB, diskUsedMB := diskUsageMB(diskPath)

	var cameraCount, recordingCount int64
	if db := database.GetDB(); db != nil {
		db.Model(&models.Camera{}).Count(&cameraCount)
		db.Model(&models.Recording{}).Count(&recordingCount)
	}

	var logSize int64
	if p := s.logFilePath(); p != "" {
		if fi, err := os.Stat(p); err == nil {
			logSize = fi.Size()
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"version":         s.version,
		"build_time":      s.buildTime,
		"git_commit":      s.gitCommit,
		"go_version":      runtime.Version(),
		"os":              runtime.GOOS,
		"arch":            runtime.GOARCH,
		"pid":             os.Getpid(),
		"start_time":      s.startTime.Unix(),
		"uptime":          int64(time.Since(s.startTime).Seconds()),
		"cpu_count":       runtime.NumCPU(),
		"mem_total_mb":    memTotalKB / 1024,
		"mem_used_mb":     (memTotalKB - memAvailKB) / 1024,
		"disk_total_mb":   diskTotalMB,
		"disk_used_mb":    diskUsedMB,
		"disk_path":       diskPath,
		"camera_count":    cameraCount,
		"recording_count": recordingCount,
		"log_size":        logSize,
		"config_path":     s.cfgPath,
	})
}

func (s *Server) logFilePath() string {
	p := s.cfg.Logging.Output
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	return p
}

func (s *Server) handleWebSocket(c *gin.Context) {
	conn, err := s.wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logrus.Errorf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	conn.WriteJSON(gin.H{"type": "welcome", "message": "Connected to surveillance system"})

	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}

		s.handleWSMessage(conn, msg)
	}
}

func (s *Server) handleCameraWS(c *gin.Context) {
	cameraID := parseUint(c.Param("cameraId"))
	conn, err := s.wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.WriteJSON(gin.H{"type": "subscribed", "camera_id": cameraID})

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (s *Server) handleWSMessage(conn *websocket.Conn, msg map[string]interface{}) {
	msgType, _ := msg["type"].(string)
	switch msgType {
	case "ping":
		conn.WriteJSON(gin.H{"type": "pong"})
	case "subscribe_camera":
		if camID, ok := msg["camera_id"].(float64); ok {
			conn.WriteJSON(gin.H{"type": "subscribed", "camera_id": uint(camID)})
		}
	case "unsubscribe_camera":
	}
}

func parseUint(s string) uint {
	var id uint
	fmt.Sscanf(s, "%d", &id)
	return id
}
