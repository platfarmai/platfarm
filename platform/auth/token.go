package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer     = "pf-auth"
	accessTTL  = 2 * time.Hour
	refreshTTL = 30 * 24 * time.Hour
	serviceTTL = 5 * time.Minute
)

// Claims 契约：docs/architecture-v2.md §2.1 + 附录 H.5（service token 的 svc/scopes）。
type Claims struct {
	TokenId         string   `json:"tokenId"`
	TokenType       string   `json:"tokenType"` // access | refresh | service
	ClientType      string   `json:"clientType,omitempty"`
	UserId          int      `json:"userId,omitempty"`
	Username        string   `json:"username,omitempty"`
	Role            string   `json:"role,omitempty"` // admin | user
	TenantId        int      `json:"tenantId"`
	IsImpersonation bool     `json:"isImpersonation,omitempty"`
	ActingUserId    int      `json:"actingUserId,omitempty"`
	Svc             string   `json:"svc,omitempty"`    // service token：调用方服务 id
	Scopes          []string `json:"scopes,omitempty"` // service token：授权范围
	jwt.RegisteredClaims
}

func newTokenID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func baseClaims(tokenType string, ttl time.Duration) Claims {
	now := time.Now()
	return Claims{
		TokenId:   newTokenID(),
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
}

// signUser 签发用户 token（RS256，kid 进 header 供 JWKS 轮换）。
func (s *server) signUser(u user, tokenType string, ttl time.Duration) (string, *Claims, error) {
	c := baseClaims(tokenType, ttl)
	c.ClientType = "web"
	c.UserId = u.ID
	c.Username = u.Username
	c.Role = u.Role
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, &c)
	tok.Header["kid"] = keyID
	signed, err := tok.SignedString(s.key)
	return signed, &c, err
}

// signService 签发服务 token（client_credentials 兑换，短时效）。
func (s *server) signService(svc string, scopes []string) (string, error) {
	c := baseClaims("service", serviceTTL)
	c.Svc = svc
	c.Scopes = scopes
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, &c)
	tok.Header["kid"] = keyID
	return tok.SignedString(s.key)
}

func (s *server) parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return &s.key.PublicKey, nil
	}, jwt.WithIssuer(issuer))
	if err != nil || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	if s.isRevoked(claims.TokenId) {
		return nil, errors.New("token revoked")
	}
	return claims, nil
}

// bearer 从请求头取用户 access token。
func (s *server) bearer(r *http.Request) (*Claims, error) {
	c, err := s.bearerRaw(r)
	if err != nil {
		return nil, err
	}
	if c.TokenType != "access" {
		return nil, errors.New("access token required")
	}
	return c, nil
}

// bearerService 从请求头取 service token，并校验调用方在白名单内。
func (s *server) bearerService(r *http.Request, allowed map[string]bool) (*Claims, error) {
	c, err := s.bearerRaw(r)
	if err != nil {
		return nil, err
	}
	if c.TokenType != "service" || !allowed[c.Svc] {
		return nil, errors.New("whitelisted service token required")
	}
	return c, nil
}

func (s *server) bearerRaw(r *http.Request) (*Claims, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, errors.New("missing bearer token")
	}
	return s.parse(strings.TrimPrefix(h, "Bearer "))
}

// ── 吊销黑名单（单实例内存；多实例换 Redis，见附录 C）──

func (s *server) revoke(c *Claims) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[c.TokenId] = c.ExpiresAt.Time
}

func (s *server) isRevoked(tokenID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.revoked[tokenID]
	return ok
}

func (s *server) janitor() {
	for range time.Tick(10 * time.Minute) {
		now := time.Now()
		s.mu.Lock()
		for id, exp := range s.revoked {
			if now.After(exp) {
				delete(s.revoked, id)
			}
		}
		s.mu.Unlock()
	}
}

type tokenPair struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
}

func (s *server) issuePair(w http.ResponseWriter, u user) {
	access, _, err := s.signUser(u, "access", accessTTL)
	if err != nil {
		writeErr(w, 500, "sign failed")
		return
	}
	refresh, _, err := s.signUser(u, "refresh", refreshTTL)
	if err != nil {
		writeErr(w, 500, "sign failed")
		return
	}
	writeJSON(w, 200, tokenPair{access, refresh, int(accessTTL.Seconds())})
}
