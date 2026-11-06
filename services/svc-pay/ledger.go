// 台账（specs/030 M3/M4）。pay_ledger 追加写，(kind, ref_type, ref_id, ref_sub) 唯一：
// 同一事件重放不会记两次，所以可以在任何 CAS 成功路径上无脑调用。
// 直清模式只记 fee / rebate 及其退款冲回；分账记 sharing；归集模式再加 settle / withdrawal。
package main

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5"
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

// onOrderTerminal 支付单到终态后的统一动作：成功记台账；成功/失败都入下游通知。
func onOrderTerminal(ctx context.Context, payOrderID, to string) {
	if to == orderSuccess {
		if o, err := findOrder(ctx, payOrderID); err == nil {
			if _, err := postLedger(ctx, nil, orderSettledEntries(o)); err != nil {
				log.Printf("ledger order %s: %v", payOrderID, err)
			}
		}
	}
	if to == orderSuccess || to == orderFailed {
		_ = enqueueNotify(ctx, "pay", payOrderID)
	}
}

// onRefundTerminal 退款单到终态：回写支付单 refund_state；成功记冲回台账；入通知。
func onRefundTerminal(ctx context.Context, refund Refund, to string) {
	success := to == refundSuccess
	_ = rollupRefund(ctx, refund.PayOrderID, refund.RefundAmount, success)
	if success {
		if o, err := findOrder(ctx, refund.PayOrderID); err == nil {
			if _, err := postLedger(ctx, nil, refundSettledEntries(o, refund)); err != nil {
				log.Printf("ledger refund %s: %v", refund.RefundOrderID, err)
			}
		}
	}
	if refund.NotifyURL != "" {
		_ = enqueueNotify(ctx, "refund", refund.RefundOrderID)
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
