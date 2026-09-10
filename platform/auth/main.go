// Platfarm auth 底座：用户 + JWT(RS256) 签发/刷新/吊销 + 粗角色 + 外部身份兑换。
// 契约见 docs/architecture-v2.md §2/§4/附录 G/H。刻意保持极薄：业务概念不得进入。
package main

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type user struct {
	ID           int
	Username     string
	PasswordHash string
	Role         string
	Status       int
}

type server struct {
	db            *pgxpool.Pool
	key           *rsa.PrivateKey
	loginServices map[string]bool
	mu            sync.Mutex
	revoked       map[string]time.Time
}

func main() {
	registerClient := flag.String("register-client", "", "注册服务/插件凭据后退出（打印明文 secret）")
	scopes := flag.String("scopes", "", "register-client 的授权范围（逗号分隔）")
	flag.Parse()

	db := mustConnect(requireEnv("DATABASE_URL"))
	if *registerClient != "" {
		if err := migrateClients(db); err != nil {
			log.Fatal(err)
		}
		if err := registerClientCLI(db, *registerClient, *scopes); err != nil {
			log.Fatal(err)
		}
		return
	}

	key, err := loadOrCreateKeys(envOr("JWT_PRIVATE_KEY_FILE", "/keys/pf-auth.pem"))
	if err != nil {
		log.Fatalf("keys: %v", err)
	}
	if err := migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	s := &server{db: db, key: key, loginServices: loginServiceWhitelist(), revoked: map[string]time.Time{}}
	go s.janitor()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/login", s.handleLogin)
	mux.HandleFunc("POST /auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	mux.HandleFunc("GET /auth/me", s.handleMe)
	mux.HandleFunc("GET /auth/.well-known/jwks.json", s.handleJWKS)
	mux.HandleFunc("POST /auth/service-token", s.handleServiceToken)
	mux.HandleFunc("POST /internal/auth/external-login", s.handleExternalLogin)
	mux.HandleFunc("POST /internal/auth/bind-external", s.handleBindExternal)
	mux.HandleFunc("POST /internal/auth/introspect", s.handleIntrospect)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /{$}", handlePlatformInfo)
	mux.HandleFunc("/", handleNotFound) // 网关兜底路由指向 auth：未匹配路径返回平台风格 JSON 404

	log.Println("pf-auth (RS256) listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func requireEnv(name string) string {
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

func mustConnect(url string) *pgxpool.Pool {
	var db *pgxpool.Pool
	var err error
	for i := 0; i < 30; i++ {
		db, err = pgxpool.New(context.Background(), url)
		if err == nil {
			if err = db.Ping(context.Background()); err == nil {
				return db
			}
		}
		log.Printf("waiting for database (%d/30): %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	log.Fatalf("database unreachable: %v", err)
	return nil
}

func migrate(db *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id            SERIAL PRIMARY KEY,
			username      TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'user',
			status        INT  NOT NULL DEFAULT 1,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return err
	}
	if err := migrateExternal(db); err != nil {
		return err
	}
	if err := migrateClients(db); err != nil {
		return err
	}
	// 试运行种子用户；生产删除
	for _, u := range []struct{ name, pass, role string }{
		{"admin", "admin123", "admin"}, {"alice", "user123", "user"},
	} {
		hash, herr := bcrypt.GenerateFromPassword([]byte(u.pass), bcrypt.DefaultCost)
		if herr != nil {
			return herr
		}
		if _, err = db.Exec(ctx,
			`INSERT INTO users (username, password_hash, role) VALUES ($1,$2,$3) ON CONFLICT (username) DO NOTHING`,
			u.name, string(hash), u.role); err != nil {
			return err
		}
	}
	return nil
}

// ── handlers ─────────────────────────────────────────────────

func (s *server) loadUser(ctx context.Context, by string, val any) (user, error) {
	var u user
	row := s.db.QueryRow(ctx,
		`SELECT id, username, password_hash, role, status FROM users WHERE `+by+` = $1`, val)
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Status)
	return u, err
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Username == "" {
		writeErr(w, 400, "username and password required")
		return
	}
	u, err := s.loadUser(r.Context(), "username", in.Username)
	if err != nil || u.Status != 1 ||
		bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		writeErr(w, 401, "invalid credentials")
		return
	}
	s.issuePair(w, u)
}

func (s *server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var in struct{ RefreshToken string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.RefreshToken == "" {
		writeErr(w, 400, "refreshToken required")
		return
	}
	c, err := s.parse(in.RefreshToken)
	if err != nil || c.TokenType != "refresh" {
		writeErr(w, 401, "invalid refresh token")
		return
	}
	u, err := s.loadUser(r.Context(), "id", c.UserId)
	if err != nil || u.Status != 1 {
		writeErr(w, 401, "user unavailable")
		return
	}
	s.revoke(c) // refresh 轮换
	s.issuePair(w, u)
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, err := s.bearer(r)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	s.revoke(c)
	var in struct{ RefreshToken string }
	if json.NewDecoder(r.Body).Decode(&in) == nil && in.RefreshToken != "" {
		if rc, rerr := s.parse(in.RefreshToken); rerr == nil && rc.UserId == c.UserId {
			s.revoke(rc)
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	c, err := s.bearer(r)
	if err != nil {
		writeErr(w, 401, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"userId": c.UserId, "username": c.Username, "role": c.Role,
		"tenantId": c.TenantId, "tokenId": c.TokenId, "isImpersonation": c.IsImpersonation,
	})
}

func (s *server) handleIntrospect(w http.ResponseWriter, r *http.Request) {
	var in struct{ TokenId string }
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.TokenId == "" {
		writeErr(w, 400, "tokenId required")
		return
	}
	writeJSON(w, 200, map[string]bool{"active": !s.isRevoked(in.TokenId)})
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeErr(w, 500, "db unreachable")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func handlePlatformInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"platform": "Platfarm",
		"docs":     "https://github.com/platfarmai/platfarm",
		"login":    "POST /auth/login",
		"jwks":     "GET /auth/.well-known/jwks.json",
		"services": "业务服务挂载于 /api/*（清单见仓库 services/*/service.yaml）",
	})
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 404, map[string]string{"error": "not found", "path": r.URL.Path, "hint": "GET / 查看平台入口"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
