package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteRoutes(t *testing.T) {
	h := newHandler()

	cases := []struct {
		method string
		path   string
		accept string
		code   int
		ctype  string
		needle string
	}{
		{http.MethodGet, "/healthz", "", 200, "application/json", `"ok":true`},
		{http.MethodGet, "/readyz", "", 200, "application/json", `"ok":true`},
		{http.MethodGet, "/", "text/html", 200, "text/html", "三润支付中心"},
		{http.MethodGet, "/style.css", "", 200, "text/css", "--pf-"},
		{http.MethodGet, "/app.js", "", 200, "application/javascript", "/auth/login"},
		{http.MethodGet, "/logo-108.png", "", 200, "image/png", ""},
		{http.MethodGet, "/favicon.ico", "", 200, "image/png", ""},
		{http.MethodGet, "/pages.js", "", 200, "application/javascript", "/platform/info"},
		{http.MethodGet, "/docs", "text/html,application/xhtml+xml", 200, "text/html", "支付开放 API 接入指南"},
		{http.MethodGet, "/docs/", "text/html", 200, "text/html", "X-Pay-Signature"},
		{http.MethodGet, "/platform/info", "text/html", 200, "text/html", "平台接口索引"},
		{http.MethodGet, "/platform/info", "text/html", 200, "text/html", "/oauth/token"},
		{http.MethodGet, "/docs/nope", "text/html", 404, "text/html", "404"},
		{http.MethodGet, "/unknown-page", "text/html,application/xhtml+xml", 404, "text/html", "404"},
		{http.MethodPost, "/unknown-api", "application/json", 404, "application/json", "/platform/info"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.accept != "" {
			req.Header.Set("Accept", tc.accept)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Fatalf("%s %s: expected %d, got %d (%s)", tc.method, tc.path, tc.code, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), tc.ctype) {
			t.Fatalf("%s %s: expected content-type %q, got %q", tc.method, tc.path, tc.ctype, rec.Header().Get("Content-Type"))
		}
		if tc.needle != "" && !strings.Contains(rec.Body.String(), tc.needle) {
			t.Fatalf("%s %s: body missing %q", tc.method, tc.path, tc.needle)
		}
	}

	// Verify JSON 404 structure for programmatic callers
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["error"] != "not found" {
		t.Fatalf("expected structured JSON 404, got %s", rec.Body.String())
	}
}

func TestDocsPage_rendersGuideWithAnchorsAndTables(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)
	body := rec.Body.String()

	for _, want := range []string{
		`<h2 id="s-1">`,                         // "## 1. 接入前要拿到什么"
		`<h3 id="s-2-1">`,                       // "### 2.1 平台 app token"
		`<a class="site-toc-item" href="#s-6">`, // 目录指向同一批锚点
		`<a class="site-toc-item site-toc-sub" href="#s-4-5">`,
		`<div class="site-table-wrap"><table>`,
		`<code>POST /api/pay/v1/orders</code>`,
		`<pre><code class="language-json">`,
		"Bearer &lt;accessToken&gt;", // 代码里的尖括号要转义，不能变成标签
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs page missing %q", want)
		}
	}
	for _, reject := range []string{
		"{{",            // 模板占位符必须全部替换
		"维护者",           // 文档里给维护者看的 HTML 注释不能发布出去
		"service.yaml",  // 同上
		"no docs index", // 不应再是那段空的 JSON
	} {
		if strings.Contains(body, reject) {
			t.Errorf("docs page must not contain %q", reject)
		}
	}
	if n := strings.Count(body, `id="s-`); n < 10 {
		t.Errorf("expected at least 10 section anchors, got %d", n)
	}
}

func TestRenderGuide_headingIDs(t *testing.T) {
	g, err := renderGuide([]byte("# 标题\r\n\r\n## 1. 甲\r\n\r\n### 1.1 乙 `code`\r\n\r\n## 附录\r\n\r\n## 1. 甲\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Title != "标题" {
		t.Fatalf("title = %q", g.Title)
	}
	var got []string
	for _, e := range g.TOC {
		got = append(got, e.ID+"="+e.Title)
	}
	want := "s-1=1. 甲|s-1-1=1.1 乙 code|s-h3=附录|s-1-h4=1. 甲"
	if strings.Join(got, "|") != want {
		t.Fatalf("toc = %q, want %q", strings.Join(got, "|"), want)
	}
}

// 发布出去的指南是仓库 docs/ 下那份的副本（go:embed 够不到模块外）。改了一份忘了另一份时在这里失败。
// 镜像内 / 单独拷出本目录构建时没有仓库文档，跳过。
func TestGuideCopyMatchesRepoDoc(t *testing.T) {
	repoDoc, err := os.ReadFile(filepath.Join("..", "..", "docs", "pay-open-api.zh-CN.md"))
	if err != nil {
		t.Skipf("repo doc not available: %v", err)
	}
	norm := func(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }
	if norm(repoDoc) != norm(payGuideMD) {
		t.Fatal("services/svc-site/content/pay-open-api.zh-CN.md 与 docs/pay-open-api.zh-CN.md 不一致：改完文档后把它复制到 svc-site/content/")
	}
}
