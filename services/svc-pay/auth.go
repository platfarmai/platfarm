package main

import (
	"github.com/gin-gonic/gin"
	"github.com/platfarmai/sdk/go/pfauth"
)

type Claims = pfauth.Claims

func mustLoadPub() { pfauth.Load() }

func identity(c *gin.Context) (*Claims, error) {
	return pfauth.Identity(c.GetHeader("Authorization"), c.GetHeader("X-PF-User-Token"))
}
