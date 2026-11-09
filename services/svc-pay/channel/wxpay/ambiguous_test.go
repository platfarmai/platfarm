package wxpay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"platfarm/svc-pay/channel"
)

// replyStub 按用例切换的微信应答，并记下最后一次请求体。
type replyStub struct {
	mu     sync.Mutex
	status int
	body   string
	got    map[string]any
}

func (s *replyStub) set(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body = status, body
}

func newReplyStub(t *testing.T) (*replyStub, map[string]string) {
	t.Helper()
	merchantPriv, _, _ := testKeys(t)
	s := &replyStub{status: 200, body: `{}`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.got = nil
		_ = json.Unmarshal(raw, &s.got)
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("WXPAY_API_BASE", srv.URL)
	return s, map[string]string{"sp_mchid": "1900000109", "sp_appid": "wxsp", "mch_serial_no": "A", "private_key": merchantPriv}
}

func TestQuery_ErrorsAreNeverAFailedPayment(t *testing.T) {
	s, prov := newReplyStub(t)
	sub := map[string]string{"sub_mchid": "1900001"}
	cases := []struct {
		status int
		body   string
	}{
		{500, `{"code":"SYSTEM_ERROR","message":"系统繁忙"}`},
		{404, `{"code":"ORDER_NOT_EXIST","message":"订单不存在"}`},
		{429, `{"code":"FREQUENCY_LIMITED","message":"频率超限"}`},
		{401, `{"code":"SIGN_ERROR","message":"签名错误"}`},
	}
	for _, tc := range cases {
		s.set(tc.status, tc.body)
		for _, ch := range []channel.PayChannel{&partner{}, &ecommerce{}} {
			res, err := ch.Query(context.Background(), channel.OrderRef{PayOrderID: "P1", Provider: prov, Sub: sub})
			if err != nil || res.State != channel.StateUnknown {
				t.Errorf("%s query %d: %+v %v", ch.IfCode(), tc.status, res, err)
			}
		}
	}
	s.set(200, `{"trade_state":"SUCCESS","transaction_id":"T1","amount":{"total":1234}}`)
	res, _ := (&partner{}).Query(context.Background(), channel.OrderRef{PayOrderID: "P1", Provider: prov, Sub: sub})
	if res.State != channel.StateSuccess || res.Amount != 1234 {
		t.Fatalf("query success: %+v", res)
	}
}

func TestPay_ClassifiesErrorsAndSendsExpiry(t *testing.T) {
	s, prov := newReplyStub(t)
	in := channel.PayIn{PayOrderID: "P1", WayCode: "WX_NATIVE", Amount: 100, Subject: "t", NotifyURL: "https://n",
		ExpireAt: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Provider: prov, Sub: map[string]string{"sub_mchid": "1900001"}}

	s.set(200, `{"code_url":"weixin://x"}`)
	for _, ch := range []channel.PayChannel{&partner{}, &ecommerce{}} {
		if res, err := ch.Pay(context.Background(), in); err != nil || res.State != channel.StateWaiting {
			t.Fatalf("%s pay: %+v %v", ch.IfCode(), res, err)
		}
		s.mu.Lock()
		exp := s.got["time_expire"]
		s.mu.Unlock()
		if exp != "2026-11-09T18:00:00+08:00" {
			t.Fatalf("%s time_expire = %v", ch.IfCode(), exp)
		}
	}

	cases := []struct {
		status int
		body   string
		want   string
	}{
		{403, `{"code":"ORDERPAID","message":"订单已支付"}`, channel.StateUnknown},
		{403, `{"code":"OUT_TRADE_NO_USED","message":"商户订单号重复"}`, channel.StateUnknown},
		{500, `{"code":"SYSTEM_ERROR","message":"系统错误"}`, channel.StateUnknown},
		{429, `{"code":"FREQUENCY_LIMITED","message":"频率超限"}`, channel.StateUnknown},
		{400, `{"code":"PARAM_ERROR","message":"参数错误"}`, channel.StateFail},
		{400, `{"code":"ORDER_CLOSED","message":"订单已关闭"}`, channel.StateFail},
	}
	for _, tc := range cases {
		s.set(tc.status, tc.body)
		for _, ch := range []channel.PayChannel{&partner{}, &ecommerce{}} {
			res, err := ch.Pay(context.Background(), in)
			if err != nil || res.State != tc.want {
				t.Errorf("%s pay %d %s: %+v %v", ch.IfCode(), tc.status, tc.body, res, err)
			}
		}
	}
}

func TestMoneyMovements_ClassifyErrors(t *testing.T) {
	s, prov := newReplyStub(t)
	sub := map[string]string{"sub_mchid": "1900001"}
	ctx := context.Background()
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{500, `{"code":"SYSTEM_ERROR","message":"系统超时"}`, channel.StateUnknown},
		{400, `{"code":"BANK_ERROR","message":"银行系统异常"}`, channel.StateUnknown},
		{403, `{"code":"NOT_ENOUGH","message":"余额不足"}`, channel.StateFail},
		{400, `{"code":"PARAM_ERROR","message":"参数错误"}`, channel.StateFail},
	}
	for _, tc := range cases {
		s.set(tc.status, tc.body)
		for _, ch := range []channel.PayChannel{&partner{}, &ecommerce{}} {
			res, _ := ch.Refund(ctx, channel.RefundIn{RefundOrderID: "R1", PayOrderID: "P1", PayAmount: 100, RefundAmount: 100, Provider: prov, Sub: sub})
			if res.State != tc.want {
				t.Errorf("%s refund %s: %s", ch.IfCode(), tc.body, res.State)
			}
		}
		for _, sh := range []channel.SharingChannel{&partner{}, &ecommerce{}} {
			res, _ := sh.Share(ctx, channel.DivisionIn{DivisionNo: "D1", ChannelOrderNo: "T1", Provider: prov, Sub: sub,
				Receivers: []channel.DivisionReceiver{{Type: "MERCHANT_ID", Account: "1900000110", Amount: 10}}})
			if res.State != tc.want {
				t.Errorf("share %s: %s", tc.body, res.State)
			}
		}
		res, _ := (&ecommerce{}).Withdraw(ctx, channel.WithdrawIn{WithdrawNo: "W1", Amount: 100, Provider: prov, Sub: sub})
		if res.State != tc.want {
			t.Errorf("withdraw %s: %s", tc.body, res.State)
		}
	}
}

func TestParseNotify_ReportsAmountAndKeepsAbnormalRefundOpen(t *testing.T) {
	_, wxPub, wxPriv := testKeys(t)
	apiV3 := "0123456789abcdef0123456789abcdef"
	prov := map[string]string{"api_v3_key": apiV3, "wx_public_key": wxPub, "wx_public_key_id": "PUB_KEY_ID_T"}
	notify := func(eventType, plain string) *http.Request {
		nonce, aad := "abcdefghijkl", "transaction"
		block, _ := aes.NewCipher([]byte(apiV3))
		gcm, _ := cipher.NewGCM(block)
		ct := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(nonce), []byte(plain), []byte(aad)))
		body, _ := json.Marshal(map[string]any{"id": "EV-1", "event_type": eventType, "resource_type": "encrypt-resource",
			"resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": ct, "nonce": nonce, "associated_data": aad}})
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		h := sha256.Sum256([]byte(ts + "\nn1\n" + string(body) + "\n"))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, wxPriv, crypto.SHA256, h[:])
		req := httptest.NewRequest(http.MethodPost, "/notify", bytes.NewReader(body))
		req.Header.Set("Wechatpay-Timestamp", ts)
		req.Header.Set("Wechatpay-Nonce", "n1")
		req.Header.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(sig))
		req.Header.Set("Wechatpay-Serial", "PUB_KEY_ID_T")
		return req
	}

	n, err := (&partner{}).ParseNotify(notify("TRANSACTION.SUCCESS",
		`{"out_trade_no":"P1","transaction_id":"T1","trade_state":"SUCCESS","amount":{"total":1234,"payer_total":1234}}`), prov)
	if err != nil || n.OrderType != "pay" || n.OrderID != "P1" || n.Result.State != channel.StateSuccess || n.Result.Amount != 1234 {
		t.Fatalf("pay notify: %+v %v", n, err)
	}
	n, err = (&ecommerce{}).ParseNotify(notify("REFUND.ABNORMAL",
		`{"out_refund_no":"R1","refund_id":"RF1","refund_status":"ABNORMAL","amount":{"refund":500,"total":1234}}`), prov)
	if err != nil || n.OrderType != "refund" || n.OrderID != "R1" || n.Result.State != channel.StateWaiting ||
		n.Result.ErrCode != "ABNORMAL" || n.Result.Amount != 500 {
		t.Fatalf("abnormal refund notify: %+v %v", n, err)
	}
}
