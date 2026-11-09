package alipay

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"platfarm/svc-pay/channel"
)

func testKeyPair(t *testing.T) (privPEM, pubPEM string, priv *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	privPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
	pubPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
	return privPEM, pubPEM, key
}

// signResponse 用商户私钥冒充支付宝给响应签名（测试里两把钥匙是一对）。
func signResponse(t *testing.T, privPEM, method string, node any) []byte {
	t.Helper()
	body, _ := json.Marshal(node)
	sig, err := sign(privPEM, string(body))
	if err != nil {
		t.Fatal(err)
	}
	key := strings.ReplaceAll(method, ".", "_") + "_response"
	out, _ := json.Marshal(map[string]any{key: json.RawMessage(body), "sign": sig})
	return out
}

func TestZFT_SignRoundTripAndPay(t *testing.T) {
	privPEM, pubPEM, _ := testKeyPair(t)
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(raw))
		// 签名串用原始百分号编码的值（空格是 %20，不是表单解码后的空格）。
		if err := verify(pubPEM, canonicalRaw(string(raw)), got.Get("sign")); err != nil {
			t.Errorf("request signature: %v\ncanon=%s", err, canonicalRaw(string(raw)))
		}
		_, _ = w.Write(signResponse(t, privPEM, got.Get("method"), map[string]string{"code": "10000", "msg": "Success", "qr_code": "https://qr"}))
	}))
	defer srv.Close()
	t.Setenv("ALIPAY_GATEWAY", srv.URL)
	prov := map[string]string{"app_id": "2021", "private_key": privPEM, "alipay_public_key": pubPEM}
	sub := map[string]string{"smid": "2088"}
	res, err := (&zft{}).Pay(context.Background(), channel.PayIn{
		PayOrderID: "P1", WayCode: "ALI_QR", Amount: 1234, Subject: "测试", NotifyURL: "https://n", Provider: prov, Sub: sub,
	})
	if err != nil || res.State != channel.StateWaiting {
		t.Fatalf("pay: %+v %v", res, err)
	}
	if got.Get("method") != "alipay.trade.precreate" || got.Get("app_id") != "2021" || got.Get("sign_type") != "RSA2" {
		t.Fatalf("form = %v", got)
	}
	// 下单时不冻结：分账冻结在确认结算时按需声明。
	if !strings.Contains(got.Get("biz_content"), `"merchant_id":"2088"`) || strings.Contains(got.Get("biz_content"), "royalty_freeze") {
		t.Fatalf("biz = %s", got.Get("biz_content"))
	}
	if !strings.Contains(got.Get("biz_content"), `"total_amount":"12.34"`) {
		t.Fatalf("amount = %s", got.Get("biz_content"))
	}
	cred := res.Credential.(map[string]string)
	if cred["qrCode"] != "https://qr" {
		t.Fatalf("credential = %v", cred)
	}
}

// canonicalRaw 按支付宝规则重拼签名串：值先百分号解码（+ 保持为加号，不是空格），key 排序，排除 sign 与空值。
func canonicalRaw(form string) string {
	params := map[string]string{}
	for _, pair := range strings.Split(form, "&") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "sign" || v == "" {
			continue
		}
		dec, err := url.PathUnescape(v)
		if err != nil {
			dec = v
		}
		params[k] = dec
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sortStrings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, "&")
}

func canonical(v url.Values) string {
	params := map[string]string{}
	for k := range v {
		params[k] = v.Get(k)
	}
	delete(params, "sign")
	delete(params, "sign_type")
	keys := make([]string, 0, len(params))
	for k, val := range params {
		if val != "" {
			keys = append(keys, k)
		}
	}
	sortStrings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, "&")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestZFT_SettleRoyaltyAndNotify(t *testing.T) {
	privPEM, pubPEM, priv := testKeyPair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		method := form.Get("method")
		biz := form.Get("biz_content")
		var node any
		switch method {
		case "alipay.trade.settle.confirm":
			// 幂等号是结算单号；要分账时结算带冻结标识。
			if !strings.Contains(biz, `"amount":"90.00"`) || !strings.Contains(biz, `"out_request_no":"S-1"`) ||
				!strings.Contains(biz, `"royalty_freeze":"true"`) || !strings.Contains(biz, `"defaultSettle"`) {
				t.Errorf("settle biz = %s", biz)
			}
			node = map[string]string{"code": "10000", "msg": "Success", "trade_no": "T1", "settle_no": "S1"}
		case "alipay.trade.order.settle":
			// 最后一笔分账带完结标识，解冻剩余资金。
			if !strings.Contains(biz, `"royalty_finish":"true"`) {
				t.Errorf("royalty biz = %s", biz)
			}
			node = map[string]string{"code": "10000", "msg": "Success", "trade_no": "T1", "settle_no": "R1"}
		case "alipay.trade.order.settle.query":
			node = map[string]any{"code": "10000", "msg": "Success", "royalty_detail_list": []map[string]string{{"state": "SUCCESS", "trans_in": "a@b.c", "amount": "10.00"}}}
		case "ant.merchant.expand.indirect.zft.create":
			node = map[string]string{"code": "10000", "msg": "Success", "order_id": "Z1"}
		case "ant.merchant.expand.indirect.zftorder.query":
			node = map[string]any{"code": "10000", "msg": "Success", "orders": []map[string]string{{"order_id": "Z1", "status": "99", "smid": "2088001"}}}
		default:
			t.Errorf("unexpected method %s", method)
			node = map[string]string{"code": "40004", "msg": "Fail", "sub_code": "x", "sub_msg": "no"}
		}
		_, _ = w.Write(signResponse(t, privPEM, method, node))
	}))
	defer srv.Close()
	t.Setenv("ALIPAY_GATEWAY", srv.URL)
	prov := map[string]string{"app_id": "2021", "private_key": privPEM, "alipay_public_key": pubPEM}
	sub := map[string]string{"smid": "2088"}
	z := &zft{}
	ctx := context.Background()

	st, err := z.SettleConfirm(ctx, channel.SettleIn{SettleNo: "S-1", PayOrderID: "P1", ChannelOrderNo: "T1", Amount: 9000,
		HoldForDivision: true, Provider: prov, Sub: sub})
	if err != nil || st.State != channel.StateSuccess || st.ChannelOrderNo != "S1" {
		t.Fatalf("settle: %+v %v", st, err)
	}
	sh, err := z.Share(ctx, channel.DivisionIn{DivisionNo: "D1", ChannelOrderNo: "T1", UnfreezeUnsplit: true, Provider: prov, Sub: sub,
		Receivers: []channel.DivisionReceiver{{Type: "loginName", Account: "a@b.c", Amount: 1000}}})
	if err != nil || sh.State != channel.StateWaiting || sh.ChannelOrderNo != "R1" {
		t.Fatalf("share: %+v %v", sh, err)
	}
	qs, err := z.QueryShare(ctx, channel.DivisionRef{DivisionNo: "D1", ChannelOrderNo: "T1", Provider: prov, Sub: sub})
	if err != nil || qs.State != channel.StateSuccess || qs.Receivers[0].Amount != 1000 {
		t.Fatalf("query share: %+v %v", qs, err)
	}
	// 直付通没有平台侧余额，也就没有提现能力。
	if _, err := channel.Withdrawing("alipay_zft"); err == nil {
		t.Fatal("alipay_zft must not offer withdrawals")
	}
	if _, err := channel.Settling("alipay_zft"); err != nil {
		t.Fatalf("alipay_zft settles: %v", err)
	}
	ar, err := z.SubmitApplyment(ctx, channel.ApplymentIn{BusinessCode: "B1", Provider: prov, Payload: sampleApplyment()})
	if err != nil || ar.ChannelApplymentID != "Z1" {
		t.Fatalf("applyment: %+v %v", ar, err)
	}
	view, err := z.QueryApplyment(ctx, "B1", prov)
	if err != nil || view.State != channel.ApplymentFinished || view.SubMchID != "2088001" {
		t.Fatalf("query applyment: %+v %v", view, err)
	}

	// 回调验签：篡改 trade_status 必须失败。
	form := url.Values{"out_trade_no": {"P1"}, "trade_no": {"T1"}, "trade_status": {"TRADE_SUCCESS"}, "total_amount": {"100.00"}}
	sig, _ := sign(privPEM, canonical(form))
	form.Set("sign", sig)
	form.Set("sign_type", "RSA2")
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	n, err := z.ParseNotify(req, prov)
	if err != nil || n.Result.State != channel.StateSuccess || n.OrderID != "P1" || n.AckBody != "success" {
		t.Fatalf("notify: %+v %v", n, err)
	}
	form.Set("trade_status", "TRADE_CLOSED")
	req = httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(form.Encode()))
	if _, err := z.ParseNotify(req, prov); err == nil {
		t.Fatal("tampered notify must fail verification")
	}
	_ = priv
	if fenOf("12.34") != 1234 || yuan(1234) != "12.34" {
		t.Fatal("fen/yuan")
	}
}

func sampleApplyment() channel.Applyment {
	var a channel.Applyment
	a.Subject.Type = "enterprise"
	a.Subject.Name = "某某公司"
	a.Subject.ShortName = "某某"
	a.Subject.LicenseNo = "91110000"
	a.Subject.MCC = "5411"
	a.LegalPerson.Name = "张三"
	a.LegalPerson.IDNo = "110101199001011234"
	a.Contact.Name = "张三"
	a.Contact.Phone = "13800000000"
	a.Business.ServicePhone = "13800000000"
	a.SettlementAccount.Type = "corporate"
	a.SettlementAccount.AccountName = "某某公司"
	a.SettlementAccount.AccountNo = "62220000"
	a.SettlementAccount.Bank = "工商银行"
	a.Extra = map[string]string{"alipay_logon_id": "merchant@example.com"}
	return a
}

func TestZFT_ApplymentNeedsExplicitAlipayAccount(t *testing.T) {
	a := sampleApplyment()
	a.Extra = nil
	// 不能拿联系人手机号冒充签约/结算支付宝账号。
	if _, err := (&zft{}).SubmitApplyment(context.Background(), channel.ApplymentIn{BusinessCode: "B1", Payload: a}); err == nil ||
		!strings.Contains(err.Error(), "alipay_logon_id") {
		t.Fatalf("missing alipay account: %v", err)
	}
}

func TestZFT_BillParseAndBadSign(t *testing.T) {
	csv := "#支付宝业务明细查询\n支付宝交易号,商户订单号,业务类型,订单金额（元）,服务费（元）,退款批次号/请求号,完成时间\n" +
		"2020110122001,P1,交易,100.00,0.60,,2020-11-01 10:00:00\n" +
		"2020110122001,P1,退款,-10.00,-0.06,R1,2020-11-01 11:00:00\n" +
		"2020110122001,P1,分账,-5.00,0.00,,2020-11-01 12:00:00\n#合计\n"
	rows, err := parseAlipayBill(csv)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%+v %v", rows, err)
	}
	// 支付行必须带对账认得的成功状态，否则对账会把它整行跳过、什么差异也报不出来。
	if rows[0].OutTradeNo != "P1" || rows[0].TradeState != "SUCCESS" || rows[0].Amount != 10000 || rows[0].Fee != 60 || rows[0].IsRefund() {
		t.Fatalf("pay row = %+v", rows[0])
	}
	if rows[1].OutRefundNo != "R1" || rows[1].OutTradeNo != "P1" || rows[1].RefundAmount != 1000 || rows[1].RefundState != "SUCCESS" || rows[1].Amount != 0 {
		t.Fatalf("refund row = %+v", rows[1])
	}
	if err := verifyResponse([]byte(`{"x":1}`), "alipay.trade.query", "not a pem"); err == nil {
		t.Fatal("garbage response should fail")
	}
	raw := base64.StdEncoding.EncodeToString([]byte("nope"))
	_ = raw
}
