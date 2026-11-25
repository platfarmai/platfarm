package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
