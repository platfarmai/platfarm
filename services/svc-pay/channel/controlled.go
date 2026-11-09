// 归集模式（specs/030 M4）的渠道无关模型：资金先落平台控制的账户，平台确认结算、分账。
// 两个产品的资金去向不同，能力按接口拆开，业务层据此决定合法的动作：
//   - 微信收付通：资金冻结在二级商户账户，分账后完结分账（确认结算）释放到二级商户可用余额，
//     平台代二级商户发起提现 → 实现 SettleChannel + WithdrawChannel。
//   - 支付宝直付通：确认结算即把资金结算到二级商户进件时的默认结算账户（可冻结用于之后的分账），
//     平台侧没有二级商户余额可提现 → 只实现 SettleChannel。
//
// 直清渠道两者都不实现，业务层回 400。
package channel

import (
	"context"
	"fmt"
)

// SettleIn 确认结算：把订单资金（扣除已退款，收付通再扣除已分账）释放给子商户。
type SettleIn struct {
	SettleNo        string // 我们的结算单号，作为渠道幂等号（out_request_no / out_order_no）
	PayOrderID      string
	ChannelOrderNo  string
	Amount          int64 // 本次确认结算金额
	HoldForDivision bool  // 直付通：结算后冻结，供之后分账（royalty_freeze）
	Provider        map[string]string
	Sub             map[string]string
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

// Balance 渠道侧子商户余额（分）。
type Balance struct {
	Available int64
	Pending   int64
}

// SettleChannel 归集模式确认结算。
type SettleChannel interface {
	SettleConfirm(ctx context.Context, in SettleIn) (Result, error)
}

// WithdrawChannel 资金留在子商户可提现账户、由平台代为提现的渠道。
type WithdrawChannel interface {
	Withdraw(ctx context.Context, in WithdrawIn) (Result, error)
	QueryWithdraw(ctx context.Context, ref WithdrawRef) (Result, error)
	Balance(ctx context.Context, provider, sub map[string]string) (Balance, error)
}

// Settling 取归集模式的结算能力；直清渠道返回明确错误（业务层以此回 400）。
func Settling(ifCode string) (SettleChannel, error) {
	c, err := controlled(ifCode)
	if err != nil {
		return nil, err
	}
	s, ok := c.(SettleChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s 未实现确认结算", ifCode)
	}
	return s, nil
}

// Withdrawing 取提现能力。直付通确认结算即结算到二级商户结算账户，没有平台侧余额可提现。
func Withdrawing(ifCode string) (WithdrawChannel, error) {
	c, err := controlled(ifCode)
	if err != nil {
		return nil, err
	}
	w, ok := c.(WithdrawChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s 确认结算即到二级商户结算账户，没有可提现余额", ifCode)
	}
	return w, nil
}

func controlled(ifCode string) (PayChannel, error) {
	c, err := Get(ifCode)
	if err != nil {
		return nil, err
	}
	if c.FundMode() != FundControlled {
		return nil, fmt.Errorf("channel %s 是直清模式，没有结算确认/提现", ifCode)
	}
	return c, nil
}

// IsControlled 渠道是否归集模式（未知渠道按直清）。
func IsControlled(ifCode string) bool {
	c, err := Get(ifCode)
	return err == nil && c.FundMode() == FundControlled
}

// Withdrawable 归集渠道且资金留在可提现的子商户账户（收付通）。
func Withdrawable(ifCode string) bool {
	_, err := Withdrawing(ifCode)
	return err == nil
}
