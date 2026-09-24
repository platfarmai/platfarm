// svc-jobs — Platfarm 异步任务队列 + cron 调度能力服务。
// 提交方：service token（白名单）或 admin 用户；调度定义仅 admin 可管理。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

const mount = "/api/jobs"

var (
	db       *pgxpool.Pool
	draining atomic.Bool
)

func main() {
	mustLoadPub()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustEnv("JOBS_DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	db = pool
	waitDB(ctx)
	if err := runMigrations(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	wctx, wcancel := context.WithCancel(ctx)
	go deliveryWorker(wctx)
	go schedulerWorker(wctx)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/readyz", func(c *gin.Context) {
		if draining.Load() {
			c.JSON(503, gin.H{"error": "draining"})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})

	r.GET(mount+"/me", func(c *gin.Context) {
		claims, err := identity(c)
		if err != nil {
			c.JSON(401, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"service": "svc-jobs", "userId": claims.UserId,
			"username": claims.Username, "role": claims.Role, "tenantId": claims.TenantId})
	})

	api := r.Group(mount, func(c *gin.Context) {
		claims, err := identity(c)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": err.Error()})
			return
		}
		c.Set("claims", claims)
	})
	api.POST("/enqueue", handleEnqueue)
	api.GET("/list", handleList)
	api.POST("/schedules", handleCreateSchedule)
	api.GET("/schedules", handleListSchedules)
	api.PATCH("/schedules/:id", handleUpdateSchedule)
	api.DELETE("/schedules/:id", handleDeleteSchedule)
	api.GET("/:id", handleGetJob)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("svc-jobs listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	draining.Store(true)
	wcancel()
	sctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}

func claimsOf(c *gin.Context) *Claims { return c.MustGet("claims").(*Claims) }

// senderIdentity 提交权限：service token（网关外不可达 + 白名单）或 admin 用户。
func senderIdentity(claims *Claims) (string, bool) {
	if claims.TokenType == "service" {
		return claims.Svc, true
	}
	if claims.Role == "admin" {
		return claims.Username, true
	}
	return "", false
}

func handleEnqueue(c *gin.Context) {
	claims := claimsOf(c)
	sender, ok := senderIdentity(claims)
	if !ok {
		c.JSON(403, gin.H{"error": "service token or admin required"})
		return
	}
	var in struct {
		Type        string `json:"type"`
		Payload     string `json:"payload"`
		Webhook     string `json:"webhook"`
		RunAt       string `json:"runAt"`
		MaxAttempts int    `json:"maxAttempts"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Type == "" || in.Webhook == "" {
		c.JSON(400, gin.H{"error": "type and webhook required"})
		return
	}
	if !strings.HasPrefix(in.Webhook, "http://") && !strings.HasPrefix(in.Webhook, "https://") {
		c.JSON(400, gin.H{"error": "webhook must start with http:// or https://"})
		return
	}
	runAt := time.Now()
	if in.RunAt != "" {
		t, perr := time.Parse(time.RFC3339, in.RunAt)
		if perr != nil {
			c.JSON(400, gin.H{"error": "runAt must be RFC3339"})
			return
		}
		runAt = t
	}
	maxAttempts := in.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	var id int64
	err := db.QueryRow(c.Request.Context(),
		`INSERT INTO jobs (type, payload, webhook, run_at, max_attempts, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		in.Type, in.Payload, in.Webhook, runAt, maxAttempts, sender).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": "insert failed"})
		return
	}
	c.JSON(200, gin.H{"id": id, "status": "pending"})
}

func handleGetJob(c *gin.Context) {
	claims := claimsOf(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "bad id"})
		return
	}
	type jobRow struct {
		ID          int64  `json:"id"`
		Type        string `json:"type"`
		Payload     string `json:"payload"`
		Webhook     string `json:"webhook"`
		Status      string `json:"status"`
		Attempts    int    `json:"attempts"`
		MaxAttempts int    `json:"maxAttempts"`
		RunAt       string `json:"runAt"`
		LastError   string `json:"lastError"`
		CreatedBy   string `json:"createdBy"`
		CreatedAt   string `json:"createdAt"`
	}
	var j jobRow
	err = db.QueryRow(c.Request.Context(),
		`SELECT id, type, payload, webhook, status, attempts, max_attempts,
		        run_at::text, last_error, created_by, created_at::text
		 FROM jobs WHERE id=$1`, id).
		Scan(&j.ID, &j.Type, &j.Payload, &j.Webhook, &j.Status, &j.Attempts, &j.MaxAttempts,
			&j.RunAt, &j.LastError, &j.CreatedBy, &j.CreatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "job not found"})
		return
	}
	if !canView(claims, j.CreatedBy) {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(200, j)
}

// canView admin / service token 可看任意；否则仅创建者本人。
func canView(claims *Claims, createdBy string) bool {
	if claims.Role == "admin" || claims.TokenType == "service" {
		return true
	}
	return claims.Username != "" && claims.Username == createdBy
}

func handleList(c *gin.Context) {
	claims := claimsOf(c)
	if claims.Role != "admin" && claims.TokenType != "service" {
		c.JSON(403, gin.H{"error": "admin or service required"})
		return
	}
	status := c.Query("status")
	query := `SELECT id, type, webhook, status, attempts, max_attempts, last_error, created_by, created_at::text
		 FROM jobs`
	args := []any{}
	if status != "" {
		query += ` WHERE status=$1`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT 100`
	rows, qerr := db.Query(c.Request.Context(), query, args...)
	if qerr != nil {
		c.JSON(500, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	type row struct {
		ID          int64  `json:"id"`
		Type        string `json:"type"`
		Webhook     string `json:"webhook"`
		Status      string `json:"status"`
		Attempts    int    `json:"attempts"`
		MaxAttempts int    `json:"maxAttempts"`
		LastError   string `json:"lastError"`
		CreatedBy   string `json:"createdBy"`
		CreatedAt   string `json:"createdAt"`
	}
	items := []row{}
	for rows.Next() {
		var m row
		if rows.Scan(&m.ID, &m.Type, &m.Webhook, &m.Status, &m.Attempts, &m.MaxAttempts,
			&m.LastError, &m.CreatedBy, &m.CreatedAt) == nil {
			items = append(items, m)
		}
	}
	c.JSON(200, gin.H{"items": items})
}

func handleCreateSchedule(c *gin.Context) {
	claims := claimsOf(c)
	if claims.Role != "admin" {
		c.JSON(403, gin.H{"error": "admin required"})
		return
	}
	var in struct {
		Name    string `json:"name"`
		Cron    string `json:"cron"`
		Type    string `json:"type"`
		Payload string `json:"payload"`
		Webhook string `json:"webhook"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Name == "" || in.Cron == "" || in.Type == "" || in.Webhook == "" {
		c.JSON(400, gin.H{"error": "name, cron, type and webhook required"})
		return
	}
	if !strings.HasPrefix(in.Webhook, "http://") && !strings.HasPrefix(in.Webhook, "https://") {
		c.JSON(400, gin.H{"error": "webhook must start with http:// or https://"})
		return
	}
	if _, perr := cron.ParseStandard(in.Cron); perr != nil {
		c.JSON(400, gin.H{"error": "invalid cron expression"})
		return
	}
	var id int64
	err := db.QueryRow(c.Request.Context(),
		`INSERT INTO schedules (name, cron, type, payload, webhook)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		in.Name, in.Cron, in.Type, in.Payload, in.Webhook).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": "insert failed (name must be unique)"})
		return
	}
	c.JSON(200, gin.H{"id": id, "enabled": true})
}

func handleListSchedules(c *gin.Context) {
	claims := claimsOf(c)
	if claims.Role != "admin" {
		c.JSON(403, gin.H{"error": "admin required"})
		return
	}
	rows, err := db.Query(c.Request.Context(),
		`SELECT id, name, cron, type, payload, webhook, enabled,
		        COALESCE(last_run_at::text,''), created_at::text
		 FROM schedules ORDER BY id DESC LIMIT 100`)
	if err != nil {
		c.JSON(500, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	type row struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Cron      string `json:"cron"`
		Type      string `json:"type"`
		Payload   string `json:"payload"`
		Webhook   string `json:"webhook"`
		Enabled   bool   `json:"enabled"`
		LastRunAt string `json:"lastRunAt"`
		CreatedAt string `json:"createdAt"`
	}
	items := []row{}
	for rows.Next() {
		var m row
		if rows.Scan(&m.ID, &m.Name, &m.Cron, &m.Type, &m.Payload, &m.Webhook, &m.Enabled,
			&m.LastRunAt, &m.CreatedAt) == nil {
			items = append(items, m)
		}
	}
	c.JSON(200, gin.H{"items": items})
}

func handleUpdateSchedule(c *gin.Context) {
	claims := claimsOf(c)
	if claims.Role != "admin" {
		c.JSON(403, gin.H{"error": "admin required"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "bad id"})
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "enabled required"})
		return
	}
	tag, err := db.Exec(c.Request.Context(),
		`UPDATE schedules SET enabled=$2 WHERE id=$1`, id, in.Enabled)
	if err != nil || tag.RowsAffected() == 0 {
		c.JSON(404, gin.H{"error": "schedule not found"})
		return
	}
	c.JSON(200, gin.H{"id": id, "enabled": in.Enabled})
}

func handleDeleteSchedule(c *gin.Context) {
	claims := claimsOf(c)
	if claims.Role != "admin" {
		c.JSON(403, gin.H{"error": "admin required"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "bad id"})
		return
	}
	tag, err := db.Exec(c.Request.Context(), `DELETE FROM schedules WHERE id=$1`, id)
	if err != nil || tag.RowsAffected() == 0 {
		c.JSON(404, gin.H{"error": "schedule not found"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ── infra helpers ────────────────────────────────────────────

func waitDB(ctx context.Context) {
	for i := 0; i < 30; i++ {
		if err := db.Ping(ctx); err == nil {
			return
		}
		log.Printf("waiting for database (%d/30)", i+1)
		time.Sleep(2 * time.Second)
	}
	log.Fatal("database unreachable")
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("%s is required", name)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
