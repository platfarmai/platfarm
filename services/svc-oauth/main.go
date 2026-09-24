package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	st       *store
	draining atomic.Bool
)

func main() {
	registerProvider(newMockProvider())
	registerProvider(newGitHubProvider())
	registerProvider(newWeChatProvider())
	st = newStore()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/readyz", func(c *gin.Context) {
		if draining.Load() {
			c.JSON(503, gin.H{"error": "draining"})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})

	r.GET("/api/oauth/:provider/authorize", handleAuthorize)
	r.GET("/api/oauth/:provider/callback", handleCallback)
	r.POST("/api/oauth/exchange", handleExchange)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		log.Println("svc-oauth listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	draining.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func handleAuthorize(c *gin.Context) {
	p, ok := lookupProvider(c.Param("provider"))
	if !ok {
		c.JSON(404, gin.H{"error": "unknown provider"})
		return
	}
	state := newState()
	st.putState(state)
	loc, err := p.Start(state)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			c.JSON(501, gin.H{"error": err.Error()})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, loc)
}

func handleCallback(c *gin.Context) {
	p, ok := lookupProvider(c.Param("provider"))
	if !ok {
		c.JSON(404, gin.H{"error": "unknown provider"})
		return
	}
	state := c.Query("state")
	code := c.Query("code")
	if !st.takeState(state) {
		c.JSON(401, gin.H{"error": "invalid or expired state"})
		return
	}
	profile, err := p.Callback(code)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			c.JSON(501, gin.H{"error": err.Error()})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	tokens, err := exchangeExternal(profile)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	once := newState()
	st.putCode(once, tokens)
	c.JSON(200, gin.H{"exchangeCode": once, "expiresIn": int(codeTTL.Seconds())})
}

func handleExchange(c *gin.Context) {
	var in struct {
		Code string `json:"code"`
	}
	_ = c.ShouldBindJSON(&in)
	tokens := st.takeCode(in.Code)
	if tokens == nil {
		c.JSON(401, gin.H{"error": "invalid or expired exchange code"})
		return
	}
	c.JSON(200, tokens)
}

func newState() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
