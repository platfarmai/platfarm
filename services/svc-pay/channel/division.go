// 分账与账单的渠道无关模型。PayChannel 实现者按需额外实现 SharingChannel / BillChannel，
// 业务层用 Sharing() / Bill() 取（类型断言），不支持的渠道得到明确错误。
package channel

import (
	"context"
	"fmt"
)

// ReceiverIn 绑定分账接收方。
type ReceiverIn struct {
	Type         string // MERCHANT_ID | PERSONAL_OPENID（微信）；支付宝 loginName | userId
	Account      string
	Name         string // 明文，适配器负责加密
	RelationType string
	Provider     map[string]string
	Sub          map[string]string
}

// DivisionReceiver 分账单里的一个接收方（快照 + 渠道结果）。
type DivisionReceiver struct {
	Type        string `json:"type"`
	Account     string `json:"account"`
	RatioBps    int    `json:"ratio_bps"`
	Amount      int64  `json:"amount"`
	Description string `json:"description"`
	Result      string `json:"result,omitempty"` // PENDING|SUCCESS|CLOSED（渠道原文）
	Detail      string `json:"detail,omitempty"` // 失败原因 / 渠道明细单号
}

// DivisionIn 发起分账。
type DivisionIn struct {
	DivisionNo      string // 渠道侧 out_order_no
	PayOrderID      string
	ChannelOrderNo  string // 渠道支付单号（微信 transaction_id）
	Receivers       []DivisionReceiver
	UnfreezeUnsplit bool // 分账后解冻剩余资金给子商户
	Provider        map[string]string
	Sub             map[string]string
}

// DivisionRef 查询 / 完结分账定位。
type DivisionRef struct {
	DivisionNo     string
	ChannelOrderNo string
	Provider       map[string]string
	Sub            map[string]string
}

// DivisionResult 分账结果：State 用四态，Receivers 带渠道逐个结果。
type DivisionResult struct {
	State          string
	ChannelOrderNo string // 渠道分账单号
	Receivers      []DivisionReceiver
	ErrCode        string
	ErrMsg         string
	Raw            string
}

// SharingChannel 支持分账的渠道。
type SharingChannel interface {
	AddReceiver(ctx context.Context, in ReceiverIn) error
	Share(ctx context.Context, in DivisionIn) (DivisionResult, error)
	QueryShare(ctx context.Context, ref DivisionRef) (DivisionResult, error)
	// FinishShare 分账完结：解冻订单剩余未分资金给子商户（微信 unfreeze）。
	FinishShare(ctx context.Context, ref DivisionRef) (DivisionResult, error)
}

// Sharing 取支持分账的渠道。
func Sharing(ifCode string) (SharingChannel, error) {
	c, err := Get(ifCode)
	if err != nil {
		return nil, err
	}
	s, ok := c.(SharingChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s 不支持分账", ifCode)
	}
	return s, nil
}

// BillRow 渠道账单里的一行（支付或退款）。金额单位分。
type BillRow struct {
	TradeTime      string
	SubMchID       string
	ChannelOrderNo string // 微信订单号 / 支付宝交易号
	OutTradeNo     string // 我们的 pay_order_id
	TradeState     string // SUCCESS | REFUND | …（渠道原文）
	Amount         int64  // 订单金额
	RefundID       string // 渠道退款单号
	OutRefundNo    string // 我们的 refund_order_id
	RefundAmount   int64
	RefundState    string
	Fee            int64
}

// IsRefund 该行是否退款记录。
func (r BillRow) IsRefund() bool { return r.OutRefundNo != "" || r.RefundID != "" }

// ErrNoBill 账单日无交易，渠道没有账单（不是错误）。
var ErrNoBill = fmt.Errorf("no statement for this date")

// BillChannel 支持下载交易账单的渠道。
type BillChannel interface {
	// TradeBill billDate "YYYY-MM-DD"，billType ALL|SUCCESS|REFUND。无账单返回 ErrNoBill。
	TradeBill(ctx context.Context, billDate, billType string, provider, sub map[string]string) ([]BillRow, error)
}

// Bill 取支持账单的渠道。
func Bill(ifCode string) (BillChannel, error) {
	c, err := Get(ifCode)
	if err != nil {
		return nil, err
	}
	b, ok := c.(BillChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s 不支持账单下载", ifCode)
	}
	return b, nil
}
