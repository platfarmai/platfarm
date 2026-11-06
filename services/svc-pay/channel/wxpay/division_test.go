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

func TestProfitSharing_AddReceiverEncryptsName(t *testing.T) {
	merchantPriv, wxPub, wxPriv := testKeys(t)
	var got map[string]any
	var serial string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/profitsharing/receivers/add" {
			w.WriteHeader(404)
			return
		}
		serial = r.Header.Get("Wechatpay-Serial")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"type":"MERCHANT_ID","account":"1900000110"}`))
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)
	prov := map[string]string{"sp_mchid": "1900000109", "sp_appid": "wxsp", "mch_serial_no": "A", "private_key": merchantPriv,
		"wx_public_key": wxPub, "wx_public_key_id": "PUB_KEY_ID_7"}
	err := (&partner{}).AddReceiver(context.Background(), channel.ReceiverIn{
		Type: "MERCHANT_ID", Account: "1900000110", Name: "某某供应商", RelationType: "SUPPLIER",
		Provider: prov, Sub: map[string]string{"sub_mchid": "1900001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if serial != "PUB_KEY_ID_7" || got["sub_mchid"] != "1900001" || got["appid"] != "wxsp" || got["relation_type"] != "SUPPLIER" {
		t.Fatalf("serial=%s body=%v", serial, got)
	}
	if got["name"] == "某某供应商" {
		t.Fatal("name sent in plaintext")
	}
	if decryptOAEP(t, wxPriv, got["name"].(string)) != "某某供应商" {
		t.Fatal("name not decryptable")
	}
}

func TestProfitSharing_ShareAndFold(t *testing.T) {
	merchantPriv, _, _ := testKeys(t)
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/profitsharing/orders" && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			_, _ = w.Write([]byte(`{"order_id":"3008450740201411110007820472","state":"PROCESSING",
				"receivers":[{"type":"MERCHANT_ID","account":"1900000110","amount":100,"description":"分账","result":"PENDING"}]}`))
		case strings.HasPrefix(r.URL.Path, "/v3/profitsharing/orders/D1"):
			if r.URL.Query().Get("sub_mchid") != "1900001" || r.URL.Query().Get("transaction_id") != "T1" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"order_id":"300845","state":"FINISHED",
				"receivers":[{"type":"MERCHANT_ID","account":"1900000110","amount":100,"result":"SUCCESS","detail_id":"DT1"},
				             {"type":"PERSONAL_OPENID","account":"oabc","amount":50,"result":"CLOSED","fail_reason":"ACCOUNT_ABNORMAL"}]}`))
		case r.URL.Path == "/v3/profitsharing/orders/unfreeze":
			_, _ = w.Write([]byte(`{"order_id":"300846","state":"FINISHED","receivers":[]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)
	prov := map[string]string{"sp_mchid": "1900000109", "sp_appid": "wxsp", "mch_serial_no": "A", "private_key": merchantPriv}
	sub := map[string]string{"sub_mchid": "1900001"}
	p := &partner{}

	res, err := p.Share(context.Background(), channel.DivisionIn{
		DivisionNo: "D1", ChannelOrderNo: "T1", UnfreezeUnsplit: true,
		Receivers: []channel.DivisionReceiver{{Type: "MERCHANT_ID", Account: "1900000110", Amount: 100}},
		Provider:  prov, Sub: sub,
	})
	if err != nil || res.State != channel.StateWaiting || res.ChannelOrderNo != "3008450740201411110007820472" {
		t.Fatalf("share: %+v %v", res, err)
	}
	if got["out_order_no"] != "D1" || got["transaction_id"] != "T1" || got["unfreeze_unsplit"] != true {
		t.Fatalf("share body = %v", got)
	}
	rcv := got["receivers"].([]any)[0].(map[string]any)
	if rcv["amount"] != float64(100) || rcv["description"] != "分账" {
		t.Fatalf("receiver = %v", rcv)
	}

	q, err := p.QueryShare(context.Background(), channel.DivisionRef{DivisionNo: "D1", ChannelOrderNo: "T1", Provider: prov, Sub: sub})
	if err != nil || q.State != channel.StateSuccess || len(q.Receivers) != 2 {
		t.Fatalf("query: %+v %v", q, err)
	}
	if q.Receivers[0].Result != "SUCCESS" || q.Receivers[0].Detail != "DT1" || q.Receivers[1].Result != "CLOSED" || q.Receivers[1].Detail != "ACCOUNT_ABNORMAL" {
		t.Fatalf("receivers: %+v", q.Receivers)
	}

	f, err := p.FinishShare(context.Background(), channel.DivisionRef{DivisionNo: "D1", ChannelOrderNo: "T1", Provider: prov, Sub: sub})
	if err != nil || f.State != channel.StateSuccess {
		t.Fatalf("finish: %+v %v", f, err)
	}
}

func TestParseDivision_AllClosedIsFail(t *testing.T) {
	res := parseDivision([]byte(`{"order_id":"x","state":"FINISHED","receivers":[{"account":"a","result":"CLOSED","fail_reason":"NO_RELATION"}]}`))
	if res.State != channel.StateFail || res.ErrCode != "CLOSED" {
		t.Fatalf("%+v", res)
	}
	if parseDivision([]byte(`{"state":"WHATEVER"}`)).State != channel.StateUnknown {
		t.Fatal("unknown state must fold to UNKNOWN")
	}
}

const sampleBill = "\ufeff交易时间,公众账号ID,商户号,特约商户号,设备号,微信订单号,商户订单号,用户标识,交易类型,交易状态,付款银行,货币种类,应结订单金额,代金券金额,微信退款单号,商户退款单号,退款金额,充值券退款金额,退款类型,退款状态,商品名称,商户数据包,手续费,费率,订单金额,申请退款金额,费率备注\r\n" +
	"`2026-11-05 10:00:01,`wxsp,`1900000109,`1900001,`,`4200001,`P0001,`oUser1,`JSAPI,`SUCCESS,`OTHERS,`CNY,`12.34,`0.00,`,`,`0.00,`0.00,`,`,`测试商品,`,`0.07000,`0.60%,`12.34,`0.00,`\r\n" +
	"`2026-11-05 11:00:01,`wxsp,`1900000109,`1900001,`,`4200002,`P0002,`oUser2,`JSAPI,`REFUND,`OTHERS,`CNY,`0.00,`0.00,`5000001,`R0001,`5.00,`0.00,`ORIGINAL,`SUCCESS,`测试商品,`,`-0.03000,`0.60%,`20.00,`5.00,`\r\n" +
	"总交易单数,应结订单总金额,退款总金额,充值券退款总金额,手续费总金额,订单总金额,申请退款总金额\r\n" +
	"`2,`12.34,`5.00,`0.00,`0.04,`32.34,`5.00\r\n"

func TestParseTradeBill(t *testing.T) {
	rows, err := parseTradeBill(sampleBill)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (summary must be excluded)", len(rows))
	}
	pay := rows[0]
	if pay.OutTradeNo != "P0001" || pay.ChannelOrderNo != "4200001" || pay.SubMchID != "1900001" || pay.TradeState != "SUCCESS" || pay.Amount != 1234 || pay.Fee != 7 || pay.IsRefund() {
		t.Fatalf("pay row: %+v", pay)
	}
	rf := rows[1]
	if !rf.IsRefund() || rf.OutRefundNo != "R0001" || rf.RefundID != "5000001" || rf.RefundAmount != 500 || rf.RefundState != "SUCCESS" || rf.Amount != 2000 {
		t.Fatalf("refund row: %+v", rf)
	}
}

func TestTradeBill_DownloadsSignedURLAndMapsNoStatement(t *testing.T) {
	merchantPriv, _, _ := testKeys(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/bill/tradebill":
			if r.URL.Query().Get("bill_date") == "2026-11-04" {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"code":"NO_STATEMENT_EXIST","message":"账单不存在"}`))
				return
			}
			_, _ = w.Write([]byte(`{"hash_type":"SHA1","hash_value":"x","download_url":"` + srv.URL + `/v3/billdownload/file?token=abc"}`))
		case "/v3/billdownload/file":
			if r.URL.Query().Get("token") != "abc" || !strings.HasPrefix(r.Header.Get("Authorization"), "WECHATPAY2-SHA256-RSA2048") {
				t.Errorf("download not signed/tokened: %s %s", r.URL.RawQuery, r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(sampleBill))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)
	prov := map[string]string{"sp_mchid": "1900000109", "mch_serial_no": "A", "private_key": merchantPriv}

	rows, err := (&partner{}).TradeBill(context.Background(), "2026-11-05", "ALL", prov, nil)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if _, err := (&partner{}).TradeBill(context.Background(), "2026-11-04", "ALL", prov, nil); err != channel.ErrNoBill {
		t.Fatalf("expected ErrNoBill, got %v", err)
	}
}

func TestYuanToFen(t *testing.T) {
	for in, want := range map[string]int64{"12.34": 1234, "0.07000": 7, "-0.03000": -3, "": 0, "abc": 0, "100": 10000} {
		if got := yuanToFen(in); got != want {
			t.Errorf("%q → %d, want %d", in, got, want)
		}
	}
}
