// svc-openapi — open-platform admin console (specs/010).
// Proxies the auth internal apps API (service token + admin OBO) and serves an
// embedded Vue SPA under /api/openapi/console. Admin-only.
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

const consolePrefix = "/api/openapi/console"

func main() {
	mustLoadPub()
	ac := newAuthClient()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/readyz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	mountConsole(r)

	api := r.Group("/api/openapi", requireAdmin)
	api.GET("/scopes", func(c *gin.Context) { c.JSON(200, scopeCatalog()) })
	api.GET("/apps", proxyList(ac))
	api.POST("/apps", proxyBody(ac, http.MethodPost, func(c *gin.Context) string { return "/internal/auth/apps" }))
	api.PATCH("/apps/:key", proxyBody(ac, http.MethodPatch, func(c *gin.Context) string {
		return "/internal/auth/apps/" + c.Param("key")
	}))
	api.POST("/apps/:key/rotate", proxyBody(ac, http.MethodPost, func(c *gin.Context) string {
		return "/internal/auth/apps/" + c.Param("key") + "/rotate"
	}))
	api.GET("/usage", handleUsage) // 用量视图（specs/013）

	log.Println("svc-openapi listening on :8080")
	log.Fatal(r.Run(":8080"))
}

func proxyList(ac *authClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, out, err := ac.call(http.MethodGet, "/internal/auth/apps", c.GetString("userToken"), nil)
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

// scopeCatalog: M2 static list; M3 auto-derives from manifest sync.
func scopeCatalog() []map[string]string {
	return []map[string]string{
		{"name": "data.orders.read", "desc": "读取订单数据"},
	}
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

// ── tiny JSON helpers (avoid a struct just to read one field) ──

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
