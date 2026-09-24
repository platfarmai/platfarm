// svc-notify — Platfarm 通知能力服务（specs/017）。
// 发送方：service token（白名单）或 admin 用户；站内信任意登录用户可读自己的。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

const mount = "/api/notify"

var (
	db       *pgxpool.Pool
	draining atomic.Bool
)

func main() {
	mustLoadPub()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustEnv("NOTIFY_DATABASE_URL"))
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
		c.JSON(200, gin.H{"service": "svc-notify", "userId": claims.UserId,
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
	api.POST("/send", handleSend)
	api.GET("/mine", handleMine)
	api.POST("/:id/read", handleMarkRead)
	api.GET("/outbox", handleOutbox)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("svc-notify listening on :8080")
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

// senderIdentity 发送权限：service token（网关外不可达 + 白名单）或 admin 用户。
func senderIdentity(claims *Claims) (string, bool) {
	if claims.TokenType == "service" {
		return claims.Svc, true
	}
	if claims.Role == "admin" {
		return claims.Username, true
	}
	return "", false
}

func handleSend(c *gin.Context) {
	claims := claimsOf(c)
	sender, ok := senderIdentity(claims)
	if !ok {
		c.JSON(403, gin.H{"error": "service token or admin required"})
		return
	}
	var in struct {
		Channel string `json:"channel"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if c.ShouldBindJSON(&in) != nil || in.To == "" {
		c.JSON(400, gin.H{"error": "channel and to required"})
		return
	}
	status := "pending"
	switch in.Channel {
	case "email", "webhook":
	case "inbox":
		status = "unread"
		if _, err := strconv.Atoi(in.To); err != nil {
			c.JSON(400, gin.H{"error": "inbox recipient must be a userId"})
			return
		}
	default:
		c.JSON(400, gin.H{"error": "channel must be email|webhook|inbox"})
		return
	}
	var id int64
	err := db.QueryRow(c.Request.Context(),
		`INSERT INTO notifications (channel, recipient, subject, body, status, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		in.Channel, in.To, in.Subject, in.Body, status, sender).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": "insert failed"})
		return
	}
	c.JSON(200, gin.H{"id": id, "status": status})
}

func handleMine(c *gin.Context) {
	claims := claimsOf(c)
	rows, err := db.Query(c.Request.Context(),
		`SELECT id, subject, body, status, created_at::text FROM notifications
		 WHERE channel='inbox' AND recipient=$1 ORDER BY id DESC LIMIT 100`,
		strconv.Itoa(claims.UserId))
	if err != nil {
		c.JSON(500, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	type msg struct {
		ID        int64  `json:"id"`
		Subject   string `json:"subject"`
		Body      string `json:"body"`
		Status    string `json:"status"`
		CreatedAt string `json:"createdAt"`
	}
	items := []msg{}
	for rows.Next() {
		var m msg
		if rows.Scan(&m.ID, &m.Subject, &m.Body, &m.Status, &m.CreatedAt) == nil {
			items = append(items, m)
		}
	}
	c.JSON(200, gin.H{"items": items})
}

func handleMarkRead(c *gin.Context) {
	claims := claimsOf(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "bad id"})
		return
	}
	tag, err := db.Exec(c.Request.Context(),
		`UPDATE notifications SET status='read' WHERE id=$1 AND channel='inbox' AND recipient=$2`,
		id, strconv.Itoa(claims.UserId))
	if err != nil || tag.RowsAffected() == 0 {
		c.JSON(404, gin.H{"error": "message not found"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// handleOutbox admin 运维视图：最近投递记录与状态。
func handleOutbox(c *gin.Context) {
	claims := claimsOf(c)
	if claims.Role != "admin" && claims.TokenType != "service" {
		c.JSON(403, gin.H{"error": "admin required"})
		return
	}
	rows, err := db.Query(c.Request.Context(),
		`SELECT id, channel, recipient, subject, status, attempts, last_error, created_by, created_at::text
		 FROM notifications WHERE channel IN ('email','webhook') ORDER BY id DESC LIMIT 100`)
	if err != nil {
		c.JSON(500, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	type row struct {
		ID        int64  `json:"id"`
		Channel   string `json:"channel"`
		Recipient string `json:"recipient"`
		Subject   string `json:"subject"`
		Status    string `json:"status"`
		Attempts  int    `json:"attempts"`
		LastError string `json:"lastError"`
		CreatedBy string `json:"createdBy"`
		CreatedAt string `json:"createdAt"`
	}
	items := []row{}
	for rows.Next() {
		var m row
		if rows.Scan(&m.ID, &m.Channel, &m.Recipient, &m.Subject, &m.Status, &m.Attempts,
			&m.LastError, &m.CreatedBy, &m.CreatedAt) == nil {
			items = append(items, m)
		}
	}
	c.JSON(200, gin.H{"items": items})
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
