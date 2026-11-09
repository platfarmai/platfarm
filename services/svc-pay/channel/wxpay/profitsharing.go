// 微信服务商分账（profitsharing v3，直清模式）：绑定接收方、请求分账、查询、完结（解冻）。
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

var _ channel.SharingChannel = (*partner)(nil)

// AddReceiver POST /v3/profitsharing/receivers/add。MERCHANT_ID 类型的 name 需加密（Wechatpay-Serial）。
func (p *partner) AddReceiver(ctx context.Context, in channel.ReceiverIn) error {
	body := map[string]any{
		"sub_mchid": in.Sub["sub_mchid"], "appid": in.Provider["sp_appid"],
		"type": in.Type, "account": in.Account, "relation_type": in.RelationType,
	}
	if in.Sub["sub_appid"] != "" {
		body["sub_appid"] = in.Sub["sub_appid"]
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
	resp, out, err := doRequest(ctx, http.MethodPost, "/v3/profitsharing/receivers/add", raw, bytes.NewReader(raw),
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

// Share POST /v3/profitsharing/orders。
func (p *partner) Share(ctx context.Context, in channel.DivisionIn) (channel.DivisionResult, error) {
	receivers := make([]map[string]any, 0, len(in.Receivers))
	for _, r := range in.Receivers {
		receivers = append(receivers, map[string]any{
			"type": r.Type, "account": r.Account, "amount": r.Amount, "description": orDefault(r.Description, "分账"),
		})
	}
	body, _ := json.Marshal(map[string]any{
		"sub_mchid": in.Sub["sub_mchid"], "appid": in.Provider["sp_appid"],
		"transaction_id": in.ChannelOrderNo, "out_order_no": in.DivisionNo,
		"receivers": receivers, "unfreeze_unsplit": in.UnfreezeUnsplit,
	})
	resp, raw, err := doSigned(ctx, http.MethodPost, "/v3/profitsharing/orders", body, in.Provider)
	if err != nil {
		return channel.DivisionResult{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		return divisionFailOrUnknown(resp.StatusCode, raw), nil
	}
	return parseDivision(raw), nil
}

// divisionFailOrUnknown 分账类请求的非 2xx，规则同 failOrUnknown（同一 out_order_no 重试等同一次请求）。
func divisionFailOrUnknown(status int, raw []byte) channel.DivisionResult {
	r := failOrUnknown(status, raw)
	return channel.DivisionResult{State: r.State, ErrCode: r.ErrCode, ErrMsg: r.ErrMsg, Raw: r.Raw}
}

// QueryShare GET /v3/profitsharing/orders/{out_order_no}?sub_mchid=&transaction_id=
func (p *partner) QueryShare(ctx context.Context, ref channel.DivisionRef) (channel.DivisionResult, error) {
	q := url.Values{"sub_mchid": {ref.Sub["sub_mchid"]}, "transaction_id": {ref.ChannelOrderNo}}
	resp, raw, err := doSigned(ctx, http.MethodGet, "/v3/profitsharing/orders/"+ref.DivisionNo+"?"+q.Encode(), nil, ref.Provider)
	if err != nil {
		return channel.DivisionResult{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		f := failFromBody(raw)
		return channel.DivisionResult{State: channel.StateUnknown, ErrCode: f.ErrCode, ErrMsg: f.ErrMsg, Raw: string(raw)}, nil
	}
	return parseDivision(raw), nil
}

// FinishShare POST /v3/profitsharing/orders/unfreeze：解冻剩余资金给子商户。
func (p *partner) FinishShare(ctx context.Context, ref channel.DivisionRef) (channel.DivisionResult, error) {
	body, _ := json.Marshal(map[string]any{
		"sub_mchid": ref.Sub["sub_mchid"], "transaction_id": ref.ChannelOrderNo,
		"out_order_no": ref.DivisionNo + "U", "description": "分账完结",
	})
	resp, raw, err := doSigned(ctx, http.MethodPost, "/v3/profitsharing/orders/unfreeze", body, ref.Provider)
	if err != nil {
		return channel.DivisionResult{State: channel.StateUnknown, ErrMsg: err.Error()}, nil
	}
	if resp.StatusCode/100 != 2 {
		return divisionFailOrUnknown(resp.StatusCode, raw), nil
	}
	return parseDivision(raw), nil
}

// parseDivision 微信分账单 → 四态。FINISHED 且至少一个接收方 SUCCESS 为 SUCCESS（接收方逐个结果随行返回），
// FINISHED 但无人成功为 FAIL，PROCESSING 为 WAITING。
func parseDivision(raw []byte) channel.DivisionResult {
	var d struct {
		OrderID   string `json:"order_id"`
		State     string `json:"state"`
		Receivers []struct {
			Type       string `json:"type"`
			Account    string `json:"account"`
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
	switch d.State {
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
