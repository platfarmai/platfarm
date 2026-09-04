package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// 第三方登录（specs/changes/001，附录 G）：外部身份的验证是登录服务（svc-oauth）的
// 责任，auth 只做兑换与绑定。

func migrateExternal(db *pgxpool.Pool) error {
	_, err := db.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS external_identities (
			provider    TEXT NOT NULL,
			external_id TEXT NOT NULL,
			user_id     INT  NOT NULL REFERENCES users(id),
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (provider, external_id)
		)`)
	return err
}

// loginServiceWhitelist 允许调用 external-login 的服务（env 覆盖，默认 svc-oauth）。
func loginServiceWhitelist() map[string]bool {
	raw := os.Getenv("AUTH_LOGIN_SERVICES")
	if raw == "" {
		raw = "svc-oauth"
	}
	out := map[string]bool{}
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out[s] = true
		}
	}
	return out
}

type externalIdentity struct {
	Provider    string
	ExternalId  string
	DisplayName string
}

// handleExternalLogin 仅内网路由（/internal 不经网关）+ service token 白名单。
func (s *server) handleExternalLogin(w http.ResponseWriter, r *http.Request) {
	if _, err := s.bearerService(r, s.loginServices); err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	var in externalIdentity
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Provider == "" || in.ExternalId == "" {
		writeErr(w, 400, "provider and externalId required")
		return
	}
	ctx := r.Context()

	var userID int
	err := s.db.QueryRow(ctx,
		`SELECT user_id FROM external_identities WHERE provider=$1 AND external_id=$2`,
		in.Provider, in.ExternalId).Scan(&userID)
	if err != nil { // 未绑定 → 自动建号并绑定
		userID, err = s.provisionExternalUser(ctx, in)
		if err != nil {
			writeErr(w, 500, fmt.Sprintf("provision failed: %v", err))
			return
		}
	}
	u, err := s.loadUser(ctx, "id", userID)
	if err != nil || u.Status != 1 {
		writeErr(w, 401, "user unavailable")
		return
	}
	s.issuePair(w, u)
}

func (s *server) provisionExternalUser(ctx context.Context, in externalIdentity) (int, error) {
	// 不可登录的随机密码占位：外部用户没有本地口令
	hash, err := bcrypt.GenerateFromPassword([]byte(newTokenID()+newTokenID()), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	username := in.Provider + "_" + in.ExternalId
	if len(username) > 60 {
		username = username[:60]
	}
	var userID int
	err = s.db.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1,$2,'user')
		 ON CONFLICT (username) DO UPDATE SET username = users.username
		 RETURNING id`, username, string(hash)).Scan(&userID)
	if err != nil {
		return 0, err
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO external_identities (provider, external_id, user_id) VALUES ($1,$2,$3)
		 ON CONFLICT DO NOTHING`, in.Provider, in.ExternalId, userID)
	return userID, err
}

// handleBindExternal OBO：service token（谁验证的）+ X-PF-User-Token（绑给谁）。
func (s *server) handleBindExternal(w http.ResponseWriter, r *http.Request) {
	if _, err := s.bearerService(r, s.loginServices); err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	userTok := r.Header.Get("X-PF-User-Token")
	uc, err := s.parse(userTok)
	if err != nil || uc.TokenType != "access" {
		writeErr(w, 401, "valid X-PF-User-Token required")
		return
	}
	var in externalIdentity
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Provider == "" || in.ExternalId == "" {
		writeErr(w, 400, "provider and externalId required")
		return
	}
	_, err = s.db.Exec(r.Context(),
		`INSERT INTO external_identities (provider, external_id, user_id) VALUES ($1,$2,$3)`,
		in.Provider, in.ExternalId, uc.UserId)
	if err != nil {
		writeErr(w, 409, "identity already bound")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
