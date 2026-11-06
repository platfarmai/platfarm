package wxpay

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func TestSignRoundTrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))

	sig, err := sign(pemStr, "GET\n/v3/x\n1\nn\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(sig, "=") && len(sig) < 300 {
		t.Fatalf("signature looks wrong: %d bytes", len(sig))
	}
	if _, err := parsePrivateKey(pemStr); err != nil {
		t.Fatal(err)
	}
	if _, err := parsePrivateKey("not pem"); err == nil {
		t.Fatal("garbage pem should fail")
	}
}

func TestTradeStateFold(t *testing.T) {
	cases := map[string]string{
		`{"trade_state":"SUCCESS","transaction_id":"T1"}`:  "SUCCESS",
		`{"trade_state":"NOTPAY"}`:                          "WAITING",
		`{"trade_state":"USERPAYING"}`:                      "WAITING",
		`{"trade_state":"CLOSED","trade_state_desc":"已关"}`: "FAIL",
		`{"trade_state":"PAYERROR"}`:                        "FAIL",
	}
	for raw, want := range cases {
		if got := tradeState([]byte(raw)).State; got != want {
			t.Errorf("%s → %s, want %s", raw, got, want)
		}
	}
	if got := tradeState([]byte(`{"trade_state":"SUCCESS","transaction_id":"T9"}`)).ChannelOrderNo; got != "T9" {
		t.Errorf("channel order no = %s", got)
	}
}

func TestRefundState(t *testing.T) {
	if refundState("PROCESSING") != "WAITING" || refundState("SUCCESS") != "SUCCESS" || refundState("ABNORMAL") != "FAIL" {
		t.Fatal("refund state fold mismatch")
	}
}

func TestVerifyKeyFor_SerialSelectsKey(t *testing.T) {
	both := map[string]string{
		"wx_public_key": "PUB", "wx_public_key_id": "PUB_KEY_ID_1",
		"wx_platform_cert": "CERT", "wx_platform_cert_serial": "ABC123",
	}
	cases := []struct {
		name   string
		prov   map[string]string
		serial string
		want   string
		fail   bool
	}{
		{"pubkey id match", both, "PUB_KEY_ID_1", "PUB", false},
		{"platform cert serial match", both, "ABC123", "CERT", false},
		{"pubkey id mismatch", both, "PUB_KEY_ID_9", "", true},
		{"cert serial mismatch", both, "DEADBEEF", "", true},
		{"pubkey without id configured", map[string]string{"wx_public_key": "PUB"}, "PUB_KEY_ID_X", "PUB", false},
		{"only cert, no serial configured", map[string]string{"wx_platform_cert": "CERT"}, "DEADBEEF", "CERT", false},
		{"m1 legacy: only pubkey, cert serial header", map[string]string{"wx_public_key": "PUB"}, "DEADBEEF", "PUB", false},
		{"nothing configured", map[string]string{}, "X", "", true},
	}
	for _, tc := range cases {
		got, err := verifyKeyFor(tc.prov, tc.serial)
		if tc.fail {
			if err == nil {
				t.Errorf("%s: expected error, got %q", tc.name, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q err=%v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestFailFromBody(t *testing.T) {
	res := failFromBody([]byte(`{"code":"PARAM_ERROR","message":"缺参数"}`))
	if res.State != "FAIL" || res.ErrCode != "PARAM_ERROR" || res.ErrMsg != "缺参数" {
		t.Fatalf("%+v", res)
	}
}
