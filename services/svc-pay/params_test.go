package main

import (
	"testing"
	"time"
)

func TestFeeAmount(t *testing.T) {
	cases := []struct {
		amount int64
		bps    int
		want   int64
	}{
		{10000, 60, 60}, // 100 元 × 0.60% = 0.60 元
		{10001, 60, 60}, // 60.006 分 → 四舍五入 60
		{10100, 60, 61}, // 60.6 分 → 61
		{100, 0, 0},     // 未配置费率
		{1, 5000, 1},    // 最小额向上
	}
	for _, c := range cases {
		if got := feeAmount(c.amount, c.bps); got != c.want {
			t.Errorf("fee(%d, %d)=%d, want %d", c.amount, c.bps, got, c.want)
		}
	}
}

func TestSignPayloadStable(t *testing.T) {
	a := signPayload("secret", "app", "1000", "n", "POST", "/api/pay/v1/refunds", `{"a":1}`)
	b := signPayload("secret", "app", "1000", "n", "POST", "/api/pay/v1/refunds", `{"a":1}`)
	if a != b || len(a) != 64 {
		t.Fatalf("sign not stable hex: %s", a)
	}
	if signPayload("other", "app", "1000", "n", "POST", "/api/pay/v1/refunds", `{"a":1}`) == a {
		t.Fatal("different secret produced same signature")
	}
	// 同一份正文、随机串：换接口或换单号，签名必须不同。
	if signPayload("secret", "app", "1000", "n", "GET", "/api/pay/v1/refunds", `{"a":1}`) == a ||
		signPayload("secret", "app", "1000", "n", "POST", "/api/pay/v1/orders/X/close", `{"a":1}`) == a {
		t.Fatal("method and path must be part of the signature")
	}
}

func TestNonceCacheRejectsReplay(t *testing.T) {
	c := &nonceCache{seen: map[string]time.Time{}}
	if !c.remember("app:n1", time.Minute) || c.remember("app:n1", time.Minute) {
		t.Fatal("second use of a nonce must be rejected")
	}
	if !c.remember("app:n2", -time.Second) || !c.remember("app:n2", time.Minute) {
		t.Fatal("an expired nonce may be reused")
	}
}

func TestSignFieldsOrder(t *testing.T) {
	s1 := signFields("k", map[string]string{"b": "2", "a": "1", "sign": "ignored", "c": ""})
	s2 := signFields("k", map[string]string{"a": "1", "b": "2"})
	if s1 != s2 {
		t.Fatalf("field order or empty/sign handling differs: %s vs %s", s1, s2)
	}
}

func TestParseParams(t *testing.T) {
	m := parseParams(`{"sp_mchid":"123","sp_appid":"wx1"}`)
	if m["sp_mchid"] != "123" || m["sp_appid"] != "wx1" {
		t.Fatalf("%v", m)
	}
	if len(parseParams("not json")) != 0 {
		t.Fatal("bad json should yield empty map")
	}
}
