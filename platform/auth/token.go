package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
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
	AppKey          string   `json:"appKey,omitempty"` // app token：开放平台 app（specs/009）
	Scopes          []string `json:"scopes,omitempty"` // service/app token：授权范围
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

// signApp 签发开放平台 app token（specs/009）：带 appKey 与授权 scopes。
func (s *server) signApp(appKey string, scopes []string) (string, error) {
	c := baseClaims("app", appTokenTTL)
	c.AppKey = appKey
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
	// 用户级吊销（specs/011）：禁用/改密后，此刻之前签发的用户 token 作废。
	if claims.UserId != 0 && claims.IssuedAt != nil {
		if cutoff := s.userKilledBefore(claims.UserId); !cutoff.IsZero() &&
			claims.IssuedAt.Time.Before(cutoff) {
			return nil, errors.New("token revoked (user)")
		}
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
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return s.parse(strings.TrimPrefix(h, "Bearer "))
	}
	// SSO 会话 Cookie 回退（specs/006）：浏览器直接调 /auth/me、/auth/logout 用。
	if ck, err := r.Cookie(sessionCookie); err == nil && ck.Value != "" {
		return s.parse(ck.Value)
	}
	return nil, errors.New("missing bearer token")
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
	// SSO 会话 Cookie（specs/006）：同源浏览器免二次登录；Bearer 仍照常返回给 API/curl。
	setSessionCookie(w, access)
	writeJSON(w, 200, tokenPair{access, refresh, int(accessTTL.Seconds())})
}

const sessionCookie = "pf_access"

// cookieSecure 由 PF_COOKIE_SECURE 控制（TLS 后端置 true）；默认 false 便于本机 http 调试。
func cookieSecure() bool {
	return strings.EqualFold(os.Getenv("PF_COOKIE_SECURE"), "true")
}

func setSessionCookie(w http.ResponseWriter, access string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    access,
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(accessTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
