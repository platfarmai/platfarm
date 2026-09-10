// __SVC_ID__ — Platfarm 业务服务（接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5）。
package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	mount  = "__MOUNT__"
	issuer = "pf-auth"
)

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
	pubKey         *rsa.PublicKey
	acceptServices = map[string]bool{}
)

func mustLoadPub() {
	path := os.Getenv("JWT_PUBLIC_KEY_FILE")
	if path == "" {
		path = "/pf/jwt.pub"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read public key: %v", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		log.Fatal("invalid public key PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		log.Fatalf("parse public key: %v", err)
	}
	pk, ok := key.(*rsa.PublicKey)
	if !ok {
		log.Fatal("public key is not RSA")
	}
	pubKey = pk
	for _, s := range strings.Split(os.Getenv("PF_ACCEPT_SERVICE_TOKENS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			acceptServices[s] = true
		}
	}
}

func decode(token string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return pubKey, nil
	}, jwt.WithIssuer(issuer))
	if err != nil || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// identity 六步约定 + OBO：access 直接得身份；service 需在白名单，可携 X-PF-User-Token 代表用户。
func identity(c *gin.Context) (*Claims, error) {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, errors.New("missing bearer token")
	}
	claims, err := decode(strings.TrimPrefix(h, "Bearer "))
	if err != nil {
		return nil, err
	}
	switch claims.TokenType {
	case "access":
		return claims, nil
	case "service":
		if !acceptServices[claims.Svc] {
			return nil, errors.New("service caller not allowed")
		}
		if userTok := c.GetHeader("X-PF-User-Token"); userTok != "" {
			uc, uerr := decode(userTok)
			if uerr != nil || uc.TokenType != "access" {
				return nil, errors.New("X-PF-User-Token must be an access token")
			}
			return uc, nil
		}
		return claims, nil
	}
	return nil, errors.New("access or service token required")
}

func main() {
	mustLoadPub()
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET(mount+"/public/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pong": true, "service": "__SVC_ID__", "auth": "not required"})
	})

	r.GET(mount+"/me", func(c *gin.Context) {
		claims, err := identity(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"service": "__SVC_ID__", "userId": claims.UserId, "username": claims.Username,
			"role": claims.Role, "tenantId": claims.TenantId, "svc": claims.Svc,
		})
	})

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	log.Println("__SVC_ID__ listening on :8080")
	log.Fatal(r.Run(":8080"))
}
