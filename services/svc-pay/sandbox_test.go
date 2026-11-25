package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
	"platfarm/svc-pay/channel/sandbox"
)

// partnerRouter 合作方联调会用到的开放 API：下单 / 查单 / 关单 / 模拟支付 / 退款 / 退款查询。
func partnerRouter() *gin.Engine {
	r := gin.New()
	v1 := r.Group(mount + "/v1")
	v1.POST("/orders", handleCreateOrder)
	v1.GET("/orders/:mchOrderNo", handleQueryOrder)
	v1.POST("/orders/:mchOrderNo/close", handleCloseOrder)
	v1.POST("/orders/:mchOrderNo/mock", handleMockOrder)
	v1.POST("/refunds", handleCreateRefund)
	v1.GET("/refunds/:mchRefundNo", handleQueryRefund)
	return r
}

func field(t *testing.T, r resp, obj, key string) any {
	t.Helper()
	m, ok := r.Body[obj].(map[string]any)
	if !ok {
		t.Fatalf("响应里没有 %s: %v", obj, r.Body)
	}
	return m[key]
}

// seedSandboxApp 服务商 + 商户 + 应用，应用的 wayCode 走 sandbox 渠道。
// 故意不给 sandbox 配任何渠道参数：沙箱必须在没有 pay_channel_config 行时也能用。
func seedSandboxApp(t *testing.T, isvNo, mchNo, appID, wayCode string) (App, string) {
	t.Helper()
	sandbox.Enable()
	seedISVAndMerchant(t, isvNo, mchNo, "stub_channel")
	app, secret := seedApp(t, mchNo, appID)
	mustCode(t, call(t, testRouter(), "op", http.MethodPut, "/api/pay/admin/passages",
		map[string]any{"appId": appID, "ifCode": sandbox.IfCode, "wayCode": wayCode, "rateBps": 60}), 200)
	return app, secret
}

func TestRefundQuery_onlyOwnMerchant(t *testing.T) {
	requireDB(t)
	stub.resetLifecycle()
	app, secret := seedStubMerchant(t, "ISV-RFQ", "M-RFQ", "A-RFQ")
	other, otherSecret := seedStubMerchant(t, "ISV-RFQ", "M-RFQ-OTHER", "A-RFQ-OTHER")
	o := seedSuccessOrder(t, "M-RFQ", "ISV-RFQ", app.AppID, "P-RFQ-1", 1000, 60, 20)
	r := partnerRouter()

	created := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
		map[string]any{"mchOrderNo": o.MchOrderNo, "mchRefundNo": "RF-RFQ-1", "refundAmount": 400})
	mustCode(t, created, 200)

	got := callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/refunds/RF-RFQ-1", nil)
	mustCode(t, got, 200)
	if field(t, got, "refund", "state") != refundSuccess || field(t, got, "refund", "refundAmount").(float64) != 400 ||
		field(t, got, "refund", "payOrderId") != o.PayOrderID {
		t.Fatalf("refund: %v", got.Body)
	}

	// 别的商户用自己的合法签名也查不到这笔退款；不存在的单号同样 404。
	mustCode(t, callSigned(t, r, other.AppID, otherSecret, http.MethodGet, "/api/pay/v1/refunds/RF-RFQ-1", nil), 404)
	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/refunds/RF-NOPE", nil), 404)
	// 签名错误在查库之前就被拒绝。
	mustCode(t, callSigned(t, r, app.AppID, "wrong-secret", http.MethodGet, "/api/pay/v1/refunds/RF-RFQ-1", nil), 401)
}

func TestSandbox_payMockRefund(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	app, secret := seedSandboxApp(t, "ISV-SBX", "M-SBX", "A-SBX", "WX_NATIVE")
	r := partnerRouter()

	created := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders", map[string]any{
		"mchOrderNo": "SBX-1", "wayCode": "WX_NATIVE", "amount": 1000, "subject": "沙箱",
		"notifyUrl": "https://partner.example.com/notify"})
	mustCode(t, created, 200)
	if field(t, created, "order", "state") != orderPaying || field(t, created, "order", "ifCode") != sandbox.IfCode {
		t.Fatalf("order: %v", created.Body)
	}
	cred, _ := created.Body["credential"].(map[string]any)
	if cred["sandbox"] != "true" || cred["codeUrl"] == "" {
		t.Fatalf("credential: %v", created.Body)
	}
	payOrderID := field(t, created, "order", "payOrderId").(string)

	paid := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/SBX-1/mock", nil)
	mustCode(t, paid, 200)
	if field(t, paid, "order", "state") != orderSuccess || field(t, paid, "order", "channelOrderNo") != sandbox.OrderNo(payOrderID) {
		t.Fatalf("mock: %v", paid.Body)
	}
	// 模拟支付走的是真实的终态路径：下游通知已入队。
	if state, _, _ := notifyRow(t, payOrderID, "pay"); state != "notifying" {
		t.Fatalf("notify state = %s", state)
	}
	again := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/SBX-1/mock", map[string]any{"state": "success"})
	mustCode(t, again, 200)
	if again.Body["idempotent"] != true {
		t.Fatalf("second mock: %v", again.Body)
	}
	// 已成功的单不能再模拟成失败。
	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/SBX-1/mock", map[string]any{"state": "failed"}), 409)
	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/SBX-1/mock", map[string]any{"state": "paying"}), 400)

	// 退款：普通金额立即成功；尾数 01 失败并退回额度；尾数 02 处理中，查询后成功。
	ok := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
		map[string]any{"mchOrderNo": "SBX-1", "mchRefundNo": "SBX-RF-1", "refundAmount": 300})
	mustCode(t, ok, 200)
	if field(t, ok, "refund", "state") != refundSuccess {
		t.Fatalf("refund: %v", ok.Body)
	}
	failed := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
		map[string]any{"mchOrderNo": "SBX-1", "mchRefundNo": "SBX-RF-2", "refundAmount": 101})
	mustCode(t, failed, 200)
	if field(t, failed, "refund", "state") != refundFailed || field(t, failed, "refund", "errCode") != "SANDBOX_REFUND_FAIL" {
		t.Fatalf("failed refund: %v", failed.Body)
	}
	slow := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/refunds",
		map[string]any{"mchOrderNo": "SBX-1", "mchRefundNo": "SBX-RF-3", "refundAmount": 202})
	mustCode(t, slow, 200)
	if field(t, slow, "refund", "state") != refundProcessing {
		t.Fatalf("slow refund: %v", slow.Body)
	}
	pending, err := findRefundByMch(ctx, "M-SBX", "SBX-RF-3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syncRefund(ctx, pending); err != nil {
		t.Fatal(err)
	}
	done := callSigned(t, r, app.AppID, secret, http.MethodGet, "/api/pay/v1/refunds/SBX-RF-3", nil)
	mustCode(t, done, 200)
	if field(t, done, "refund", "state") != refundSuccess {
		t.Fatalf("synced refund: %v", done.Body)
	}
	// 成功退了 300 + 202；失败的 101 不占额度。
	if o, _ := findOrder(ctx, payOrderID); o.RefundAmount != 502 || o.RefundState != "partial" {
		t.Fatalf("order refund rollup: amount=%d state=%s", o.RefundAmount, o.RefundState)
	}
}

func TestSandbox_amountTailsDriveOutcome(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	app, secret := seedSandboxApp(t, "ISV-SBT", "M-SBT", "A-SBT", "ALI_QR")
	r := partnerRouter()
	order := func(no string, amount int64) resp {
		return callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders",
			map[string]any{"mchOrderNo": no, "wayCode": "ALI_QR", "amount": amount, "subject": "沙箱"})
	}

	// 尾数 01：下单即失败。
	failed := order("SBT-1", 1001)
	mustCode(t, failed, 200)
	if field(t, failed, "order", "state") != orderFailed || field(t, failed, "order", "errCode") != "SANDBOX_PAY_FAIL" {
		t.Fatalf("tail 01: %v", failed.Body)
	}

	// 尾数 02：结果未知，订单不动；补单查询后成功。
	unknown := order("SBT-2", 1002)
	mustCode(t, unknown, 200)
	if unknown.Body["pending"] != true || field(t, unknown, "order", "state") != orderCreated {
		t.Fatalf("tail 02: %v", unknown.Body)
	}
	o2, err := findOrderByMch(ctx, "M-SBT", "SBT-2")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reconcileOrder(ctx, o2); err != nil || got.State != orderSuccess {
		t.Fatalf("tail 02 reconcile: %s %v", got.State, err)
	}

	// 尾数 03：补单查询也一直待支付，可以关单；关掉后不能再模拟支付。
	waiting := order("SBT-3", 1003)
	mustCode(t, waiting, 200)
	o3, _ := findOrderByMch(ctx, "M-SBT", "SBT-3")
	if got, err := reconcileOrder(ctx, o3); err != nil || got.State != orderPaying {
		t.Fatalf("tail 03 reconcile: %s %v", got.State, err)
	}
	closed := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/SBT-3/close", nil)
	mustCode(t, closed, 200)
	if field(t, closed, "order", "state") != orderClosed {
		t.Fatalf("close: %v", closed.Body)
	}
	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/SBT-3/mock", nil), 409)

	// 普通金额：模拟失败。
	mustCode(t, order("SBT-4", 1000), 200)
	lost := callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/SBT-4/mock", map[string]any{"state": "failed"})
	mustCode(t, lost, 200)
	if field(t, lost, "order", "state") != orderFailed {
		t.Fatalf("mock failed: %v", lost.Body)
	}
}

// 模拟支付只对 sandbox 渠道的订单生效：真实渠道的单绝不能被一个 API 调用改成已支付。
func TestSandbox_mockRefusesRealChannelOrders(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	sandbox.Enable()
	stub.resetLifecycle()
	app, secret := seedStubMerchant(t, "ISV-SBR", "M-SBR", "A-SBR")
	o := seedPayingOrder(t, "M-SBR", "ISV-SBR", app.AppID, "P-SBR-1", 1000, "", orderTTL)
	r := partnerRouter()

	mustCode(t, callSigned(t, r, app.AppID, secret, http.MethodPost, "/api/pay/v1/orders/"+o.MchOrderNo+"/mock", nil), 400)
	if got, _ := findOrder(ctx, o.PayOrderID); got.State != orderPaying {
		t.Fatalf("real-channel order moved by mock: %s", got.State)
	}
	// 沙箱渠道没有上游回调：往它的回调地址发请求改不了任何订单。
	if w := postChannelNotify(notifyRouter(), sandbox.IfCode, o.PayOrderID); w.Code != http.StatusBadRequest {
		t.Fatalf("sandbox notify: %d %s", w.Code, w.Body.String())
	}
}

// 沙箱凭证的键名要和真实渠道同一支付方式返回的一致，合作方的解析代码上线时才不用改。
func TestSandbox_credentialKeysMirrorRealChannels(t *testing.T) {
	sandbox.Enable()
	ch, err := channel.Get(sandbox.IfCode)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"WX_NATIVE": "codeUrl", "WX_JSAPI": "prepayId",
		"ALI_QR": "qrCode", "ALI_JSAPI": "tradeNo", "ALI_APP": "payUrl", "ALI_WAP": "payUrl", "ALI_PAGE": "payUrl",
	}
	for _, way := range channel.Ways(sandbox.IfCode) {
		key, ok := want[way]
		if !ok {
			t.Errorf("%s: 沙箱新增了支付方式，补上它在真实渠道里的凭证键名", way)
			continue
		}
		res, err := ch.Pay(context.Background(), channel.PayIn{PayOrderID: "P-CRED", WayCode: way, Amount: 1000})
		if err != nil || res.State != channel.StateWaiting {
			t.Errorf("%s: state=%s err=%v", way, res.State, err)
			continue
		}
		cred, _ := res.Credential.(map[string]string)
		if cred[key] == "" || cred["sandbox"] != "true" {
			t.Errorf("%s: credential %v, want key %s", way, res.Credential, key)
		}
	}
	if _, err := ch.Pay(context.Background(), channel.PayIn{PayOrderID: "P-CRED", WayCode: "NOPE", Amount: 1000}); err == nil {
		t.Error("unsupported wayCode must be rejected")
	}
}

func TestSandbox_channelDeclaresNoParams(t *testing.T) {
	sandbox.Enable()
	if !channel.ParamsOptional(sandbox.IfCode) || channel.ParamsOptional("wxpay_partner") || channel.ParamsOptional("nope") {
		t.Fatal("only the sandbox channel may run without channel params")
	}
	if got := channel.Fields(sandbox.IfCode, "isv"); len(got) != 0 {
		t.Fatalf("sandbox provider fields: %v", got)
	}
}
