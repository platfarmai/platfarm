package wxpay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"platfarm/svc-pay/channel"
)

func TestEcommerce_PayDeclaresProfitSharing(t *testing.T) {
	merchantPriv, _, _ := testKeys(t)
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "WECHATPAY2-SHA256-RSA2048 mchid=\"1900000109\"") {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"prepay_id":"wx123"}`))
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)
	prov := map[string]string{"sp_mchid": "1900000109", "sp_appid": "wxsp", "mch_serial_no": "A", "private_key": merchantPriv}
	res, err := (&ecommerce{}).Pay(context.Background(), channel.PayIn{
		PayOrderID: "P1", WayCode: "WX_JSAPI", Amount: 100, Subject: "t", NotifyURL: "https://n",
		ChannelExtra: map[string]string{"sp_openid": "o1"}, Provider: prov, Sub: map[string]string{"sub_mchid": "1900001"},
	})
	if err != nil || res.State != channel.StateWaiting || path != "/v3/pay/partner/transactions/jsapi" {
		t.Fatalf("pay: %+v %v path=%s", res, err, path)
	}
	settle, _ := got["settle_info"].(map[string]any)
	if settle["profit_sharing"] != true {
		t.Fatalf("ecommerce pay must freeze for profit sharing: %v", got["settle_info"])
	}
}

func TestEcommerce_SettleWithdrawBalance(t *testing.T) {
	merchantPriv, _, _ := testKeys(t)
	var mu sync.Mutex
	var withdraw, finish map[string]any
	finishStatus, finishBody := 200, `{"order_id":"F1","status":"FINISHED"}`
	setFinish := func(status int, body string) {
		mu.Lock()
		defer mu.Unlock()
		finishStatus, finishBody = status, body
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/v3/ecommerce/profitsharing/finish-order":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &finish)
			w.WriteHeader(finishStatus)
			_, _ = w.Write([]byte(finishBody))
		case r.URL.Path == "/v3/ecommerce/profitsharing/orders" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"order_id":"D1","status":"FINISHED","receivers":[{"type":"MERCHANT_ID","receiver_account":"1900000110","amount":300,"result":"SUCCESS"}]}`))
		case r.URL.Path == "/v3/ecommerce/fund/withdraw" && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &withdraw)
			_, _ = w.Write([]byte(`{"withdraw_id":"W1","status":"CREATE_SUCCESS"}`))
		case strings.HasPrefix(r.URL.Path, "/v3/ecommerce/fund/withdraw/out-request-no/"):
			if r.URL.Query().Get("sub_mchid") != "1900001" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"withdraw_id":"W1","status":"SUCCESS"}`))
		case r.URL.Path == "/v3/ecommerce/fund/balance/1900001":
			_, _ = w.Write([]byte(`{"available_amount":700,"pending_amount":0}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)
	prov := map[string]string{"sp_mchid": "1900000109", "sp_appid": "wxsp", "mch_serial_no": "A", "private_key": merchantPriv}
	sub := map[string]string{"sub_mchid": "1900001"}
	e := &ecommerce{}
	ctx := context.Background()

	// 合单支付已下线：子单金额与收款子商户必须先按子单建模。
	if _, err := e.Pay(ctx, channel.PayIn{PayOrderID: "C1", WayCode: "WX_COMBINE", Amount: 1000, Provider: prov, Sub: sub}); err == nil {
		t.Fatal("WX_COMBINE must be rejected")
	}
	for _, w := range e.Ways() {
		if w == "WX_COMBINE" {
			t.Fatal("WX_COMBINE must not be offered")
		}
	}
	sh, err := e.Share(ctx, channel.DivisionIn{DivisionNo: "D1", ChannelOrderNo: "T1", Provider: prov, Sub: sub,
		Receivers: []channel.DivisionReceiver{{Type: "MERCHANT_ID", Account: "1900000110", Amount: 300}}})
	if err != nil || sh.State != channel.StateSuccess || sh.Receivers[0].Account != "1900000110" {
		t.Fatalf("share: %+v %v", sh, err)
	}
	// 确认结算 = 完结分账，幂等号用结算单号。
	st, err := e.SettleConfirm(ctx, channel.SettleIn{SettleNo: "S-9", PayOrderID: "P9", ChannelOrderNo: "T9", Amount: 700, Provider: prov, Sub: sub})
	mu.Lock()
	finishNo := finish["out_order_no"]
	mu.Unlock()
	if err != nil || st.State != channel.StateSuccess || st.ChannelOrderNo != "F1" || finishNo != "S-9" {
		t.Fatalf("settle: %+v %v out_order_no=%v", st, err, finishNo)
	}
	// 已无冻结资金可解冻（已全部分出或此前已释放）：释放已经发生，按成功处理。
	setFinish(403, `{"code":"NOT_ENOUGH","message":"分账金额不足"}`)
	if st, _ := e.SettleConfirm(ctx, channel.SettleIn{SettleNo: "S-10", PayOrderID: "P10", ChannelOrderNo: "T10", Provider: prov, Sub: sub}); st.State != channel.StateSuccess {
		t.Fatalf("NOT_ENOUGH settle: %+v", st)
	}
	setFinish(500, `{"code":"SYSTEM_ERROR","message":"系统繁忙"}`)
	if st, _ := e.SettleConfirm(ctx, channel.SettleIn{SettleNo: "S-11", PayOrderID: "P11", ChannelOrderNo: "T11", Provider: prov, Sub: sub}); st.State != channel.StateUnknown {
		t.Fatalf("5xx settle: %+v", st)
	}
	wd, err := e.Withdraw(ctx, channel.WithdrawIn{WithdrawNo: "WD1", Amount: 700, Provider: prov, Sub: sub})
	mu.Lock()
	reqNo := withdraw["out_request_no"]
	mu.Unlock()
	if err != nil || wd.State != channel.StateWaiting || wd.ChannelOrderNo != "W1" || reqNo != "WD1" {
		t.Fatalf("withdraw: %+v %v out_request_no=%v", wd, err, reqNo)
	}
	qd, err := e.QueryWithdraw(ctx, channel.WithdrawRef{WithdrawNo: "WD1", Provider: prov, Sub: sub})
	if err != nil || qd.State != channel.StateSuccess {
		t.Fatalf("query withdraw: %+v %v", qd, err)
	}
	bal, err := e.Balance(ctx, prov, sub)
	if err != nil || bal.Available != 700 {
		t.Fatalf("balance: %+v %v", bal, err)
	}
}

func TestEcommerce_ApplymentAndWithdrawFold(t *testing.T) {
	if ecommerceOrgType("enterprise") != "2" || ecommerceOrgType("individual") != "4" {
		t.Fatal("org type")
	}
	if withdrawStatus("SUCCESS", "") != channel.StateSuccess || withdrawStatus("REFUND", "") != channel.StateFail ||
		withdrawStatus("CREATE_SUCCESS", "") != channel.StateWaiting || withdrawStatus("", channel.StateWaiting) != channel.StateWaiting {
		t.Fatal("withdraw fold")
	}
}
