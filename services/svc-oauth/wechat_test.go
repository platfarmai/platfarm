package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWeChatStart_fails_when_config_missing(t *testing.T) {
	t.Setenv("WECHAT_APPID", "")
	t.Setenv("WECHAT_SECRET", "")
	p := newWeChatProvider()

	_, err := p.Start("state-1")

	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestWeChatCallback_prefers_unionid_over_openid(t *testing.T) {
	t.Setenv("WECHAT_APPID", "app-id")
	t.Setenv("WECHAT_SECRET", "app-secret")
	p := newWeChatProvider()

	orig := wechatGet
	defer func() { wechatGet = orig }()
	wechatGet = func(rawURL string) (*http.Response, error) {
		switch {
		case strings.Contains(rawURL, "/sns/oauth2/access_token"):
			return jsonResponse(200, map[string]any{
				"access_token": "tok",
				"openid":       "oid-1",
				"unionid":      "uid-9",
			}), nil
		case strings.Contains(rawURL, "/sns/userinfo"):
			return jsonResponse(200, map[string]any{
				"openid":   "oid-1",
				"nickname": "Wei",
				"unionid":  "uid-9",
			}), nil
		default:
			t.Fatalf("unexpected url: %s", rawURL)
			return nil, nil
		}
	}

	got, err := p.Callback("auth-code")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if got.Provider != "wechat" {
		t.Fatalf("provider=%q", got.Provider)
	}
	if got.Subject != "uid-9" {
		t.Fatalf("subject=%q want unionid", got.Subject)
	}
	if got.DisplayName != "Wei" {
		t.Fatalf("display=%q", got.DisplayName)
	}
}

func TestWeChatCallback_falls_back_to_openid_when_no_unionid(t *testing.T) {
	t.Setenv("WECHAT_APPID", "app-id")
	t.Setenv("WECHAT_SECRET", "app-secret")
	p := newWeChatProvider()

	orig := wechatGet
	defer func() { wechatGet = orig }()
	wechatGet = func(rawURL string) (*http.Response, error) {
		if strings.Contains(rawURL, "/sns/oauth2/access_token") {
			return jsonResponse(200, map[string]any{
				"access_token": "tok",
				"openid":       "oid-only",
			}), nil
		}
		return jsonResponse(200, map[string]any{
			"openid":   "oid-only",
			"nickname": "Open",
		}), nil
	}

	got, err := p.Callback("auth-code")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if got.Subject != "oid-only" {
		t.Fatalf("subject=%q want openid", got.Subject)
	}
}

func TestWeChatCallback_surfaces_errcode_from_token(t *testing.T) {
	t.Setenv("WECHAT_APPID", "app-id")
	t.Setenv("WECHAT_SECRET", "app-secret")
	p := newWeChatProvider()

	orig := wechatGet
	defer func() { wechatGet = orig }()
	wechatGet = func(rawURL string) (*http.Response, error) {
		return jsonResponse(200, map[string]any{
			"errcode": 40029,
			"errmsg":  "invalid code",
		}), nil
	}

	_, err := p.Callback("bad-code")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "40029") {
		t.Fatalf("error should surface errcode, got %v", err)
	}
}

func TestWeChatStart_builds_qrconnect_url(t *testing.T) {
	t.Setenv("WECHAT_APPID", "wx-app")
	t.Setenv("WECHAT_SECRET", "wx-secret")
	t.Setenv("SELF_URL", "http://localhost:18000")
	p := newWeChatProvider()

	loc, err := p.Start("st-xyz")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.HasPrefix(loc, "https://open.weixin.qq.com/connect/qrconnect?") {
		t.Fatalf("bad prefix: %s", loc)
	}
	if !strings.Contains(loc, "appid=wx-app") {
		t.Fatalf("missing appid: %s", loc)
	}
	if !strings.Contains(loc, "scope=snsapi_login") {
		t.Fatalf("missing scope: %s", loc)
	}
	if !strings.Contains(loc, "state=st-xyz") {
		t.Fatalf("missing state: %s", loc)
	}
	if !strings.HasSuffix(loc, "#wechat_redirect") {
		t.Fatalf("missing fragment: %s", loc)
	}
}

func jsonResponse(code int, v any) *http.Response {
	b, _ := json.Marshal(v)
	rec := httptest.NewRecorder()
	rec.WriteHeader(code)
	_, _ = rec.Write(b)
	resp := rec.Result()
	resp.Body = io.NopCloser(strings.NewReader(string(b)))
	return resp
}

