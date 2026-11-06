// wxpay 微信普通服务商（直清，partner v3）适配器：JSAPI 与 Native。
// 请求用商户私钥签名，回调用微信支付公钥验签、APIv3 密钥解密。
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
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"platfarm/svc-pay/channel"
)

func init() { channel.Register(&partner{}) }

type partner struct{}

func (p *partner) IfCode() string   { return "wxpay_partner" }
func (p *partner) FundMode() string { return channel.FundDirect }
func (p *partner) Ways() []string   { return []string{"WX_JSAPI", "WX_NATIVE"} }

// apiBase 生产地址。联调可设 WXPAY_API_BASE 指向沙箱或桩，签名与路径不变。
func apiBase() string {
	if v := os.Getenv("WXPAY_API_BASE"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.mch.weixin.qq.com"
}

// prov 服务商参数键：sp_mchid sp_appid mch_serial_no private_key api_v3_key wx_public_key wx_public_key_id。
// sub 子商户参数键：sub_mchid，JSAPI 另需 sub_appid+sub_openid 或 sp_openid。

type jsapiReq struct {
	SpAppID     string         `json:"sp_appid"`
	SpMchID     string         `json:"sp_mchid"`
	SubMchID    string         `json:"sub_mchid"`
	SubAppID    string         `json:"sub_appid,omitempty"`
	Description string         `json:"description"`
	OutTradeNo  string         `json:"out_trade_no"`
	NotifyURL   string         `json:"notify_url"`
	Amount      map[string]any `json:"amount"`
	Payer       map[string]any `json:"payer,omitempty"`
	SceneInfo   map[string]any `json:"scene_info,omitempty"`
}

func (p *partner) Pay(ctx context.Context, in channel.PayIn) (channel.Result, error) {
	path := "/v3/pay/partner/transactions/"
	req := jsapiReq{
		SpAppID: in.Provider["sp_appid"], SpMchID: in.Provider["sp_mchid"],
		SubMchID: in.Sub["sub_mchid"], Description: in.Subject,
		OutTradeNo: in.PayOrderID, NotifyURL: in.NotifyURL,
		Amount: map[string]any{"total": in.Amount, "currency": orDefault(in.Currency, "CNY")},
	}
	switch in.WayCode {
	case "WX_NATIVE":
		path += "native"
		if in.ClientIP != "" {
			req.SceneInfo = map[string]any{"payer_client_ip": in.ClientIP}
		}
	case "WX_JSAPI":
		path += "jsapi"
		req.SubAppID = in.Sub["sub_appid"]
		switch {
		case in.ChannelExtra["sp_openid"] != "":
			req.Payer = map[string]any{"sp_openid": in.ChannelExtra["sp_openid"]}
		case in.ChannelExtra["sub_openid"] != "":
			req.Payer = map[string]any{"sub_openid": in.ChannelExtra["sub_openid"]}
		default:
			return channel.Result{}, fmt.Errorf("WX_JSAPI 需要 sp_openid 或 sub_openid")
		}
	default:
		return channel.Result{}, fmt.Errorf("wxpay_partner 不支持 %s", in.WayCode)
	}
	body, _ := json.Marshal(req)
	resp, raw, err := doSigned(ctx, http.MethodPost, path, body, in.Provider)
	if err != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		return failFromBody(raw), nil
	}
	var out struct {
		PrepayID string `json:"prepay_id"`
		CodeURL  string `json:"code_url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: "bad response", Raw: string(raw)}, nil
	}
	res := channel.Result{State: channel.StateWaiting, Raw: string(raw)}
	if in.WayCode == "WX_NATIVE" {
		res.Credential = map[string]string{"codeUrl": out.CodeURL}
	} else {
		res.Credential = jsapiPayParams(in.Provider, out.PrepayID)
	}
	return res, nil
}

func (p *partner) Query(ctx context.Context, ref channel.OrderRef) (channel.Result, error) {
	path := fmt.Sprintf("/v3/pay/partner/transactions/out-trade-no/%s?sp_mchid=%s&sub_mchid=%s",
		ref.PayOrderID, ref.Provider["sp_mchid"], ref.Sub["sub_mchid"])
	resp, raw, err := doSigned(ctx, http.MethodGet, path, nil, ref.Provider)
	if err != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		return failFromBody(raw), nil
	}
	return tradeState(raw), nil
}

func (p *partner) Close(ctx context.Context, ref channel.OrderRef) error {
	path := "/v3/pay/partner/transactions/out-trade-no/" + ref.PayOrderID + "/close"
	body, _ := json.Marshal(map[string]string{"sp_mchid": ref.Provider["sp_mchid"], "sub_mchid": ref.Sub["sub_mchid"]})
	resp, raw, err := doSigned(ctx, http.MethodPost, path, body, ref.Provider)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode/100 != 2 {
		return fmt.Errorf("close: %s", string(raw))
	}
	return nil
}

func (p *partner) Refund(ctx context.Context, in channel.RefundIn) (channel.Result, error) {
	body, _ := json.Marshal(map[string]any{
		"sub_mchid": in.Sub["sub_mchid"], "out_trade_no": in.PayOrderID,
		"out_refund_no": in.RefundOrderID,
		"amount":        map[string]any{"refund": in.RefundAmount, "total": in.PayAmount, "currency": "CNY"},
		"reason":        in.Reason, "notify_url": in.NotifyURL,
	})
	resp, raw, err := doSigned(ctx, http.MethodPost, "/v3/refund/domestic/refunds", body, in.Provider)
	if err != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		return failFromBody(raw), nil
	}
	var out struct {
		RefundID string `json:"refund_id"`
		Status   string `json:"status"`
	}
	_ = json.Unmarshal(raw, &out)
	return channel.Result{State: refundState(out.Status), ChannelOrderNo: out.RefundID, Raw: string(raw)}, nil
}

// ParseNotify 验签并解密回调。验签失败返回 error（调用方回 400）；
// 解密成功但交易未终态时 Result.State 为 WAITING。
func (p *partner) ParseNotify(r *http.Request, provider map[string]string) (channel.Notify, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return channel.Notify{}, err
	}
	if err := verifyNotify(r, body, provider); err != nil {
		return channel.Notify{}, err
	}
	var env struct {
		EventType string `json:"event_type"`
		Resource  struct {
			Nonce      string `json:"nonce"`
			Ciphertext string `json:"ciphertext"`
			Associated string `json:"associated_data"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return channel.Notify{}, fmt.Errorf("bad notify body")
	}
	plain, err := decryptAESGCM(provider["api_v3_key"], env.Resource.Associated, env.Resource.Nonce, env.Resource.Ciphertext)
	if err != nil {
		return channel.Notify{}, fmt.Errorf("decrypt notify: %w", err)
	}
	n := channel.Notify{Ack: http.StatusNoContent, OrderType: "pay"}
	if strings.HasPrefix(env.EventType, "REFUND.") {
		n.OrderType = "refund"
		var d struct {
			OutRefundNo string `json:"out_refund_no"`
			RefundID    string `json:"refund_id"`
			Status      string `json:"refund_status"`
		}
		_ = json.Unmarshal(plain, &d)
		n.OrderID = d.OutRefundNo
		n.Result = channel.Result{State: refundState(d.Status), ChannelOrderNo: d.RefundID, Raw: string(plain)}
		return n, nil
	}
	var d struct {
		OutTradeNo    string `json:"out_trade_no"`
		TransactionID string `json:"transaction_id"`
		TradeState    string `json:"trade_state"`
		PayerOpenID   string `json:"-"`
	}
	_ = json.Unmarshal(plain, &d)
	res := tradeState(plain)
	res.Raw = string(plain)
	n.OrderID, n.Result = d.OutTradeNo, res
	return n, nil
}

// ── 签名与请求 ──────────────────────────────────────────────

var httpClient = &http.Client{Timeout: 10 * time.Second}

func doSigned(ctx context.Context, method, path string, body []byte, prov map[string]string) (*http.Response, []byte, error) {
	return doRequest(ctx, method, path, body, bytes.NewReader(body), "application/json", nil, prov)
}

// doRequest 发一个带微信 v3 签名的请求。signBody 是参与签名的正文（multipart 上传时是 meta JSON，
// 与实际发送的 body 不同）；extra 为附加头（如敏感字段加密所需的 Wechatpay-Serial）。
func doRequest(ctx context.Context, method, path string, signBody []byte, body io.Reader, contentType string,
	extra map[string]string, prov map[string]string) (*http.Response, []byte, error) {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce, err := randNonce()
	if err != nil {
		return nil, nil, err
	}
	msg := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n", method, path, ts, nonce, string(signBody))
	sig, err := sign(prov["private_key"], msg)
	if err != nil {
		return nil, nil, err
	}
	auth := fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",signature="%s",timestamp="%s",serial_no="%s"`,
		prov["sp_mchid"], nonce, sig, ts, prov["mch_serial_no"])
	req, err := http.NewRequestWithContext(ctx, method, apiBase()+path, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "svc-pay/0.2")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, raw, nil
}

func sign(privateKeyPEM, msg string) (string, error) {
	key, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("private key pem 无法解析")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("不是 RSA 私钥")
		}
		return rk, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// jsapiPayParams 生成前端调起支付的签名参数。
func jsapiPayParams(prov map[string]string, prepayID string) map[string]string {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce, _ := randNonce()
	pkg := "prepay_id=" + prepayID
	appid := prov["sp_appid"]
	msg := fmt.Sprintf("%s\n%s\n%s\n%s\n", appid, ts, nonce, pkg)
	sig, _ := sign(prov["private_key"], msg)
	return map[string]string{
		"appId": appid, "timeStamp": ts, "nonceStr": nonce,
		"package": pkg, "signType": "RSA", "paySign": sig, "prepayId": prepayID,
	}
}

func randNonce() (string, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", n), nil
}

// ── 回调验签与解密 ──────────────────────────────────────────

func verifyNotify(r *http.Request, body []byte, prov map[string]string) error {
	ts := r.Header.Get("Wechatpay-Timestamp")
	nonce := r.Header.Get("Wechatpay-Nonce")
	sig := r.Header.Get("Wechatpay-Signature")
	serial := r.Header.Get("Wechatpay-Serial")
	if ts == "" || nonce == "" || sig == "" {
		return fmt.Errorf("missing wechatpay signature headers")
	}
	when, err := parseUnix(ts)
	if err != nil || time.Since(when) > 5*time.Minute || time.Until(when) > time.Minute {
		return fmt.Errorf("wechatpay timestamp out of window")
	}
	pubPEM, err := verifyKeyFor(prov, serial)
	if err != nil {
		return err
	}
	return verifySignature(pubPEM, ts, nonce, body, sig)
}

// verifyKeyFor 按 Wechatpay-Serial 选验签公钥。微信同时存在两种签名体系：
//   - 微信支付公钥：serial 形如 PUB_KEY_ID_*，公钥配在 wx_public_key（可选 wx_public_key_id）
//   - 平台证书：serial 为证书序列号，公钥配在 wx_platform_cert + wx_platform_cert_serial
//     （GET /v3/certificates 用 APIv3 密钥解出，见 live_test）
//
// 同一商户可能收到任一种签名的回调，因此两把钥匙都可配置，由 serial 决定用哪把。
func verifyKeyFor(prov map[string]string, serial string) (string, error) {
	switch {
	case serial != "" && serial == prov["wx_public_key_id"]:
		return prov["wx_public_key"], nil
	case serial != "" && serial == prov["wx_platform_cert_serial"]:
		return prov["wx_platform_cert"], nil
	case strings.HasPrefix(serial, "PUB_KEY_ID_"):
		if prov["wx_public_key_id"] != "" {
			return "", fmt.Errorf("unexpected wechatpay serial %s", serial)
		}
		if prov["wx_public_key"] == "" {
			return "", fmt.Errorf("wx_public_key 未配置，无法验证 %s", serial)
		}
		return prov["wx_public_key"], nil
	case prov["wx_platform_cert"] != "":
		if prov["wx_platform_cert_serial"] != "" && serial != "" {
			return "", fmt.Errorf("unexpected wechatpay serial %s", serial)
		}
		return prov["wx_platform_cert"], nil
	case prov["wx_public_key"] != "":
		return prov["wx_public_key"], nil
	}
	return "", fmt.Errorf("no wechatpay verify key configured")
}

// verifySignature 微信支付公钥验签：SHA256withRSA over "ts\nnonce\nbody\n"。
// 回调与 API 响应共用（响应头 Wechatpay-Timestamp/Nonce/Signature 同一规则）。
func verifySignature(pubPEM, ts, nonce string, body []byte, sig string) error {
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		return fmt.Errorf("wx public key pem 无法解析")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("wx public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("wx public key 不是 RSA")
	}
	mac := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%s\n", ts, nonce, body)))
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("signature base64: %w", err)
	}
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, mac[:], raw); err != nil {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// verifyResponse 校验 API 响应签名，钥匙选择规则与回调一致（verifyKeyFor）。
func verifyResponse(resp *http.Response, body []byte, prov map[string]string) error {
	pubPEM, err := verifyKeyFor(prov, resp.Header.Get("Wechatpay-Serial"))
	if err != nil {
		return err
	}
	return verifySignature(pubPEM, resp.Header.Get("Wechatpay-Timestamp"),
		resp.Header.Get("Wechatpay-Nonce"), body, resp.Header.Get("Wechatpay-Signature"))
}

func decryptAESGCM(apiV3Key, aad, nonce, ciphertext string) ([]byte, error) {
	if len(apiV3Key) != 32 {
		return nil, fmt.Errorf("api_v3_key 必须为 32 字节")
	}
	ct, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(apiV3Key))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, []byte(nonce), ct, []byte(aad))
}

// ── 状态折叠 ────────────────────────────────────────────────

func tradeState(raw []byte) channel.Result {
	var d struct {
		TradeState    string `json:"trade_state"`
		TradeStateDesc string `json:"trade_state_desc"`
		TransactionID string `json:"transaction_id"`
		Payer         struct {
			SpOpenID  string `json:"sp_openid"`
			SubOpenID string `json:"sub_openid"`
		} `json:"payer"`
	}
	_ = json.Unmarshal(raw, &d)
	res := channel.Result{ChannelOrderNo: d.TransactionID, Raw: string(raw)}
	if d.Payer.SpOpenID != "" {
		res.ChannelUser = d.Payer.SpOpenID
	} else {
		res.ChannelUser = d.Payer.SubOpenID
	}
	switch d.TradeState {
	case "SUCCESS":
		res.State = channel.StateSuccess
	case "CLOSED", "REVOKED", "PAYERROR":
		res.State = channel.StateFail
		res.ErrCode, res.ErrMsg = d.TradeState, d.TradeStateDesc
	default: // NOTPAY USERPAYING ACCEPT
		res.State = channel.StateWaiting
	}
	return res
}

func refundState(status string) string {
	switch status {
	case "SUCCESS":
		return channel.StateSuccess
	case "CLOSED", "ABNORMAL":
		return channel.StateFail
	default: // PROCESSING
		return channel.StateWaiting
	}
}

func failFromBody(raw []byte) channel.Result {
	var d struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &d)
	return channel.Result{State: channel.StateFail, ErrCode: d.Code, ErrMsg: d.Message, Raw: string(raw)}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func parseUnix(s string) (time.Time, error) {
	var sec int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return time.Time{}, fmt.Errorf("bad timestamp")
		}
		sec = sec*10 + int64(c-'0')
	}
	return time.Unix(sec, 0), nil
}
