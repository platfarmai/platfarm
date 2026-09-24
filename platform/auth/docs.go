package main

import (
	"net/http"
	"os"
	"strings"
)

// handleDocs 列出已声明文档的服务。真实聚合由 pctl sync 把清单写进
// docs/index.json 并挂进镜像；这里读那份静态索引，没有就返回空列表。
func handleDocsIndex(w http.ResponseWriter, r *http.Request) {
	raw, err := os.ReadFile(envOr("PF_DOCS_INDEX", "docs/index.json"))
	if err != nil {
		writeJSON(w, 200, map[string]any{"services": []any{}, "note": "no docs index published"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(strings.TrimSpace(string(raw))))
}
