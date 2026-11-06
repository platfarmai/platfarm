// svc-pay — 微信/支付宝支付服务商系统（specs/030）。
// M1：商户体系、微信普通服务商 JSAPI/Native 支付、退款、渠道回调、下游通知。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	_ "platfarm/svc-pay/channel/wxpay" // 注册 wxpay_partner
)

const mount = "/api/pay"

var (
	db       *pgxpool.Pool
	rdb      *redis.Client
	draining atomic.Bool
)

func main() {
	mustLoadPub()
	mustInitSecret()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustEnv("PAY_DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	db = pool
	waitDB(ctx)
	if err := runMigrations(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	rdb = dialRedis()

	wctx, wcancel := context.WithCancel(ctx)
	go notifyWorker(wctx)
	go reconcileWorker(wctx)
	go applymentWorker(wctx)
	go divisionWorker(wctx)

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

	// 渠道回调：网关按 public_routes 放行，验签在适配器内。
	r.POST(mount+"/notify/:ifCode/*orderId", handleChannelNotify)

	r.GET(mount+"/me", func(c *gin.Context) {
		claims, err := identity(c)
		if err != nil {
			c.JSON(401, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"service": "svc-pay", "userId": claims.UserId,
			"username": claims.Username, "role": claims.Role, "tenantId": claims.TenantId})
	})

	// 开放 API：网关已验 app token 与 scope，这里再验商户签名。
	v1 := r.Group(mount + "/v1")
	v1.POST("/orders", handleCreateOrder)
	v1.GET("/orders/:mchOrderNo", handleQueryOrder)
	v1.POST("/orders/:mchOrderNo/close", handleCloseOrder)
	v1.POST("/refunds", handleCreateRefund)
	v1.POST("/divisions", handleCreateDivision)
	v1.GET("/divisions/:mchOrderNo", handleQueryDivision)

	// 内部回调：svc-jobs cron（服务 token）触发每日对账。
	r.POST(mount+"/internal/recon/daily", handleDailyRecon)

	// 管理接口：平台 JWT。
	admin := r.Group(mount+"/admin", func(c *gin.Context) {
		claims, err := identity(c)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": err.Error()})
			return
		}
		c.Set("claims", claims)
	})
	registerAdminRoutes(admin)
	mountConsole(r)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("svc-pay listening on :8080")
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

// registerAdminRoutes 管理接口全集；测试用自定义身份中间件挂同一组路由。
func registerAdminRoutes(admin *gin.RouterGroup) {
	admin.POST("/isv", handleCreateISV)
	admin.POST("/merchants", handleCreateMerchant)
	admin.GET("/merchants", handleListMerchants)
	admin.POST("/apps", handleCreateApp)
	admin.PUT("/channel-config", handlePutChannelConfig)
	admin.POST("/channel-config/wxpay/platform-cert", handleSyncPlatformCert)
	admin.PUT("/passages", handlePutPassage)
	admin.GET("/orders", handleListOrders)
	registerApplymentRoutes(admin)
	registerDivisionReconRoutes(admin)
}

// dialRedis 防重放随机串的存储。PF_REDIS_URL 为空时返回 nil，随机串校验降级为跳过。
func dialRedis() *redis.Client {
	addr := os.Getenv("PF_REDIS_URL")
	if addr == "" {
		log.Println("PF_REDIS_URL 未配置：请求随机串防重放已关闭（仅限单副本 dev）")
		return nil
	}
	opt, err := redis.ParseURL(addr)
	if err != nil {
		log.Fatalf("PF_REDIS_URL: %v", err)
	}
	client := redis.NewClient(opt)
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(pctx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	return client
}

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
