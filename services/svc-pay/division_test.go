package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"platfarm/svc-pay/channel"
)

func boundReceivers() []Receiver {
	return []Receiver{
		{Account: "A", Type: "MERCHANT_ID", MaxRatioBps: 2000, DefaultRatioBps: 1000, BindState: "bound", State: "active", RelationType: "SUPPLIER"},
		{Account: "B", Type: "PERSONAL_OPENID", MaxRatioBps: 500, DefaultRatioBps: 0, BindState: "bound", State: "active", RelationType: "STAFF"},
		{Account: "C", Type: "MERCHANT_ID", MaxRatioBps: 3000, DefaultRatioBps: 500, BindState: "failed", State: "active"},
	}
}

func TestPlanDivision_DefaultsFloorAndCaps(t *testing.T) {
	// 默认接收方：只有 default_ratio_bps>0 且已绑定的 A；C 绑定失败不参与。
	plan, err := planDivision(9999, boundReceivers(), nil, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Account != "A" || plan[0].Amount != 999 { // 9999*1000/10000 = 999.9 → 999 向下取整
		t.Fatalf("plan = %+v", plan)
	}
	// 显式比例：逐个上限、合计上限。
	if _, err := planDivision(10000, boundReceivers(), []divisionReq{{"A", 2500}}, 3000); err == nil || !strings.Contains(err.Error(), "超过授权上限") {
		t.Fatalf("per-receiver cap: %v", err)
	}
	if _, err := planDivision(10000, boundReceivers(), []divisionReq{{"A", 2000}, {"B", 500}}, 2400); err == nil || !strings.Contains(err.Error(), "合计") {
		t.Fatalf("total cap: %v", err)
	}
	if _, err := planDivision(10000, boundReceivers(), []divisionReq{{"C", 100}}, 3000); err == nil || !strings.Contains(err.Error(), "未完成渠道绑定") {
		t.Fatalf("unbound: %v", err)
	}
	if _, err := planDivision(10000, boundReceivers(), []divisionReq{{"Z", 100}}, 3000); err == nil || !strings.Contains(err.Error(), "未绑定") {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := planDivision(10000, boundReceivers(), []divisionReq{{"A", 100}, {"A", 100}}, 3000); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("dup: %v", err)
	}
	if _, err := planDivision(5, boundReceivers(), []divisionReq{{"B", 100}}, 3000); err == nil || !strings.Contains(err.Error(), "金额为 0") {
		t.Fatalf("zero amount: %v", err)
	}
	plan, err = planDivision(10000, boundReceivers(), []divisionReq{{"A", 2000}, {"B", 500}}, 3000)
	if err != nil || sumAmount(plan) != 2500 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestDivision_FlowIdempotentAndLedger(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-D", "M-DIV", "stub_channel")
	seedMerchantParams(t, "M-DIV", "stub_channel", map[string]string{"sub_mchid": "1900001", "max_division_ratio_bps": "3000"})
	app, _ := seedApp(t, "M-DIV", "A-DIV")
	mch, _ := loadMerchant(ctx, "M-DIV")
	r := testRouter()

	// 绑定两个接收方（admin 接口）；一个渠道绑定失败落 failed。
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/division/receivers",
		map[string]any{"mchNo": "M-DIV", "ifCode": "stub_channel", "type": "MERCHANT_ID", "account": "1900000110", "name": "供应商甲", "relationType": "SUPPLIER", "maxRatioBps": 2000, "defaultRatioBps": 1000}), 200)
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/division/receivers",
		map[string]any{"mchNo": "M-DIV", "ifCode": "stub_channel", "type": "PERSONAL_OPENID", "account": "BAD", "maxRatioBps": 500}), 502)
	res := call(t, r, "op", http.MethodGet, "/api/pay/admin/division/receivers?mchNo=M-DIV", nil)
	mustCode(t, res, 200)
	// 名称密封存储、对外脱敏；渠道拿到的是明文。
	if items := res.Body["items"].([]any); len(items) != 2 || items[0].(map[string]any)["name"] != "供***" {
		t.Fatalf("receivers = %v", items)
	}
	stub.mu.Lock()
	gotName := stub.receivers[len(stub.receivers)-1].Name
	stub.mu.Unlock()
	if gotName != "供应商甲" {
		t.Fatalf("channel should receive plaintext name, got %q", gotName)
	}

	order := seedSuccessOrder(t, "M-DIV", "ISV-D", "A-DIV", "P-DIV-1", 10000, 60, 20)

	// 渠道受理中 → accepted；订单 division_state=pending。
	stub.mu.Lock()
	stub.shareResult = channel.DivisionResult{State: channel.StateWaiting, ChannelOrderNo: "CH-D1"}
	stub.shareCalls = 0
	stub.mu.Unlock()
	d, dup, err := createDivision(ctx, app, mch, order, nil, true, "test")
	if err != nil || dup {
		t.Fatalf("create: %v dup=%v", err, dup)
	}
	if d.State != divisionAccepted || d.DivisionAmount != 1000 || d.ChannelOrderNo != "CH-D1" || len(d.Receivers) != 1 {
		t.Fatalf("division = %+v", d)
	}
	o, _ := findOrder(ctx, order.PayOrderID)
	if o.DivisionState != orderDivisionPending {
		t.Fatalf("order division_state = %s", o.DivisionState)
	}

	// 已分账订单再分 = no-op：返回同一单，不再调渠道。
	d2, dup, err := createDivision(ctx, app, mch, o, []divisionReq{{"1900000110", 500}}, true, "test")
	if err != nil || !dup || d2.DivisionNo != d.DivisionNo {
		t.Fatalf("second create should be no-op: %+v dup=%v err=%v", d2, dup, err)
	}
	stub.mu.Lock()
	calls := stub.shareCalls
	stub.mu.Unlock()
	if calls != 1 {
		t.Fatalf("channel Share called %d times, want 1", calls)
	}

	// 轮询：渠道完成 → success，台账 sharing 行，订单 done。
	stub.mu.Lock()
	stub.queryShare = channel.DivisionResult{State: channel.StateSuccess, ChannelOrderNo: "CH-D1",
		Receivers: []channel.DivisionReceiver{{Account: "1900000110", Amount: 1000, Result: "SUCCESS", Detail: "DT1"}}}
	stub.mu.Unlock()
	d, err = syncDivision(ctx, d)
	if err != nil || d.State != divisionSuccess || d.Receivers[0].Result != "SUCCESS" || d.Receivers[0].Detail != "DT1" {
		t.Fatalf("after sync: %+v %v", d, err)
	}
	o, _ = findOrder(ctx, order.PayOrderID)
	if o.DivisionState != orderDivisionDone {
		t.Fatalf("order division_state = %s", o.DivisionState)
	}
	sum, _ := ledgerSum(ctx, "M-DIV", "stub_channel", []string{ledgerSharing})
	if sum != -1000 {
		t.Fatalf("sharing ledger = %d, want -1000", sum)
	}
	// 重放同步不重复记账。
	_, _ = syncDivision(ctx, d)
	_, _ = applyDivisionResult(ctx, d, stub.queryShare)
	sum, _ = ledgerSum(ctx, "M-DIV", "stub_channel", []string{ledgerSharing})
	if sum != -1000 {
		t.Fatalf("replay changed ledger: %d", sum)
	}

	// 渠道直接失败：failed 释放订单，可再发起新分账单。
	order2 := seedSuccessOrder(t, "M-DIV", "ISV-D", "A-DIV", "P-DIV-2", 5000, 60, 20)
	stub.mu.Lock()
	stub.shareResult = channel.DivisionResult{State: channel.StateFail, ErrCode: "NO_AUTH", ErrMsg: "未开通分账"}
	stub.mu.Unlock()
	f, _, err := createDivision(ctx, app, mch, order2, nil, true, "test")
	if err != nil || f.State != divisionFailed || f.ErrCode != "NO_AUTH" {
		t.Fatalf("failed division: %+v %v", f, err)
	}
	o2, _ := findOrder(ctx, order2.PayOrderID)
	if o2.DivisionState != orderDivisionNone {
		t.Fatalf("failed division must release order, got %s", o2.DivisionState)
	}
	stub.mu.Lock()
	stub.shareResult = channel.DivisionResult{State: channel.StateSuccess, ChannelOrderNo: "CH-D2",
		Receivers: []channel.DivisionReceiver{{Account: "1900000110", Amount: 500, Result: "SUCCESS"}}}
	stub.mu.Unlock()
	g, dup, err := createDivision(ctx, app, mch, o2, nil, true, "test")
	if err != nil || dup || g.State != divisionSuccess || g.DivisionNo == f.DivisionNo {
		t.Fatalf("retry after failure: %+v dup=%v err=%v", g, dup, err)
	}

	// 已退款订单不可分账。
	order3 := seedSuccessOrder(t, "M-DIV", "ISV-D", "A-DIV", "P-DIV-3", 5000, 60, 20)
	_, _ = db.Exec(ctx, `UPDATE pay_order SET refund_state='partial', refund_amount=100 WHERE pay_order_id=$1`, order3.PayOrderID)
	order3, _ = findOrder(ctx, order3.PayOrderID)
	if _, _, err := createDivision(ctx, app, mch, order3, nil, true, "test"); err == nil || !strings.Contains(err.Error(), "已退款") {
		t.Fatalf("refunded order: %v", err)
	}

	// 管理列表 + 台账查询。
	res = call(t, r, "admin", http.MethodGet, "/api/pay/admin/divisions?mchNo=M-DIV&state=success", nil)
	mustCode(t, res, 200)
	if n := len(res.Body["items"].([]any)); n != 2 {
		t.Fatalf("success divisions = %d", n)
	}
	res = call(t, r, "admin", http.MethodGet, "/api/pay/admin/ledger?mchNo=M-DIV&kind=sharing", nil)
	mustCode(t, res, 200)
	if res.Body["total"].(float64) != -1500 {
		t.Fatalf("ledger total = %v", res.Body["total"])
	}
}

func TestLedger_OrderAndRefundEntriesIdempotent(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-L", "M-LED", "stub_channel")
	seedApp(t, "M-LED", "A-LED")
	o := seedSuccessOrder(t, "M-LED", "ISV-L", "A-LED", "P-LED-1", 10000, 60, 20) // fee 60, rebate 20

	onOrderTerminal(ctx, o.PayOrderID, orderSuccess)
	onOrderTerminal(ctx, o.PayOrderID, orderSuccess) // 重放
	fee, _ := ledgerSum(ctx, "M-LED", "", []string{ledgerFee})
	rebate, _ := ledgerSum(ctx, "M-LED", "", []string{ledgerRebate})
	if fee != -60 || rebate != 20 {
		t.Fatalf("fee=%d rebate=%d", fee, rebate)
	}
	var n int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM pay_ledger WHERE ref_id=$1`, o.PayOrderID).Scan(&n)
	if n != 2 {
		t.Fatalf("ledger rows for order = %d, want 2 (replay must not duplicate)", n)
	}

	// 退一半：冲回一半手续费与返佣（四舍五入到分）。
	rf := Refund{RefundOrderID: "R-LED-1", PayOrderID: o.PayOrderID, MchNo: o.MchNo, IsvNo: o.IsvNo, AppID: o.AppID,
		MchRefundNo: "MR1", IfCode: o.IfCode, WayCode: o.WayCode, RefundAmount: 5000}
	saved, err := createRefund(ctx, rf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := casRefund(ctx, saved.RefundOrderID, refundCreated, refundSuccess, "RF-CH", "", ""); err != nil {
		t.Fatal(err)
	}
	onRefundTerminal(ctx, saved, refundSuccess)
	onRefundTerminal(ctx, saved, refundSuccess)
	feeBack, _ := ledgerSum(ctx, "M-LED", "", []string{ledgerFeeRefund})
	rebateBack, _ := ledgerSum(ctx, "M-LED", "", []string{ledgerRebateRefund})
	if feeBack != 30 || rebateBack != -10 {
		t.Fatalf("feeBack=%d rebateBack=%d", feeBack, rebateBack)
	}
	o, _ = findOrder(ctx, o.PayOrderID)
	if o.RefundState != refundPartial || o.RefundAmount != 5000 {
		t.Fatalf("rollup: %s %d", o.RefundState, o.RefundAmount)
	}
}

func TestProportional(t *testing.T) {
	cases := []struct{ total, part, whole, want int64 }{
		{60, 5000, 10000, 30}, {60, 3333, 10000, 20}, {61, 5000, 10000, 31}, {60, 10000, 10000, 60},
		{60, 0, 10000, 0}, {0, 5000, 10000, 0}, {60, 12000, 10000, 60}, {-60, 5000, 10000, -30},
	}
	for _, c := range cases {
		if got := proportional(c.total, c.part, c.whole); got != c.want {
			t.Errorf("proportional(%d,%d,%d)=%d want %d", c.total, c.part, c.whole, got, c.want)
		}
	}
}
