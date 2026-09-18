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
	var status int
	err := s.db.QueryRow(r.Context(),
		`SELECT secret_hash, scopes, status FROM oauth_apps WHERE app_key=$1`, in.AppKey).
		Scan(&hash, &scopes, &status)
	if err != nil || status != 1 ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.AppSecret)) != nil {
		writeErr(w, 401, "invalid app credentials")
		return
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
