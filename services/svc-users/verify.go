// 邮箱验证流（specs/021）：登录用户为自己账号上的邮箱做归属验证。
// 码在本服务生成校验（reset_codes 表 purpose=verify），邮件经 svc-notify，
// 结论经 auth 内网 set-email-verified 记录（附录 G 模式）。
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// requireUser 任意登录用户（access token）→ claims；失败已写响应。
func requireUser(c *gin.Context) *Claims {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		return nil
	}
	claims, err := decode(strings.TrimPrefix(h, "Bearer "))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return nil
	}
	return claims
}

func initVerify(r *gin.Engine, ac *authClient) {
	if resetDB == nil { // 与找回密码共用库；未配置则同样降级
		unavailable := func(c *gin.Context) { c.JSON(503, gin.H{"error": "email verification not configured"}) }
		r.POST("/api/users/verify-email/send", unavailable)
		r.POST("/api/users/verify-email/confirm", unavailable)
		return
	}
	r.POST("/api/users/verify-email/send", handleVerifySend(ac))
	r.POST("/api/users/verify-email/confirm", handleVerifyConfirm(ac))
}

func handleVerifySend(ac *authClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := requireUser(c)
		if claims == nil {
			return
		}
		// 从 auth 取本人邮箱（email 不进 claims）
		status, body, err := ac.callService(http.MethodGet,
			"/internal/auth/users/lookup?username="+claims.Username, nil)
		if err != nil || status != 200 {
			c.JSON(502, gin.H{"error": "lookup failed"})
			return
		}
		var u struct {
			ID            int    `json:"id"`
			Email         string `json:"email"`
			EmailVerified bool   `json:"emailVerified"`
		}
		if json.Unmarshal(body, &u) != nil || u.ID == 0 {
			c.JSON(502, gin.H{"error": "lookup failed"})
			return
		}
		if u.Email == "" {
			c.JSON(400, gin.H{"error": "no email on this account"})
			return
		}
		if u.EmailVerified {
			c.JSON(200, gin.H{"ok": true, "note": "already verified"})
			return
		}
		var recent int
		_ = resetDB.QueryRow(c.Request.Context(),
			`SELECT count(*) FROM reset_codes
			 WHERE user_id=$1 AND purpose='verify' AND created_at > now()-interval '1 hour'`,
			u.ID).Scan(&recent)
		if recent >= maxCodesPerHour {
			c.JSON(429, gin.H{"error": "too many codes, try later"})
			return
		}
		code := randomCode()
		if _, err := resetDB.Exec(c.Request.Context(),
			`INSERT INTO reset_codes (user_id, email, code_hash, purpose, expires_at)
			 VALUES ($1,$2,$3,'verify',now()+$4::interval)`,
			u.ID, u.Email, hashCode(code), resetTTL.String()); err != nil {
			c.JSON(500, gin.H{"error": "store failed"})
			return
		}
		payload, _ := json.Marshal(map[string]string{
			"channel": "email", "to": u.Email,
			"subject": "Platfarm 邮箱验证码",
			"body":    "您的邮箱验证码是 " + code + "，15 分钟内有效。",
		})
		if status, _, err := ac.callNotify(payload); err != nil || status != 200 {
			log.Printf("verify mail send failed: status=%d err=%v", status, err)
		}
		c.JSON(200, gin.H{"ok": true, "note": "code sent to " + maskEmail(u.Email)})
	}
}

func handleVerifyConfirm(ac *authClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := requireUser(c)
		if claims == nil {
			return
		}
		var in struct{ Code string }
		if c.ShouldBindJSON(&in) != nil || in.Code == "" {
			c.JSON(400, gin.H{"error": "code required"})
			return
		}
		var id int64
		var userID, attempts int
		var codeHash string
		err := resetDB.QueryRow(c.Request.Context(),
			`SELECT id, user_id, code_hash, attempts FROM reset_codes
			 WHERE user_id=$1 AND purpose='verify' AND NOT used AND expires_at > now()
			 ORDER BY id DESC LIMIT 1`, claims.UserId).Scan(&id, &userID, &codeHash, &attempts)
		if err != nil || attempts >= maxResetAttempts {
			c.JSON(401, gin.H{"error": "invalid or expired code"})
			return
		}
		if codeHash != hashCode(in.Code) {
			_, _ = resetDB.Exec(c.Request.Context(),
				`UPDATE reset_codes SET attempts=attempts+1 WHERE id=$1`, id)
			c.JSON(401, gin.H{"error": "invalid or expired code"})
			return
		}
		status, body, err := ac.callService(http.MethodPost,
			fmt.Sprintf("/internal/auth/users/%d/set-email-verified", userID), nil)
		if err != nil || status != 200 {
			log.Printf("set-email-verified failed: status=%d err=%v body=%s", status, err, body)
			c.JSON(502, gin.H{"error": "verification update failed"})
			return
		}
		_, _ = resetDB.Exec(c.Request.Context(), `UPDATE reset_codes SET used=true WHERE id=$1`, id)
		c.JSON(200, gin.H{"ok": true, "note": "email verified"})
	}
}

func maskEmail(e string) string {
	at := strings.IndexByte(e, '@')
	if at <= 1 {
		return "***" + e[at:]
	}
	return e[:1] + "***" + e[at:]
}
