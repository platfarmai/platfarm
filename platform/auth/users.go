package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// 平台用户管理（specs/011）：内网 admin 接口 + 用户自助改密码。
// 平台 role 仅 admin|user；应用业务角色留应用（L3）。

type userRow struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	Status    int    `json:"status"`
	CreatedAt string `json:"createdAt"`
}

func randomPassword() string { return "pw_" + newTokenID() }

// handleUsersList GET /internal/auth/users?limit=&offset=&q= —— 分页列表（specs/014，不含 password_hash）。
func (s *server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	limit := clampInt(r.URL.Query().Get("limit"), 20, 1, 100)
	offset := clampInt(r.URL.Query().Get("offset"), 0, 0, 1<<31)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	like := "%" + q + "%"

	var total int
	if err := s.db.QueryRow(r.Context(),
		`SELECT count(*) FROM users WHERE ($1='' OR username ILIKE $2)`, q, like).Scan(&total); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	rows, err := s.db.Query(r.Context(),
		`SELECT id, username, role, status, created_at FROM users
		 WHERE ($1='' OR username ILIKE $2) ORDER BY id LIMIT $3 OFFSET $4`,
		q, like, limit, offset)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	items := []userRow{}
	for rows.Next() {
		var u userRow
		var ts time.Time
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Status, &ts); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		u.CreatedAt = ts.Format(time.RFC3339)
		items = append(items, u)
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

// clampInt 解析查询参数并夹在 [min,max]，非法用 def。
func clampInt(s string, def, min, max int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// handleUsersCreate POST /internal/auth/users {username, role} → 初始密码只返回一次。
func (s *server) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	var in struct{ Username, Role string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Username == "" {
		writeErr(w, 400, "username required")
		return
	}
	if in.Role != "admin" {
		in.Role = "user"
	}
	pw := randomPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "hash failed")
		return
	}
	var id int
	err = s.db.QueryRow(r.Context(),
		`INSERT INTO users (username, password_hash, role) VALUES ($1,$2,$3) RETURNING id`,
		in.Username, string(hash), in.Role).Scan(&id)
	if err != nil {
		writeErr(w, 409, "username exists")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "username": in.Username, "password": pw, "note": "password shown once"})
}

// handleUsersUpdate PATCH /internal/auth/users/{id} {role?, status?} —— 防锁死。
func (s *server) handleUsersUpdate(w http.ResponseWriter, r *http.Request) {
	actor := s.actorClaims(w, r)
	if actor == nil {
		return
	}
	id, _ := strconv.Atoi(r.PathValue("id"))
	var in struct {
		Role     *string `json:"role"`
		Status   *int    `json:"status"`
		TenantId *int    `json:"tenantId"` // 多租户归属调整（specs/022）
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "invalid body")
		return
	}
	// 目标当前状态
	var curRole string
	var curStatus int
	if err := s.db.QueryRow(r.Context(),
		`SELECT role, status FROM users WHERE id=$1`, id).Scan(&curRole, &curStatus); err != nil {
		writeErr(w, 404, "user not found")
		return
	}
	demoting := in.Role != nil && *in.Role != "admin" && curRole == "admin"
	disabling := in.Status != nil && *in.Status != 1 && curStatus == 1
	if (demoting || disabling) && id == actor.UserId {
		writeErr(w, 400, "cannot disable or demote yourself")
		return
	}
	if (demoting || disabling) && curRole == "admin" {
		var admins int
		s.db.QueryRow(r.Context(),
			`SELECT count(*) FROM users WHERE role='admin' AND status=1`).Scan(&admins)
		if admins <= 1 {
			writeErr(w, 400, "cannot demote/disable the last active admin")
			return
		}
	}
	role := "user"
	if in.Role == nil {
		role = curRole
	} else if *in.Role == "admin" {
		role = "admin"
	}
	status := curStatus
	if in.Status != nil {
		status = *in.Status
	}
	if _, err := s.db.Exec(r.Context(),
		`UPDATE users SET role=$2, status=$3 WHERE id=$1`, id, role, status); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if in.TenantId != nil { // 换租户即吊销旧 token（其 tenantId claim 已过期）
		if _, err := s.db.Exec(r.Context(),
			`UPDATE users SET tenant_id=$2 WHERE id=$1`, id, *in.TenantId); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		s.revokeUserTokens(id)
	}
	if status != 1 || role != curRole {
		s.revokeUserTokens(id) // 禁用或改角色：旧 token 里的 role 立即作废
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleUsersReset POST /internal/auth/users/{id}/reset-password → 新密码只返回一次。
func (s *server) handleUsersReset(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	id, _ := strconv.Atoi(r.PathValue("id"))
	pw := randomPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "hash failed")
		return
	}
	ct, err := s.db.Exec(r.Context(),
		`UPDATE users SET password_hash=$2 WHERE id=$1`, id, string(hash))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
		writeErr(w, 404, "user not found")
		return
	}
	s.revokeUserTokens(id)
	writeJSON(w, 200, map[string]any{"id": id, "password": pw, "note": "password shown once"})
}

// handleChangePassword POST /auth/change-password {oldPassword,newPassword} —— 用户本人。
func (s *server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	c, err := s.bearer(r)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	var in struct{ OldPassword, NewPassword string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.NewPassword == "" {
		writeErr(w, 400, "oldPassword and newPassword required")
		return
	}
	u, err := s.loadUser(r.Context(), "id", c.UserId)
	if err != nil ||
		bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.OldPassword)) != nil {
		writeErr(w, 401, "old password incorrect")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "hash failed")
		return
	}
	if _, err := s.db.Exec(r.Context(),
		`UPDATE users SET password_hash=$2 WHERE id=$1`, c.UserId, string(hash)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.revoke(c) // 改密后当前 token 失效，强制重登
	clearSessionCookie(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// actorClaims 校验内网 service token + 代持用户须 admin，返回代持用户 claims。
func (s *server) actorClaims(w http.ResponseWriter, r *http.Request) *Claims {
	if _, err := s.bearerService(r, s.loginServices); err != nil {
		writeErr(w, 401, err.Error())
		return nil
	}
	uc, err := s.parse(r.Header.Get("X-PF-User-Token"))
	if err != nil || uc.TokenType != "access" || uc.Role != "admin" {
		writeErr(w, 403, "platform admin required")
		return nil
	}
	return uc
}
