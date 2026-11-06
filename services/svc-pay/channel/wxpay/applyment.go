// 微信特约商户进件（applyment4sub）：图片上传、提交申请单、查询状态、状态折叠。
// 敏感字段（姓名/证件号/手机/邮箱/银行账号）用微信支付公钥或平台证书公钥 RSA-OAEP 加密，
// 并在请求头 Wechatpay-Serial 声明所用公钥的序列号。
package wxpay

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
	"time"

	"platfarm/svc-pay/channel"
)

// 编译期保证 partner 实现了进件接口。
var _ channel.OnboardingChannel = (*partner)(nil)

// UploadMedia POST /v3/merchant/media/upload：multipart，签名正文是 meta JSON。
func (p *partner) UploadMedia(ctx context.Context, filename string, data []byte, prov map[string]string) (string, error) {
	sum := sha256.Sum256(data)
	meta, _ := json.Marshal(map[string]string{"filename": filename, "sha256": hex.EncodeToString(sum[:])})

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mh := textproto.MIMEHeader{}
	mh.Set("Content-Disposition", `form-data; name="meta";`)
	mh.Set("Content-Type", "application/json")
	part, err := w.CreatePart(mh)
	if err != nil {
		return "", err
	}
	_, _ = part.Write(meta)
	fh := textproto.MIMEHeader{}
	fh.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s";`, filename))
	fh.Set("Content-Type", mimeOf(filename))
	part, err = w.CreatePart(fh)
	if err != nil {
		return "", err
	}
	_, _ = part.Write(data)
	_ = w.Close()

	resp, raw, err := doRequest(ctx, http.MethodPost, "/v3/merchant/media/upload", meta, &buf,
		w.FormDataContentType(), nil, prov)
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return "", fmt.Errorf("media upload: %s %s", f.ErrCode, f.ErrMsg)
	}
	var out struct {
		MediaID string `json:"media_id"`
	}
	if json.Unmarshal(raw, &out) != nil || out.MediaID == "" {
		return "", fmt.Errorf("media upload: bad response")
	}
	return out.MediaID, nil
}

func mimeOf(filename string) string {
	switch strings.ToLower(filename[strings.LastIndex(filename, ".")+1:]) {
	case "png":
		return "image/png"
	case "bmp":
		return "image/bmp"
	default:
		return "image/jpeg"
	}
}

// SubmitApplyment 把归一资料映射成 applyment4sub 请求。微信不接受自定义费率，rates 永远报为无法表达。
func (p *partner) SubmitApplyment(ctx context.Context, in channel.ApplymentIn) (channel.ApplymentResult, error) {
	serial, pubPEM, err := encryptKey(in.Provider)
	if err != nil {
		return channel.ApplymentResult{}, err
	}
	enc := func(s string) (string, error) { return encryptSensitive(pubPEM, s) }

	a := in.Payload
	unexpressed := []string{"rates"}
	body := map[string]any{"business_code": in.BusinessCode}

	contactName, err := enc(a.Contact.Name)
	if err != nil {
		return channel.ApplymentResult{}, err
	}
	contactPhone, _ := enc(a.Contact.Phone)
	contact := map[string]any{
		"contact_type": orDefault(a.Extra["wx_contact_type"], "LEGAL"),
		"contact_name": contactName, "mobile_phone": contactPhone,
	}
	if a.Contact.Email != "" {
		contact["contact_email"], _ = enc(a.Contact.Email)
	}
	if contact["contact_type"] == "SUPER" {
		contact["contact_id_doc_type"] = "IDENTIFICATION_TYPE_IDCARD"
		contact["contact_id_number"], _ = enc(orDefault(a.Extra["wx_contact_id_no"], a.LegalPerson.IDNo))
	}
	body["contact_info"] = contact

	begin, end := splitValid(a.Subject.LicenseValid)
	idBegin, idEnd := splitValid(a.LegalPerson.IDValid)
	idName, _ := enc(a.LegalPerson.Name)
	idNo, _ := enc(a.LegalPerson.IDNo)
	idCard := map[string]any{
		"id_card_copy": in.Media["id_front"], "id_card_national": in.Media["id_back"],
		"id_card_name": idName, "id_card_number": idNo,
		"card_period_begin": begin0(idBegin), "card_period_end": idEnd,
	}
	if addr := a.Extra["wx_id_card_address"]; addr != "" {
		idCard["id_card_address"], _ = enc(addr)
	}
	body["subject_info"] = map[string]any{
		"subject_type": subjectType(a.Subject.Type),
		"business_license_info": map[string]any{
			"license_copy": in.Media["license"], "license_number": a.Subject.LicenseNo,
			"merchant_name": a.Subject.Name, "legal_person": a.LegalPerson.Name,
			"period_begin": begin0(begin), "period_end": end,
		},
		"identity_info": map[string]any{
			"id_holder_type": "LEGAL", "id_doc_type": idDocType(a.LegalPerson.IDType),
			"id_card_info": idCard, "owner": true,
		},
	}

	sales := map[string]any{}
	var scenes []string
	for _, t := range a.Business.Scene.Types {
		switch t {
		case "offline":
			scenes = append(scenes, "SALES_SCENES_STORE")
			store := map[string]any{
				"biz_store_name": a.Subject.ShortName, "biz_address_code": a.Business.Address.Code,
				"biz_store_address": strings.Join([]string{a.Business.Address.Province, a.Business.Address.City,
					a.Business.Address.District, a.Business.Address.Detail}, ""),
			}
			if m := in.Media["storefront"]; m != "" {
				store["store_entrance_pic"] = []string{m}
			}
			if m := in.Media["interior"]; m != "" {
				store["indoor_pic"] = []string{m}
			}
			sales["biz_store_info"] = store
		case "mp":
			scenes = append(scenes, "SALES_SCENES_MP")
			sales["mp_info"] = map[string]any{"mp_appid": a.Business.Scene.AppID}
		case "mini_program":
			scenes = append(scenes, "SALES_SCENES_MINI_PROGRAM")
			sales["mini_program_info"] = map[string]any{"mini_program_appid": a.Business.Scene.AppID}
		case "app":
			scenes = append(scenes, "SALES_SCENES_APP")
			sales["app_info"] = map[string]any{"app_appid": a.Business.Scene.AppID}
		case "web":
			if a.Extra["web_domain"] == "" {
				unexpressed = append(unexpressed, "business.scene.types:web（缺 extra.web_domain）")
				continue
			}
			scenes = append(scenes, "SALES_SCENES_WEB")
			sales["web_info"] = map[string]any{"domain": a.Extra["web_domain"]}
		}
	}
	sales["sales_scenes_type"] = scenes
	body["business_info"] = map[string]any{
		"merchant_shortname": a.Subject.ShortName, "service_phone": a.Business.ServicePhone, "sales_info": sales,
	}
	body["settlement_info"] = map[string]any{
		"settlement_id": a.Subject.MCC, "qualification_type": a.Extra["wx_qualification_type"],
	}

	acctName, _ := enc(a.SettlementAccount.AccountName)
	acctNo, _ := enc(a.SettlementAccount.AccountNo)
	bank := map[string]any{
		"bank_account_type": bankAccountType(a.SettlementAccount.Type),
		"account_name":      acctName, "account_bank": a.SettlementAccount.Bank,
		"bank_address_code": orDefault(a.SettlementAccount.AddressCode, a.Business.Address.Code),
		"account_number":    acctNo,
	}
	if a.SettlementAccount.Branch != "" {
		bank["bank_name"] = a.SettlementAccount.Branch
	}
	body["bank_account_info"] = bank

	raw, _ := json.Marshal(body)
	resp, out, err := doRequest(ctx, http.MethodPost, "/v3/applyment4sub/applyment/", raw, bytes.NewReader(raw),
		"application/json", map[string]string{"Wechatpay-Serial": serial}, in.Provider)
	if err != nil {
		return channel.ApplymentResult{}, err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(out)
		return channel.ApplymentResult{Raw: string(out)}, fmt.Errorf("applyment submit: %s %s", f.ErrCode, f.ErrMsg)
	}
	var res struct {
		ApplymentID json.Number `json:"applyment_id"`
	}
	_ = json.Unmarshal(out, &res)
	sort.Strings(unexpressed)
	return channel.ApplymentResult{ChannelApplymentID: res.ApplymentID.String(), Unexpressed: unexpressed, Raw: string(out)}, nil
}

// QueryApplyment GET /v3/applyment4sub/applyment/business_code/{business_code}。
func (p *partner) QueryApplyment(ctx context.Context, businessCode string, prov map[string]string) (channel.ApplymentView, error) {
	resp, raw, err := doSigned(ctx, http.MethodGet, "/v3/applyment4sub/applyment/business_code/"+businessCode, nil, prov)
	if err != nil {
		return channel.ApplymentView{}, err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return channel.ApplymentView{Raw: string(raw)}, fmt.Errorf("applyment query: %s %s", f.ErrCode, f.ErrMsg)
	}
	return parseApplymentView(raw), nil
}

func parseApplymentView(raw []byte) channel.ApplymentView {
	var d struct {
		ApplymentID json.Number `json:"applyment_id"`
		SubMchID    string      `json:"sub_mchid"`
		SignURL     string      `json:"sign_url"`
		State       string      `json:"applyment_state"`
		StateMsg    string      `json:"applyment_state_msg"`
		AuditDetail []struct {
			Field        string `json:"field"`
			FieldName    string `json:"field_name"`
			RejectReason string `json:"reject_reason"`
		} `json:"audit_detail"`
	}
	_ = json.Unmarshal(raw, &d)
	msg := d.StateMsg
	for _, ad := range d.AuditDetail {
		msg += fmt.Sprintf("；%s(%s)：%s", ad.FieldName, ad.Field, ad.RejectReason)
	}
	return channel.ApplymentView{
		ChannelApplymentID: d.ApplymentID.String(), State: foldApplymentState(d.State), RawState: d.State,
		Message: strings.TrimPrefix(msg, "；"), SignURL: d.SignURL, SubMchID: d.SubMchID, Raw: string(raw),
	}
}

// foldApplymentState 微信申请单状态 → 业务状态。未知状态返回空：不推进。
// EDITTING（编辑中/需修改）按 rejected 处理，资料修正后可沿用 business_code 重提。
func foldApplymentState(s string) string {
	switch s {
	case "APPLYMENT_STATE_AUDITING":
		return channel.ApplymentChannelAuditing
	case "APPLYMENT_STATE_REJECTED", "APPLYMENT_STATE_EDITTING":
		return channel.ApplymentRejected
	case "APPLYMENT_STATE_TO_BE_CONFIRMED":
		return channel.ApplymentNeedVerify
	case "APPLYMENT_STATE_TO_BE_SIGNED", "APPLYMENT_STATE_SIGNING":
		return channel.ApplymentNeedSign
	case "APPLYMENT_STATE_FINISHED":
		return channel.ApplymentFinished
	case "APPLYMENT_STATE_CANCELED":
		return channel.ApplymentCanceled
	}
	return ""
}

func subjectType(t string) string {
	switch t {
	case "enterprise":
		return "SUBJECT_TYPE_ENTERPRISE"
	case "institution":
		return "SUBJECT_TYPE_INSTITUTIONS"
	default:
		return "SUBJECT_TYPE_INDIVIDUAL"
	}
}

func idDocType(t string) string {
	switch t {
	case "passport":
		return "IDENTIFICATION_TYPE_OVERSEA_PASSPORT"
	case "hk_macao":
		return "IDENTIFICATION_TYPE_HONGKONG_PASSPORT"
	case "taiwan":
		return "IDENTIFICATION_TYPE_TAIWAN_PASSPORT"
	case "foreign_residence":
		return "IDENTIFICATION_TYPE_FOREIGN_RESIDENT"
	default:
		return "IDENTIFICATION_TYPE_IDCARD"
	}
}

func bankAccountType(t string) string {
	if t == "personal" {
		return "BANK_ACCOUNT_TYPE_PERSONAL"
	}
	return "BANK_ACCOUNT_TYPE_CORPORATE"
}

// splitValid "2020-01-01,长期" → ("2020-01-01","长期")。
func splitValid(s string) (string, string) {
	i := strings.Index(s, ",")
	if i < 0 {
		return s, "长期"
	}
	return s[:i], s[i+1:]
}

func begin0(s string) string {
	if s == "" {
		return "1970-01-01"
	}
	return s
}

// ── 敏感字段加密 ────────────────────────────────────────────

// encryptKey 选加密公钥：优先微信支付公钥（wx_public_key + wx_public_key_id），
// 其次平台证书（wx_platform_cert + wx_platform_cert_serial）。返回 (序列号, 公钥 PEM)。
func encryptKey(prov map[string]string) (string, string, error) {
	switch {
	case prov["wx_public_key_id"] != "" && prov["wx_public_key"] != "":
		return prov["wx_public_key_id"], prov["wx_public_key"], nil
	case prov["wx_platform_cert_serial"] != "" && prov["wx_platform_cert"] != "":
		return prov["wx_platform_cert_serial"], prov["wx_platform_cert"], nil
	}
	return "", "", fmt.Errorf("敏感字段加密需要 wx_public_key+wx_public_key_id 或 wx_platform_cert+wx_platform_cert_serial（可用「同步平台证书」获取）")
}

// encryptSensitive RSA/ECB/OAEPWithSHA-1AndMGF1Padding，base64 输出（微信 v3 敏感信息加密规范）。
func encryptSensitive(pubPEM, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		return "", fmt.Errorf("encrypt public key pem 无法解析")
	}
	pub, err := parseAnyPublicKey(block)
	if err != nil {
		return "", err
	}
	ct, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, pub, []byte(plain), nil)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// parseAnyPublicKey 接受 PUBLIC KEY（PKIX）或 CERTIFICATE 两种 PEM。
func parseAnyPublicKey(block *pem.Block) (*rsa.PublicKey, error) {
	if block.Type == "CERTIFICATE" {
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("证书公钥不是 RSA")
		}
		return pub, nil
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("公钥不是 RSA")
	}
	return pub, nil
}

// ── 平台证书 ────────────────────────────────────────────────

// PlatformCert 微信平台证书（GET /v3/certificates 用 APIv3 密钥解出）。
type PlatformCert struct {
	Serial       string
	PublicKeyPEM string
	NotAfter     time.Time
}

// FetchPlatformCert 拉取并解密平台证书，返回有效期最晚的一张。
// 证书模式下回调验签与敏感字段加密都依赖它；公钥模式商户也可能同时持有平台证书。
func FetchPlatformCert(ctx context.Context, prov map[string]string) (PlatformCert, error) {
	resp, raw, err := doSigned(ctx, http.MethodGet, "/v3/certificates", nil, prov)
	if err != nil {
		return PlatformCert{}, err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return PlatformCert{}, fmt.Errorf("certificates: %s %s", f.ErrCode, f.ErrMsg)
	}
	var out struct {
		Data []struct {
			EncryptCert struct {
				Nonce      string `json:"nonce"`
				Associated string `json:"associated_data"`
				Ciphertext string `json:"ciphertext"`
			} `json:"encrypt_certificate"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Data) == 0 {
		return PlatformCert{}, fmt.Errorf("certificates: 返回为空")
	}
	var best PlatformCert
	for _, d := range out.Data {
		plain, err := decryptAESGCM(prov["api_v3_key"], d.EncryptCert.Associated, d.EncryptCert.Nonce, d.EncryptCert.Ciphertext)
		if err != nil {
			return PlatformCert{}, fmt.Errorf("解密平台证书失败（api_v3_key 不匹配？）: %w", err)
		}
		block, _ := pem.Decode(plain)
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		der, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
		if err != nil {
			continue
		}
		if cert.NotAfter.After(best.NotAfter) {
			best = PlatformCert{
				Serial:       strings.ToUpper(cert.SerialNumber.Text(16)),
				PublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
				NotAfter:     cert.NotAfter,
			}
		}
	}
	if best.Serial == "" {
		return PlatformCert{}, fmt.Errorf("certificates: 无可解析证书")
	}
	return best, nil
}
