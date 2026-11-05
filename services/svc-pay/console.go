// 内嵌管理台。同源 Cookie SSO，挂在 /api/pay/console。
package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:web/dist
var distFS embed.FS

const consolePrefix = mount + "/console"

func mountConsole(r *gin.Engine) {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	serveIndex := func(c *gin.Context) {
		data, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			c.String(500, "console not built")
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
