// svc-site — 支付中心根站点首页（mount.website）。
// 仅承载公开静态资产与探活端点；登录直走同源 /auth/login（Cookie pf_access）。
package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

//go:embed all:static
var rawStatic embed.FS

func newHandler() http.Handler {
	sub, err := fs.Sub(rawStatic, "static")
	if err != nil {
		panic(err)
	}
	mustRead := func(name string) []byte {
		b, rerr := fs.ReadFile(sub, name)
		if rerr != nil {
			panic(rerr)
		}
		return b
	}

	indexHTML := mustRead("index.html")
	notFoundHTML := []byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>页面未找到 · 三润支付中心</title><link rel="icon" type="image/png" href="/logo-28.png"><link rel="stylesheet" href="/platform/ui/v1/pf-ui.css"><script src="/platform/ui/v1/pf-ui.js" defer></script></head><body class="pf-body"><div class="pf-container"><div class="pf-card"><h1 style="margin:0 0 8px;font-size:var(--pf-fs-20)">404 页面不存在</h1><p class="pf-muted" style="margin:0 0 16px">请求的路径不存在，请检查地址或返回支付中心首页。</p><a class="pf-link" href="/">返回首页</a></div></div></body></html>`)
	styleCSS := mustRead("style.css")
	appJS := mustRead("app.js")
	logo108 := mustRead("logo-108.png")
	logo28 := mustRead("logo-28.png")

	serveAsset := func(body []byte, contentType, cacheControl string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", cacheControl)
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(body)
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	indexHandler := serveAsset(indexHTML, "text/html; charset=utf-8", "no-cache")
	mux.HandleFunc("GET /{$}", indexHandler)
	mux.HandleFunc("HEAD /{$}", indexHandler)
	mux.HandleFunc("GET /index.html", indexHandler)
	mux.HandleFunc("HEAD /index.html", indexHandler)

	cssHandler := serveAsset(styleCSS, "text/css; charset=utf-8", "public, max-age=300")
	mux.HandleFunc("GET /style.css", cssHandler)
	mux.HandleFunc("HEAD /style.css", cssHandler)

	jsHandler := serveAsset(appJS, "application/javascript; charset=utf-8", "public, max-age=300")
	mux.HandleFunc("GET /app.js", jsHandler)
	mux.HandleFunc("HEAD /app.js", jsHandler)

	logo108Handler := serveAsset(logo108, "image/png", "public, max-age=3600")
	mux.HandleFunc("GET /logo-108.png", logo108Handler)
	mux.HandleFunc("HEAD /logo-108.png", logo108Handler)

	logo28Handler := serveAsset(logo28, "image/png", "public, max-age=3600")
	mux.HandleFunc("GET /logo-28.png", logo28Handler)
	mux.HandleFunc("HEAD /logo-28.png", logo28Handler)
	mux.HandleFunc("GET /favicon.ico", logo28Handler)
	mux.HandleFunc("HEAD /favicon.ico", logo28Handler)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			if r.Method != http.MethodHead {
				_, _ = w.Write(notFoundHTML)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "not found",
			"path":  r.URL.Path,
			"hint":  "GET /platform/info 查看平台接口索引",
		})
	})

	return mux
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           newHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("svc-site listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
