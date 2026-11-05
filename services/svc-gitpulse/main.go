// svc-gitpulse — GitHub 提交分析与项目周期管理（接入约定见 docs/architecture-v2.md §2.2）。
// 定时轮询 commit 元数据 → 规则 + AI 分析 → 中文日报/周报 → issue 修复闭环与 lead time 核验。
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
	mount         = "/api/gitpulse"
	consolePrefix = mount + "/console"
)

//go:embed all:web/dist
var distFS embed.FS

var draining atomic.Bool

// requireUser：验签并把身份写入 gin context。
func requireUser(c *gin.Context) {
	claims, err := pfauth.Identity(c.GetHeader("Authorization"), c.GetHeader("X-PF-User-Token"))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.Set("userId", strconv.Itoa(claims.UserId))
	c.Set("username", claims.Username)
	c.Set("role", claims.Role)
	c.Next()
}

func requireAdmin(c *gin.Context) {
	if c.GetString("role") != "admin" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要 admin 角色"})
	}
}

func main() {
	pfauth.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbURL := os.Getenv("GITPULSE_DATABASE_URL")
	if dbURL == "" {
		log.Fatal("GITPULSE_DATABASE_URL 未配置")
	}
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()
	if err := runMigrations(ctx, db); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	ai := newAIClient()
	if ai.enabled() {
		log.Printf("AI enabled: model=%s", ai.model)
	} else {
		log.Printf("AI disabled（未配置 ANTHROPIC_API_KEY）：仅规则分析")
	}
	startPoller(ctx, db, ai)

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
		c.JSON(http.StatusOK, gin.H{"service": "svc-gitpulse", "userId": claims.UserId,
			"username": claims.Username, "role": claims.Role})
	})

	api := r.Group(mount, requireUser)
	api.GET("/overview", overviewHandler(db))
	api.GET("/repos", listReposHandler(db))
	api.POST("/repos", requireAdmin, createRepoHandler(db))
	api.PATCH("/repos/:id", requireAdmin, updateRepoHandler(db))
	api.DELETE("/repos/:id", requireAdmin, deleteRepoHandler(db))
	api.POST("/repos/:id/poll", pollRepoHandler(db, ai))
	api.POST("/repos/:id/analyze", analyzeRepoHandler(db, ai))
	api.GET("/repos/:id/commits", listCommitsHandler(db))
	api.GET("/repos/:id/analyses", listAnalysesHandler(db))
	api.GET("/repos/:id/trends", trendsHandler(db))
	api.GET("/issues", listIssuesHandler(db))
	api.POST("/issues", createIssueHandler(db, newAIClient()))
	api.GET("/issues/:id", getIssueHandler(db))
	api.PATCH("/issues/:id", patchIssueHandler(db))
	api.POST("/issues/:id/matches/:matchId/confirm", confirmMatchHandler(db))
	api.POST("/issues/:id/matches/:matchId/reject", rejectMatchHandler(db))

	mountConsole(r)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("svc-gitpulse listening on :8080")
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

// ── 内嵌管理台 SPA（同 svc-users 模式）────────────────

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
