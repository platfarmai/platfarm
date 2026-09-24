package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// 开放平台 app 身份（specs/009）：第三条身份线，client_credentials → 短时效 app token。
// 与 service token 同构，但用于外部合作方：带 appKey、按 app 计量限流。

const appTokenTTL = 1 * time.Hour

func migrateApps(db *pgxpool.Pool) error {
	_, err := db.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS oauth_apps (
			app_key      TEXT PRIMARY KEY,
			secret_hash  TEXT NOT NULL,
			name         TEXT NOT NULL DEFAULT '',
			owner_id     INT,
			scopes       TEXT NOT NULL DEFAULT '',
			rate_per_min INT  NOT NULL DEFAULT 60,
			status       INT  NOT NULL DEFAULT 1,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return err
	}
	// 日配额（specs/023）：0 = 不限
	_, err = db.Exec(context.Background(),
		`ALTER TABLE oauth_apps ADD COLUMN IF NOT EXISTS quota_per_day INT NOT NULL DEFAULT 0`)
	return err
}

// handleOAuthToken POST /oauth/token {appKey, appSecret}（经网关，公开+限流）。
func (s *server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	var in struct{ AppKey, AppSecret string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.AppKey == "" {
		writeErr(w, 400, "appKey and appSecret required")
		return
	}
	var hash, scopes string
	var status, quota int
	err := s.db.QueryRow(r.Context(),
		`SELECT secret_hash, scopes, status, quota_per_day FROM oauth_apps WHERE app_key=$1`, in.AppKey).
		Scan(&hash, &scopes, &status, &quota)
	if err != nil || status != 1 ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.AppSecret)) != nil {
		writeErr(w, 401, "invalid app credentials")
		return
	}
	// 日配额强制（specs/023）：token 签发即闸门——app token 短时效（1h），
	// 超额后拒绝续签，最坏超放一个 TTL 窗口。用量来自网关计量日志（Loki，specs/013）。
	if quota > 0 {
		if used, qerr := lokiAppUsage(r.Context(), in.AppKey); qerr != nil {
			// fail-open：计量不可用不阻断合作方（Loki 属可选组件），仅记日志
			logQuotaSkip(in.AppKey, qerr)
		} else if used >= quota {
			writeJSON(w, 429, map[string]any{
				"error": "daily quota exceeded", "quotaPerDay": quota, "usedToday": used,
			})
			return
		}
	}
	var scopeList []string
	for _, sc := range strings.Split(scopes, ",") {
		if sc = strings.TrimSpace(sc); sc != "" {
			scopeList = append(scopeList, sc)
		}
	}
	tok, err := s.signApp(in.AppKey, scopeList)
	if err != nil {
		writeErr(w, 500, "sign failed")
		return
	}
	writeJSON(w, 200, map[string]any{
		"accessToken": tok, "tokenType": "app",
		"scopes": scopeList, "expiresIn": int(appTokenTTL.Seconds()),
	})
}

// adminActor 校验内网调用方（service token 白名单）+ 代持用户须平台 admin。
// 返回 nil 表示放行，否则已写响应。
func (s *server) adminActor(w http.ResponseWriter, r *http.Request) bool {
	if _, err := s.bearerService(r, s.loginServices); err != nil {
		writeErr(w, 401, err.Error())
		return false
	}
	uc, err := s.parse(r.Header.Get("X-PF-User-Token"))
	if err != nil || uc.TokenType != "access" || uc.Role != "admin" {
		writeErr(w, 403, "platform admin required")
		return false
	}
	return true
}

type appRow struct {
	AppKey      string   `json:"appKey"`
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	RatePerMin  int      `json:"ratePerMin"`
	QuotaPerDay int      `json:"quotaPerDay"` // 0 = 不限（specs/023）
	Status      int      `json:"status"`
	CreatedAt   string   `json:"createdAt"`
}

func splitScopes(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// handleAppsList GET /internal/auth/apps —— 列出 app（不含 secret）。
func (s *server) handleAppsList(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(),
		`SELECT app_key, name, scopes, rate_per_min, quota_per_day, status, created_at FROM oauth_apps ORDER BY created_at DESC`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []appRow{}
	for rows.Next() {
		var a appRow
		var scopes string
		var ts time.Time
		if err := rows.Scan(&a.AppKey, &a.Name, &scopes, &a.RatePerMin, &a.QuotaPerDay, &a.Status, &ts); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		a.Scopes = splitScopes(scopes)
		a.CreatedAt = ts.Format(time.RFC3339)
		out = append(out, a)
	}
	writeJSON(w, 200, out)
}

// handleAppsCreate POST /internal/auth/apps {appKey,name,scopes[],ratePerMin} → secret 只返回一次。
func (s *server) handleAppsCreate(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	var in struct {
		AppKey      string   `json:"appKey"`
		Name        string   `json:"name"`
		Scopes      []string `json:"scopes"`
		RatePerMin  int      `json:"ratePerMin"`
		QuotaPerDay int      `json:"quotaPerDay"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.AppKey == "" {
		writeErr(w, 400, "appKey required")
		return
	}
	if in.RatePerMin <= 0 {
		in.RatePerMin = 60
	}
	if in.QuotaPerDay < 0 {
		in.QuotaPerDay = 0
	}
	secret := "as_" + newTokenID() + newTokenID()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "hash failed")
		return
	}
	_, err = s.db.Exec(r.Context(),
		`INSERT INTO oauth_apps (app_key, secret_hash, name, scopes, rate_per_min, quota_per_day) VALUES ($1,$2,$3,$4,$5,$6)`,
		in.AppKey, string(hash), in.Name, strings.Join(in.Scopes, ","), in.RatePerMin, in.QuotaPerDay)
	if err != nil {
		writeErr(w, 409, "appKey exists or invalid")
		return
	}
	writeJSON(w, 201, map[string]any{"appKey": in.AppKey, "appSecret": secret, "note": "secret shown once"})
}

// handleAppsUpdate PATCH /internal/auth/apps/{key} {scopes?,ratePerMin?,status?}。
func (s *server) handleAppsUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	key := r.PathValue("key")
	var in struct {
		Scopes      *[]string `json:"scopes"`
		RatePerMin  *int      `json:"ratePerMin"`
		QuotaPerDay *int      `json:"quotaPerDay"`
		Status      *int      `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "invalid body")
		return
	}
	ct, err := s.db.Exec(r.Context(),
		`UPDATE oauth_apps SET
		   scopes        = COALESCE($2, scopes),
		   rate_per_min  = COALESCE($3, rate_per_min),
		   quota_per_day = COALESCE($4, quota_per_day),
		   status        = COALESCE($5, status)
		 WHERE app_key = $1`,
		key,
		scopesArg(in.Scopes),
		intArg(in.RatePerMin),
		intArg(in.QuotaPerDay),
		intArg(in.Status),
	)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
		writeErr(w, 404, "app not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleAppsRotate POST /internal/auth/apps/{key}/rotate → 新 secret 只返回一次。
func (s *server) handleAppsRotate(w http.ResponseWriter, r *http.Request) {
	if !s.adminActor(w, r) {
		return
	}
	key := r.PathValue("key")
	secret := "as_" + newTokenID() + newTokenID()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "hash failed")
		return
	}
	ct, err := s.db.Exec(r.Context(),
		`UPDATE oauth_apps SET secret_hash=$2 WHERE app_key=$1`, key, string(hash))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
		writeErr(w, 404, "app not found")
		return
	}
	writeJSON(w, 200, map[string]any{"appKey": key, "appSecret": secret, "note": "secret shown once"})
}

// scopesArg/intArg：nil → SQL NULL（COALESCE 保持原值）。
func scopesArg(s *[]string) any {
	if s == nil {
		return nil
	}
	return strings.Join(*s, ",")
}

func intArg(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

// registerAppCLI 一次性 CLI（docker compose exec auth /auth -register-app <key> -scopes a,b）。
func registerAppCLI(db *pgxpool.Pool, appKey, name, scopes string) error {
	secret := "as_" + newTokenID() + newTokenID()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if name == "" {
		name = appKey
	}
	_, err = db.Exec(context.Background(),
		`INSERT INTO oauth_apps (app_key, secret_hash, name, scopes) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (app_key) DO UPDATE SET secret_hash=$2, name=$3, scopes=$4`,
		appKey, string(hash), name, scopes)
	if err != nil {
		return err
	}
	fmt.Printf("APP_KEY=%s\nAPP_SECRET=%s\nSCOPES=%s\n", appKey, secret, scopes)
	return nil
}
