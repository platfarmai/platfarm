// svc-file — Platfarm 文件能力服务（specs/016，架构 §5 参考实现）。
// 职责：元数据 + 归属判定（L3）+ 预签名 URL；字节流走浏览器 ↔ MinIO 直连。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	mount      = "/api/file"
	presignTTL = 15 * time.Minute
	maxSize    = int64(1 << 30) // 1 GiB 单文件上限
)

var (
	db       *pgxpool.Pool
	s3       *minio.Client // 内网端点：bucket 管理、删除对象
	s3Sign   *minio.Client // 公网端点：预签名（浏览器可达的 host 才能通过签名校验）
	bucket   string
	draining atomic.Bool
)

type fileRow struct {
	ID         int64  `json:"id"`
	OwnerID    int    `json:"ownerId"`
	Visibility string `json:"visibility"`
	Filename   string `json:"filename"`
	Mime       string `json:"mime"`
	Size       int64  `json:"size"`
	CreatedAt  string `json:"createdAt"`
}

func mustS3Client(endpoint string) *minio.Client {
	u, err := url.Parse(endpoint)
	if err != nil {
		log.Fatalf("bad S3 endpoint %q: %v", endpoint, err)
	}
	c, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"), ""),
		Secure: u.Scheme == "https",
		Region: envOr("S3_REGION", "us-east-1"), // 固定 region：签名客户端指向公网端点，不能探测 bucket location
	})
	if err != nil {
		log.Fatalf("s3 client: %v", err)
	}
	return c
}

func main() {
	mustLoadPub()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustEnv("FILE_DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	db = pool
	waitDB(ctx)
	if err := runMigrations(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	bucket = envOr("S3_BUCKET", "pf-files")
	s3 = mustS3Client(mustEnv("S3_ENDPOINT"))
	s3Sign = mustS3Client(envOr("S3_PUBLIC_ENDPOINT", mustEnv("S3_ENDPOINT")))
	ensureBucket(ctx)

	sctx0, scancel := context.WithCancel(ctx)
	defer scancel()
	go orphanSweeper(sctx0) // specs/021：预签未上传的孤儿元数据回收

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
		c.JSON(200, gin.H{"service": "svc-file", "userId": claims.UserId,
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
	api.POST("/upload-url", handleUploadURL)
	api.GET("/:id/download-url", handleDownloadURL)
	api.DELETE("/:id", handleDelete)
	api.GET("/mine", handleMine)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("svc-file listening on :8080")
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

// handleUploadURL 建元数据行 + 预签名 PUT（15min）；storage_key 归属前缀防越权覆盖。
func handleUploadURL(c *gin.Context) {
	claims := claimsOf(c)
	var in struct {
		Filename   string `json:"filename"`
		Mime       string `json:"mime"`
		Size       int64  `json:"size"`
		Visibility string `json:"visibility"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Filename == "" {
		c.JSON(400, gin.H{"error": "filename required"})
		return
	}
	if in.Size < 0 || in.Size > maxSize {
		c.JSON(400, gin.H{"error": "size out of range (max 1GiB)"})
		return
	}
	if in.Visibility == "" {
		in.Visibility = "private"
	}
	if in.Visibility != "private" && in.Visibility != "public" {
		c.JSON(400, gin.H{"error": "visibility must be private|public"})
		return
	}
	key := "u" + strconv.Itoa(claims.UserId) + "/" + randomHex(16) + "_" + path.Base(in.Filename)
	token := ""
	if in.Visibility == "public" {
		token = randomHex(16) // 公开链接不可按自增 id 枚举
	}
	var id int64
	err := db.QueryRow(c.Request.Context(),
		`INSERT INTO files (owner_id, tenant_id, visibility, filename, mime, size, storage_key, public_token)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		claims.UserId, claims.TenantId, in.Visibility, path.Base(in.Filename), in.Mime, in.Size, key, token).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": "insert failed"})
		return
	}
	u, err := s3Sign.PresignedPutObject(c.Request.Context(), bucket, key, presignTTL)
	if err != nil {
		c.JSON(502, gin.H{"error": "presign failed"})
		return
	}
	out := gin.H{"fileId": id, "uploadUrl": u.String(), "expiresIn": int(presignTTL.Seconds())}
	if token != "" {
		out["publicToken"] = token
	}
	c.JSON(200, out)
}

func loadFile(c *gin.Context) (fileRow, string, bool) {
	ref := c.Param("id")
	query := `SELECT id, owner_id, visibility, filename, mime, size, storage_key, created_at::text
		 FROM files WHERE id=$1`
	var arg any
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		arg = id
	} else {
		query = `SELECT id, owner_id, visibility, filename, mime, size, storage_key, created_at::text
		 FROM files WHERE public_token=$1 AND public_token <> ''`
		arg = ref
	}
	var f fileRow
	var key string
	err := db.QueryRow(c.Request.Context(), query, arg).
		Scan(&f.ID, &f.OwnerID, &f.Visibility, &f.Filename, &f.Mime, &f.Size, &key, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(404, gin.H{"error": "file not found"})
		return fileRow{}, "", false
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "query failed"})
		return fileRow{}, "", false
	}
	return f, key, true
}

// L3 归属判定（架构 §5）：private 仅 owner；admin 越权；public 任意登录者可读。
func canRead(f fileRow, claims *Claims) bool {
	return f.Visibility == "public" || f.OwnerID == claims.UserId || claims.Role == "admin"
}
func canWrite(f fileRow, claims *Claims) bool {
	return f.OwnerID == claims.UserId || claims.Role == "admin"
}

func handleDownloadURL(c *gin.Context) {
	f, key, ok := loadFile(c)
	if !ok {
		return
	}
	if !canRead(f, claimsOf(c)) {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}
	params := url.Values{"response-content-disposition": {`attachment; filename="` + f.Filename + `"`}}
	u, err := s3Sign.PresignedGetObject(c.Request.Context(), bucket, key, presignTTL, params)
	if err != nil {
		c.JSON(502, gin.H{"error": "presign failed"})
		return
	}
	c.JSON(200, gin.H{"downloadUrl": u.String(), "expiresIn": int(presignTTL.Seconds()), "file": f})
}

func handleDelete(c *gin.Context) {
	f, key, ok := loadFile(c)
	if !ok {
		return
	}
	if !canWrite(f, claimsOf(c)) {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}
	if err := s3.RemoveObject(c.Request.Context(), bucket, key, minio.RemoveObjectOptions{}); err != nil {
		log.Printf("remove object %s: %v", key, err) // 对象可能从未上传；元数据删除继续
	}
	if _, err := db.Exec(c.Request.Context(), `DELETE FROM files WHERE id=$1`, f.ID); err != nil {
		c.JSON(500, gin.H{"error": "delete failed"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func handleMine(c *gin.Context) {
	claims := claimsOf(c)
	rows, err := db.Query(c.Request.Context(),
		`SELECT id, owner_id, visibility, filename, mime, size, storage_key, created_at::text
		 FROM files WHERE owner_id=$1 ORDER BY id DESC LIMIT 200`, claims.UserId)
	if err != nil {
		c.JSON(500, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	items := []fileRow{}
	for rows.Next() {
		var f fileRow
		var key string
		if rows.Scan(&f.ID, &f.OwnerID, &f.Visibility, &f.Filename, &f.Mime, &f.Size, &key, &f.CreatedAt) == nil {
			items = append(items, f)
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// ── infra helpers ────────────────────────────────────────────

func ensureBucket(ctx context.Context) {
	for i := 0; i < 30; i++ {
		exists, err := s3.BucketExists(ctx, bucket)
		if err == nil {
			if !exists {
				err = s3.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
			}
			if err == nil {
				return
			}
		}
		log.Printf("waiting for object store (%d/30): %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	log.Fatal("object store unreachable")
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

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
