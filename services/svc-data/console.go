// svc-data 内嵌管理页：单文件 HTML（go:embed），经 SSO 壳以 admin 身份访问。
// 网关全局 pre-function 会把 pf_access Cookie 转成 Authorization Bearer（specs/006），页面无需管 token。
package main

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed console.html
var consoleHTML []byte

func handleConsole(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", consoleHTML)
}
