// svc-ai — 平台 AI 管理中心（specs/029）：Anthropic Messages 透传网关 + provider/别名/虚拟 key/用量管理。
package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"embed"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/platfarmai/sdk/go/pfauth"
)

const (
	mount         = "/api/ai"
	consolePrefix = mount + "/console"
)

//go:embed all:web/dist
var distFS embed.FS

var draining atomic.Bool

func requireUser(c *gin.Context) {
	claims, err := pfauth.Identity(c.GetHeader("Authorization"), c.GetHeader("X-PF-User-Token"))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.Set("role", claims.Role)
	c.Next()
}

func requireAdmin(c *gin.Context) {
	if c.GetString("role") != "admin" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要 admin 角色"})
	}
}

// seedProvider：providers 表为空且配置了种子 key 时自动建默认 provider（dev 降级路径）。
func seedProvider(ctx context.Context, db *pgxpool.Pool) {
	key := os.Getenv("AI_SEED_ANTHROPIC_KEY")
	if key == "" {
		return
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil || n > 0 {
		return
	}
	base := os.Getenv("AI_SEED_ANTHROPIC_BASE_URL")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	if _, err := db.Exec(ctx, `INSERT INTO providers (name, kind, base_url, api_key)
		VALUES ('anthropic-default','anthropic',$1,$2)`, base, key); err == nil {
		log.Println("seeded default anthropic provider from env")
	}
}

func main() {
	pfauth.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbURL := os.Getenv("AI_DATABASE_URL")
	if dbURL == "" {
		log.Fatal("AI_DATABASE_URL 未配置")
	}
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()
	if err := runMigrations(ctx, db); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	seedProvider(ctx, db)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.GET("/readyz", func(c *gin.Context) {
		if draining.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "draining"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.GET(mount+"/me", func(c *gin.Context) {
		claims, err := pfauth.Identity(c.GetHeader("Authorization"), c.GetHeader("X-PF-User-Token"))
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"service": "svc-ai", "userId": claims.UserId,
			"username": claims.Username, "role": claims.Role})
	})

	// 网关路径：虚拟 key 鉴权（不依赖平台 JWT，内网直调可用）
	r.POST(mount+"/v1/messages", proxyMessages(db))

	admin := r.Group(mount+"/admin", requireUser, requireAdmin)
	admin.GET("/providers", listProviders(db))
	admin.POST("/providers", createProvider(db))
	admin.PATCH("/providers/:id", updateProvider(db))
	admin.DELETE("/providers/:id", deleteProvider(db))
	admin.GET("/aliases", listAliases(db))
	admin.POST("/aliases", createAlias(db))
	admin.DELETE("/aliases/:id", deleteAlias(db))
	admin.GET("/keys", listKeys(db))
	admin.POST("/keys", createKey(db))
	admin.PATCH("/keys/:id", updateKey(db))
	admin.DELETE("/keys/:id", deleteKey(db))
	admin.GET("/usage", usageSummary(db))

	mountConsole(r)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("svc-ai listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	draining.Store(true)
	n := 25
	if v := os.Getenv("PF_DRAIN_SECONDS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), time.Duration(n)*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}

// ── 内嵌管理台 SPA ───────────────────────────────────

func mountConsole(r *gin.Engine) {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	serveIndex := func(c *gin.Context) {
		data, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "console 未构建")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}
	serve := func(c *gin.Context) {
		rel := strings.TrimPrefix(strings.TrimPrefix(c.Request.URL.Path, consolePrefix), "/")
		if rel == "" {
			serveIndex(c)
			return
		}
		if st, err := fs.Stat(sub, rel); err != nil || st.IsDir() {
			serveIndex(c)
			return
		}
		req := c.Request.Clone(c.Request.Context())
		req.URL.Path = "/" + rel
		fileServer.ServeHTTP(c.Writer, req)
	}
	r.GET(consolePrefix, func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, consolePrefix+"/") })
	r.GET(consolePrefix+"/*filepath", serve)
}
