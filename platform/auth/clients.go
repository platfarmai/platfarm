package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// 服务/插件身份（002 Stage 4）：client_credentials → 短时效 service token。
// 凭据由 pctl install 通过 register-client CLI 开出（附录 H）。

func migrateClients(db *pgxpool.Pool) error {
	_, err := db.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS service_clients (
			client_id   TEXT PRIMARY KEY,
			secret_hash TEXT NOT NULL,
			scopes      TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

// handleServiceToken POST /auth/service-token {clientId, clientSecret}（经网关，公开+限流）。
func (s *server) handleServiceToken(w http.ResponseWriter, r *http.Request) {
	var in struct{ ClientId, ClientSecret string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.ClientId == "" {
		writeErr(w, 400, "clientId and clientSecret required")
		return
	}
	var hash, scopes string
	err := s.db.QueryRow(r.Context(),
		`SELECT secret_hash, scopes FROM service_clients WHERE client_id=$1`, in.ClientId).
		Scan(&hash, &scopes)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.ClientSecret)) != nil {
		writeErr(w, 401, "invalid client credentials")
		return
	}
	var scopeList []string
	for _, sc := range strings.Split(scopes, ",") {
		if sc = strings.TrimSpace(sc); sc != "" {
			scopeList = append(scopeList, sc)
		}
	}
	tok, err := s.signService(in.ClientId, scopeList)
	if err != nil {
		writeErr(w, 500, "sign failed")
		return
	}
	writeJSON(w, 200, map[string]any{"accessToken": tok, "tokenType": "service", "expiresIn": int(serviceTTL.Seconds())})
}

// registerClientCLI 一次性 CLI（docker compose exec auth /auth -register-client <id> -scopes a,b）。
// 生成随机 secret，落库 bcrypt 哈希，明文只打印一次——由 pctl install 捕获写入插件 env。
func registerClientCLI(db *pgxpool.Pool, clientID, scopes string) error {
	secret := newTokenID() + newTokenID()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec(context.Background(),
		`INSERT INTO service_clients (client_id, secret_hash, scopes) VALUES ($1,$2,$3)
		 ON CONFLICT (client_id) DO UPDATE SET secret_hash=$2, scopes=$3`,
		clientID, string(hash), scopes)
	if err != nil {
		return err
	}
	fmt.Printf("CLIENT_ID=%s\nCLIENT_SECRET=%s\nSCOPES=%s\n", clientID, secret, scopes)
	return nil
}
