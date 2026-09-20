// svc-users — platform user management console (specs/011).
// Proxies the auth internal users API (service token + admin OBO) and passes
// through self change-password. Embedded Vue SPA under /api/users/console.
package main

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:web/dist
var distFS embed.FS

const consolePrefix = "/api/users/console"

func main() {
	mustLoadPub()
	ac := newAuthClient()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/readyz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	mountConsole(r)

	// self change-password: any logged-in user; proxy straight to auth with the user's own token
	r.POST("/api/users/change-password", func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.JSON(401, gin.H{"error": "missing bearer token"})
			return
		}
		body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 8192))
		status, out, err := ac.callUser(http.MethodPost, "/auth/change-password",
			strings.TrimPrefix(h, "Bearer "), body)
		writeProxy(c, status, out, err)
	})

	// admin user management
	api := r.Group("/api/users", requireAdmin)
	api.GET("", proxyList(ac))
	api.POST("", proxyBody(ac, http.MethodPost, func(c *gin.Context) string { return "/internal/auth/users" }))
	api.PATCH("/:id", proxyBody(ac, http.MethodPatch, func(c *gin.Context) string {
		return "/internal/auth/users/" + c.Param("id")
	}))
	api.POST("/:id/reset-password", proxyBody(ac, http.MethodPost, func(c *gin.Context) string {
		return "/internal/auth/users/" + c.Param("id") + "/reset-password"
	}))

	log.Println("svc-users listening on :8080")
	log.Fatal(r.Run(":8080"))
}

func proxyList(ac *authClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, out, err := ac.call(http.MethodGet, "/internal/auth/users", c.GetString("userToken"), nil)
		writeProxy(c, status, out, err)
	}
}

func proxyBody(ac *authClient, method string, path func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 8192))
		status, out, err := ac.call(method, path(c), c.GetString("userToken"), body)
		writeProxy(c, status, out, err)
	}
}

func writeProxy(c *gin.Context, status int, out []byte, err error) {
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Data(status, "application/json", out)
}

// ── embedded SPA ─────────────────────────────────────────────

func mountConsole(r *gin.Engine) {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	serveIndex := func(c *gin.Context) {
		data, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			c.String(500, "index missing")
			return
		}
		c.Data(200, "text/html; charset=utf-8", data)
	}
	serve := func(c *gin.Context) {
		rel := strings.TrimPrefix(strings.TrimPrefix(c.Request.URL.Path, consolePrefix), "/")
		if rel == "" {
			serveIndex(c)
			return
		}
		if st, err := fs.Stat(sub, rel); err != nil || st.IsDir() {
			serveIndex(c)
			return
		}
		req := c.Request.Clone(c.Request.Context())
		req.URL.Path = "/" + rel
		fileServer.ServeHTTP(c.Writer, req)
	}
	r.GET(consolePrefix, func(c *gin.Context) { c.Redirect(301, consolePrefix+"/") })
	r.GET(consolePrefix+"/*filepath", serve)
}

// ── tiny JSON helpers ──

func extractJSONString(raw []byte, key string) string {
	s := string(raw)
	needle := `"` + key + `":"`
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	rest := s[i+len(needle):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func errFromBody(raw []byte) error {
	if e := extractJSONString(raw, "error"); e != "" {
		return errors.New(e)
	}
	return errors.New("service-token request failed")
}
