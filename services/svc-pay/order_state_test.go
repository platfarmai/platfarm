package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
)

// seedStubMerchant 服务商 + 特约商户（含商户侧渠道参数）+ 应用，渠道为 stub_channel。
func seedStubMerchant(t *testing.T, isvNo, mchNo, appID string) (App, string) {
	t.Helper()
	seedISVAndMerchant(t, isvNo, mchNo, "stub_channel")
	seedMerchantParams(t, mchNo, "stub_channel", map[string]string{"sub_mchid": "1900001"})
	return seedApp(t, mchNo, appID)
}

func setStub(f func(s *stubChannel)) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	f(stub)
}

// seedPayingOrder 落一笔已在渠道下单（paying）的支付单。ttl 为负即已过期。
func seedPayingOrder(t *testing.T, mchNo, isvNo, appID, id string, amount int64, notifyURL string, ttl time.Duration) Order {
	t.Helper()
	ctx := context.Background()
	exp := time.Now().Add(ttl)
	o := Order{PayOrderID: id, MchNo: mchNo, IsvNo: isvNo, AppID: appID, MchType: 2, MchOrderNo: "M-" + id,
		IfCode: "stub_channel", WayCode: "STUB_PAY", Amount: amount, Currency: "CNY", FeeRateBps: 60,
		FeeAmount: feeAmount(amount, 60), Subject: "t", NotifyURL: notifyURL, ExpiredAt: &exp}
	if _, dup, err := insertOrder(ctx, o); err != nil || dup {
		t.Fatalf("seed order: %v dup=%v", err, dup)
	}
	if ok, err := casOrder(ctx, id, orderCreated, orderPaying, "", "", "", ""); err != nil || !ok {
		t.Fatalf("seed paying: %v", err)
	}
	saved, _ := findOrder(ctx, id)
	return saved
}

func seedISVParams(t *testing.T, isvNo, ifCode string, params map[string]string) {
	t.Helper()
	sealed, err := mustSealParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE pay_channel_config SET if_params=$3
		WHERE owner_type='isv' AND owner_id=$1 AND if_code=$2`, isvNo, ifCode, sealed); err != nil {
		t.Fatal(err)
	}
}

func notifyRouter() *gin.Engine {
	r := gin.New()
	r.POST(mount+"/notify/:ifCode/*orderId", handleChannelNotify)
	return r
}

func postChannelNotify(r *gin.Engine, ifCode, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/pay/notify/"+ifCode+"/"+id, strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func notifyRow(t *testing.T, orderID, orderType string) (state, payload string, count int) {
	t.Helper()
	err := db.QueryRow(context.Background(), `SELECT state, payload, notify_count FROM pay_notify
		WHERE order_id=$1 AND order_type=$2`, orderID, orderType).Scan(&state, &payload, &count)
	if err != nil {
		t.Fatalf("notify row %s: %v", orderID, err)
	}
	return
}

func TestOrderState_OnlyDefinitiveResultsMoveOrders(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	stub.resetLifecycle()
	seedStubMerchant(t, "ISV-OS", "M-OS", "A-OS")
	o := seedPayingOrder(t, "M-OS", "ISV-OS", "A-OS", "P-OS-1", 1000, "https://merchant.example/n", time.Hour)

	// 查无此单 / 渠道繁忙（UNKNOWN）与待支付（WAITING）都不能把单判死。
	for _, res := range []channel.Result{{State: channel.StateUnknown, ErrCode: "ORDER_NOT_EXIST"}, {State: channel.StateWaiting}} {
		setStub(func(s *stubChannel) { s.queryResult = res })
		got, err := reconcileOrder(ctx, o)
		if err != nil || got.State != orderPaying {
			t.Fatalf("%s must keep paying: %s %v", res.State, got.State, err)
		}
	}
	// 渠道报的成功金额与订单不符：不推进。
	setStub(func(s *stubChannel) {
		s.queryResult = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "T-OS-1", Amount: 999}
	})
	if _, err := reconcileOrder(ctx, o); !errors.Is(err, errAmountMismatch) {
		t.Fatalf("amount mismatch: %v", err)
	}
	if got, _ := findOrder(ctx, o.PayOrderID); got.State != orderPaying {
		t.Fatalf("mismatch must not advance: %s", got.State)
	}
	// 确定成功：同一事务记手续费台账、入下游通知。
	setStub(func(s *stubChannel) {
		s.queryResult = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "T-OS-1", Amount: 1000}
	})
	got, err := reconcileOrder(ctx, o)
	if err != nil || got.State != orderSuccess || got.ChannelOrderNo != "T-OS-1" || got.SuccessAt == nil {
		t.Fatalf("success: %+v %v", got, err)
	}
	if fee, _ := ledgerSum(ctx, "M-OS", "", []string{ledgerFee}); fee != -6 {
		t.Fatalf("fee ledger = %d, want -6", fee)
	}
	if state, payload, _ := notifyRow(t, o.PayOrderID, "pay"); state != "notifying" || !strings.Contains(payload, `"state":"success"`) {
		t.Fatalf("notify = %s %s", state, payload)
	}
}

func TestOrderState_ExpiryChecksAndClosesUpstreamFirst(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	stub.resetLifecycle()
	seedStubMerchant(t, "ISV-EX", "M-EX", "A-EX")

	// 渠道仍待支付：先在渠道关单，关成功才本地关闭。
	a := seedPayingOrder(t, "M-EX", "ISV-EX", "A-EX", "P-EX-A", 1000, "", -time.Minute)
	setStub(func(s *stubChannel) { s.queryResult = channel.Result{State: channel.StateWaiting} })
	if err := expireOrder(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := findOrder(ctx, a.PayOrderID); got.State != orderClosed || got.ErrCode != "EXPIRED" {
		t.Fatalf("expired+waiting: %s %s", got.State, got.ErrCode)
	}
	stub.mu.Lock()
	closes := stub.closeCalls
	stub.mu.Unlock()
	if closes != 1 {
		t.Fatalf("upstream close calls = %d", closes)
	}

	// 渠道关单失败（例如刚付成功）：本地不动，下一轮再查。
	b := seedPayingOrder(t, "M-EX", "ISV-EX", "A-EX", "P-EX-B", 1000, "", -time.Minute)
	setStub(func(s *stubChannel) { s.closeErr = errors.New("ORDERPAID") })
	if err := expireOrder(ctx, b); err == nil {
		t.Fatal("close failure must be reported")
	}
	if got, _ := findOrder(ctx, b.PayOrderID); got.State != orderPaying {
		t.Fatalf("close failed but order %s", got.State)
	}

	// 过期前一刻已付：查询到成功就记成功，而不是关单。
	setStub(func(s *stubChannel) {
		s.closeErr = nil
		s.queryResult = channel.Result{State: channel.StateSuccess, ChannelOrderNo: "T-EX-B", Amount: 1000}
	})
	if err := expireOrder(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := findOrder(ctx, b.PayOrderID); got.State != orderSuccess {
		t.Fatalf("paid before expiry: %s", got.State)
	}

	// 渠道查无此单：宽限期内不动；过了宽限期本地关闭（下单时已把失效时间传给渠道）。
	d := seedPayingOrder(t, "M-EX", "ISV-EX", "A-EX", "P-EX-D", 1000, "", -time.Minute)
	setStub(func(s *stubChannel) {
		s.queryResult = channel.Result{State: channel.StateUnknown, ErrCode: "ACQ.TRADE_NOT_EXIST"}
	})
	if err := expireOrder(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got, _ := findOrder(ctx, d.PayOrderID); got.State != orderPaying {
		t.Fatalf("within grace: %s", got.State)
	}
	if _, err := db.Exec(ctx, `UPDATE pay_order SET expired_at=now()-interval '11 minutes' WHERE pay_order_id=$1`, d.PayOrderID); err != nil {
		t.Fatal(err)
	}
	d, _ = findOrder(ctx, d.PayOrderID)
	if err := expireOrder(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got, _ := findOrder(ctx, d.PayOrderID); got.State != orderClosed {
		t.Fatalf("after grace: %s", got.State)
	}
}

func TestNotify_LatePaymentIsRecordedAndRenotified(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	stub.resetLifecycle()
	seedStubMerchant(t, "ISV-LP", "M-LP", "A-LP")
	r := notifyRouter()

	// 已超时关闭的单收到渠道确定的支付成功：推进到 success（标 LATE_PAYMENT），记账、通知商户。
	o := seedPayingOrder(t, "M-LP", "ISV-LP", "A-LP", "P-LP-1", 1000, "https://merchant.example/n", time.Hour)
	if ok, _ := casOrder(ctx, o.PayOrderID, orderPaying, orderClosed, "", "", "EXPIRED", "超时"); !ok {
		t.Fatal("seed closed")
	}
	paid := &channel.Notify{OrderID: o.PayOrderID, OrderType: "pay", Ack: http.StatusNoContent,
		Result: channel.Result{State: channel.StateSuccess, ChannelOrderNo: "T-LP-1", Amount: 1000}}
	setStub(func(s *stubChannel) { s.notify = paid })
	if w := postChannelNotify(r, "stub_channel", o.PayOrderID); w.Code != http.StatusNoContent {
		t.Fatalf("late notify: %d %s", w.Code, w.Body.String())
	}
	got, _ := findOrder(ctx, o.PayOrderID)
	if got.State != orderSuccess || got.ErrCode != "LATE_PAYMENT" || got.ChannelOrderNo != "T-LP-1" {
		t.Fatalf("late payment: %+v", got)
	}
	state, payload, _ := notifyRow(t, o.PayOrderID, "pay")
	if state != "notifying" || !strings.Contains(payload, `"state":"success"`) || !strings.Contains(payload, "LATE_PAYMENT") {
		t.Fatalf("notify = %s %s", state, payload)
	}
	// 回调重放：不重复记账，也不重置已投递的通知。
	if _, err := db.Exec(ctx, `UPDATE pay_notify SET state='success', notify_count=1 WHERE order_id=$1`, o.PayOrderID); err != nil {
		t.Fatal(err)
	}
	if w := postChannelNotify(r, "stub_channel", o.PayOrderID); w.Code != http.StatusNoContent {
		t.Fatalf("replay: %d", w.Code)
	}
	if state, _, n := notifyRow(t, o.PayOrderID, "pay"); state != "success" || n != 1 {
		t.Fatalf("replay reset delivered notify: %s %d", state, n)
	}
	var feeRows int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM pay_ledger WHERE ref_id=$1 AND kind='fee'`, o.PayOrderID).Scan(&feeRows)
	if feeRows != 1 {
		t.Fatalf("fee rows = %d", feeRows)
	}

	// 已经通知过“失败”的单后来确认支付成功：通知内容换成成功并重新投递。
	f := seedPayingOrder(t, "M-LP", "ISV-LP", "A-LP", "P-LP-2", 1000, "https://merchant.example/n", time.Hour)
	if ok, err := finishOrder(ctx, f.PayOrderID, orderPaying, orderFailed, "", "", "PAYERROR", "支付失败"); err != nil || !ok {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE pay_notify SET state='success', notify_count=2 WHERE order_id=$1`, f.PayOrderID); err != nil {
		t.Fatal(err)
	}
	setStub(func(s *stubChannel) {
		s.notify = &channel.Notify{OrderID: f.PayOrderID, OrderType: "pay", Ack: http.StatusNoContent,
			Result: channel.Result{State: channel.StateSuccess, ChannelOrderNo: "T-LP-2", Amount: 1000}}
	})
	if w := postChannelNotify(r, "stub_channel", f.PayOrderID); w.Code != http.StatusNoContent {
		t.Fatalf("late notify after failure: %d", w.Code)
	}
	state, payload, n := notifyRow(t, f.PayOrderID, "pay")
	if state != "notifying" || n != 0 || !strings.Contains(payload, `"state":"success"`) {
		t.Fatalf("renotify = %s %d %s", state, n, payload)
	}
}

func TestNotify_RoutesProviderByOrderAndRejectsMismatch(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	stub.resetLifecycle()
	// 同一渠道两个服务商，各自一套密钥参数。
	seedStubMerchant(t, "ISV-NA", "M-NA", "A-NA")
	seedStubMerchant(t, "ISV-NB", "M-NB", "A-NB")
	seedISVParams(t, "ISV-NB", "stub_channel", map[string]string{"sp_mchid": "1900000222"})
	oa := seedPayingOrder(t, "M-NA", "ISV-NA", "A-NA", "P-NA-1", 1000, "", time.Hour)
	ob := seedPayingOrder(t, "M-NB", "ISV-NB", "A-NB", "P-NB-1", 2000, "", time.Hour)
	r := notifyRouter()

	setStub(func(s *stubChannel) {
		s.notify = &channel.Notify{OrderID: ob.PayOrderID, OrderType: "pay", Ack: http.StatusNoContent,
			Result: channel.Result{State: channel.StateSuccess, ChannelOrderNo: "T-NB", Amount: 2000}}
	})
	if w := postChannelNotify(r, "stub_channel", ob.PayOrderID); w.Code != http.StatusNoContent {
		t.Fatalf("notify B: %d %s", w.Code, w.Body.String())
	}
	stub.mu.Lock()
	used := stub.lastProvider["sp_mchid"]
	stub.mu.Unlock()
	if used != "1900000222" {
		t.Fatalf("verified with provider %q, want ISV-NB's", used)
	}

	cases := []struct {
		name, ifCode, path string
		n                  *channel.Notify
	}{
		{"body order differs from callback path", "stub_channel", ob.PayOrderID, &channel.Notify{OrderID: oa.PayOrderID, OrderType: "pay",
			Result: channel.Result{State: channel.StateSuccess, Amount: 1000}}},
		{"amount differs from order", "stub_channel", oa.PayOrderID, &channel.Notify{OrderID: oa.PayOrderID, OrderType: "pay",
			Result: channel.Result{State: channel.StateSuccess, Amount: 1}}},
		{"unknown order", "stub_channel", "P-NOPE", &channel.Notify{OrderID: "P-NOPE", OrderType: "pay",
			Result: channel.Result{State: channel.StateSuccess}}},
		{"order of another channel", "stub_controlled", oa.PayOrderID, &channel.Notify{OrderID: oa.PayOrderID, OrderType: "pay",
			Result: channel.Result{State: channel.StateSuccess, Amount: 1000}}},
		{"signature rejected", "stub_channel", oa.PayOrderID, nil},
	}
	for _, tc := range cases {
		setStub(func(s *stubChannel) { s.notify = tc.n })
		if w := postChannelNotify(r, tc.ifCode, tc.path); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}
	if got, _ := findOrder(ctx, oa.PayOrderID); got.State != orderPaying {
		t.Fatalf("rejected notifies must not move order A: %s", got.State)
	}
}

func TestRefund_AmbiguousResultKeepsQuota(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	stub.resetLifecycle()
	app, secret := seedStubMerchant(t, "ISV-RQ", "M-RQ", "A-RQ")
	o := seedSuccessOrder(t, "M-RQ", "ISV-RQ", app.AppID, "P-RQ-1", 1000, 60, 20)
	r := gin.New()
	r.POST(mount+"/v1/refunds", handleCreateRefund)
	r.POST(mount+"/notify/:ifCode/*orderId", handleChannelNotify)

	// 渠道 5xx / 系统繁忙：退款可能已受理。退款单留在 created、额度继续占着，换单号也退不出第二笔。
	setStub(func(s *stubChannel) {
		s.refundRes = &channel.Result{State: channel.StateUnknown, ErrCode: "SYSTEM_ERROR", ErrMsg: "系统繁忙"}
	})
	res := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
		map[string]any{"mchOrderNo": o.MchOrderNo, "mchRefundNo": "MRQ-1", "refundAmount": 1000})
	mustCode(t, res, 200)
	rf := res.Body["refund"].(map[string]any)
	if rf["state"] != refundCreated || res.Body["pending"] != true || rf["errCode"] != "SYSTEM_ERROR" {
		t.Fatalf("ambiguous refund = %v", res.Body)
	}
	if got, _ := findOrder(ctx, o.PayOrderID); got.RefundAmount != 1000 {
		t.Fatalf("quota released on ambiguous result: %d", got.RefundAmount)
	}
	res = callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
		map[string]any{"mchOrderNo": o.MchOrderNo, "mchRefundNo": "MRQ-2", "refundAmount": 1000})
	mustCode(t, res, 409)

	// 支付宝的退款状态通知打到原支付单的回调地址，单号是退款请求号：确认这笔退款成功。
	refundID := rf["refundOrderId"].(string)
	setStub(func(s *stubChannel) {
		s.notify = &channel.Notify{OrderID: refundID, OrderType: "refund", Ack: http.StatusOK, AckBody: "success",
			Result: channel.Result{State: channel.StateSuccess}}
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/pay/notify/stub_channel/"+o.PayOrderID, strings.NewReader("x")))
	if w.Code != http.StatusOK || w.Body.String() != "success" {
		t.Fatalf("refund notify: %d %q", w.Code, w.Body.String())
	}
	got, _ := findOrder(ctx, o.PayOrderID)
	saved, _ := findRefund(ctx, refundID)
	if saved.State != refundSuccess || got.RefundState != refundFull || got.RefundAmount != 1000 {
		t.Fatalf("refund %s, order %s/%d", saved.State, got.RefundState, got.RefundAmount)
	}
	if back, _ := ledgerSum(ctx, "M-RQ", "", []string{ledgerFeeRefund}); back != 6 {
		t.Fatalf("fee refund ledger = %d", back)
	}

	// 确定失败才退回额度，之后可以重新退款。
	o2 := seedSuccessOrder(t, "M-RQ", "ISV-RQ", app.AppID, "P-RQ-2", 1000, 60, 20)
	setStub(func(s *stubChannel) {
		s.refundRes = &channel.Result{State: channel.StateFail, ErrCode: "NOT_ENOUGH", ErrMsg: "余额不足"}
	})
	res = callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
		map[string]any{"mchOrderNo": o2.MchOrderNo, "mchRefundNo": "MRQ-3", "refundAmount": 1000})
	mustCode(t, res, 200)
	if res.Body["refund"].(map[string]any)["state"] != refundFailed {
		t.Fatalf("definite failure = %v", res.Body)
	}
	if got, _ := findOrder(ctx, o2.PayOrderID); got.RefundAmount != 0 || got.RefundState != refundNone {
		t.Fatalf("failed refund must release quota: %d %s", got.RefundAmount, got.RefundState)
	}
	setStub(func(s *stubChannel) { s.refundRes = nil })
	res = callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
		map[string]any{"mchOrderNo": o2.MchOrderNo, "mchRefundNo": "MRQ-4", "refundAmount": 400})
	mustCode(t, res, 200)
	if got, _ := findOrder(ctx, o2.PayOrderID); got.RefundState != refundPartial || got.RefundAmount != 400 {
		t.Fatalf("retry refund: %s %d", got.RefundState, got.RefundAmount)
	}
}
