package wxpay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestEcommerce_CombineFinishWithdrawBalance(t *testing.T) {
	merchantPriv, _, _ := testKeys(t)
	var withdraw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/combine-transactions/jsapi":
			raw, _ := io.ReadAll(r.Body)
			var got map[string]any
			_ = json.Unmarshal(raw, &got)
			orders := got["sub_orders"].([]any)
			if len(orders) != 2 || got["combine_out_trade_no"] != "C1" {
				t.Errorf("combine body = %v", got)
			}
			_, _ = w.Write([]byte(`{"prepay_id":"wxcomb"}`))
		case r.URL.Path == "/v3/combine-transactions/out-trade-no/C1":
			_, _ = w.Write([]byte(`{"sub_orders":[{"trade_state":"SUCCESS","transaction_id":"T1"},{"trade_state":"SUCCESS","transaction_id":"T2"}]}`))
		case r.URL.Path == "/v3/ecommerce/profitsharing/finish-order":
			_, _ = w.Write([]byte(`{"order_id":"F1","status":"FINISHED"}`))
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

	res, err := e.Pay(ctx, channel.PayIn{
		PayOrderID: "C1", WayCode: "WX_COMBINE", Amount: 1000, Subject: "合单", NotifyURL: "https://n",
		ChannelExtra: map[string]string{"sp_openid": "o1", "combine": `[{"sub_mchid":"1900001","out_trade_no":"C1A","amount":600},{"sub_mchid":"1900002","out_trade_no":"C1B","amount":400}]`},
		Provider:     prov, Sub: sub,
	})
	if err != nil || res.State != channel.StateWaiting {
		t.Fatalf("combine pay: %+v %v", res, err)
	}
	q, err := e.Query(ctx, channel.OrderRef{PayOrderID: "C1", WayCode: "WX_COMBINE", Provider: prov, Sub: sub})
	if err != nil || q.State != channel.StateSuccess || q.ChannelOrderNo != "T1" {
		t.Fatalf("combine query: %+v %v", q, err)
	}
	sh, err := e.Share(ctx, channel.DivisionIn{DivisionNo: "D1", ChannelOrderNo: "T1", Provider: prov, Sub: sub,
		Receivers: []channel.DivisionReceiver{{Type: "MERCHANT_ID", Account: "1900000110", Amount: 300}}})
	if err != nil || sh.State != channel.StateSuccess || sh.Receivers[0].Account != "1900000110" {
		t.Fatalf("share: %+v %v", sh, err)
	}
	st, err := e.SettleConfirm(ctx, channel.SettleIn{PayOrderID: "P9", ChannelOrderNo: "T9", Amount: 700, Provider: prov, Sub: sub})
	if err != nil || st.State != channel.StateSuccess || st.ChannelOrderNo != "F1" {
		t.Fatalf("settle: %+v %v", st, err)
	}
	wd, err := e.Withdraw(ctx, channel.WithdrawIn{WithdrawNo: "WD1", Amount: 700, Provider: prov, Sub: sub})
	if err != nil || wd.State != channel.StateWaiting || wd.ChannelOrderNo != "W1" || withdraw["out_request_no"] != "WD1" {
		t.Fatalf("withdraw: %+v %v body=%v", wd, err, withdraw)
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
	raw := []byte(`{"sub_orders":[{"trade_state":"CLOSED"},{"trade_state":"PAYERROR"}]}`)
	if combineState(raw).State != channel.StateFail {
		t.Fatal("combine fail fold")
	}
	if combineState([]byte(`{"sub_orders":[{"trade_state":"SUCCESS"},{"trade_state":"USERPAYING"}]}`)).State != channel.StateWaiting {
		t.Fatal("combine waiting fold")
	}
}
