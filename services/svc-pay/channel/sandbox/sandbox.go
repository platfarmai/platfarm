// Package sandbox 是给合作方联调用的沙箱渠道：不连任何上游、不动真钱，
// 走的却是和真实渠道完全相同的下单 / 查单 / 关单 / 退款 / 通知链路。
//
// 结果由金额的"分"位决定（金额 % 100），无状态——进程重启、多副本都得到同样的结果：
//
//	支付  ..01  下单即失败
//	      ..02  下单结果未知（模拟渠道超时），之后补单查询得到成功
//	      ..03  一直待支付（用来测关单与超时）
//	      其他  待支付，补单查询时成功（约 30~60 秒），或调用模拟支付接口立即出结果
//	退款  ..01  退款失败
//	      ..02  退款处理中，之后查询得到成功
//	      其他  立即成功
//
// 默认不注册：只有 PAY_SANDBOX=on 时 main 才调用 Enable。
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"platfarm/svc-pay/channel"
)

// IfCode 沙箱渠道代码。
const IfCode = "sandbox"

// 支持所有真实渠道的支付方式：合作方在沙箱里用的 wayCode 与上线后一致。
var ways = []string{
	"WX_JSAPI", "WX_NATIVE",
	"ALI_APP", "ALI_WAP", "ALI_PAGE", "ALI_QR", "ALI_JSAPI",
}

var enableOnce sync.Once

// Enable 注册沙箱渠道（幂等）。
func Enable() {
	enableOnce.Do(func() { channel.Register(&sandbox{}) })
}

type sandbox struct{}

func (s *sandbox) IfCode() string       { return IfCode }
func (s *sandbox) FundMode() string     { return channel.FundDirect }
func (s *sandbox) Ways() []string       { return append([]string(nil), ways...) }
func (s *sandbox) ParamsOptional() bool { return true }
func (s *sandbox) Simulated() bool      { return true }

// ParamFields 沙箱没有渠道参数。
func (s *sandbox) ParamFields() (provider, sub []channel.ParamField) { return nil, nil }

// OrderNo 沙箱"渠道单号"：由我们的单号派生，查单时不需要任何存储。
func OrderNo(id string) string { return "SBX" + id }

// User 沙箱里的付款人标识。
const User = "sandbox-user"

func supported(way string) bool {
	for _, w := range ways {
		if w == way {
			return true
		}
	}
	return false
}

func (s *sandbox) Pay(_ context.Context, in channel.PayIn) (channel.Result, error) {
	if !supported(in.WayCode) {
		return channel.Result{}, fmt.Errorf("sandbox 不支持支付方式 %s", in.WayCode)
	}
	switch in.Amount % 100 {
	case 1:
		return channel.Result{State: channel.StateFail, ErrCode: "SANDBOX_PAY_FAIL", ErrMsg: "沙箱：金额尾数 01 模拟下单失败"}, nil
	case 2:
		return channel.Result{State: channel.StateUnknown, ErrCode: "SANDBOX_TIMEOUT", ErrMsg: "沙箱：金额尾数 02 模拟渠道超时"}, nil
	}
	return channel.Result{State: channel.StateWaiting, ChannelOrderNo: OrderNo(in.PayOrderID), Credential: credential(in)}, nil
}

// credential 返回给下游的"支付凭证"。键名与真实渠道同一支付方式返回的一致（下游解析代码不用为沙箱分叉），
// 但值是 sandbox:// 占位并带 sandbox 标记，拿去调微信 / 支付宝的收银台是付不了的。
func credential(in channel.PayIn) map[string]string {
	out := map[string]string{"sandbox": "true", "wayCode": in.WayCode, "payOrderId": in.PayOrderID}
	link := "sandbox://pay/" + in.PayOrderID
	switch in.WayCode {
	case "WX_NATIVE":
		out["codeUrl"] = link
	case "ALI_QR":
		out["qrCode"] = link
	case "ALI_JSAPI":
		out["tradeNo"] = OrderNo(in.PayOrderID)
	case "ALI_APP", "ALI_WAP", "ALI_PAGE":
		out["payUrl"] = link
	default: // WX_JSAPI
		out["prepayId"] = OrderNo(in.PayOrderID)
	}
	return out
}

// Query 没有上游可查：尾数 03 永远待支付，其余一律已支付。
func (s *sandbox) Query(_ context.Context, ref channel.OrderRef) (channel.Result, error) {
	if ref.Amount%100 == 3 {
		return channel.Result{State: channel.StateWaiting, ChannelOrderNo: OrderNo(ref.PayOrderID)}, nil
	}
	return Paid(ref.PayOrderID, ref.Amount), nil
}

// Paid 一笔沙箱支付成功的结果。
func Paid(payOrderID string, amount int64) channel.Result {
	return channel.Result{State: channel.StateSuccess, ChannelOrderNo: OrderNo(payOrderID), ChannelUser: User, Amount: amount}
}

// Failed 一笔沙箱支付失败的结果。
func Failed(payOrderID string) channel.Result {
	return channel.Result{State: channel.StateFail, ChannelOrderNo: OrderNo(payOrderID),
		ErrCode: "SANDBOX_PAY_FAIL", ErrMsg: "沙箱：模拟支付失败"}
}

func (s *sandbox) Close(context.Context, channel.OrderRef) error { return nil }

func (s *sandbox) Refund(_ context.Context, in channel.RefundIn) (channel.Result, error) {
	no := OrderNo(in.RefundOrderID)
	switch in.RefundAmount % 100 {
	case 1:
		return channel.Result{State: channel.StateFail, ChannelOrderNo: no,
			ErrCode: "SANDBOX_REFUND_FAIL", ErrMsg: "沙箱：金额尾数 01 模拟退款失败"}, nil
	case 2:
		return channel.Result{State: channel.StateWaiting, ChannelOrderNo: no}, nil
	}
	return channel.Result{State: channel.StateSuccess, ChannelOrderNo: no, Amount: in.RefundAmount}, nil
}

// QueryRefund 只有处理中的退款会被查询：一律成功。
func (s *sandbox) QueryRefund(_ context.Context, ref channel.RefundRef) (channel.Result, error) {
	return channel.Result{State: channel.StateSuccess, ChannelOrderNo: OrderNo(ref.RefundOrderID)}, nil
}

// ParseNotify 沙箱没有上游回调：这个地址上来的请求一律拒绝，谁也不能凭一个 HTTP 请求把订单改成已支付。
func (s *sandbox) ParseNotify(*http.Request, map[string]string) (channel.Notify, error) {
	return channel.Notify{}, errors.New("sandbox 渠道没有回调")
}

var (
	_ channel.PayChannel     = (*sandbox)(nil)
	_ channel.RefundQuerier  = (*sandbox)(nil)
	_ channel.ParamDescriber = (*sandbox)(nil)
)
