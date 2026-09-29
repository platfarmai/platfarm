// __SVC_ID__ — Platfarm 业务服务（接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5）。
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
	"github.com/platfarmai/sdk/go/pfauth"
)

const mount = "__MOUNT__"

// 验签唯一实现在 sdk/go/pfauth（含 kid 轮换）。Claims 见 pfauth.Claims。

var draining atomic.Bool

// tableName 给业务表名加上可选前缀（specs/007）。默认空 → 原样返回。
// 用法：tableName("users") → "users" 或 "cms_users"。
func tableName(name string) string { return os.Getenv("PF_TABLE_PREFIX") + name }

// 若服务声明 data.database（自有库），须遵循 specs/015 迁移规范：
// migrations/NNN_name.sql（append-only）+ 启动时自应用（参考 services/svc-file/migrate.go），
// 否则 pctl check 拒绝。裸模板不带数据库依赖，故不含 runner。

func identity(c *gin.Context) (*pfauth.Claims, error) {
	return pfauth.Identity(c.GetHeader("Authorization"), c.GetHeader("X-PF-User-Token"))
}

func main() {
	pfauth.Load()
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET(mount+"/public/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pong": true, "service": "__SVC_ID__", "auth": "not required"})
	})

	r.GET(mount+"/me", func(c *gin.Context) {
		claims, err := identity(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"service": "__SVC_ID__", "userId": claims.UserId, "username": claims.Username,
			"role": claims.Role, "tenantId": claims.TenantId, "svc": claims.Svc,
		})
	})

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.GET("/readyz", func(c *gin.Context) {
		if draining.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "draining"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("__SVC_ID__ listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	draining.Store(true)
	n := 25
	if v := os.Getenv("PF_DRAIN_SECONDS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(n)*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
