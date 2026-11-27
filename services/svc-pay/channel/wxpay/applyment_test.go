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
	a.Images = map[string]string{"license": "1", "id_front": "2", "id_back": "3", "storefront": "4", "interior": "5"}
	a.Extra = map[string]string{"wx_qualification_type": "零售", "wx_id_card_address": "北京市海淀区某路1号"}
	return a
}

var sampleMedia = map[string]string{"license": "M1", "id_front": "M2", "id_back": "M3", "storefront": "M4", "interior": "M5"}

// submitCapture 用假微信接住申请单，返回请求体。
func submitCapture(t *testing.T, a channel.Applyment, media map[string]string) map[string]any {
	t.Helper()
	merchantPriv, wxPub, _ := testKeys(t)
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"applyment_id":1}`))
	}))
	defer srv.Close()
	t.Setenv("WXPAY_API_BASE", srv.URL)
	_, err := (&partner{}).SubmitApplyment(context.Background(), channel.ApplymentIn{
		BusinessCode: "BC-1", Payload: a, Media: media,
		Provider: map[string]string{"sp_mchid": "1900000109", "mch_serial_no": "ABC", "private_key": merchantPriv,
			"wx_platform_cert": wxPub, "wx_platform_cert_serial": "544AB49A"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestApplymentProblems_SampleIsClean(t *testing.T) {
	if p := (&partner{}).ApplymentProblems(sampleApplyment()); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
}

func TestApplymentProblems_WeChatRequiredFields(t *testing.T) {
	a := sampleApplyment()
	a.Contact.Email = ""
	a.SettlementAccount.Type = "personal"
	a.SettlementAccount.Bank, a.SettlementAccount.Branch = "其他银行", ""
	a.Business.Scene.Types = []string{"offline", "mp", "app", "web"}
	delete(a.Images, "interior")
	a.Extra = map[string]string{"wx_contact_type": "SUPER"}
	p := strings.Join((&partner{}).ApplymentProblems(a), "\n")
	for _, want := range []string{"contact.email", "wx_qualification_type", "对公账户", "wx_id_card_address",
		"settlement_account.branch", "wx_contact_id_no", "wx_contact_id_valid", "contact_id_front", "contact_id_back",
		"images.interior", "images.mp_pic", "images.app_pic", "web_domain"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing problem for %s in:\n%s", want, p)
		}
	}
	// 资料不全时不该打到微信。
	_, err := (&partner{}).SubmitApplyment(context.Background(), channel.ApplymentIn{BusinessCode: "BC-1", Payload: a})
	if err == nil || !strings.Contains(err.Error(), "资料不满足微信进件要求") {
		t.Fatalf("expected preflight error, got %v", err)
	}
}

func TestApplyment_InstitutionSendsCertificateInfo(t *testing.T) {
	a := sampleApplyment()
	a.Subject.Type, a.Subject.MCC = "institution", "725"
	subject := submitCapture(t, a, sampleMedia)["subject_info"].(map[string]any)
	if subject["subject_type"] != "SUBJECT_TYPE_INSTITUTIONS" {
		t.Errorf("subject_type = %v", subject["subject_type"])
	}
	if _, has := subject["business_license_info"]; has {
		t.Error("institution must not send business_license_info")
	}
	cert := subject["certificate_info"].(map[string]any)
	if cert["cert_type"] != "CERTIFICATE_TYPE_2388" || cert["cert_copy"] != "M1" || cert["cert_number"] != "91110000MA01ABCD2E" ||
		cert["company_address"] != "北京市北京市海淀区中关村大街1号" || cert["period_end"] != "长期" {
		t.Errorf("certificate_info = %v", cert)
	}
}

func TestApplyment_NonIDCardUsesIDDocInfo(t *testing.T) {
	a := sampleApplyment()
	a.LegalPerson.IDType, a.LegalPerson.IDNo = "passport", "E12345678"
	identity := submitCapture(t, a, sampleMedia)["subject_info"].(map[string]any)["identity_info"].(map[string]any)
	if _, has := identity["id_card_info"]; has {
		t.Error("passport must not send id_card_info")
	}
	doc := identity["id_doc_info"].(map[string]any)
	if identity["id_doc_type"] != "IDENTIFICATION_TYPE_OVERSEA_PASSPORT" || doc["id_doc_copy"] != "M2" || doc["doc_period_end"] != "2035-01-01" {
		t.Errorf("identity_info = %v", identity)
	}
	if _, has := doc["id_doc_copy_back"]; has {
		t.Error("passport has no back side")
	}
	if doc["id_doc_address"] == nil || doc["id_doc_address"] == "北京市海淀区某路1号" {
		t.Errorf("id_doc_address must be sent encrypted, got %v", doc["id_doc_address"])
	}
}

func TestApplyment_SuperContactAndScenePics(t *testing.T) {
	a := sampleApplyment()
	a.Business.Scene.Types = []string{"mp", "app"}
	a.Extra["wx_contact_type"] = "SUPER"
	a.Extra["wx_contact_id_no"] = "110101199202022345"
	a.Extra["wx_contact_id_valid"] = "2018-03-01,2038-03-01"
	for _, k := range []string{"contact_id_front", "contact_id_back", "mp_pic", "app_pic", "app_pic_3"} {
		a.Images[k] = "x"
	}
	media := map[string]string{"license": "M1", "id_front": "M2", "id_back": "M3", "contact_id_front": "C1",
		"contact_id_back": "C2", "mp_pic": "P1", "app_pic": "A1", "app_pic_3": "A3"}
	body := submitCapture(t, a, media)
	contact := body["contact_info"].(map[string]any)
	if contact["contact_id_doc_copy"] != "C1" || contact["contact_id_doc_copy_back"] != "C2" ||
		contact["contact_period_begin"] != "2018-03-01" || contact["contact_period_end"] != "2038-03-01" ||
		contact["contact_id_number"] == "110101199202022345" || contact["contact_id_number"] == "" {
		t.Errorf("contact_info = %v", contact)
	}
	sales := body["business_info"].(map[string]any)["sales_info"].(map[string]any)
	if got, _ := json.Marshal(sales["mp_info"].(map[string]any)["mp_pics"]); string(got) != `["P1"]` {
		t.Errorf("mp_pics = %s", got)
	}
	if got, _ := json.Marshal(sales["app_info"].(map[string]any)["app_pics"]); string(got) != `["A1","A3"]` {
		t.Errorf("app_pics = %s", got)
	}
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
		Media:    sampleMedia,
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
	if pics, _ := json.Marshal(store["indoor_pic"]); string(pics) != `["M5"]` {
		t.Errorf("indoor_pic = %s", pics)
	}
	if email := gotBody["contact_info"].(map[string]any)["contact_email"].(string); decryptOAEP(t, wxPriv, email) != "zs@example.com" {
		t.Errorf("contact_email not sent encrypted: %q", email)
	}
	if addr, _ := idCard["id_card_address"].(string); addr == "" || decryptOAEP(t, wxPriv, addr) != "北京市海淀区某路1号" {
		t.Errorf("id_card_address = %q", addr)
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

func TestApplyment_WebDomainSchemeAndNormalization(t *testing.T) {
	a := sampleApplyment()
	a.Business.Scene.Types = []string{"web"}
	a.Business.Address.Code = ""
	a.SettlementAccount.AddressCode = ""

	for _, bad := range []string{"lab.scyytc.com", "ftp://lab.scyytc.com", "https://", "https://bad host.com"} {
		a.Extra["web_domain"] = bad
		p := strings.Join((&partner{}).ApplymentProblems(a), "\n")
		if !strings.Contains(p, "extra.web_domain") {
			t.Errorf("expected web_domain problem for %q, got %q", bad, p)
		}
	}
	for _, ok := range []string{"https://lab.scyytc.com", "http://shop.example.com", "https://shop.example.com/pay"} {
		a.Extra["web_domain"] = ok
		if p := (&partner{}).ApplymentProblems(a); len(p) != 0 {
			t.Errorf("unexpected problems for %q: %v", ok, p)
		}
	}

	// 历史存量记录若漏了协议前缀，提交渠道时自动补齐 https://，且不改动入参 map；两个编码都为空时不传空串 bank_address_code。
	a.Extra["web_domain"] = "  lab.scyytc.com  "
	body := submitCapture(t, a, sampleMedia)
	if a.Extra["web_domain"] != "  lab.scyytc.com  " {
		t.Fatalf("SubmitApplyment mutated caller Extra: %q", a.Extra["web_domain"])
	}
	web := body["business_info"].(map[string]any)["sales_info"].(map[string]any)["web_info"].(map[string]any)
	if web["domain"] != "https://lab.scyytc.com" {
		t.Fatalf("web_info.domain = %v, want https://lab.scyytc.com", web["domain"])
	}
	bank := body["bank_account_info"].(map[string]any)
	if _, has := bank["bank_address_code"]; has {
		t.Fatalf("empty bank_address_code must be omitted, got %v", bank["bank_address_code"])
	}

	// 仅填省级编码（如 510000）时 bank_address_code 不下发，避免触发微信“至少精确到市”报错。
	a.Business.Address.Code = "510000"
	body = submitCapture(t, a, sampleMedia)
	if _, has := body["bank_account_info"].(map[string]any)["bank_address_code"]; has {
		t.Fatalf("province-only bank_address_code must be omitted")
	}
}

func TestApplyment_RegionCodeValidation(t *testing.T) {
	for _, ok := range []string{"110000", "110108", "510100", "510132", "441900", "429004", "810000", "820104"} {
		if !IsValidRegionCode(ok) {
			t.Errorf("expected valid region code %s", ok)
		}
	}
	for _, bad := range []string{"", "611100", "510118", "999999", "abc"} {
		if IsValidRegionCode(bad) {
			t.Errorf("expected invalid region code %q", bad)
		}
	}
	if !isProvinceOnlyRegionCode("510000") || isProvinceOnlyRegionCode("110000") || isProvinceOnlyRegionCode("810000") {
		t.Fatal("province-only classification mismatch")
	}

	a := sampleApplyment()
	a.Business.Address.Code = "510118"        // 微信对照表未收录的新津区代码（表中仍为 510132 新津县）
	a.SettlementAccount.AddressCode = "611100" // 邮政编码
	p := strings.Join((&partner{}).ApplymentProblems(a), "\n")
	if !strings.Contains(p, "business.address.code") || !strings.Contains(p, "settlement_account.address_code") {
		t.Fatalf("expected both region code problems, got:\n%s", p)
	}
}

func TestApplyment_SuperContactRejectsMaskedID(t *testing.T) {
	a := sampleApplyment()
	a.Extra["wx_contact_type"] = "SUPER"
	a.Extra["wx_contact_id_no"] = "110***2345"
	a.Extra["wx_contact_id_valid"] = "2020-01-01,长期"
	a.Images["contact_id_front"] = "21"
	a.Images["contact_id_back"] = "22"
	p := strings.Join((&partner{}).ApplymentProblems(a), "\n")
	if !strings.Contains(p, "extra.wx_contact_id_no（经办人身份证号）不能包含脱敏星号 *") {
		t.Fatalf("expected masked wx_contact_id_no rejection, got:\n%s", p)
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
