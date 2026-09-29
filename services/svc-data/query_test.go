package main

import (
	"strings"
	"testing"
)

func TestAppCanReadDataset(t *testing.T) {
	app := func(scopes ...string) *Claims { return &Claims{TokenType: "app", Scopes: scopes} }
	cases := []struct {
		name, dataset string
		claims        *Claims
		want          bool
	}{
		{"存量 app 无数据集 scope 放行", "d4", app("data.records.read"), true},
		{"显式授权命中", "d4", app("dataset:d4:read"), true},
		{"显式授权不命中", "wow", app("dataset:d4:read"), false},
		{"通配授权", "wow", app("dataset:*:read"), true},
		{"用户 token 不受限", "wow", &Claims{TokenType: "access"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := appCanReadDataset(tt.claims, tt.dataset); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProjectFields(t *testing.T) {
	payload := []byte(`{"sku":"1","aspect":{"key":"K","name":"N"},"other":1}`)
	out := string(projectFields(payload, []string{"sku", "aspect.key"}))
	for _, want := range []string{`"sku":"1"`, `"key":"K"`} {
		if !strings.Contains(out, want) {
			t.Errorf("projected %s missing %s", out, want)
		}
	}
	if strings.Contains(out, "other") || strings.Contains(out, `"name"`) {
		t.Errorf("projected %s contains unwanted fields", out)
	}
	// 非法 JSON 原样返回
	if got := string(projectFields([]byte("not json"), []string{"a"})); got != "not json" {
		t.Errorf("bad json should pass through, got %s", got)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	secretKey = make([]byte, 32)
	for i := range secretKey {
		secretKey[i] = byte(i)
	}
	sealed, err := seal("whs_test_secret")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed == "whs_test_secret" {
		t.Fatal("sealed must not equal plain")
	}
	got, err := open(sealed)
	if err != nil || got != "whs_test_secret" {
		t.Fatalf("open = %q, %v", got, err)
	}
	// 存量明文兼容
	if got, err := open("legacy-plain"); err != nil || got != "legacy-plain" {
		t.Fatalf("legacy plain = %q, %v", got, err)
	}
	// 空串
	if s, err := seal(""); err != nil || s != "" {
		t.Fatalf("empty seal = %q, %v", s, err)
	}
}
