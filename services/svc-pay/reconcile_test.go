package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"platfarm/svc-pay/channel"
)

func TestDiffBill_AllDiffTypes(t *testing.T) {
	rows := []channel.BillRow{
		{OutTradeNo: "P1", ChannelOrderNo: "T1", TradeState: "SUCCESS", Amount: 1000},                     // 一致
		{OutTradeNo: "P2", ChannelOrderNo: "T2", TradeState: "SUCCESS", Amount: 2000},                     // 本地没有
		{OutTradeNo: "P3", ChannelOrderNo: "T3", TradeState: "SUCCESS", Amount: 3000},                     // 金额不一致
		{OutTradeNo: "P4", ChannelOrderNo: "T4", TradeState: "SUCCESS", Amount: 4000},                     // 本地未成功
		{OutTradeNo: "P5", ChannelOrderNo: "T5", TradeState: "NOTPAY", Amount: 5000},                      // 渠道未成功，忽略
		{OutTradeNo: "P1", OutRefundNo: "R1", RefundID: "RF1", RefundState: "SUCCESS", RefundAmount: 300}, // 退款一致
		{OutTradeNo: "P1", OutRefundNo: "R2", RefundID: "RF2", RefundState: "SUCCESS", RefundAmount: 200}, // 本地无此退款
	}
	local := []localTrade{
		{Kind: "pay", ID: "P1", Amount: 1000, State: "success"},
		{Kind: "pay", ID: "P3", Amount: 3001, State: "success"},
		{Kind: "pay", ID: "P4", Amount: 4000, State: "paying"},
		{Kind: "pay", ID: "P6", Amount: 6000, State: "success"}, // 渠道没有
		{Kind: "refund", ID: "R1", Amount: 300, State: "success"},
		{Kind: "refund", ID: "R9", Amount: 100, State: "success"}, // 渠道没有
	}
	diffs := diffBill(rows, local)
	got := map[string]string{}
	for _, d := range diffs {
		got[d.Kind+":"+d.LocalID] = d.Type
	}
	want := map[string]string{
		"pay:P2": "missing_local", "pay:P3": "amount_mismatch", "pay:P4": "state_mismatch", "pay:P6": "missing_channel",
		"refund:R2": "missing_local", "refund:R9": "missing_channel",
	}
	if len(got) != len(want) {
		t.Fatalf("diffs = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %s want %s", k, got[k], v)
		}
	}
}

func TestDiffBill_NoDiffWhenConsistent(t *testing.T) {
	rows := []channel.BillRow{{OutTradeNo: "P1", TradeState: "SUCCESS", Amount: 100}, {OutTradeNo: "P2", TradeState: "REFUND", Amount: 200},
		{OutTradeNo: "P2", OutRefundNo: "R1", RefundState: "SUCCESS", RefundAmount: 200}}
	local := []localTrade{{Kind: "pay", ID: "P1", Amount: 100, State: "success"}, {Kind: "pay", ID: "P2", Amount: 200, State: "success"},
		{Kind: "refund", ID: "R1", Amount: 200, State: "success"}}
	if d := diffBill(rows, local); len(d) != 0 {
		t.Fatalf("unexpected diffs: %+v", d)
	}
}

func TestRecon_RunBatchWithStubBill(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-R", "M-REC", "stub_channel")
	seedApp(t, "M-REC", "A-REC")
	// 两笔本地成功单，渠道账单只有一笔 → 一条 missing_channel。
	o1 := seedSuccessOrder(t, "M-REC", "ISV-R", "A-REC", "P-REC-1", 1234, 60, 0)
	o2 := seedSuccessOrder(t, "M-REC", "ISV-R", "A-REC", "P-REC-2", 2000, 60, 0)
	day := time.Now().In(cst)
	stub.mu.Lock()
	stub.billRows = []channel.BillRow{{OutTradeNo: o1.PayOrderID, ChannelOrderNo: "T-P-REC-1", TradeState: "SUCCESS", Amount: 1234, SubMchID: "1900001"}}
	stub.billErr = nil
	stub.mu.Unlock()

	b, err := runRecon(ctx, "stub_channel", "ISV-R", day, "ALL", false)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != "done" || b.ChannelCount != 1 || b.LocalCount != 2 || b.DiffCount != 1 || b.LocalAmount != 3234 || b.ChannelAmount != 1234 {
		t.Fatalf("batch = %+v", b)
	}
	r := testRouter()
	res := call(t, r, "op", http.MethodGet, fmt.Sprintf("/api/pay/admin/recon/batches/%d/diffs", b.ID), nil)
	mustCode(t, res, 200)
	items := res.Body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["diffType"] != "missing_channel" || items[0].(map[string]any)["localId"] != o2.PayOrderID {
		t.Fatalf("diffs = %v", items)
	}
	diffID := int64(items[0].(map[string]any)["id"].(float64))

	// 幂等：不 force 不重跑（渠道账单变了也不动）。
	stub.mu.Lock()
	stub.billRows = append(stub.billRows, channel.BillRow{OutTradeNo: o2.PayOrderID, TradeState: "SUCCESS", Amount: 2000})
	stub.mu.Unlock()
	b2, _ := runRecon(ctx, "stub_channel", "ISV-R", day, "ALL", false)
	if b2.ID != b.ID || b2.DiffCount != 1 {
		t.Fatalf("rerun without force must be no-op: %+v", b2)
	}
	// 标记解决。
	mustCode(t, call(t, r, "op", http.MethodPost, fmt.Sprintf("/api/pay/admin/recon/diffs/%d/resolve", diffID), map[string]any{"note": "渠道延迟入账"}), 200)
	// force 重跑：差异清零。
	b3, _ := runRecon(ctx, "stub_channel", "ISV-R", day, "ALL", true)
	if b3.DiffCount != 0 || b3.ChannelCount != 2 {
		t.Fatalf("forced rerun: %+v", b3)
	}

	// 无账单日：no_bill。
	stub.mu.Lock()
	stub.billErr = channel.ErrNoBill
	stub.mu.Unlock()
	b4, err := runRecon(ctx, "stub_channel", "ISV-R", day.AddDate(0, 0, -30), "ALL", false)
	if err != nil || b4.State != "no_bill" {
		t.Fatalf("no bill: %+v %v", b4, err)
	}
	// 渠道报错：failed 带原因，再跑可恢复。
	stub.mu.Lock()
	stub.billErr = fmt.Errorf("tradebill: SYSTEM_ERROR")
	stub.mu.Unlock()
	b5, _ := runRecon(ctx, "stub_channel", "ISV-R", day.AddDate(0, 0, -31), "ALL", false)
	if b5.State != "failed" || b5.ErrMsg == "" {
		t.Fatalf("failed batch: %+v", b5)
	}
	stub.mu.Lock()
	stub.billErr = nil
	stub.billRows = nil
	stub.mu.Unlock()
	b6, _ := runRecon(ctx, "stub_channel", "ISV-R", day.AddDate(0, 0, -31), "ALL", false)
	if b6.State != "done" {
		t.Fatalf("recovered batch: %+v", b6)
	}

	// 每日汇总入口：按服务商/渠道配置逐个跑。
	batches, errs := runDailyRecon(ctx, day)
	if len(errs) != 0 || len(batches) == 0 {
		t.Fatalf("daily: %v %v", batches, errs)
	}
}

func TestDailyReconEndpoint_RequiresJobsServiceTokenOrAdmin(t *testing.T) {
	requireDB(t)
	// identity() 需要真实 JWT，这里只验证拒绝路径（无 token → 401）。
	r := testRouter()
	r.POST(mount+"/internal/recon/daily", handleDailyRecon)
	res := call(t, r, "admin", http.MethodPost, "/api/pay/internal/recon/daily", nil)
	if res.Code != 401 {
		t.Fatalf("without a verifiable bearer the endpoint must 401, got %d %v", res.Code, res.Body)
	}
}
