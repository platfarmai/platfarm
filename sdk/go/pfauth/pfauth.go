// Package pfauth 是 Go 服务验签的唯一实现（specs/015 之后的 SDK 收敛）。
// 模板与各服务应调用本包，而不是各自复制 JWT 解析。
package pfauth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const issuer = "pf-auth"

// Claims 契约：docs/architecture-v2.md §2.1
type Claims struct {
	TokenType string   `json:"tokenType"`
	UserId    int      `json:"userId,omitempty"`
	Username  string   `json:"username,omitempty"`
	Role      string   `json:"role,omitempty"`
	TenantId  int      `json:"tenantId"`
	Svc       string   `json:"svc,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	jwt.RegisteredClaims
}

var (
	current   *rsa.PublicKey
	byKID     = map[string]*rsa.PublicKey{}
	acceptSvc = map[string]bool{}
)

// Load 读取当前公钥（JWT_PUBLIC_KEY_FILE）与轮换保留的旧公钥（JWT_EXTRA_PUB_DIR/*.pub，文件名即 kid）。
func Load() {
	path := os.Getenv("JWT_PUBLIC_KEY_FILE")
	if path == "" {
		path = "/pf/jwt.pub"
	}
	current = mustParse(mustRead(path))
	if dir := os.Getenv("JWT_EXTRA_PUB_DIR"); dir != "" {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if pk := parse(raw); pk != nil {
				byKID[strings.TrimSuffix(e.Name(), ".pub")] = pk
			}
		}
	}
	for _, s := range strings.Split(os.Getenv("PF_ACCEPT_SERVICE_TOKENS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			acceptSvc[s] = true
		}
	}
}

// Verify 验签并返回 claims。kid 命中旧公钥时用旧钥，否则用当前钥。
func Verify(token string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		if kid, _ := t.Header["kid"].(string); kid != "" {
			if k := byKID[kid]; k != nil {
				return k, nil
			}
		}
		return current, nil
	}, jwt.WithIssuer(issuer))
	if err != nil || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// Identity 六步约定 + OBO：access 直接得身份；service 需在白名单，
// userToken 非空时以其代表用户（调用方从 X-PF-User-Token 取出传入）。
func Identity(bearer, userToken string) (*Claims, error) {
	if !strings.HasPrefix(bearer, "Bearer ") {
		return nil, errors.New("missing bearer token")
	}
	claims, err := Verify(strings.TrimPrefix(bearer, "Bearer "))
	if err != nil {
		return nil, err
	}
	switch claims.TokenType {
	case "access":
		return claims, nil
	case "service":
		if !acceptSvc[claims.Svc] {
			return nil, errors.New("service caller not allowed")
		}
		if userToken != "" {
			uc, uerr := Verify(userToken)
			if uerr != nil || uc.TokenType != "access" {
				return nil, errors.New("X-PF-User-Token must be an access token")
			}
			return uc, nil
		}
		return claims, nil
	}
	return nil, errors.New("access or service token required")
}

func mustRead(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read public key: %v", err)
	}
	return raw
}

func mustParse(raw []byte) *rsa.PublicKey {
	pk := parse(raw)
	if pk == nil {
		log.Fatal("invalid RSA public key")
	}
	return pk
}

func parse(raw []byte) *rsa.PublicKey {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil
	}
	pk, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil
	}
	return pk
}
