// svc-site — 支付中心根站点首页（mount.website）。
// 仅承载公开静态资产与探活端点；登录直走同源 /auth/login（Cookie pf_access）。
// 另外给浏览器渲染 /docs（支付开放 API 接入指南）和 /platform/info（平台接口索引说明）。
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

	pagesJSHandler := serveAsset(mustRead("pages.js"), "application/javascript; charset=utf-8", "public, max-age=300")
	mux.HandleFunc("GET /pages.js", pagesJSHandler)
	mux.HandleFunc("HEAD /pages.js", pagesJSHandler)

	// /docs（商户开放 API 接入指南）、/guide/isv（服务商入驻与运营指南）与 /platform/info：
	// 其中 /docs 与 /platform/info 网关只把浏览器请求（Accept 含 text/html）转到这里
	// （service.yaml 的 mount.browser_pages），程序请求仍由 auth 返回 JSON；
	// /guide/isv 由根站点直接承载，同时保留 /docs/isv 作为浏览器访问别名。
	payGuide, err := renderGuide(payGuideMD)
	if err != nil {
		panic(err)
	}
	isvGuide, err := renderGuide(isvGuideMD)
	if err != nil {
		panic(err)
	}
	docsTmpl := mustRead("docs.html")
	// 同一个地址按 Accept 返回页面或 JSON，告诉缓存两者不能混用。
	varyAccept := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Vary", "Accept")
			next(w, r)
		}
	}
	merchantDocsBytes := docsPage(docsTmpl, payGuide, docMeta{
		Subtitle:   "商户接入指南 · 开放 API",
		MetaDesc:   "三润支付中心商户开放 API 接入指南：凭据、签名、统一下单、退款、交易流水与按日账单汇总、异步通知与沙箱联调。",
		BannerHTML: `本站网关地址（下文的 <code class="pf-code">{base}</code>）：<code class="pf-code js-origin">https://apipay.sanrun.fun</code>。本页供<strong>商户技术团队</strong>对接下单、退款与查账 API；服务商配置证书与商户进件请看 <a class="pf-link" href="/guide/isv">服务商入驻与运营指南</a>。`,
		FooterNote: `© 2026 三润支付中心 · 本页给人看；程序请求 <code class="pf-code">GET /docs</code> 得到的是各服务 OpenAPI 文档索引（JSON）`,
	})
	isvDocsBytes := docsPage(docsTmpl, isvGuide, docMeta{
		Subtitle:   "服务商入驻与运营指南",
		MetaDesc:   "三润支付中心服务商入驻与运营指南：微信支付与支付宝渠道参数配置、双人复核商户进件、应用通道、代理商分润与每日渠道对账。",
		BannerHTML: `本页供<strong>支付服务商与运营审核人员</strong>在 <a class="pf-link" href="/api/pay/console/">支付中心工作台</a> 操作；下游商户技术团队对接下单、退款与查账接口请看 <a class="pf-link" href="/docs">商户支付开放 API 接入指南</a>。`,
		FooterNote: `© 2026 三润支付中心 · 服务商入驻与运营指南`,
	})
	docsHandler := varyAccept(serveAsset(merchantDocsBytes, "text/html; charset=utf-8", "no-cache"))
	isvGuideHandler := serveAsset(isvDocsBytes, "text/html; charset=utf-8", "no-cache")
	isvDocsAliasHandler := varyAccept(isvGuideHandler)
	infoHandler := varyAccept(serveAsset(mustRead("info.html"), "text/html; charset=utf-8", "no-cache"))
	for _, p := range []string{"/docs", "/docs/{$}"} {
		mux.HandleFunc("GET "+p, docsHandler)
		mux.HandleFunc("HEAD "+p, docsHandler)
	}
	for _, p := range []string{"/guide/isv", "/guide/isv/{$}"} {
		mux.HandleFunc("GET "+p, isvGuideHandler)
		mux.HandleFunc("HEAD "+p, isvGuideHandler)
	}
	for _, p := range []string{"/docs/isv", "/docs/isv/{$}"} {
		mux.HandleFunc("GET "+p, isvDocsAliasHandler)
		mux.HandleFunc("HEAD "+p, isvDocsAliasHandler)
	}
	for _, p := range []string{"/platform/info", "/platform/info/{$}"} {
		mux.HandleFunc("GET "+p, infoHandler)
		mux.HandleFunc("HEAD "+p, infoHandler)
	}

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
