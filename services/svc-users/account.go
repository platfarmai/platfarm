// 账号自助页面（specs/021）：注册 / 找回密码 / 邮箱验证 / TOTP 设置的公开静态页。
// 纯静态单页（无构建步骤），登录态复用同源 pf_access Cookie（specs/006 SSO）。
package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed account
var accountFS embed.FS

const accountPrefix = "/api/users/account"

func mountAccount(r *gin.Engine) {
	sub, err := fs.Sub(accountFS, "account")
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
		rel := strings.TrimPrefix(strings.TrimPrefix(c.Request.URL.Path, accountPrefix), "/")
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
	r.GET(accountPrefix, func(c *gin.Context) { c.Redirect(301, accountPrefix+"/") })
	r.GET(accountPrefix+"/*filepath", serve)
}
