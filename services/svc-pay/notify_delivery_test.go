package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestValidNotifyURL(t *testing.T) {
	cases := map[string]bool{
		"https://merchant.example/notify":  true,
		"https://8.8.8.8/n":                true,
		"http://merchant.example/n":        false,
		"https://user:pw@merchant.example": false,
		"https://localhost/n":              false,
		"https://127.0.0.1/n":              false,
		"https://10.1.2.3/n":               false,
		"https://169.254.169.254/latest":   false,
		"https://100.64.0.1/n":             false,
		"https://0.0.0.0/n":                false,
		"https://[::1]/n":                  false,
		"https://[fd00::1]/n":              false,
		"https://[::ffff:127.0.0.1]/n":     false,
		"https:///n":                       false,
	}
	for in, want := range cases {
		if got := validNotifyURL(in); got != want {
			t.Errorf("validNotifyURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNotifyDelivery_RefusesInternalTargetsAndRedirects(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "SUCCESS")
	})
	internal := httptest.NewServer(handler)
	defer internal.Close()
	internalTLS := httptest.NewTLSServer(handler)
	defer internalTLS.Close()
	port := strings.TrimPrefix(internalTLS.URL, "https://127.0.0.1:")

	// 域名解析到回环：静态检查放过的主机名，连接时按真实 IP 拒绝。
	if _, err := notifyClient.Get("https://localhost:" + port + "/n"); err == nil || !strings.Contains(err.Error(), "非公网") {
		t.Fatalf("dial guard must refuse loopback, got %v", err)
	}
	for _, target := range []string{"https://127.0.0.1:" + port + "/n", "https://localhost:" + port + "/n", "https://localhost.:" + port + "/n"} {
		if text, ok := postNotify(ctx, target, "{}"); ok {
			t.Fatalf("%s delivered: %q", target, text)
		}
	}

	// 公网商户回 3xx 指向内网：不跟随。
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/admin", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	old := notifyClient
	defer func() { notifyClient = old }()
	notifyClient = newNotifyClient(func(net.IP) bool { return true })
	tr := notifyClient.Transport.(*http.Transport)
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, redirector.Listener.Addr().String())
	}
	text, ok := postNotify(ctx, "https://merchant.example/n", "{}")
	if ok || !strings.Contains(text, "307") {
		t.Fatalf("redirect must not be followed: ok=%v text=%q", ok, text)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("internal server reached %d times", n)
	}
}
