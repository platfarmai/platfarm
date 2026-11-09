// 台账（specs/030 M3/M4）。pay_ledger 追加写，(kind, ref_type, ref_id, ref_sub) 唯一：
// 同一事件重放不会记两次，所以可以在任何 CAS 成功路径上无脑调用。
// 直清模式只记 fee / rebate 及其退款冲回；分账记 sharing；收付通再加可提现台账
// settle / refund（结算后的退款）/ withdrawal（发起即预占）/ withdrawal_reversal。
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"

	"platfarm/svc-pay/channel"
)

const (
	ledgerFee                = "fee"
	ledgerFeeRefund          = "fee_refund"
	ledgerRebate             = "rebate"
	ledgerRebateRefund       = "rebate_refund"
	ledgerSharing            = "sharing"
	ledgerSettle             = "settle"
	ledgerRefund             = "refund"
	ledgerWithdrawal         = "withdrawal"
	ledgerWithdrawalReversal = "withdrawal_reversal"
)

// LedgerEntry 一条台账。Amount 以 MchNo 视角有符号。
type LedgerEntry struct {
	ID        int64  `json:"id"`
	MchNo     string `json:"mchNo"`
	IsvNo     string `json:"isvNo"`
	IfCode    string `json:"ifCode"`
	Kind      string `json:"kind"`
	RefType   string `json:"refType"`
	RefID     string `json:"refId"`
	RefSub    string `json:"refSub"`
	Amount    int64  `json:"amount"`
	Memo      string `json:"memo"`
	TenantID  int    `json:"tenantId"`
	CreatedAt string `json:"createdAt"`
}

// postLedger 幂等追加。返回实际新增的行数。
func postLedger(ctx context.Context, tx pgx.Tx, entries []LedgerEntry) (int, error) {
	n := 0
	for _, e := range entries {
		if e.Amount == 0 {
			continue
		}
		var tag interface{ RowsAffected() int64 }
		var err error
		if tx != nil {
			tag, err = tx.Exec(ctx, ledgerInsertSQL, e.MchNo, e.IsvNo, e.IfCode, e.Kind, e.RefType, e.RefID, e.RefSub, e.Amount, e.Memo, e.TenantID)
		} else {
			tag, err = db.Exec(ctx, ledgerInsertSQL, e.MchNo, e.IsvNo, e.IfCode, e.Kind, e.RefType, e.RefID, e.RefSub, e.Amount, e.Memo, e.TenantID)
		}
		if err != nil {
			return n, err
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

const ledgerInsertSQL = `INSERT INTO pay_ledger (mch_no, isv_no, if_code, kind, ref_type, ref_id, ref_sub, amount, memo, tenant_id)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (kind, ref_type, ref_id, ref_sub) DO NOTHING`

// orderSettledEntries 支付成功：商户手续费（借方）+ 服务商返佣（贷方，归属 isv）。
func orderSettledEntries(o Order) []LedgerEntry {
	return []LedgerEntry{
		{MchNo: o.MchNo, IsvNo: o.IsvNo, IfCode: o.IfCode, Kind: ledgerFee, RefType: "order", RefID: o.PayOrderID,
			Amount: -o.FeeAmount, Memo: "渠道手续费", TenantID: o.TenantID},
		{MchNo: o.MchNo, IsvNo: o.IsvNo, IfCode: o.IfCode, Kind: ledgerRebate, RefType: "order", RefID: o.PayOrderID,
			Amount: o.RebateAmount, Memo: "渠道返佣", TenantID: o.TenantID},
	}
}

// refundSettledEntries 退款成功：按退款占比冲回手续费与返佣（四舍五入到分）。
func refundSettledEntries(o Order, r Refund) []LedgerEntry {
	fee := proportional(o.FeeAmount, r.RefundAmount, o.Amount)
	rebate := proportional(o.RebateAmount, r.RefundAmount, o.Amount)
	return []LedgerEntry{
		{MchNo: o.MchNo, IsvNo: o.IsvNo, IfCode: o.IfCode, Kind: ledgerFeeRefund, RefType: "refund", RefID: r.RefundOrderID,
			Amount: fee, Memo: "退款冲回手续费", TenantID: o.TenantID},
		{MchNo: o.MchNo, IsvNo: o.IsvNo, IfCode: o.IfCode, Kind: ledgerRebateRefund, RefType: "refund", RefID: r.RefundOrderID,
			Amount: -rebate, Memo: "退款冲回返佣", TenantID: o.TenantID},
	}
}

// proportional total * part / whole，四舍五入；whole<=0 返回 0。
func proportional(total, part, whole int64) int64 {
	if whole <= 0 || total == 0 || part <= 0 {
		return 0
	}
	if part >= whole {
		return total
	}
	num := total * part
	if num < 0 {
		return -((-num + whole/2) / whole)
	}
	return (num + whole/2) / whole
}

// orderTerminalEffects 支付单进入终态后的统一动作：成功记手续费/返佣台账；成功或失败入下游通知。
// tx 非空时与状态推进在同一事务里（见 finishOrder），任何一步失败整体回滚。
func orderTerminalEffects(ctx context.Context, tx pgx.Tx, o Order, to string) error {
	if to == orderSuccess {
		if _, err := postLedger(ctx, tx, orderSettledEntries(o)); err != nil {
			return fmt.Errorf("ledger order %s: %w", o.PayOrderID, err)
		}
	}
	if to == orderSuccess || to == orderFailed {
		if err := enqueueNotifyFields(ctx, qOf(tx), "pay", orderNotifyFields(o), o.TenantID); err != nil {
			return fmt.Errorf("notify order %s: %w", o.PayOrderID, err)
		}
	}
	return nil
}

// onOrderTerminal 对已在终态的支付单补做终态动作（幂等）。正常路径由 finishOrder 原子完成。
func onOrderTerminal(ctx context.Context, payOrderID, to string) {
	o, err := findOrder(ctx, payOrderID)
	if err != nil {
		log.Printf("order %s terminal effects: %v", payOrderID, err)
		return
	}
	if err := orderTerminalEffects(ctx, nil, o, to); err != nil {
		log.Printf("order %s terminal effects: %v", payOrderID, err)
	}
}

// refundTerminalEffects 退款单进入终态：回写支付单退款状态（失败退回额度）；成功记冲回台账；入下游通知。
// tx 非空时与状态推进同事务（见 finishRefund）。
func refundTerminalEffects(ctx context.Context, tx pgx.Tx, r Refund, to string) error {
	q := qOf(tx)
	success := to == refundSuccess
	if err := rollupRefund(ctx, q, r.PayOrderID, r.RefundAmount, success); err != nil {
		return fmt.Errorf("rollup refund %s: %w", r.RefundOrderID, err)
	}
	if success {
		o, err := scanOrder(q.QueryRow(ctx, `SELECT `+orderCols+` FROM pay_order WHERE pay_order_id=$1`, r.PayOrderID))
		if err != nil {
			return err
		}
		entries := refundSettledEntries(o, r)
		// 收付通已确认结算的订单：退款从二级商户可用余额出，扣可提现台账（结算前的退款已在结算金额里扣除）。
		if o.SettleState == orderSettleDone && channel.Withdrawable(o.IfCode) {
			entries = append(entries, LedgerEntry{MchNo: o.MchNo, IsvNo: o.IsvNo, IfCode: o.IfCode, Kind: ledgerRefund,
				RefType: "refund", RefID: r.RefundOrderID, Amount: -r.RefundAmount, Memo: "结算后退款", TenantID: o.TenantID})
		}
		if _, err := postLedger(ctx, tx, entries); err != nil {
			return fmt.Errorf("ledger refund %s: %w", r.RefundOrderID, err)
		}
	}
	if r.NotifyURL != "" {
		if err := enqueueNotifyFields(ctx, q, "refund", refundNotifyFields(r), r.TenantID); err != nil {
			return fmt.Errorf("notify refund %s: %w", r.RefundOrderID, err)
		}
	}
	return nil
}

// onRefundTerminal 对已在终态的退款单补做终态动作。失败分支会退回额度，只能在 CAS 成功后调用一次；
// 正常路径由 finishRefund 原子完成。
func onRefundTerminal(ctx context.Context, refund Refund, to string) {
	r, err := findRefund(ctx, refund.RefundOrderID)
	if err != nil {
		log.Printf("refund %s terminal effects: %v", refund.RefundOrderID, err)
		return
	}
	if err := refundTerminalEffects(ctx, nil, r, to); err != nil {
		log.Printf("refund %s terminal effects: %v", refund.RefundOrderID, err)
	}
}

// ledgerSum 按商户（可选 kind 过滤）汇总。
func ledgerSum(ctx context.Context, mchNo, ifCode string, kinds []string) (int64, error) {
	var sum int64
	err := db.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM pay_ledger
		WHERE mch_no=$1 AND ($2='' OR if_code=$2) AND (cardinality($3::text[])=0 OR kind = ANY($3))`,
		mchNo, ifCode, kinds).Scan(&sum)
	return sum, err
}
