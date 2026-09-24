// 账号自助流程（specs/018）：自助注册（环境门控）+ TOTP 二次验证 + 内网找回密码支撑端点。
// 验证码的生成/校验/发信在 svc-users + svc-notify（附录 G 模式：验证是调用方的责任，auth 只做兑换）。
package main

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func selfRegisterEnabled() bool {
	return strings.EqualFold(os.Getenv("PF_SELF_REGISTER"), "on")
}

// handleRegister POST /auth/register {Username,Password,Email}（PF_SELF_REGISTER=on 时开放）。
func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !selfRegisterEnabled() {
		writeErr(w, 403, "self registration disabled")
		return
	}
	var in struct{ Username, Password, Email string }
	if json.NewDecoder(r.Body).Decode(&in) != nil ||
		len(in.Username) < 3 || len(in.Password) < 8 {
		writeErr(w, 400, "username (≥3) and password (≥8) required")
		return
	}
		email := ""
		if in.Email != "" {
			addr, err := mail.ParseAddress(in.Email)
			if err != nil {
				writeErr(w, 400, "invalid email")
				return
			}
			email = strings.ToLower(addr.Address)
		}
		if !s.allowRegister(clientIP(r)) {
			writeErr(w, 429, "too many registrations from this address, try later")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			writeErr(w, 500, "hash failed")
			return
		}
		var id int
		err = s.db.QueryRow(r.Context(),
			`INSERT INTO users (username, password_hash, role, email) VALUES ($1,$2,'user',$3) RETURNING id`,
			in.Username, string(hash), email).Scan(&id)
	if err != nil {
		writeErr(w, 409, "username or email already taken")
		return
	}
	u, err := s.loadUser(r.Context(), "id", id)
	if err != nil {
		writeErr(w, 500, "load failed")
		return
	}
	s.issuePair(w, u)
}

// handleUserLookup GET /internal/auth/users/lookup?email=|username= —— 仅内网白名单服务（找回密码流）。
func (s *server) handleUserLookup(w http.ResponseWriter, r *http.Request) {
	if _, err := s.bearerService(r, s.loginServices); err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	var u user
	var err error
	if email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email"))); email != "" {
		u, err = s.loadUser(r.Context(), "email", email)
	} else if name := strings.TrimSpace(r.URL.Query().Get("username")); name != "" {
		u, err = s.loadUser(r.Context(), "username", name)
	} else {
		writeErr(w, 400, "email or username required")
		return
	}
	if err != nil || u.Status != 1 {
		writeErr(w, 404, "user not found")
		return
	}
	writeJSON(w, 200, map[string]any{
		"id": u.ID, "username": u.Username, "email": u.Email, "emailVerified": u.EmailVerified,
	})
}

// handleSetEmailVerified POST /internal/auth/users/{id}/set-email-verified —— 白名单服务
// （svc-users 验证码流已确认邮箱归属，auth 只记结论；specs/021）。
func (s *server) handleSetEmailVerified(w http.ResponseWriter, r *http.Request) {
	if _, err := s.bearerService(r, s.loginServices); err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	id, _ := strconv.Atoi(r.PathValue("id"))
	ct, err := s.db.Exec(r.Context(),
		`UPDATE users SET email_verified=true WHERE id=$1`, id)
	if err != nil || ct.RowsAffected() == 0 {
		writeErr(w, 404, "user not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleSetPassword POST /internal/auth/users/{id}/set-password {newPassword}
// 仅内网白名单服务（svc-users 找回密码流，验证码已由调用方校验）；改密即用户级吊销。
func (s *server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	if _, err := s.bearerService(r, s.loginServices); err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	id, _ := strconv.Atoi(r.PathValue("id"))
	var in struct{ NewPassword string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.NewPassword) < 8 {
		writeErr(w, 400, "newPassword (≥8) required")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "hash failed")
		return
	}
	ct, err := s.db.Exec(r.Context(),
		`UPDATE users SET password_hash=$2 WHERE id=$1`, id, string(hash))
	if err != nil || ct.RowsAffected() == 0 {
		writeErr(w, 404, "user not found")
		return
	}
	s.revokeUserTokens(id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ── TOTP 二次验证 ────────────────────────────────────────────

// handleTOTPSetup POST /auth/totp/setup —— 生成待启用密钥（enable 前不生效）。
func (s *server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	c, err := s.bearer(r)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	secret := newTOTPSecret()
	if _, err := s.db.Exec(r.Context(),
		`UPDATE users SET totp_secret=$2, totp_enabled=false WHERE id=$1`, c.UserId, secret); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{
		"secret": secret, "otpauthUrl": otpauthURL(c.Username, secret),
		"note": "扫码/录入后调 POST /auth/totp/enable {code} 生效",
	})
}

// handleTOTPEnable POST /auth/totp/enable {code} —— 验证一次成功才启用。
func (s *server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	c, err := s.bearer(r)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	u, err := s.loadUser(r.Context(), "id", c.UserId)
	if err != nil || u.TotpSecret == "" {
		writeErr(w, 400, "run /auth/totp/setup first")
		return
	}
	var in struct{ Code string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || !verifyTOTP(u.TotpSecret, in.Code) {
		writeErr(w, 401, "invalid totp code")
		return
	}
	if _, err := s.db.Exec(r.Context(),
		`UPDATE users SET totp_enabled=true WHERE id=$1`, c.UserId); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleTOTPDisable POST /auth/totp/disable {code} —— 需当前有效验证码。
func (s *server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	c, err := s.bearer(r)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	u, err := s.loadUser(r.Context(), "id", c.UserId)
	if err != nil || !u.TotpEnabled {
		writeErr(w, 400, "totp not enabled")
		return
	}
	var in struct{ Code string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || !verifyTOTP(u.TotpSecret, in.Code) {
		writeErr(w, 401, "invalid totp code")
		return
	}
	if _, err := s.db.Exec(r.Context(),
		`UPDATE users SET totp_enabled=false, totp_secret='' WHERE id=$1`, c.UserId); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleTOTPReset POST /internal/auth/users/{id}/reset-totp —— admin 解锁（丢失验证器）。
func (s *server) handleTOTPReset(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	id, _ := strconv.Atoi(r.PathValue("id"))
	ct, err := s.db.Exec(r.Context(),
		`UPDATE users SET totp_enabled=false, totp_secret='' WHERE id=$1`, id)
	if err != nil || ct.RowsAffected() == 0 {
		writeErr(w, 404, "user not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
