// 找回密码流（specs/018）：公开 forgot/reset 两端点。
// 验证码本地生成与校验（pf_svc_users 库）；邮件经 svc-notify；改密经 auth 内网 set-password。
// auth 只做兑换（附录 G 模式）——验证是本服务的责任。
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	resetTTL         = 15 * time.Minute
	maxResetAttempts = 5
	maxCodesPerHour  = 3
)

var resetDB *pgxpool.Pool

// initReset 挂载 forgot/reset 公开路由；USERS_DATABASE_URL 未配置时优雅降级为 503。
func initReset(r *gin.Engine, ac *authClient) {
	dbURL := os.Getenv("USERS_DATABASE_URL")
	if dbURL == "" {
		log.Println("USERS_DATABASE_URL unset — password reset disabled")
		unavailable := func(c *gin.Context) { c.JSON(503, gin.H{"error": "password reset not configured"}) }
		r.POST("/api/users/forgot", unavailable)
		r.POST("/api/users/reset", unavailable)
		return
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("reset db: %v", err)
	}
	for i := 0; i < 30; i++ {
		if err = pool.Ping(ctx); err == nil {
			break
		}
		log.Printf("waiting for database (%d/30)", i+1)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("reset db unreachable: %v", err)
	}
	if err := runMigrations(ctx, pool); err != nil {
		log.Fatalf("reset migrate: %v", err)
	}
	resetDB = pool
	r.POST("/api/users/forgot", handleForgot(ac))
	r.POST("/api/users/reset", handleReset(ac))
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func randomCode() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1_000_000
	return fmt.Sprintf("%06d", n)
}

// handleForgot POST /api/users/forgot {email} —— 恒定应答，不暴露账号是否存在。
func handleForgot(ac *authClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct{ Email string }
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "email required"})
			return
		}
		email := strings.ToLower(strings.TrimSpace(in.Email))
		if _, err := mail.ParseAddress(email); err != nil {
			c.JSON(400, gin.H{"error": "invalid email"})
			return
		}
		ok := gin.H{"ok": true, "note": "if the address is registered, a code has been sent"}

		// 频控：同邮箱一小时最多 3 个码
		var recent int
		_ = resetDB.QueryRow(c.Request.Context(),
			`SELECT count(*) FROM reset_codes
			 WHERE email=$1 AND purpose='reset' AND created_at > now()-interval '1 hour'`,
			email).Scan(&recent)
		if recent >= maxCodesPerHour {
			c.JSON(200, ok)
			return
		}

		// auth 内网查号（仅白名单 service token 可调）
		status, body, err := ac.callService(http.MethodGet, "/internal/auth/users/lookup?email="+email, nil)
		if err != nil || status != 200 {
			c.JSON(200, ok) // 不存在同样恒定应答
			return
		}
		var u struct {
			ID    int    `json:"id"`
			Email string `json:"email"`
		}
		if json.Unmarshal(body, &u) != nil || u.ID == 0 {
			c.JSON(200, ok)
			return
		}

		code := randomCode()
		if _, err := resetDB.Exec(c.Request.Context(),
			`INSERT INTO reset_codes (user_id, email, code_hash, expires_at) VALUES ($1,$2,$3,now()+$4::interval)`,
			u.ID, email, hashCode(code), resetTTL.String()); err != nil {
			c.JSON(500, gin.H{"error": "store failed"})
			return
		}
		payload, _ := json.Marshal(map[string]string{
			"channel": "email", "to": email,
			"subject": "Platfarm 密码重置验证码",
			"body":    "您的验证码是 " + code + "，15 分钟内有效。若非本人操作请忽略。",
		})
		if status, _, err := ac.callNotify(payload); err != nil || status != 200 {
			log.Printf("notify send failed: status=%d err=%v", status, err)
		}
		c.JSON(200, ok)
	}
}

// handleReset POST /api/users/reset {email, code, newPassword}。
func handleReset(ac *authClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct{ Email, Code, NewPassword string }
		if c.ShouldBindJSON(&in) != nil || in.Code == "" || len(in.NewPassword) < 8 {
			c.JSON(400, gin.H{"error": "email, code and newPassword (≥8) required"})
			return
		}
		email := strings.ToLower(strings.TrimSpace(in.Email))
		ctx := c.Request.Context()

		var id int64
		var userID, attempts int
		var codeHash string
		err := resetDB.QueryRow(ctx,
			`SELECT id, user_id, code_hash, attempts FROM reset_codes
			 WHERE email=$1 AND purpose='reset' AND NOT used AND expires_at > now()
			 ORDER BY id DESC LIMIT 1`, email).Scan(&id, &userID, &codeHash, &attempts)
		if err != nil || attempts >= maxResetAttempts {
			c.JSON(401, gin.H{"error": "invalid or expired code"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(codeHash), []byte(hashCode(in.Code))) != 1 {
			_, _ = resetDB.Exec(ctx, `UPDATE reset_codes SET attempts=attempts+1 WHERE id=$1`, id)
			c.JSON(401, gin.H{"error": "invalid or expired code"})
			return
		}

		payload, _ := json.Marshal(map[string]string{"newPassword": in.NewPassword})
		status, body, err := ac.callService(http.MethodPost,
			fmt.Sprintf("/internal/auth/users/%d/set-password", userID), payload)
		if err != nil || status != 200 {
			log.Printf("set-password failed: status=%d err=%v body=%s", status, err, body)
			c.JSON(502, gin.H{"error": "password update failed"})
			return
		}
		_, _ = resetDB.Exec(ctx, `UPDATE reset_codes SET used=true WHERE id=$1`, id)
		c.JSON(200, gin.H{"ok": true, "note": "password updated, please login"})
	}
}
