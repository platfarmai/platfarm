// 归集模式（specs/030 M4）的渠道无关模型：资金先落平台控制的账户，平台确认结算、分账，子商户提现。
// wxpay_ecommerce（收付通）与 alipay_zft（直付通）实现 ControlledChannel；直清渠道不实现，业务层据此拒绝提现。
package channel

import (
	"context"
	"fmt"
)

// SettleIn 确认结算：把订单资金（扣除已分账）释放给子商户。
type SettleIn struct {
	PayOrderID     string
	ChannelOrderNo string
	Amount         int64 // 本次确认结算金额（订单金额 - 已分账）
	Provider       map[string]string
	Sub            map[string]string
}

// WithdrawIn 子商户提现。
type WithdrawIn struct {
	WithdrawNo string
	Amount     int64
	Remark     string
	Provider   map[string]string
	Sub        map[string]string
}

// WithdrawRef 查询提现。
type WithdrawRef struct {
	WithdrawNo     string
	ChannelOrderNo string
	Provider       map[string]string
	Sub            map[string]string
}

// Balance 渠道侧余额（分）。
type Balance struct {
	Available int64
	Pending   int64
}

// ControlledChannel 归集模式能力。
type ControlledChannel interface {
	SettleConfirm(ctx context.Context, in SettleIn) (Result, error)
	Withdraw(ctx context.Context, in WithdrawIn) (Result, error)
	QueryWithdraw(ctx context.Context, ref WithdrawRef) (Result, error)
	Balance(ctx context.Context, provider, sub map[string]string) (Balance, error)
}

// Controlled 取归集模式渠道；直清渠道返回明确错误（业务层以此回 400）。
func Controlled(ifCode string) (ControlledChannel, error) {
	c, err := Get(ifCode)
	if err != nil {
		return nil, err
	}
	if c.FundMode() != FundControlled {
		return nil, fmt.Errorf("channel %s 是直清模式，没有结算确认/提现", ifCode)
	}
	cc, ok := c.(ControlledChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s 未实现归集能力", ifCode)
	}
	return cc, nil
}

// IsControlled 渠道是否归集模式（未知渠道按直清）。
func IsControlled(ifCode string) bool {
	c, err := Get(ifCode)
	return err == nil && c.FundMode() == FundControlled
}
