package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/platfarmai/sdk/go/pfauth"
)

type Claims = pfauth.Claims

func mustLoadPub() { pfauth.Load() }

func identity(c *gin.Context) (*Claims, error) {
	return pfauth.Identity(c.GetHeader("Authorization"), c.GetHeader("X-PF-User-Token"))
}

// requireAdmin: platform-admin only; stores the raw user token for OBO forwarding.
func requireAdmin(c *gin.Context) {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		return
	}
	raw := strings.TrimPrefix(h, "Bearer ")
	claims, err := pfauth.Verify(raw)
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
