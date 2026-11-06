// wxpay_ecommerce 微信电商收付通（归集模式）。
// 资金进入二级商户账户并冻结；平台分账后调用完结分账解冻剩余，二级商户再预约提现。
// 请求签名、验签、敏感字段加密与 wxpay_partner 共用 doRequest / verifyKeyFor / encryptKey。
package wxpay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"platfarm/svc-pay/channel"
)

func init() { channel.Register(&ecommerce{}) }

type ecommerce struct{}

func (e *ecommerce) IfCode() string   { return "wxpay_ecommerce" }
func (e *ecommerce) FundMode() string { return channel.FundControlled }
func (e *ecommerce) Ways() []string   { return []string{"WX_JSAPI", "WX_NATIVE", "WX_COMBINE"} }

var (
	_ channel.PayChannel        = (*ecommerce)(nil)
	_ channel.OnboardingChannel = (*ecommerce)(nil)
	_ channel.SharingChannel    = (*ecommerce)(nil)
	_ channel.BillChannel       = (*ecommerce)(nil)
	_ channel.ControlledChannel = (*ecommerce)(nil)
)

// ── 支付 ────────────────────────────────────────────────────

// Pay 普通单走 /v3/pay/partner/transactions（与直清同形，但收付通必须声明分账冻结）；
// WX_COMBINE 走合单 /v3/combine-transactions，子单金额与单号放在 channelExtra（combine JSON）。
func (e *ecommerce) Pay(ctx context.Context, in channel.PayIn) (channel.Result, error) {
	if in.WayCode == "WX_COMBINE" {
		return e.combinePay(ctx, in)
	}
	path := "/v3/pay/partner/transactions/"
	req := jsapiReq{
		SpAppID: in.Provider["sp_appid"], SpMchID: in.Provider["sp_mchid"],
		SubMchID: in.Sub["sub_mchid"], Description: in.Subject,
		OutTradeNo: in.PayOrderID, NotifyURL: in.NotifyURL,
		Amount:     map[string]any{"total": in.Amount, "currency": orDefault(in.Currency, "CNY")},
		SettleInfo: map[string]any{"profit_sharing": true},
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
		return channel.Result{}, fmt.Errorf("wxpay_ecommerce 不支持 %s", in.WayCode)
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
	if json.Unmarshal(raw, &out) != nil {
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

// combineSub 合单子单。OutTradeNo 缺省时用 PayOrderID。
type combineSub struct {
	SubMchID    string `json:"sub_mchid"`
	OutTradeNo  string `json:"out_trade_no"`
	Amount      int64  `json:"amount"`
	Description string `json:"description"`
	Attach      string `json:"attach"`
}

// combinePay POST /v3/combine-transactions/{jsapi|native}。
// channelExtra["combine"] 是子单 JSON 数组；缺省时用当前订单自身作为唯一子单。
func (e *ecommerce) combinePay(ctx context.Context, in channel.PayIn) (channel.Result, error) {
	subs, err := combineSubs(in)
	if err != nil {
		return channel.Result{}, err
	}
	orders := make([]map[string]any, 0, len(subs))
	for _, s := range subs {
		orders = append(orders, map[string]any{
			"mchid": in.Provider["sp_mchid"], "sub_mchid": orDefault(s.SubMchID, in.Sub["sub_mchid"]),
			"out_trade_no": orDefault(s.OutTradeNo, in.PayOrderID), "description": orDefault(s.Description, in.Subject),
			"attach":      s.Attach,
			"amount":      map[string]any{"total_amount": s.Amount, "currency": orDefault(in.Currency, "CNY")},
			"settle_info": map[string]any{"profit_sharing": true},
		})
	}
	way := "jsapi"
	body := map[string]any{
		"combine_appid": in.Provider["sp_appid"], "combine_mchid": in.Provider["sp_mchid"],
		"combine_out_trade_no": in.PayOrderID, "notify_url": in.NotifyURL, "sub_orders": orders,
	}
	switch in.ChannelExtra["combine_way"] {
	case "native", "WX_NATIVE":
		way = "native"
		if in.ClientIP != "" {
			body["scene_info"] = map[string]any{"payer_client_ip": in.ClientIP}
		}
	default:
		openid := in.ChannelExtra["sp_openid"]
		if openid == "" {
			openid = in.ChannelExtra["sub_openid"]
		}
		if openid == "" {
			return channel.Result{}, fmt.Errorf("WX_COMBINE jsapi 需要 sp_openid 或 sub_openid")
		}
		body["combine_payer_info"] = map[string]any{"openid": openid}
	}
	raw, _ := json.Marshal(body)
	resp, out, err := doSigned(ctx, http.MethodPost, "/v3/combine-transactions/"+way, raw, in.Provider)
	if err != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		return failFromBody(out), nil
	}
	var parsed struct {
		PrepayID string `json:"prepay_id"`
		CodeURL  string `json:"code_url"`
	}
	if json.Unmarshal(out, &parsed) != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: "bad response", Raw: string(out)}, nil
	}
	res := channel.Result{State: channel.StateWaiting, Raw: string(out)}
	if way == "native" {
		res.Credential = map[string]string{"codeUrl": parsed.CodeURL}
	} else {
		res.Credential = jsapiPayParams(in.Provider, parsed.PrepayID)
	}
	return res, nil
}

func combineSubs(in channel.PayIn) ([]combineSub, error) {
	if raw := in.ChannelExtra["combine"]; raw != "" {
		var subs []combineSub
		if err := json.Unmarshal([]byte(raw), &subs); err != nil || len(subs) == 0 {
			return nil, fmt.Errorf("channelExtra.combine 不是合法的子单数组")
		}
		return subs, nil
	}
	return []combineSub{{SubMchID: in.Sub["sub_mchid"], OutTradeNo: in.PayOrderID, Amount: in.Amount, Description: in.Subject}}, nil
}

func (e *ecommerce) Query(ctx context.Context, ref channel.OrderRef) (channel.Result, error) {
	if ref.WayCode == "WX_COMBINE" {
		path := fmt.Sprintf("/v3/combine-transactions/out-trade-no/%s", ref.PayOrderID)
		resp, raw, err := doSigned(ctx, http.MethodGet, path, nil, ref.Provider)
		if err != nil {
			return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
		}
		if resp.StatusCode/100 != 2 {
			return failFromBody(raw), nil
		}
		return combineState(raw), nil
	}
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

// combineState 合单：全部子单 SUCCESS → SUCCESS；任一失败 → FAIL；否则 WAITING。
func combineState(raw []byte) channel.Result {
	var d struct {
		SubOrders []struct {
			TradeState    string `json:"trade_state"`
			TransactionID string `json:"transaction_id"`
		} `json:"sub_orders"`
	}
	_ = json.Unmarshal(raw, &d)
	res := channel.Result{State: channel.StateWaiting, Raw: string(raw)}
	if len(d.SubOrders) == 0 {
		return res
	}
	fail, ok := 0, 0
	for _, s := range d.SubOrders {
		switch s.TradeState {
		case "SUCCESS":
			ok++
			if res.ChannelOrderNo == "" {
				res.ChannelOrderNo = s.TransactionID
			}
		case "CLOSED", "REVOKED", "PAYERROR":
			fail++
		}
	}
	switch {
	case ok == len(d.SubOrders):
		res.State = channel.StateSuccess
	case fail > 0 && ok+fail == len(d.SubOrders):
		res.State = channel.StateFail
		res.ErrCode = "COMBINE_FAILED"
	}
	return res
}

func (e *ecommerce) Close(ctx context.Context, ref channel.OrderRef) error {
	var path string
	var body []byte
	if ref.WayCode == "WX_COMBINE" {
		path = "/v3/combine-transactions/out-trade-no/" + ref.PayOrderID + "/close"
		subs, _ := combineSubs(channel.PayIn{PayOrderID: ref.PayOrderID, Amount: 0, Sub: ref.Sub})
		orders := make([]map[string]string, 0, len(subs))
		for _, s := range subs {
			orders = append(orders, map[string]string{
				"mchid": ref.Provider["sp_mchid"], "sub_mchid": orDefault(s.SubMchID, ref.Sub["sub_mchid"]),
				"out_trade_no": orDefault(s.OutTradeNo, ref.PayOrderID),
			})
		}
		body, _ = json.Marshal(map[string]any{"combine_appid": ref.Provider["sp_appid"], "sub_orders": orders})
	} else {
		path = "/v3/pay/partner/transactions/out-trade-no/" + ref.PayOrderID + "/close"
		body, _ = json.Marshal(map[string]string{"sp_mchid": ref.Provider["sp_mchid"], "sub_mchid": ref.Sub["sub_mchid"]})
	}
	resp, raw, err := doSigned(ctx, http.MethodPost, path, body, ref.Provider)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode/100 != 2 {
		return fmt.Errorf("close: %s", string(raw))
	}
	return nil
}

func (e *ecommerce) Refund(ctx context.Context, in channel.RefundIn) (channel.Result, error) {
	body, _ := json.Marshal(map[string]any{
		"sub_mchid": in.Sub["sub_mchid"], "sp_appid": in.Provider["sp_appid"],
		"out_trade_no": in.PayOrderID, "out_refund_no": in.RefundOrderID,
		"amount": map[string]any{"refund": in.RefundAmount, "total": in.PayAmount, "currency": "CNY"},
		"reason": in.Reason, "notify_url": in.NotifyURL,
	})
	resp, raw, err := doSigned(ctx, http.MethodPost, "/v3/ecommerce/refunds/apply", body, in.Provider)
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

// ParseNotify 与普通服务商同一套验签解密；收付通退款事件以 ECOMMERCE.REFUND 开头。
func (e *ecommerce) ParseNotify(r *http.Request, provider map[string]string) (channel.Notify, error) {
	return (&partner{}).ParseNotify(r, provider)
}

// ── 进件 ────────────────────────────────────────────────────

// UploadMedia 收付通图片上传走电商媒体接口，签名正文仍是 meta JSON。
func (e *ecommerce) UploadMedia(ctx context.Context, filename string, data []byte, prov map[string]string) (string, error) {
	return (&partner{}).UploadMedia(ctx, filename, data, prov)
}

// SubmitApplyment POST /v3/ecommerce/applyments/。组织机构主体与结算账户字段与 applyment4sub 同形，
// 费率由平台与微信签约决定，rates 报为无法表达。
func (e *ecommerce) SubmitApplyment(ctx context.Context, in channel.ApplymentIn) (channel.ApplymentResult, error) {
	serial, pubPEM, err := encryptKey(in.Provider)
	if err != nil {
		return channel.ApplymentResult{}, err
	}
	enc := func(s string) (string, error) { return encryptSensitive(pubPEM, s) }
	a := in.Payload
	contactName, err := enc(a.Contact.Name)
	if err != nil {
		return channel.ApplymentResult{}, err
	}
	contactPhone, _ := enc(a.Contact.Phone)
	idName, _ := enc(a.LegalPerson.Name)
	idNo, _ := enc(a.LegalPerson.IDNo)
	accountNo, _ := enc(a.SettlementAccount.AccountNo)
	accountName, _ := enc(a.SettlementAccount.AccountName)
	begin, end := splitValid(a.Subject.LicenseValid)
	idBegin, idEnd := splitValid(a.LegalPerson.IDValid)

	body := map[string]any{
		"out_request_no":    in.BusinessCode,
		"organization_type": ecommerceOrgType(a.Subject.Type),
		"business_license_info": map[string]any{
			"business_license_copy": in.Media["license"], "business_license_number": a.Subject.LicenseNo,
			"merchant_name": a.Subject.Name, "legal_person": a.LegalPerson.Name,
			"business_time": fmt.Sprintf(`["%s","%s"]`, begin0(begin), end),
		},
		"id_card_info": map[string]any{
			"id_card_copy": in.Media["id_front"], "id_card_national": in.Media["id_back"],
			"id_card_name": idName, "id_card_number": idNo,
			"id_card_valid_time_begin": begin0(idBegin), "id_card_valid_time": idEnd,
		},
		"account_info": map[string]any{
			"bank_account_type": bankAccountType(a.SettlementAccount.Type),
			"account_bank":      a.SettlementAccount.Bank, "account_name": accountName, "account_number": accountNo,
		},
		"contact_info": map[string]any{
			"contact_type": orDefault(a.Extra["wx_contact_type"], "65"),
			"contact_name": contactName, "mobile_phone": contactPhone,
		},
		"sales_scene_info":   map[string]any{"store_name": a.Subject.ShortName, "store_url": a.Business.Scene.AppID},
		"merchant_shortname": a.Subject.ShortName,
	}
	if a.Contact.Email != "" {
		email, _ := enc(a.Contact.Email)
		body["contact_info"].(map[string]any)["contact_email"] = email
	}
	raw, _ := json.Marshal(body)
	resp, out, err := doRequest(ctx, http.MethodPost, "/v3/ecommerce/applyments/", raw, bytes.NewReader(raw),
		"application/json", map[string]string{"Wechatpay-Serial": serial}, in.Provider)
	if err != nil {
		return channel.ApplymentResult{}, err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(out)
		return channel.ApplymentResult{}, fmt.Errorf("ecommerce applyment: %s %s", f.ErrCode, f.ErrMsg)
	}
	var parsed struct {
		ApplymentID json.Number `json:"applyment_id"`
	}
	_ = json.Unmarshal(out, &parsed)
	return channel.ApplymentResult{ChannelApplymentID: parsed.ApplymentID.String(), Unexpressed: []string{"rates"}, Raw: string(out)}, nil
}

// QueryApplyment GET /v3/ecommerce/applyments/out-request-no/{out_request_no}。
func (e *ecommerce) QueryApplyment(ctx context.Context, businessCode string, prov map[string]string) (channel.ApplymentView, error) {
	path := "/v3/ecommerce/applyments/out-request-no/" + url.PathEscape(businessCode)
	resp, raw, err := doSigned(ctx, http.MethodGet, path, nil, prov)
	if err != nil {
		return channel.ApplymentView{}, err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return channel.ApplymentView{}, fmt.Errorf("query applyment: %s %s", f.ErrCode, f.ErrMsg)
	}
	var d struct {
		ApplymentID json.Number `json:"applyment_id"`
		State       string      `json:"applyment_state"`
		StateMsg    string      `json:"applyment_state_desc"`
		SignURL     string      `json:"sign_url"`
		SubMchID    string      `json:"sub_mchid"`
		AuditDetail []struct {
			Field        string `json:"param_name"`
			RejectReason string `json:"reject_reason"`
		} `json:"audit_detail"`
	}
	_ = json.Unmarshal(raw, &d)
	msg := d.StateMsg
	for _, ad := range d.AuditDetail {
		msg += fmt.Sprintf("；%s：%s", ad.Field, ad.RejectReason)
	}
	return channel.ApplymentView{
		ChannelApplymentID: d.ApplymentID.String(), State: foldApplymentState(d.State), RawState: d.State,
		Message: msg, SignURL: d.SignURL, SubMchID: d.SubMchID, Raw: string(raw),
	}, nil
}

// ecommerceOrgType 收付通 organization_type：个体 4、企业 2、事业单位 3。
func ecommerceOrgType(t string) string {
	switch t {
	case "enterprise":
		return "2"
	case "institution":
		return "3"
	default:
		return "4"
	}
}

// ── 分账 ────────────────────────────────────────────────────

// AddReceiver POST /v3/ecommerce/profitsharing/receivers/add。
func (e *ecommerce) AddReceiver(ctx context.Context, in channel.ReceiverIn) error {
	body := map[string]any{
		"appid": in.Provider["sp_appid"], "type": in.Type, "account": in.Account, "relation_type": in.RelationType,
	}
	extra := map[string]string{}
	if in.Name != "" {
		serial, pubPEM, err := encryptKey(in.Provider)
		if err != nil {
			return err
		}
		enc, err := encryptSensitive(pubPEM, in.Name)
		if err != nil {
			return err
		}
		body["name"] = enc
		extra["Wechatpay-Serial"] = serial
	}
	raw, _ := json.Marshal(body)
	resp, out, err := doRequest(ctx, http.MethodPost, "/v3/ecommerce/profitsharing/receivers/add", raw, bytes.NewReader(raw),
		"application/json", extra, in.Provider)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(out)
		return fmt.Errorf("add receiver: %s %s", f.ErrCode, f.ErrMsg)
	}
	return nil
}

// Share POST /v3/ecommerce/profitsharing/orders。
func (e *ecommerce) Share(ctx context.Context, in channel.DivisionIn) (channel.DivisionResult, error) {
	receivers := make([]map[string]any, 0, len(in.Receivers))
	for _, r := range in.Receivers {
		receivers = append(receivers, map[string]any{
			"type": r.Type, "receiver_account": r.Account, "amount": r.Amount, "description": orDefault(r.Description, "分账"),
		})
	}
	body, _ := json.Marshal(map[string]any{
		"appid": in.Provider["sp_appid"], "sub_mchid": in.Sub["sub_mchid"],
		"transaction_id": in.ChannelOrderNo, "out_order_no": in.DivisionNo,
		"receivers": receivers, "finish": in.UnfreezeUnsplit,
	})
	resp, raw, err := doSigned(ctx, http.MethodPost, "/v3/ecommerce/profitsharing/orders", body, in.Provider)
	if err != nil {
		return channel.DivisionResult{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return channel.DivisionResult{State: channel.StateFail, ErrCode: f.ErrCode, ErrMsg: f.ErrMsg, Raw: string(raw)}, nil
	}
	return parseEcommerceDivision(raw), nil
}

// QueryShare GET /v3/ecommerce/profitsharing/orders。
func (e *ecommerce) QueryShare(ctx context.Context, ref channel.DivisionRef) (channel.DivisionResult, error) {
	q := url.Values{"sub_mchid": {ref.Sub["sub_mchid"]}, "transaction_id": {ref.ChannelOrderNo}, "out_order_no": {ref.DivisionNo}}
	resp, raw, err := doSigned(ctx, http.MethodGet, "/v3/ecommerce/profitsharing/orders?"+q.Encode(), nil, ref.Provider)
	if err != nil {
		return channel.DivisionResult{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return channel.DivisionResult{State: channel.StateUnknown, ErrCode: f.ErrCode, ErrMsg: f.ErrMsg, Raw: string(raw)}, nil
	}
	return parseEcommerceDivision(raw), nil
}

// FinishShare POST /v3/ecommerce/profitsharing/finish-order：解冻剩余资金。
func (e *ecommerce) FinishShare(ctx context.Context, ref channel.DivisionRef) (channel.DivisionResult, error) {
	body, _ := json.Marshal(map[string]any{
		"sub_mchid": ref.Sub["sub_mchid"], "transaction_id": ref.ChannelOrderNo,
		"out_order_no": ref.DivisionNo, "description": "分账完结",
	})
	resp, raw, err := doSigned(ctx, http.MethodPost, "/v3/ecommerce/profitsharing/finish-order", body, ref.Provider)
	if err != nil {
		return channel.DivisionResult{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return channel.DivisionResult{State: channel.StateFail, ErrCode: f.ErrCode, ErrMsg: f.ErrMsg, Raw: string(raw)}, nil
	}
	res := parseEcommerceDivision(raw)
	if res.State == "" || res.State == channel.StateUnknown {
		res.State = channel.StateSuccess
	}
	return res, nil
}

// parseEcommerceDivision 收付通分账单。status FINISHED 且有成功接收方为 SUCCESS。
func parseEcommerceDivision(raw []byte) channel.DivisionResult {
	var d struct {
		OrderID   string `json:"order_id"`
		Status    string `json:"status"`
		Receivers []struct {
			Type       string `json:"type"`
			Account    string `json:"receiver_account"`
			Amount     int64  `json:"amount"`
			Desc       string `json:"description"`
			Result     string `json:"result"`
			FailReason string `json:"fail_reason"`
			DetailID   string `json:"detail_id"`
		} `json:"receivers"`
	}
	_ = json.Unmarshal(raw, &d)
	res := channel.DivisionResult{ChannelOrderNo: d.OrderID, Raw: string(raw)}
	succeeded := 0
	for _, r := range d.Receivers {
		detail := r.DetailID
		if r.FailReason != "" {
			detail = r.FailReason
		}
		res.Receivers = append(res.Receivers, channel.DivisionReceiver{
			Type: r.Type, Account: r.Account, Amount: r.Amount, Description: r.Desc, Result: r.Result, Detail: detail,
		})
		if r.Result == "SUCCESS" {
			succeeded++
		}
	}
	switch d.Status {
	case "FINISHED":
		if succeeded > 0 || len(d.Receivers) == 0 {
			res.State = channel.StateSuccess
		} else {
			res.State = channel.StateFail
			res.ErrCode, res.ErrMsg = "CLOSED", "所有接收方分账失败"
		}
	case "PROCESSING":
		res.State = channel.StateWaiting
	default:
		res.State = channel.StateUnknown
	}
	return res
}

// ── 结算确认 / 提现 / 余额 ──────────────────────────────────

// SettleConfirm 收付通的「确认结算」是完结分账：把订单剩余冻结资金解冻给二级商户。
func (e *ecommerce) SettleConfirm(ctx context.Context, in channel.SettleIn) (channel.Result, error) {
	res, err := e.FinishShare(ctx, channel.DivisionRef{
		DivisionNo: in.PayOrderID + "S", ChannelOrderNo: in.ChannelOrderNo, Provider: in.Provider, Sub: in.Sub,
	})
	if err != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	return channel.Result{State: res.State, ChannelOrderNo: res.ChannelOrderNo, ErrCode: res.ErrCode, ErrMsg: res.ErrMsg, Raw: res.Raw}, nil
}

// Withdraw POST /v3/ecommerce/fund/withdraw。预约提现受理即 WAITING，终态靠查询。
func (e *ecommerce) Withdraw(ctx context.Context, in channel.WithdrawIn) (channel.Result, error) {
	body, _ := json.Marshal(map[string]any{
		"sub_mchid": in.Sub["sub_mchid"], "out_request_no": in.WithdrawNo,
		"amount": in.Amount, "remark": orDefault(in.Remark, "提现"),
		"bank_memo": in.Remark,
	})
	resp, raw, err := doSigned(ctx, http.MethodPost, "/v3/ecommerce/fund/withdraw", body, in.Provider)
	if err != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		return failFromBody(raw), nil
	}
	var out struct {
		WithdrawID string `json:"withdraw_id"`
		Status     string `json:"status"`
	}
	_ = json.Unmarshal(raw, &out)
	return channel.Result{State: withdrawStatus(out.Status, channel.StateWaiting), ChannelOrderNo: out.WithdrawID, Raw: string(raw)}, nil
}

// QueryWithdraw GET /v3/ecommerce/fund/withdraw/out-request-no/{out_request_no}?sub_mchid=
func (e *ecommerce) QueryWithdraw(ctx context.Context, ref channel.WithdrawRef) (channel.Result, error) {
	q := url.Values{"sub_mchid": {ref.Sub["sub_mchid"]}}
	path := "/v3/ecommerce/fund/withdraw/out-request-no/" + url.PathEscape(ref.WithdrawNo) + "?" + q.Encode()
	resp, raw, err := doSigned(ctx, http.MethodGet, path, nil, ref.Provider)
	if err != nil {
		return channel.Result{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return channel.Result{State: channel.StateUnknown, ErrCode: f.ErrCode, ErrMsg: f.ErrMsg, Raw: string(raw)}, nil
	}
	var out struct {
		WithdrawID string `json:"withdraw_id"`
		Status     string `json:"status"`
		Reason     string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &out)
	res := channel.Result{State: withdrawStatus(out.Status, channel.StateUnknown), ChannelOrderNo: out.WithdrawID, Raw: string(raw)}
	if res.State == channel.StateFail {
		res.ErrCode, res.ErrMsg = out.Status, out.Reason
	}
	return res, nil
}

// Balance GET /v3/ecommerce/fund/balance/{sub_mchid}。available_amount 为可提现余额（分）。
func (e *ecommerce) Balance(ctx context.Context, prov, sub map[string]string) (channel.Balance, error) {
	path := "/v3/ecommerce/fund/balance/" + url.PathEscape(sub["sub_mchid"])
	resp, raw, err := doSigned(ctx, http.MethodGet, path, nil, prov)
	if err != nil {
		return channel.Balance{}, err
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return channel.Balance{}, fmt.Errorf("balance: %s %s", f.ErrCode, f.ErrMsg)
	}
	var out struct {
		Available int64 `json:"available_amount"`
		Pending   int64 `json:"pending_amount"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return channel.Balance{}, fmt.Errorf("balance: bad response")
	}
	return channel.Balance{Available: out.Available, Pending: out.Pending}, nil
}

// withdrawStatus CREATE_SUCCESS/INIT → WAITING，SUCCESS → SUCCESS，FAIL/REFUND/CLOSE → FAIL。
// empty 是状态字段缺失时的默认（受理响应通常不带终态）。
func withdrawStatus(status, empty string) string {
	switch status {
	case "SUCCESS":
		return channel.StateSuccess
	case "FAIL", "REFUND", "CLOSE":
		return channel.StateFail
	case "CREATE_SUCCESS", "INIT":
		return channel.StateWaiting
	case "":
		return empty
	default:
		return channel.StateWaiting
	}
}

// TradeBill 收付通账单与普通服务商同一接口，按二级商户号过滤。
func (e *ecommerce) TradeBill(ctx context.Context, billDate, billType string, prov, sub map[string]string) ([]channel.BillRow, error) {
	return (&partner{}).TradeBill(ctx, billDate, billType, prov, sub)
}
