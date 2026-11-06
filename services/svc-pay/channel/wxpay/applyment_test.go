package wxpay

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"platfarm/svc-pay/channel"
)

// testKeys 生成商户私钥与一把“微信侧”密钥对（模拟平台证书/支付公钥）。
func testKeys(t *testing.T) (merchantPriv string, wxPub string, wxPriv *rsa.PrivateKey) {
	t.Helper()
	mk, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(mk)
	merchantPriv = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
	wxPriv, _ = rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKIXPublicKey(&wxPriv.PublicKey)
	wxPub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	return
}

func sampleApplyment() channel.Applyment {
	var a channel.Applyment
	a.Subject.Type = "enterprise"
	a.Subject.Name = "示例科技有限公司"
	a.Subject.ShortName = "示例科技"
	a.Subject.LicenseNo = "91110000MA01ABCD2E"
	a.Subject.LicenseValid = "2020-01-01,长期"
	a.Subject.MCC = "719"
	a.LegalPerson.Name = "张三"
	a.LegalPerson.IDType = "id_card"
	a.LegalPerson.IDNo = "110101199001011234"
	a.LegalPerson.IDValid = "2015-01-01,2035-01-01"
	a.LegalPerson.Phone = "13800138000"
	a.Contact.Name = "张三"
	a.Contact.Phone = "13800138000"
	a.Contact.Email = "zs@example.com"
	a.Business.ServicePhone = "010-12345678"
	a.Business.Address.Province = "北京市"
	a.Business.Address.City = "北京市"
	a.Business.Address.District = "海淀区"
	a.Business.Address.Detail = "中关村大街1号"
	a.Business.Address.Code = "110108"
	a.Business.Scene.Types = []string{"offline", "mini_program"}
	a.Business.Scene.AppID = "wx1234567890"
	a.SettlementAccount.Type = "corporate"
	a.SettlementAccount.Bank = "工商银行"
	a.SettlementAccount.Branch = "工商银行北京中关村支行"
	a.SettlementAccount.AccountName = "示例科技有限公司"
	a.SettlementAccount.AccountNo = "6222021234567890123"
	a.SettlementAccount.AddressCode = "110108"
	a.Rates = []struct {
		WayCode     string `json:"way_code"`
		FeeRateBps  int    `json:"fee_rate_bps"`
		SettleCycle string `json:"settle_cycle"`
	}{{WayCode: "WX_JSAPI", FeeRateBps: 60, SettleCycle: "T1"}}
	a.Images = map[string]string{"license": "1", "id_front": "2", "id_back": "3", "storefront": "4"}
	a.Extra = map[string]string{"wx_qualification_type": "零售批发/生活娱乐/其他"}
	return a
}

func TestApplyment_SubmitEncryptsSensitiveFieldsAndSetsSerial(t *testing.T) {
	merchantPriv, wxPub, wxPriv := testKeys(t)
	var gotSerial string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/applyment4sub/applyment/" && r.Method == http.MethodPost:
			gotSerial = r.Header.Get("Wechatpay-Serial")
			if !strings.HasPrefix(r.Header.Get("Authorization"), "WECHATPAY2-SHA256-RSA2048 mchid=\"1900000109\"") {
				t.Errorf("bad authorization header: %s", r.Header.Get("Authorization"))
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"applyment_id":2000002124775691}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)

	prov := map[string]string{
		"sp_mchid": "1900000109", "mch_serial_no": "ABC", "private_key": merchantPriv,
		"wx_platform_cert": wxPub, "wx_platform_cert_serial": "544AB49A",
	}
	res, err := (&partner{}).SubmitApplyment(context.Background(), channel.ApplymentIn{
		BusinessCode: "BC-1", Payload: sampleApplyment(),
		Media:    map[string]string{"license": "M1", "id_front": "M2", "id_back": "M3", "storefront": "M4"},
		Provider: prov,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ChannelApplymentID != "2000002124775691" {
		t.Fatalf("applyment id = %q", res.ChannelApplymentID)
	}
	if gotSerial != "544AB49A" {
		t.Fatalf("Wechatpay-Serial = %q, want platform cert serial", gotSerial)
	}
	if len(res.Unexpressed) != 1 || res.Unexpressed[0] != "rates" {
		t.Fatalf("unexpressed = %v", res.Unexpressed)
	}

	subject := gotBody["subject_info"].(map[string]any)
	if subject["subject_type"] != "SUBJECT_TYPE_ENTERPRISE" {
		t.Errorf("subject_type = %v", subject["subject_type"])
	}
	lic := subject["business_license_info"].(map[string]any)
	if lic["license_copy"] != "M1" || lic["period_end"] != "长期" || lic["license_number"] != "91110000MA01ABCD2E" {
		t.Errorf("business_license_info = %v", lic)
	}
	// 敏感字段必须是密文，且能用“微信侧”私钥解回明文。
	idCard := subject["identity_info"].(map[string]any)["id_card_info"].(map[string]any)
	if idCard["id_card_number"] == "110101199001011234" {
		t.Fatal("id_card_number sent in plaintext")
	}
	if got := decryptOAEP(t, wxPriv, idCard["id_card_number"].(string)); got != "110101199001011234" {
		t.Errorf("id_card_number decrypts to %q", got)
	}
	bank := gotBody["bank_account_info"].(map[string]any)
	if got := decryptOAEP(t, wxPriv, bank["account_number"].(string)); got != "6222021234567890123" {
		t.Errorf("account_number decrypts to %q", got)
	}
	if bank["bank_account_type"] != "BANK_ACCOUNT_TYPE_CORPORATE" || bank["bank_name"] != "工商银行北京中关村支行" {
		t.Errorf("bank_account_info = %v", bank)
	}
	sales := gotBody["business_info"].(map[string]any)["sales_info"].(map[string]any)
	scenes, _ := json.Marshal(sales["sales_scenes_type"])
	if string(scenes) != `["SALES_SCENES_STORE","SALES_SCENES_MINI_PROGRAM"]` {
		t.Errorf("scenes = %s", scenes)
	}
	store := sales["biz_store_info"].(map[string]any)
	if pics, _ := json.Marshal(store["store_entrance_pic"]); string(pics) != `["M4"]` {
		t.Errorf("store_entrance_pic = %s", pics)
	}
	if gotBody["settlement_info"].(map[string]any)["settlement_id"] != "719" {
		t.Errorf("settlement_id = %v", gotBody["settlement_info"])
	}
}

func TestApplyment_SubmitRequiresEncryptKey(t *testing.T) {
	merchantPriv, _, _ := testKeys(t)
	_, err := (&partner{}).SubmitApplyment(context.Background(), channel.ApplymentIn{
		BusinessCode: "BC-1", Payload: sampleApplyment(),
		Provider: map[string]string{"sp_mchid": "1", "mch_serial_no": "A", "private_key": merchantPriv},
	})
	if err == nil || !strings.Contains(err.Error(), "敏感字段加密") {
		t.Fatalf("expected encrypt key error, got %v", err)
	}
}

func TestApplyment_UploadMediaMultipart(t *testing.T) {
	merchantPriv, _, _ := testKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/merchant/media/upload" {
			w.WriteHeader(404)
			return
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		meta := r.FormValue("meta")
		var m map[string]string
		_ = json.Unmarshal([]byte(meta), &m)
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file part: %v", err)
			w.WriteHeader(400)
			return
		}
		data, _ := io.ReadAll(f)
		if hdr.Filename != "license.png" || m["filename"] != "license.png" || string(data) != "PNGDATA" {
			t.Errorf("bad upload: filename=%s meta=%v data=%s", hdr.Filename, m, data)
		}
		if m["sha256"] == "" {
			t.Error("meta.sha256 missing")
		}
		if hdr.Header.Get("Content-Type") != "image/png" {
			t.Errorf("file content-type = %s", hdr.Header.Get("Content-Type"))
		}
		_, _ = w.Write([]byte(`{"media_id":"MEDIA-1"}`))
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)

	id, err := (&partner{}).UploadMedia(context.Background(), "license.png", []byte("PNGDATA"),
		map[string]string{"sp_mchid": "1", "mch_serial_no": "A", "private_key": merchantPriv})
	if err != nil || id != "MEDIA-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestApplyment_QueryFoldsState(t *testing.T) {
	merchantPriv, _, _ := testKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v3/applyment4sub/applyment/business_code/BC-9") {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"business_code":"BC-9","applyment_id":123,"sub_mchid":"1900001","sign_url":"https://pay.weixin.qq.com/sign",
			"applyment_state":"APPLYMENT_STATE_REJECTED","applyment_state_msg":"资料有误",
			"audit_detail":[{"field":"id_card_copy","field_name":"身份证人像面","reject_reason":"模糊"}]}`))
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)

	v, err := (&partner{}).QueryApplyment(context.Background(), "BC-9",
		map[string]string{"sp_mchid": "1", "mch_serial_no": "A", "private_key": merchantPriv})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != channel.ApplymentRejected || v.ChannelApplymentID != "123" || v.SubMchID != "1900001" {
		t.Fatalf("%+v", v)
	}
	if !strings.Contains(v.Message, "资料有误") || !strings.Contains(v.Message, "身份证人像面(id_card_copy)：模糊") {
		t.Fatalf("message = %q", v.Message)
	}
}

func TestFoldApplymentState(t *testing.T) {
	cases := map[string]string{
		"APPLYMENT_STATE_AUDITING":        channel.ApplymentChannelAuditing,
		"APPLYMENT_STATE_EDITTING":        channel.ApplymentRejected,
		"APPLYMENT_STATE_REJECTED":        channel.ApplymentRejected,
		"APPLYMENT_STATE_TO_BE_CONFIRMED": channel.ApplymentNeedVerify,
		"APPLYMENT_STATE_TO_BE_SIGNED":    channel.ApplymentNeedSign,
		"APPLYMENT_STATE_SIGNING":         channel.ApplymentNeedSign,
		"APPLYMENT_STATE_FINISHED":        channel.ApplymentFinished,
		"APPLYMENT_STATE_CANCELED":        channel.ApplymentCanceled,
		"SOMETHING_NEW":                   "",
	}
	for in, want := range cases {
		if got := foldApplymentState(in); got != want {
			t.Errorf("%s → %q, want %q", in, got, want)
		}
	}
}

func TestEncryptKeyPreference(t *testing.T) {
	serial, _, err := encryptKey(map[string]string{
		"wx_public_key": "P", "wx_public_key_id": "PUB_KEY_ID_1",
		"wx_platform_cert": "C", "wx_platform_cert_serial": "S1",
	})
	if err != nil || serial != "PUB_KEY_ID_1" {
		t.Fatalf("prefer public key: %s %v", serial, err)
	}
	serial, _, err = encryptKey(map[string]string{"wx_platform_cert": "C", "wx_platform_cert_serial": "S1"})
	if err != nil || serial != "S1" {
		t.Fatalf("fallback platform cert: %s %v", serial, err)
	}
}

func decryptOAEP(t *testing.T, priv *rsa.PrivateKey, b64 string) string {
	t.Helper()
	ct, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	plain, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, priv, ct, nil)
	if err != nil {
		t.Fatalf("oaep: %v", err)
	}
	return string(plain)
}
