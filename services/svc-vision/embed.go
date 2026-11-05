package main

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var distFS embed.FS

// consoleDist 返回以 web/dist 为根的文件系统，用于内嵌控制台静态资源与 SPA 回退。
func consoleDist() (fs.FS, error) {
	return fs.Sub(distFS, "web/dist")
}
