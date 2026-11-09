package alipay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"platfarm/svc-pay/channel"
)

// gatewayStub 支付宝网关桩：应答节点按用例切换，用测试私钥签名（与 prov 里的公钥成对）。
type gatewayStub struct {
	mu     sync.Mutex
	status int
	node   any
	biz    map[string]any
}

func (g *gatewayStub) reply(node any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status, g.node = 200, node
}

func newGatewayStub(t *testing.T) (*gatewayStub, map[string]string, map[string]string) {
	t.Helper()
	privPEM, pubPEM, _ := testKeyPair(t)
	g := &gatewayStub{status: 200}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		g.mu.Lock()
		defer g.mu.Unlock()
		g.biz = nil
		_ = json.Unmarshal([]byte(form.Get("biz_content")), &g.biz)
		if g.status != 200 {
			w.WriteHeader(g.status)
			return
		}
		_, _ = w.Write(signResponse(t, privPEM, form.Get("method"), g.node))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ALIPAY_GATEWAY", srv.URL)
	return g, map[string]string{"app_id": "2021", "private_key": privPEM, "alipay_public_key": pubPEM}, map[string]string{"smid": "2088"}
}

func fail(code, sub string) map[string]string {
	return map[string]string{"code": code, "msg": "Business Failed", "sub_code": sub, "sub_msg": sub}
}

func TestZFT_QueryAndCloseToleratePendingTrade(t *testing.T) {
	g, prov, sub := newGatewayStub(t)
	z, ctx := &zft{}, context.Background()
	ref := channel.OrderRef{PayOrderID: "P1", Provider: prov, Sub: sub}

	// 用户还没扫码 / 进收银台：交易尚未创建，查询结果是“未知”而不是失败。
	g.reply(fail("40004", "ACQ.TRADE_NOT_EXIST"))
	if res, err := z.Query(ctx, ref); err != nil || res.State != channel.StateUnknown || res.ErrCode != "ACQ.TRADE_NOT_EXIST" {
		t.Fatalf("query not exist: %+v %v", res, err)
	}
	if err := z.Close(ctx, ref); err != nil {
		t.Fatalf("closing a trade that was never created should succeed: %v", err)
	}
	g.reply(fail("40004", "ACQ.REASON_ILLEGAL_STATUS"))
	if err := z.Close(ctx, ref); err == nil {
		t.Fatal("closing a paid trade must fail")
	}
	g.reply(map[string]string{"code": "10000", "msg": "Success", "trade_status": "TRADE_SUCCESS", "trade_no": "T1", "total_amount": "12.34"})
	if res, _ := z.Query(ctx, ref); res.State != channel.StateSuccess || res.Amount != 1234 {
		t.Fatalf("query success: %+v", res)
	}
	g.mu.Lock()
	g.status = http.StatusBadGateway
	g.mu.Unlock()
	if res, _ := z.Query(ctx, ref); res.State != channel.StateUnknown {
		t.Fatalf("gateway 502: %+v", res)
	}
}

func TestZFT_PayAndMoneyMovementsClassifyErrors(t *testing.T) {
	g, prov, sub := newGatewayStub(t)
	z, ctx := &zft{}, context.Background()
	in := channel.PayIn{PayOrderID: "P1", WayCode: "ALI_QR", Amount: 100, Subject: "t", NotifyURL: "https://n",
		ExpireAt: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Provider: prov, Sub: sub}

	g.reply(map[string]string{"code": "10000", "msg": "Success", "qr_code": "https://qr"})
	if res, _ := z.Pay(ctx, in); res.State != channel.StateWaiting {
		t.Fatalf("pay: %+v", res)
	}
	g.mu.Lock()
	exp := g.biz["time_expire"]
	g.mu.Unlock()
	if exp != "2026-11-09 18:00:00" {
		t.Fatalf("time_expire = %v", exp)
	}

	payCases := []struct {
		node any
		want string
	}{
		{fail("40004", "ACQ.TRADE_HAS_SUCCESS"), channel.StateUnknown},
		{map[string]string{"code": "20000", "msg": "Service Currently Unavailable", "sub_code": "isp.unknow-error"}, channel.StateUnknown},
		{fail("40004", "ACQ.SYSTEM_ERROR"), channel.StateUnknown},
		{fail("40004", "ACQ.TRADE_HAS_CLOSE"), channel.StateFail},
		{fail("40002", "isv.invalid-signature"), channel.StateFail},
	}
	for _, tc := range payCases {
		g.reply(tc.node)
		if res, _ := z.Pay(ctx, in); res.State != tc.want {
			t.Errorf("pay %v: %s", tc.node, res.State)
		}
	}

	moneyCases := []struct {
		node any
		want string
	}{
		{fail("40004", "ACQ.SYSTEM_ERROR"), channel.StateUnknown},
		{fail("40004", "aop.ACQ.SYSTEM_ERROR"), channel.StateUnknown},
		{fail("40004", "SYSTEM_ERROR"), channel.StateUnknown},
		{map[string]string{"code": "20000", "msg": "Service Currently Unavailable"}, channel.StateUnknown},
		{fail("40004", "ACQ.REFUND_AMT_NOT_EQUAL_TOTAL"), channel.StateFail},
	}
	for _, tc := range moneyCases {
		g.reply(tc.node)
		if res, _ := z.Refund(ctx, channel.RefundIn{RefundOrderID: "R1", PayOrderID: "P1", RefundAmount: 100, Provider: prov, Sub: sub}); res.State != tc.want {
			t.Errorf("refund %v: %s", tc.node, res.State)
		}
		if res, _ := z.Share(ctx, channel.DivisionIn{DivisionNo: "D1", ChannelOrderNo: "T1", Provider: prov, Sub: sub,
			Receivers: []channel.DivisionReceiver{{Account: "a@b.c", Amount: 10}}}); res.State != tc.want {
			t.Errorf("share %v: %s", tc.node, res.State)
		}
		if res, _ := z.SettleConfirm(ctx, channel.SettleIn{PayOrderID: "P1", ChannelOrderNo: "T1", Amount: 100, Provider: prov, Sub: sub}); res.State != tc.want {
			t.Errorf("settle %v: %s", tc.node, res.State)
		}
	}
}

func TestZFT_SettleConfirmAndFinishAnswers(t *testing.T) {
	g, prov, sub := newGatewayStub(t)
	z, ctx := &zft{}, context.Background()
	in := channel.SettleIn{SettleNo: "S1", PayOrderID: "P1", ChannelOrderNo: "T1", Amount: 100, Provider: prov, Sub: sub}
	cases := []struct {
		node any
		want string
	}{
		{fail("40004", "ACQ.ALREADY_CONFIRM_SETTLE"), channel.StateSuccess}, // 已结算过：重试命中原结果
		{fail("40004", "ACQ.TRADE_SETTLE_ERROR"), channel.StateUnknown},     // 文档要求原参数重试
		{fail("40004", "ACQ.CONFIRM_REFUSE_AFTER_REFUND"), channel.StateFail},
	}
	for _, tc := range cases {
		g.reply(tc.node)
		if res, _ := z.SettleConfirm(ctx, in); res.State != tc.want {
			t.Errorf("settle %v: %s", tc.node, res.State)
		}
	}
	// 不需要分账：结算不冻结。
	g.reply(map[string]string{"code": "10000", "msg": "Success", "trade_no": "T1"})
	if res, _ := z.SettleConfirm(ctx, in); res.State != channel.StateSuccess {
		t.Fatalf("settle: %+v", res)
	}
	g.mu.Lock()
	_, frozen := g.biz["extend_params"]
	reqNo := g.biz["out_request_no"]
	g.mu.Unlock()
	if frozen || reqNo != "S1" {
		t.Fatalf("settle without hold: extend_params present=%v out_request_no=%v", frozen, reqNo)
	}
	// 单独完结分账；已无冻结资金可解冻也算完成。
	g.reply(fail("40004", "ACQ.ALREADY_FINISH_ALLOC"))
	if res, _ := z.FinishShare(ctx, channel.DivisionRef{DivisionNo: "F1", ChannelOrderNo: "T1", Provider: prov, Sub: sub}); res.State != channel.StateSuccess {
		t.Fatalf("finish: %+v", res)
	}
}

func TestZFT_QueryRefund(t *testing.T) {
	g, prov, sub := newGatewayStub(t)
	z, ctx := &zft{}, context.Background()
	ref := channel.RefundRef{RefundOrderID: "R1", PayOrderID: "P1", Provider: prov, Sub: sub}
	g.reply(map[string]string{"code": "10000", "msg": "Success", "refund_status": "REFUND_SUCCESS", "refund_amount": "5.00", "trade_no": "T1"})
	if res, _ := z.QueryRefund(ctx, ref); res.State != channel.StateSuccess || res.Amount != 500 {
		t.Fatalf("refund success: %+v", res)
	}
	g.mu.Lock()
	reqNo := g.biz["out_request_no"]
	g.mu.Unlock()
	if reqNo != "R1" {
		t.Fatalf("out_request_no = %v", reqNo)
	}
	// 没有 refund_status：退款还没成功，不能判失败。
	g.reply(map[string]string{"code": "10000", "msg": "Success", "trade_no": "T1"})
	if res, _ := z.QueryRefund(ctx, ref); res.State != channel.StateWaiting {
		t.Fatalf("no status: %+v", res)
	}
	g.reply(fail("40004", "ACQ.SYSTEM_ERROR"))
	if res, _ := z.QueryRefund(ctx, ref); res.State != channel.StateUnknown {
		t.Fatalf("query error: %+v", res)
	}
}

func TestZFT_NotifyRefundStatusAndAmount(t *testing.T) {
	privPEM, pubPEM, _ := testKeyPair(t)
	prov := map[string]string{"alipay_public_key": pubPEM}
	post := func(form url.Values) (channel.Notify, error) {
		sig, _ := sign(privPEM, canonical(form))
		form.Set("sign", sig)
		form.Set("sign_type", "RSA2")
		req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return (&zft{}).ParseNotify(req, prov)
	}
	// 全额退款后交易变成 TRADE_CLOSED：这是“退款成功”，不是退款失败。
	n, err := post(url.Values{"out_trade_no": {"P1"}, "out_biz_no": {"R1"}, "trade_no": {"T1"}, "trade_status": {"TRADE_CLOSED"},
		"refund_fee": {"10.00"}, "gmt_refund": {"2026-11-08 10:00:00.123"}, "total_amount": {"10.00"}})
	if err != nil || n.OrderType != "refund" || n.OrderID != "R1" || n.Result.State != channel.StateSuccess || n.AckBody != "success" {
		t.Fatalf("full refund notify: %+v %v", n, err)
	}
	// 部分退款：trade_status 仍是 TRADE_SUCCESS，同样是退款成功通知，不能当成支付通知。
	n, err = post(url.Values{"out_trade_no": {"P1"}, "out_biz_no": {"R2"}, "trade_no": {"T1"}, "trade_status": {"TRADE_SUCCESS"},
		"refund_fee": {"3.00"}, "gmt_refund": {"2026-11-08 11:00:00.000"}, "total_amount": {"10.00"}})
	if err != nil || n.OrderType != "refund" || n.OrderID != "R2" || n.Result.State != channel.StateSuccess {
		t.Fatalf("partial refund notify: %+v %v", n, err)
	}
	n, err = post(url.Values{"out_trade_no": {"P1"}, "trade_no": {"T1"}, "trade_status": {"TRADE_SUCCESS"}, "total_amount": {"12.34"}})
	if err != nil || n.OrderType != "pay" || n.Result.State != channel.StateSuccess || n.Result.Amount != 1234 {
		t.Fatalf("pay notify: %+v %v", n, err)
	}
}

func TestFenOf_ExactDecimal(t *testing.T) {
	cases := map[string]int64{"12.34": 1234, "0.29": 29, "1.1": 110, "10": 1000, ".5": 50, "-0.03": -3, "-12.34": -1234,
		" 7.00 ": 700, "": 0, "abc": 0, "1.2.3": 0}
	for in, want := range cases {
		if got := fenOf(in); got != want {
			t.Errorf("fenOf(%q) = %d, want %d", in, got, want)
		}
	}
}
