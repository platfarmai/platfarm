package main

import "testing"

func TestParseScopeCatalog(t *testing.T) {
	got := parseScopeCatalog(`[{"service":"svc-pay","name":"pay.order.create","desc":"创建订单","routes":["POST /api/pay/v1/orders"]},
		{"service":"svc-pay","name":"pay.order.query","desc":"查询订单"}]`)
	if len(got) != 2 || got[0].Service != "svc-pay" || got[0].Name != "pay.order.create" || got[0].Routes[0] != "POST /api/pay/v1/orders" {
		t.Fatalf("got %+v", got)
	}
	if got[1].Routes == nil {
		t.Fatal("routes must serialize as [] rather than null")
	}
}

// 没注入或内容损坏时给空目录，而不是让控制台起不来。
func TestParseScopeCatalog_whenMissingOrBroken_thenEmpty(t *testing.T) {
	for _, raw := range []string{"", "  ", "{not json"} {
		if got := parseScopeCatalog(raw); got == nil || len(got) != 0 {
			t.Errorf("%q: got %+v", raw, got)
		}
	}
}
