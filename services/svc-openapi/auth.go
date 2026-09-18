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

const issuer = "pf-auth"

// Claims: platform identity contract (architecture-v2 §2.1).
type Claims struct {
	TokenType string `json:"tokenType"`
	UserId    int    `json:"userId"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	jwt.RegisteredClaims
}

var pubKey *rsa.PublicKey

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
	if claims.TokenType != "access" {
		return nil, errors.New("access token required")
	}
	return claims, nil
}

// requireAdmin: platform-admin only; stores the raw user token for OBO forwarding.
func requireAdmin(c *gin.Context) {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		return
	}
	raw := strings.TrimPrefix(h, "Bearer ")
	claims, err := decode(raw)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if claims.Role != "admin" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "platform admin required"})
		return
	}
	c.Set("userToken", raw)
	c.Next()
}
