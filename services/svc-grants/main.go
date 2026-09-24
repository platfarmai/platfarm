// svc-grants — Platfarm 集中式业务角色分配能力服务（ADR #18）。
// 角色"分配"集中于本服务；角色"语义"仍留在各服务代码（L3）。
// 管理员管理分配；用户查看自己的分配；服务用自身 service token 查询自己的成员。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

const mount = "/api/grants"

var (
	db       *pgxpool.Pool
	draining atomic.Bool

	serviceRe = regexp.MustCompile(`^[a-z0-9-]+$`)
	roleRe    = regexp.MustCompile(`^[a-z0-9_.-]+$`)
)

func main() {
	mustLoadPub()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustEnv("GRANTS_DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	db = pool
	waitDB(ctx)
	if err := runMigrations(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

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
		c.JSON(200, gin.H{"service": "svc-grants", "userId": claims.UserId,
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
	api.GET("/mine", handleMine)
	api.GET("/users", handleUsers)
	api.PUT("/:service/:role/:userId", handleGrant)
	api.DELETE("/:service/:role/:userId", handleRevoke)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("svc-grants listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	draining.Store(true)
	sctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}

func claimsOf(c *gin.Context) *Claims { return c.MustGet("claims").(*Claims) }

// handleMine 任意登录用户查看自己的角色分配。
func handleMine(c *gin.Context) {
	claims := claimsOf(c)
	rows, err := db.Query(c.Request.Context(),
		`SELECT service_id, role, created_at::text FROM grants
		 WHERE user_id=$1 ORDER BY id DESC LIMIT 500`, claims.UserId)
	if err != nil {
		c.JSON(500, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	type grant struct {
		ServiceId string `json:"serviceId"`
		Role      string `json:"role"`
		CreatedAt string `json:"createdAt"`
	}
	items := []grant{}
	for rows.Next() {
		var g grant
		if rows.Scan(&g.ServiceId, &g.Role, &g.CreatedAt) == nil {
			items = append(items, g)
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// handleUsers 查询某服务某角色的成员：admin 用户可查任意服务；
// service token 只能查询自身（claims.Svc == service）。role 为可选过滤。
func handleUsers(c *gin.Context) {
	claims := claimsOf(c)
	service := c.Query("service")
	if service == "" {
		c.JSON(400, gin.H{"error": "service query param required"})
		return
	}
	role := c.Query("role")
	allowed := claims.Role == "admin" ||
		(claims.TokenType == "service" && claims.Svc == service)
	if !allowed {
		c.JSON(403, gin.H{"error": "admin user or matching service token required"})
		return
	}
	sql := `SELECT user_id, role, granted_by, created_at::text FROM grants WHERE service_id=$1`
	args := []any{service}
	if role != "" {
		sql += ` AND role=$2`
		args = append(args, role)
	}
	sql += ` ORDER BY id DESC LIMIT 500`
	rows, err := db.Query(c.Request.Context(), sql, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	type member struct {
		UserId    int    `json:"userId"`
		Role      string `json:"role"`
		GrantedBy string `json:"grantedBy"`
		CreatedAt string `json:"createdAt"`
	}
	items := []member{}
	for rows.Next() {
		var m member
		if rows.Scan(&m.UserId, &m.Role, &m.GrantedBy, &m.CreatedAt) == nil {
			items = append(items, m)
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// handleGrant admin 分配角色（幂等 upsert）。
func handleGrant(c *gin.Context) {
	claims := claimsOf(c)
	if claims.Role != "admin" {
		c.JSON(403, gin.H{"error": "admin required"})
		return
	}
	service, role, userId, ok := parseTarget(c)
	if !ok {
		return
	}
	if _, err := db.Exec(c.Request.Context(),
		`INSERT INTO grants (service_id, role, user_id, granted_by)
		 VALUES ($1,$2,$3,$4) ON CONFLICT (service_id, role, user_id) DO NOTHING`,
		service, role, userId, claims.Username); err != nil {
		c.JSON(500, gin.H{"error": "insert failed"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// handleRevoke admin 撤销角色；不存在则 404。
func handleRevoke(c *gin.Context) {
	claims := claimsOf(c)
	if claims.Role != "admin" {
		c.JSON(403, gin.H{"error": "admin required"})
		return
	}
	service, role, userId, ok := parseTarget(c)
	if !ok {
		return
	}
	tag, err := db.Exec(c.Request.Context(),
		`DELETE FROM grants WHERE service_id=$1 AND role=$2 AND user_id=$3`,
		service, role, userId)
	if err != nil {
		c.JSON(500, gin.H{"error": "delete failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(404, gin.H{"error": "grant not found"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// parseTarget 校验路径参数：service ^[a-z0-9-]+$、role ^[a-z0-9_.-]+$、userId 整数。
func parseTarget(c *gin.Context) (service, role string, userId int, ok bool) {
	service = c.Param("service")
	role = c.Param("role")
	if !serviceRe.MatchString(service) {
		c.JSON(400, gin.H{"error": "invalid service"})
		return "", "", 0, false
	}
	if !roleRe.MatchString(role) {
		c.JSON(400, gin.H{"error": "invalid role"})
		return "", "", 0, false
	}
	uid, err := strconv.Atoi(c.Param("userId"))
	if err != nil {
		c.JSON(400, gin.H{"error": "userId must be an integer"})
		return "", "", 0, false
	}
	return service, role, uid, true
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
