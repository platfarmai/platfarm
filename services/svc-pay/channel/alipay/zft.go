// alipay_zft 支付宝直付通（归集模式）。
// 网关 RSA2：请求参数按 key 排序后 SHA256withRSA，响应与回调用支付宝公钥验签。
// 资金流：支付 → 待结算（可退款，不可分账）→ settle.confirm 结算到二级商户进件时的默认结算账户
// （需要分账时带 royalty_freeze 冻结）→ order.settle 分账，最后一笔带 royalty_finish 解冻剩余。
// 平台侧没有二级商户余额，没有“提现”：不实现 WithdrawChannel。
package alipay

import (
	"context"
	"crypto"
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
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"platfarm/svc-pay/channel"
)

// cst 支付宝网关时间（timestamp、time_expire）一律东八区。
var cst = time.FixedZone("CST", 8*3600)

func init() { channel.Register(&zft{}) }

type zft struct{}

func (z *zft) IfCode() string   { return "alipay_zft" }
func (z *zft) FundMode() string { return channel.FundControlled }
func (z *zft) Ways() []string {
	return []string{"ALI_APP", "ALI_WAP", "ALI_PAGE", "ALI_QR", "ALI_JSAPI"}
}

var (
	_ channel.PayChannel        = (*zft)(nil)
	_ channel.OnboardingChannel = (*zft)(nil)
	_ channel.SharingChannel    = (*zft)(nil)
	_ channel.BillChannel       = (*zft)(nil)
	_ channel.SettleChannel     = (*zft)(nil)
)

// gateway 生产网关。联调可设 ALIPAY_GATEWAY 指向沙箱或桩。
func gateway() string {
	if v := os.Getenv("ALIPAY_GATEWAY"); v != "" {
		return v
	}
	return "https://openapi.alipay.com/gateway.do"
}

// prov 服务商参数键：app_id private_key alipay_public_key [app_cert_sn] [alipay_root_cert_sn]。
// sub 子商户参数键：smid（直付通二级商户号），分账另需 app_auth_token（可选）。

var httpClient = &http.Client{Timeout: 10 * time.Second}

// ── 网关 ────────────────────────────────────────────────────

// call 调一个 alipay 接口，返回业务响应 JSON。验签失败或网关错误返回 error；
// 业务失败（code != 10000）也返回 error，调用方据此折叠 FAIL。
func call(ctx context.Context, method string, biz map[string]any, prov, sub map[string]string, notifyURL string) (json.RawMessage, error) {
	bizJSON, err := json.Marshal(biz)
	if err != nil {
		return nil, err
	}
	params := map[string]string{
		"app_id": prov["app_id"], "method": method, "format": "JSON", "charset": "utf-8",
		"sign_type": "RSA2", "timestamp": time.Now().In(cst).Format("2006-01-02 15:04:05"), "version": "1.0",
		"biz_content": string(bizJSON),
	}
	if notifyURL != "" {
		params["notify_url"] = notifyURL
	}
	if tok := sub["app_auth_token"]; tok != "" {
		params["app_auth_token"] = tok
	}
	if sn := prov["app_cert_sn"]; sn != "" {
		params["app_cert_sn"] = sn
	}
	if sn := prov["alipay_root_cert_sn"]; sn != "" {
		params["alipay_root_cert_sn"] = sn
	}
	sig, err := signParams(prov["private_key"], params)
	if err != nil {
		return nil, err
	}
	params["sign"] = sig

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway(), strings.NewReader(encodeForm(params)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("alipay http %d: %s", resp.StatusCode, trunc(string(raw), 300))
	}
	if err := verifyResponse(raw, method, prov["alipay_public_key"]); err != nil {
		return nil, err
	}
	node, err := responseNode(raw, method)
	if err != nil {
		return nil, err
	}
	var head struct {
		Code    string `json:"code"`
		Msg     string `json:"msg"`
		SubCode string `json:"sub_code"`
		SubMsg  string `json:"sub_msg"`
	}
	_ = json.Unmarshal(node, &head)
	if head.Code != "" && head.Code != "10000" {
		return node, fmt.Errorf("%s %s: %s %s", head.Code, head.Msg, head.SubCode, head.SubMsg)
	}
	return node, nil
}

// pagePay 生成一个已签名的收银台表单参数（APP/WAP/PAGE 由调用方拼成跳转）。
func pagePay(method string, biz map[string]any, prov, sub map[string]string, notifyURL, returnURL string) (string, error) {
	bizJSON, _ := json.Marshal(biz)
	params := map[string]string{
		"app_id": prov["app_id"], "method": method, "format": "JSON", "charset": "utf-8",
		"sign_type": "RSA2", "timestamp": time.Now().In(cst).Format("2006-01-02 15:04:05"), "version": "1.0",
		"biz_content": string(bizJSON), "notify_url": notifyURL,
	}
	if returnURL != "" {
		params["return_url"] = returnURL
	}
	if tok := sub["app_auth_token"]; tok != "" {
		params["app_auth_token"] = tok
	}
	sig, err := signParams(prov["private_key"], params)
	if err != nil {
		return "", err
	}
	params["sign"] = sig
	return gateway() + "?" + encodeForm(params), nil
}

// encodeForm 按 RFC 3986 编码（空格为 %20）。url.Values.Encode 把空格编成 +，
// 支付宝按解码后的原文验签，+ 会被当成空格，时间戳因此对不上。
func encodeForm(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(params[k]))
	}
	return strings.ReplaceAll(strings.Join(parts, "&"), "+", "%20")
}

func signParams(privateKeyPEM string, params map[string]string) (string, error) {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return sign(privateKeyPEM, strings.Join(parts, "&"))
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
		return nil, fmt.Errorf("alipay private key pem 无法解析")
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

// verifyResponse 校验网关响应签名。桩或沙箱未配公钥时跳过（生产必须配置 alipay_public_key）。
func verifyResponse(raw []byte, method, pubPEM string) error {
	if pubPEM == "" {
		return fmt.Errorf("alipay_public_key 未配置")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return fmt.Errorf("bad alipay response")
	}
	sigRaw, ok := envelope["sign"]
	if !ok {
		return fmt.Errorf("alipay response has no sign")
	}
	var sig string
	if json.Unmarshal(sigRaw, &sig) != nil || sig == "" {
		return fmt.Errorf("alipay response sign empty")
	}
	node, err := responseNode(raw, method)
	if err != nil {
		return err
	}
	return verify(pubPEM, string(node), sig)
}

func verify(pubPEM, content, sig string) error {
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		return fmt.Errorf("alipay public key pem 无法解析")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("alipay public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("alipay public key 不是 RSA")
	}
	mac := sha256.Sum256([]byte(content))
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("sign base64: %w", err)
	}
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, mac[:], raw); err != nil {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// responseNode 取 "<method 去点改下划线>_response" 节点的原始 JSON。
func responseNode(raw []byte, method string) (json.RawMessage, error) {
	key := strings.ReplaceAll(method, ".", "_") + "_response"
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, fmt.Errorf("bad alipay response")
	}
	node, ok := envelope[key]
	if !ok {
		return nil, fmt.Errorf("alipay response missing %s", key)
	}
	return node, nil
}

func yuan(fen int64) string { return strconv.FormatFloat(float64(fen)/100, 'f', 2, 64) }

// fenOf "12.34" → 1234，按十进制精确换算（不走浮点，负数同样正确）；非法返回 0。
func fenOf(yuanStr string) int64 {
	s := strings.TrimSpace(yuanStr)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 2 {
		frac = frac[:2]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil || f < 0 {
		return 0
	}
	if neg {
		return -(w*100 + f)
	}
	return w*100 + f
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ── 支付 ────────────────────────────────────────────────────

func (z *zft) Pay(ctx context.Context, in channel.PayIn) (channel.Result, error) {
	biz := map[string]any{
		"out_trade_no": in.PayOrderID, "total_amount": yuan(in.Amount),
		"subject": orDefault(in.Subject, "支付"), "product_code": productCode(in.WayCode),
		"sub_merchant": map[string]string{"merchant_id": in.Sub["smid"]},
		"settle_info":  map[string]any{"settle_detail_infos": []map[string]any{{"trans_in_type": "defaultSettle", "amount": yuan(in.Amount)}}},
	}
	if in.Body != "" {
		biz["body"] = in.Body
	}
	if !in.ExpireAt.IsZero() {
		// 过期后支付宝拒绝支付：本地超时关单之后不会再冒出一笔成功交易。
		biz["time_expire"] = in.ExpireAt.In(cst).Format("2006-01-02 15:04:05")
	}
	// 分账冻结在确认结算时按需声明（royalty_freeze），下单时不冻结：不分账的订单结算后直接可用。
	if id := in.ChannelExtra["sys_service_provider_id"]; id != "" {
		biz["extend_params"] = map[string]string{"sys_service_provider_id": id}
	}

	switch in.WayCode {
	case "ALI_QR":
		node, err := call(ctx, "alipay.trade.precreate", biz, in.Provider, in.Sub, in.NotifyURL)
		if err != nil {
			return resultOf(err, node, payAmbiguousCodes...), nil
		}
		var out struct {
			QRCode string `json:"qr_code"`
		}
		_ = json.Unmarshal(node, &out)
		return channel.Result{State: channel.StateWaiting, Credential: map[string]string{"qrCode": out.QRCode}, Raw: string(node)}, nil
	case "ALI_JSAPI":
		if in.ChannelExtra["buyer_id"] == "" && in.ChannelExtra["buyer_open_id"] == "" {
			return channel.Result{}, fmt.Errorf("ALI_JSAPI 需要 buyer_id 或 buyer_open_id")
		}
		if in.ChannelExtra["buyer_id"] != "" {
			biz["buyer_id"] = in.ChannelExtra["buyer_id"]
		} else {
			biz["buyer_open_id"] = in.ChannelExtra["buyer_open_id"]
		}
		node, err := call(ctx, "alipay.trade.create", biz, in.Provider, in.Sub, in.NotifyURL)
		if err != nil {
			return resultOf(err, node, payAmbiguousCodes...), nil
		}
		var out struct {
			TradeNo string `json:"trade_no"`
		}
		_ = json.Unmarshal(node, &out)
		return channel.Result{State: channel.StateWaiting, ChannelOrderNo: out.TradeNo,
			Credential: map[string]string{"tradeNo": out.TradeNo}, Raw: string(node)}, nil
	default:
		method := "alipay.trade.app.pay"
		switch in.WayCode {
		case "ALI_WAP":
			method = "alipay.trade.wap.pay"
		case "ALI_PAGE":
			method = "alipay.trade.page.pay"
		}
		link, err := pagePay(method, biz, in.Provider, in.Sub, in.NotifyURL, in.ChannelExtra["return_url"])
		if err != nil {
			return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
		}
		return channel.Result{State: channel.StateWaiting, Credential: map[string]string{"payUrl": link}}, nil
	}
}

func productCode(way string) string {
	switch way {
	case "ALI_APP":
		return "QUICK_MSECURITY_PAY"
	case "ALI_WAP":
		return "QUICK_WAP_WAY"
	case "ALI_PAGE":
		return "FAST_INSTANT_TRADE_PAY"
	case "ALI_JSAPI":
		return "JSAPI_PAY"
	default:
		return "FACE_TO_FACE_PAYMENT"
	}
}

// payAmbiguousCodes 下单时这些子码说明同号交易已在支付宝侧存在且可能已支付，不能据此判失败。
var payAmbiguousCodes = []string{"ACQ.TRADE_HAS_SUCCESS"}

// Query 查询失败一律是 UNKNOWN：ACQ.TRADE_NOT_EXIST 常见于用户还没扫码/进收银台（交易尚未创建），
// 不是“支付失败”。
func (z *zft) Query(ctx context.Context, ref channel.OrderRef) (channel.Result, error) {
	biz := map[string]any{"out_trade_no": ref.PayOrderID}
	if ref.ChannelOrderNo != "" {
		biz["trade_no"] = ref.ChannelOrderNo
	}
	node, err := call(ctx, "alipay.trade.query", biz, ref.Provider, ref.Sub, "")
	if err != nil {
		res := resultOf(err, node)
		res.State = channel.StateUnknown
		return res, nil
	}
	return tradeState(node), nil
}

// Close 交易尚未创建（ACQ.TRADE_NOT_EXIST）时没有可关的东西，视为关闭成功；
// 下单时已传 time_expire，过期后也无法再付。
func (z *zft) Close(ctx context.Context, ref channel.OrderRef) error {
	node, err := call(ctx, "alipay.trade.close", map[string]any{"out_trade_no": ref.PayOrderID}, ref.Provider, ref.Sub, "")
	if err != nil {
		if _, sub, _ := headOf(node); sub == "ACQ.TRADE_NOT_EXIST" {
			return nil
		}
	}
	return err
}

func (z *zft) Refund(ctx context.Context, in channel.RefundIn) (channel.Result, error) {
	biz := map[string]any{
		"out_trade_no": in.PayOrderID, "out_request_no": in.RefundOrderID,
		"refund_amount": yuan(in.RefundAmount), "refund_reason": in.Reason,
	}
	node, err := call(ctx, "alipay.trade.refund", biz, in.Provider, in.Sub, "")
	if err != nil {
		return resultOf(err, node), nil
	}
	var out struct {
		TradeNo string `json:"trade_no"`
		Fund    string `json:"fund_change"`
	}
	_ = json.Unmarshal(node, &out)
	state := channel.StateWaiting
	if out.Fund == "Y" {
		state = channel.StateSuccess
	}
	return channel.Result{State: state, ChannelOrderNo: out.TradeNo, Raw: string(node)}, nil
}

var _ channel.RefundQuerier = (*zft)(nil)

// QueryRefund alipay.trade.fastpay.refund.query：只有 refund_status=REFUND_SUCCESS 是成功；
// 没有该字段表示退款尚未成功（或未受理），按处理中对待，不据此判失败。
func (z *zft) QueryRefund(ctx context.Context, ref channel.RefundRef) (channel.Result, error) {
	node, err := call(ctx, "alipay.trade.fastpay.refund.query", map[string]any{
		"out_trade_no": ref.PayOrderID, "out_request_no": ref.RefundOrderID,
	}, ref.Provider, ref.Sub, "")
	if err != nil {
		res := resultOf(err, node)
		res.State = channel.StateUnknown
		return res, nil
	}
	var out struct {
		Status  string `json:"refund_status"`
		Amount  string `json:"refund_amount"`
		TradeNo string `json:"trade_no"`
	}
	_ = json.Unmarshal(node, &out)
	res := channel.Result{State: channel.StateWaiting, ChannelOrderNo: out.TradeNo, Raw: string(node)}
	if out.Status == "REFUND_SUCCESS" {
		res.State, res.Amount = channel.StateSuccess, fenOf(out.Amount)
	}
	return res, nil
}

// ParseNotify 验签 form 回调。trade_status TRADE_SUCCESS/FINISHED → SUCCESS。
func (z *zft) ParseNotify(r *http.Request, provider map[string]string) (channel.Notify, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return channel.Notify{}, err
	}
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return channel.Notify{}, fmt.Errorf("bad notify body")
	}
	params := map[string]string{}
	for k := range vals {
		params[k] = vals.Get(k)
	}
	sig := params["sign"]
	delete(params, "sign")
	delete(params, "sign_type")
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	if err := verify(provider["alipay_public_key"], strings.Join(parts, "&"), sig); err != nil {
		return channel.Notify{}, err
	}
	n := channel.Notify{Ack: http.StatusOK, AckBody: "success", OrderType: "pay", OrderID: params["out_trade_no"]}
	res := channel.Result{ChannelOrderNo: params["trade_no"], ChannelUser: params["buyer_id"], Raw: string(body)}
	// 退款引起的交易状态通知：部分退款 trade_status 仍是 TRADE_SUCCESS，全额退款变成 TRADE_CLOSED；
	// 两者都带 gmt_refund 与 out_biz_no（退款请求号 = 我们的退款单号），含义都是“这笔退款已成功”。
	if params["gmt_refund"] != "" {
		n.OrderType, n.OrderID = "refund", params["out_biz_no"]
		res.State = channel.StateSuccess
		n.Result = res
		return n, nil
	}
	res.Amount = fenOf(params["total_amount"])
	switch params["trade_status"] {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		res.State = channel.StateSuccess
	case "TRADE_CLOSED":
		res.State = channel.StateFail
		res.ErrCode = "TRADE_CLOSED"
	default:
		res.State = channel.StateWaiting
	}
	n.Result = res
	return n, nil
}

func tradeState(node json.RawMessage) channel.Result {
	var d struct {
		TradeStatus string `json:"trade_status"`
		TradeNo     string `json:"trade_no"`
		BuyerID     string `json:"buyer_user_id"`
		TotalAmount string `json:"total_amount"`
	}
	_ = json.Unmarshal(node, &d)
	res := channel.Result{ChannelOrderNo: d.TradeNo, ChannelUser: d.BuyerID, Amount: fenOf(d.TotalAmount), Raw: string(node)}
	switch d.TradeStatus {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		res.State = channel.StateSuccess
	case "TRADE_CLOSED":
		res.State = channel.StateFail
		res.ErrCode = d.TradeStatus
	default:
		res.State = channel.StateWaiting
	}
	return res
}

// headOf 业务响应头 code / sub_code / sub_msg。
func headOf(node json.RawMessage) (code, subCode, subMsg string) {
	var h struct {
		Code    string `json:"code"`
		SubCode string `json:"sub_code"`
		SubMsg  string `json:"sub_msg"`
	}
	if node != nil {
		_ = json.Unmarshal(node, &h)
	}
	return h.Code, h.SubCode, h.SubMsg
}

// ambiguous 结果不确定的网关错误：20000 服务不可用、isp.* / aop.* 服务端异常、各类 SYSTEM_ERROR。
// 支付宝要求用原请求号重试或先查询，不能当成业务失败。
func ambiguous(code, subCode string) bool {
	return code == "20000" || strings.HasPrefix(subCode, "isp.") || strings.HasPrefix(subCode, "aop.") ||
		strings.Contains(subCode, "SYSTEM_ERROR")
}

// resultOf 把 call 的错误折叠成四态：拿不到验签通过的业务响应（网络、验签、网关 HTTP 错误）或模糊错误码是 UNKNOWN；
// extra 是该接口特有的“同号请求可能已生效”子码，也是 UNKNOWN；其余明确的业务失败是 FAIL。
func resultOf(err error, node json.RawMessage, extra ...string) channel.Result {
	res := channel.Result{State: channel.StateUnknown, ErrMsg: err.Error(), Raw: string(node)}
	if node == nil {
		return res
	}
	code, sub, msg := headOf(node)
	res.ErrCode = orDefault(sub, code)
	if msg != "" {
		res.ErrMsg = msg
	}
	if code != "" && !ambiguous(code, sub) && !slices.Contains(extra, sub) {
		res.State = channel.StateFail
	}
	return res
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ── 进件 ────────────────────────────────────────────────────

// UploadMedia 直付通图片走 ant.merchant.expand.indirect.image.upload，返回 image_id。
func (z *zft) UploadMedia(ctx context.Context, filename string, data []byte, prov map[string]string) (string, error) {
	node, err := call(ctx, "ant.merchant.expand.indirect.image.upload", map[string]any{
		"image_type":    strings.TrimPrefix(strings.ToLower(filename[strings.LastIndex(filename, "."):]), "."),
		"image_content": base64.StdEncoding.EncodeToString(data),
	}, prov, nil, "")
	if err != nil {
		return "", err
	}
	var out struct {
		ImageID string `json:"image_id"`
	}
	if json.Unmarshal(node, &out) != nil || out.ImageID == "" {
		return "", fmt.Errorf("image upload: bad response")
	}
	return out.ImageID, nil
}

// SubmitApplyment ant.merchant.expand.indirect.zft.create。
// 签约 / 结算支付宝账号必须显式给出（extra.alipay_logon_id）：联系人手机号不一定是二级商户本人的支付宝账号，
// 用它兜底可能把签约和结算绑到别人的账户上。
func (z *zft) SubmitApplyment(ctx context.Context, in channel.ApplymentIn) (channel.ApplymentResult, error) {
	a := in.Payload
	logonID := a.Extra["alipay_logon_id"]
	if logonID == "" {
		return channel.ApplymentResult{}, fmt.Errorf("直付通进件需要 extra.alipay_logon_id（二级商户签约与结算用的支付宝账号）")
	}
	biz := map[string]any{
		"external_id": in.BusinessCode, "name": a.Subject.Name, "alias_name": a.Subject.ShortName,
		"merchant_type": zftMerchantType(a.Subject.Type), "mcc": a.Subject.MCC,
		"cert_no": a.Subject.LicenseNo, "cert_type": "201",
		"legal_name": a.LegalPerson.Name, "legal_cert_no": a.LegalPerson.IDNo,
		"service_phone": a.Business.ServicePhone,
		"contact_infos": []map[string]string{{"name": a.Contact.Name, "mobile": a.Contact.Phone, "email": a.Contact.Email}},
		"biz_cards": []map[string]string{{
			"account_holder_name": a.SettlementAccount.AccountName, "account_no": a.SettlementAccount.AccountNo,
			"account_inst_name": a.SettlementAccount.Bank, "account_branch_name": a.SettlementAccount.Branch,
			"usage_type": "01", "account_type": zftAccountType(a.SettlementAccount.Type),
		}},
		"business_address": map[string]string{
			"province_code": a.Business.Address.Province, "city_code": a.Business.Address.City,
			"district_code": a.Business.Address.District, "address": a.Business.Address.Detail,
		},
		"service":                 zftServices(a.Business.Scene.Types),
		"alipay_logon_id":         logonID,
		"binding_alipay_logon_id": logonID,
	}
	if in.Media["license"] != "" {
		biz["cert_image"] = in.Media["license"]
	}
	if in.Media["id_front"] != "" {
		biz["legal_cert_front_image"] = in.Media["id_front"]
	}
	if in.Media["id_back"] != "" {
		biz["legal_cert_back_image"] = in.Media["id_back"]
	}
	node, err := call(ctx, "ant.merchant.expand.indirect.zft.create", biz, in.Provider, nil, "")
	if err != nil {
		return channel.ApplymentResult{}, err
	}
	var out struct {
		OrderID string `json:"order_id"`
	}
	_ = json.Unmarshal(node, &out)
	return channel.ApplymentResult{ChannelApplymentID: out.OrderID, Raw: string(node)}, nil
}

// QueryApplyment ant.merchant.expand.indirect.zftorder.query。
func (z *zft) QueryApplyment(ctx context.Context, businessCode string, prov map[string]string) (channel.ApplymentView, error) {
	node, err := call(ctx, "ant.merchant.expand.indirect.zftorder.query", map[string]any{"external_id": businessCode}, prov, nil, "")
	if err != nil {
		return channel.ApplymentView{}, err
	}
	var out struct {
		Orders []struct {
			OrderID    string `json:"order_id"`
			Status     string `json:"status"`
			SMID       string `json:"smid"`
			Reason     string `json:"reason"`
			SubConfirm string `json:"sub_confirm"`
		} `json:"orders"`
	}
	_ = json.Unmarshal(node, &out)
	if len(out.Orders) == 0 {
		return channel.ApplymentView{State: channel.ApplymentChannelAuditing, Raw: string(node)}, nil
	}
	o := out.Orders[0]
	view := channel.ApplymentView{ChannelApplymentID: o.OrderID, RawState: o.Status, Message: o.Reason, SubMchID: o.SMID, Raw: string(node)}
	switch o.Status {
	case "99":
		view.State = channel.ApplymentFinished
	case "-1":
		view.State = channel.ApplymentRejected
	case "031":
		view.State = channel.ApplymentNeedSign
	default:
		view.State = channel.ApplymentChannelAuditing
	}
	return view, nil
}

func zftMerchantType(t string) string {
	switch t {
	case "enterprise":
		return "01"
	case "institution":
		return "07"
	default:
		return "06"
	}
}

func zftAccountType(t string) string {
	if t == "personal" {
		return "DC"
	}
	return "CC"
}

func zftServices(types []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range types {
		name := "当面付"
		switch t {
		case "app":
			name = "app支付"
		case "mini_program", "mp", "web":
			name = "jsapi支付"
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		out = []string{"当面付"}
	}
	return out
}

// ── 分账（royalty）──────────────────────────────────────────

// AddReceiver alipay.trade.royalty.relation.bind。
func (z *zft) AddReceiver(ctx context.Context, in channel.ReceiverIn) error {
	_, err := call(ctx, "alipay.trade.royalty.relation.bind", map[string]any{
		"receiver_list": []map[string]string{{
			"type": in.Type, "account": in.Account, "name": in.Name, "memo": in.RelationType,
		}},
		"out_request_no": nonce(),
	}, in.Provider, in.Sub, "")
	return err
}

// Share alipay.trade.order.settle（分账结算）。直付通必须先确认结算（结算时冻结）；
// UnfreezeUnsplit 带 royalty_finish：本次分账后把剩余冻结资金解冻给二级商户。
func (z *zft) Share(ctx context.Context, in channel.DivisionIn) (channel.DivisionResult, error) {
	params := make([]map[string]any, 0, len(in.Receivers))
	for _, r := range in.Receivers {
		params = append(params, map[string]any{
			"trans_in_type": orDefault(r.Type, "loginName"), "trans_in": r.Account,
			"amount": yuan(r.Amount), "desc": orDefault(r.Description, "分账"),
		})
	}
	biz := map[string]any{
		"out_request_no": in.DivisionNo, "trade_no": in.ChannelOrderNo,
		"royalty_parameters": params,
	}
	if in.UnfreezeUnsplit {
		biz["extend_params"] = map[string]string{"royalty_finish": "true"}
	}
	node, err := call(ctx, "alipay.trade.order.settle", biz, in.Provider, in.Sub, "")
	if err != nil {
		// ACQ.SYSTEM_ERROR 等必须原参数、原请求号重试：UNKNOWN 让分账单留在 pending 由 worker 重提。
		r := resultOf(err, node)
		return channel.DivisionResult{State: r.State, ErrCode: r.ErrCode, ErrMsg: r.ErrMsg, Raw: r.Raw}, nil
	}
	var out struct {
		TradeNo string `json:"trade_no"`
		Settle  string `json:"settle_no"`
	}
	_ = json.Unmarshal(node, &out)
	return channel.DivisionResult{State: channel.StateWaiting, ChannelOrderNo: orDefault(out.Settle, out.TradeNo), Raw: string(node)}, nil
}

// QueryShare alipay.trade.order.settle.query。
func (z *zft) QueryShare(ctx context.Context, ref channel.DivisionRef) (channel.DivisionResult, error) {
	node, err := call(ctx, "alipay.trade.order.settle.query", map[string]any{
		"out_request_no": ref.DivisionNo, "trade_no": ref.ChannelOrderNo,
	}, ref.Provider, ref.Sub, "")
	if err != nil {
		return channel.DivisionResult{State: channel.StateUnknown, ErrMsg: err.Error(), Raw: string(node)}, nil
	}
	var out struct {
		RoyaltyDetailList []struct {
			State     string `json:"state"`
			ErrorDesc string `json:"error_desc"`
			TransIn   string `json:"trans_in"`
			Amount    string `json:"amount"`
		} `json:"royalty_detail_list"`
	}
	_ = json.Unmarshal(node, &out)
	res := channel.DivisionResult{Raw: string(node), State: channel.StateWaiting}
	ok, fail := 0, 0
	for _, d := range out.RoyaltyDetailList {
		res.Receivers = append(res.Receivers, channel.DivisionReceiver{
			Account: d.TransIn, Amount: fenOf(d.Amount), Result: d.State, Detail: d.ErrorDesc,
		})
		switch d.State {
		case "SUCCESS":
			ok++
		case "FAIL":
			fail++
		}
	}
	switch {
	case len(out.RoyaltyDetailList) > 0 && ok == len(out.RoyaltyDetailList):
		res.State = channel.StateSuccess
	case fail > 0 && ok+fail == len(out.RoyaltyDetailList):
		res.State = channel.StateFail
		res.ErrCode = "ROYALTY_FAIL"
	}
	return res, nil
}

// FinishShare 单独发起分账完结（不带分账条款，只带 royalty_finish）：解冻结算时冻结的剩余资金。
// ACQ.ALREADY_FINISH_ALLOC 表示已无冻结资金可解冻，目的已达成。
func (z *zft) FinishShare(ctx context.Context, ref channel.DivisionRef) (channel.DivisionResult, error) {
	node, err := call(ctx, "alipay.trade.order.settle", map[string]any{
		"out_request_no": ref.DivisionNo, "trade_no": ref.ChannelOrderNo,
		"extend_params": map[string]string{"royalty_finish": "true"},
	}, ref.Provider, ref.Sub, "")
	if err != nil {
		r := resultOf(err, node)
		if r.ErrCode == "ACQ.ALREADY_FINISH_ALLOC" {
			return channel.DivisionResult{State: channel.StateSuccess, Raw: r.Raw}, nil
		}
		return channel.DivisionResult{State: r.State, ErrCode: r.ErrCode, ErrMsg: r.ErrMsg, Raw: r.Raw}, nil
	}
	return channel.DivisionResult{State: channel.StateSuccess, Raw: string(node)}, nil
}

// ── 确认结算 ────────────────────────────────────────────────

// settleAmbiguousCodes 文档要求“请求信息保持一致”重试的结算错误：结果未知。
var settleAmbiguousCodes = []string{"ACQ.TRADE_SETTLE_ERROR"}

// SettleConfirm alipay.trade.settle.confirm：把待结算资金结算到二级商户进件时的默认结算账户。
// amount 必须等于交易金额减去结算前累计已退款金额；HoldForDivision 时带 royalty_freeze，
// 资金到账后冻结供之后分账。幂等号用结算单号；ACQ.ALREADY_CONFIRM_SETTLE 说明已结算过，按成功处理。
func (z *zft) SettleConfirm(ctx context.Context, in channel.SettleIn) (channel.Result, error) {
	biz := map[string]any{
		"out_request_no": in.SettleNo, "trade_no": in.ChannelOrderNo,
		"settle_info": map[string]any{"settle_detail_infos": []map[string]any{{
			"trans_in_type": "defaultSettle", "amount": yuan(in.Amount),
		}}},
	}
	if in.HoldForDivision {
		biz["extend_params"] = map[string]string{"royalty_freeze": "true"}
	}
	node, err := call(ctx, "alipay.trade.settle.confirm", biz, in.Provider, in.Sub, "")
	if err != nil {
		res := resultOf(err, node, settleAmbiguousCodes...)
		if res.ErrCode == "ACQ.ALREADY_CONFIRM_SETTLE" {
			res.State = channel.StateSuccess
		}
		return res, nil
	}
	var out struct {
		TradeNo  string `json:"trade_no"`
		SettleNo string `json:"settle_no"`
	}
	_ = json.Unmarshal(node, &out)
	return channel.Result{State: channel.StateSuccess, ChannelOrderNo: orDefault(out.SettleNo, out.TradeNo), Raw: string(node)}, nil
}

// TradeBill alipay.data.dataservice.bill.downloadurl.query，下载后按 CSV 解析。
func (z *zft) TradeBill(ctx context.Context, billDate, billType string, prov, sub map[string]string) ([]channel.BillRow, error) {
	node, err := call(ctx, "alipay.data.dataservice.bill.downloadurl.query", map[string]any{
		"bill_type": orDefault(billType, "trade"), "bill_date": billDate,
	}, prov, sub, "")
	if err != nil {
		if strings.Contains(err.Error(), "isp.bill_not_exist") || strings.Contains(err.Error(), "BILL_NOT_EXIST") {
			return nil, channel.ErrNoBill
		}
		return nil, err
	}
	var out struct {
		URL string `json:"bill_download_url"`
	}
	if json.Unmarshal(node, &out) != nil || out.URL == "" {
		return nil, fmt.Errorf("bill: bad response")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, out.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("bill download: status %d", resp.StatusCode)
	}
	return parseAlipayBill(string(data))
}

// parseAlipayBill 解析支付宝账单 CSV。表头含「支付宝交易号」，数据行到「#」开头的汇总行为止。
func parseAlipayBill(csv string) ([]channel.BillRow, error) {
	csv = strings.TrimPrefix(csv, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(csv, "\r\n", "\n"), "\n")
	headerAt := -1
	for i, line := range lines {
		if strings.Contains(line, "支付宝交易号") && strings.Contains(line, "商户订单号") {
			headerAt = i
			break
		}
	}
	if headerAt < 0 {
		return nil, fmt.Errorf("alipay bill: header not found")
	}
	header := splitCSV(lines[headerAt])
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	get := func(fields []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(fields) {
			return ""
		}
		return strings.TrimSpace(fields[i])
	}
	var rows []channel.BillRow
	for _, line := range lines[headerAt+1:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			if strings.HasPrefix(line, "#") {
				break
			}
			continue
		}
		f := splitCSV(line)
		// 业务明细只列已完成的资金动作：「业务类型」是交易/退款，不是交易状态。
		// 退款行的「商户订单号」是原支付单号，退款单号在「退款批次号/请求号」（= 我们的 out_request_no）。
		row := channel.BillRow{
			TradeTime: get(f, "完成时间"), ChannelOrderNo: get(f, "支付宝交易号"), OutTradeNo: get(f, "商户订单号"),
			Fee: fenOf(get(f, "服务费（元）")),
		}
		switch bizType := get(f, "业务类型"); {
		case strings.Contains(bizType, "退款"):
			row.OutRefundNo = get(f, "退款批次号/请求号")
			row.RefundAmount = absFen(fenOf(get(f, "订单金额（元）")))
			row.RefundState = "SUCCESS"
		case strings.Contains(bizType, "交易"):
			row.TradeState = "SUCCESS"
			row.Amount = fenOf(get(f, "订单金额（元）"))
		default:
			continue // 其他资金动作（如分账、结算）不在本地支付/退款对账范围内
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func absFen(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func splitCSV(line string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	out = append(out, b.String())
	return out
}

func nonce() string {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	return fmt.Sprintf("%x", n)
}
