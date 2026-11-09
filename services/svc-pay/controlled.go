// 归集模式（specs/030 M4）：确认结算、提现、余额。直清渠道没有这些动作，接口回 400。
// 两个归集产品的资金去向不同（见 channel/controlled.go）：
//   - 收付通：分账 → 确认结算（完结分账，剩余资金释放到二级商户可用余额）→ 平台代为提现。
//     可提现台账 = settle 贷方（释放金额）+ refund 借方（结算后的退款）+ withdrawal 借方（发起即预占）
//   - withdrawal_reversal（失败或银行退票冲回）。
//     本次可提 = min(台账余额, 渠道可用余额 − 提交中的提现)：任何一边不够都不能提；
//     两边的差额作为漂移展示，用于对账核查，不阻断提现。
//   - 直付通：确认结算（需要分账时冻结）→ 分账（最后一笔解冻剩余）。资金直接到二级商户结算账户，
//     平台侧没有余额，没有提现。
//
// 结算、提现的提交结果未知时保持 pending，由 worker 用原单号重提（渠道按单号幂等）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"platfarm/svc-pay/channel"
)

const (
	settlePending = "pending"
	settleSuccess = "success"
	settleFailed  = "failed"

	withdrawPending  = "pending"
	withdrawAccepted = "accepted"
	withdrawSuccess  = "success"
	withdrawFailed   = "failed"

	orderSettleNone    = "none"
	orderSettlePending = "pending"
	orderSettleDone    = "done"
)

// withdrawableKinds 可提现余额台账的科目。
var withdrawableKinds = []string{ledgerSettle, ledgerRefund, ledgerWithdrawal, ledgerWithdrawalReversal}

// reqError 请求本身不成立（直清渠道、渠道没有该能力、参数非法）：接口回 400；其余业务拒绝回 409。
type reqError struct{ msg string }

func (e reqError) Error() string { return e.msg }

func badRequest(format string, a ...any) error { return reqError{fmt.Sprintf(format, a...)} }

var (
	errOverBalance   = errors.New("超出可提现额度")
	errNotConfigured = errors.New("渠道参数未配置")
)

// moneyStatus 结算/提现接口的错误码：请求不成立 400，其余（额度不足、状态不允许、参数未配置）409。
func moneyStatus(err error) int {
	var re reqError
	if errors.As(err, &re) {
		return http.StatusBadRequest
	}
	return http.StatusConflict
}

// Settle pay_settle 一行。
type Settle struct {
	SettleNo        string    `json:"settleNo"`
	PayOrderID      string    `json:"payOrderId"`
	MchNo           string    `json:"mchNo"`
	IsvNo           string    `json:"isvNo"`
	IfCode          string    `json:"ifCode"`
	OrderAmount     int64     `json:"orderAmount"`
	RefundedAmount  int64     `json:"refundedAmount"`
	SharedAmount    int64     `json:"sharedAmount"`
	Amount          int64     `json:"amount"`       // 本次确认结算金额
	CreditAmount    int64     `json:"creditAmount"` // 记入可提现台账的金额（直付通为 0）
	HoldForDivision bool      `json:"holdForDivision"`
	State           string    `json:"state"`
	ChannelOrderNo  string    `json:"channelOrderNo"`
	ErrCode         string    `json:"errCode"`
	ErrMsg          string    `json:"errMsg"`
	TenantID        int       `json:"tenantId"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

const settleCols = `settle_no, pay_order_id, mch_no, isv_no, if_code, order_amount, refunded_amount, shared_amount, amount,
	credit_amount, hold_for_division, state, channel_order_no, err_code, err_msg, tenant_id, created_at, updated_at`

func scanSettle(row pgx.Row) (Settle, error) {
	var s Settle
	err := row.Scan(&s.SettleNo, &s.PayOrderID, &s.MchNo, &s.IsvNo, &s.IfCode, &s.OrderAmount, &s.RefundedAmount,
		&s.SharedAmount, &s.Amount, &s.CreditAmount, &s.HoldForDivision, &s.State, &s.ChannelOrderNo, &s.ErrCode,
		&s.ErrMsg, &s.TenantID, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func findSettle(ctx context.Context, no string) (Settle, error) {
	return scanSettle(db.QueryRow(ctx, `SELECT `+settleCols+` FROM pay_settle WHERE settle_no=$1`, no))
}

func findActiveSettle(ctx context.Context, payOrderID string) (Settle, error) {
	return scanSettle(db.QueryRow(ctx, `SELECT `+settleCols+` FROM pay_settle WHERE pay_order_id=$1 AND state<>'failed'`, payOrderID))
}

// sharedAmount 订单上已成功分出的金额（台账 sharing 为负数，取绝对值）。
func sharedAmount(ctx context.Context, q querier, payOrderID string) (int64, error) {
	var sum int64
	err := q.QueryRow(ctx, `SELECT COALESCE(SUM(-l.amount),0) FROM pay_ledger l
		JOIN pay_division d ON d.division_no=l.ref_id
		WHERE l.kind=$1 AND l.ref_type='division' AND d.pay_order_id=$2 AND d.state='success'`,
		ledgerSharing, payOrderID).Scan(&sum)
	return sum, err
}

// refundedAmount 订单上已成功的退款合计（不含处理中的）。
func refundedAmount(ctx context.Context, q querier, payOrderID string) (int64, error) {
	var sum int64
	err := q.QueryRow(ctx, `SELECT COALESCE(SUM(refund_amount),0) FROM pay_refund
		WHERE pay_order_id=$1 AND state='success'`, payOrderID).Scan(&sum)
	return sum, err
}

// ── 确认结算 ────────────────────────────────────────────────

// createSettle 确认结算。已有未失败确认单则原样返回（dup）。
// 金额：直付通 = 交易金额 − 已退款（支付宝的硬性规则）；收付通 = 交易金额 − 已退款 − 已分账（完结分账释放剩余）。
// hold（只对直付通）：结算后冻结，之后才能分账。
func createSettle(ctx context.Context, mch Merchant, order Order, hold bool) (Settle, bool, error) {
	sc, err := channel.Settling(order.IfCode)
	if err != nil {
		return Settle{}, false, badRequest("%v", err)
	}
	withdrawable := channel.Withdrawable(order.IfCode)
	if hold && withdrawable {
		return Settle{}, false, badRequest("收付通的分账须在确认结算前完成，不支持结算后冻结")
	}
	if existing, err := findActiveSettle(ctx, order.PayOrderID); err == nil {
		return existing, true, nil
	}
	provider, sub, err := resolveParams(ctx, mch, order.IfCode)
	if err != nil {
		return Settle{}, false, errNotConfigured
	}
	s := Settle{
		SettleNo: newOrderID("S"), PayOrderID: order.PayOrderID, MchNo: mch.MchNo, IsvNo: mch.IsvNo,
		IfCode: order.IfCode, HoldForDivision: hold, State: settlePending, TenantID: mch.TenantID,
	}
	dup := false
	_, err = inTx(ctx, func(tx pgx.Tx) (bool, error) {
		// 锁住支付单：与发起退款、分账互斥，金额按锁内的状态计算。
		o, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM pay_order WHERE pay_order_id=$1 FOR UPDATE`, order.PayOrderID))
		if err != nil {
			return false, err
		}
		if o.State != orderSuccess {
			return false, fmt.Errorf("订单状态 %s 不可确认结算", o.State)
		}
		refunded, err := refundedAmount(ctx, tx, o.PayOrderID)
		if err != nil {
			return false, err
		}
		if o.RefundAmount > refunded {
			return false, fmt.Errorf("订单有处理中的退款，退款完结后再确认结算")
		}
		var shared int64
		if withdrawable {
			if o.DivisionState == orderDivisionPending {
				return false, fmt.Errorf("订单有处理中的分账，分账完结后再确认结算")
			}
			if shared, err = sharedAmount(ctx, tx, o.PayOrderID); err != nil {
				return false, err
			}
		} else if o.DivisionState != orderDivisionNone {
			return false, fmt.Errorf("直付通须先确认结算再分账")
		}
		s.OrderAmount, s.RefundedAmount, s.SharedAmount = o.Amount, refunded, shared
		s.Amount = o.Amount - refunded - shared
		if s.Amount <= 0 {
			return false, fmt.Errorf("没有可确认结算的金额（订单 %d，已退款 %d，已分账 %d）", o.Amount, refunded, shared)
		}
		if withdrawable {
			s.CreditAmount = s.Amount
		}
		_, err = tx.Exec(ctx, `INSERT INTO pay_settle (settle_no, pay_order_id, mch_no, isv_no, if_code, order_amount,
			refunded_amount, shared_amount, amount, credit_amount, hold_for_division, tenant_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			s.SettleNo, s.PayOrderID, s.MchNo, s.IsvNo, s.IfCode, s.OrderAmount, s.RefundedAmount, s.SharedAmount,
			s.Amount, s.CreditAmount, s.HoldForDivision, s.TenantID)
		if isUniqueViolation(err) {
			dup = true
			return false, nil
		}
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE pay_order SET settle_state='pending', updated_at=now() WHERE pay_order_id=$1`, o.PayOrderID)
		return err == nil, err
	})
	if dup {
		existing, ferr := findActiveSettle(ctx, order.PayOrderID)
		return existing, true, ferr
	}
	if err != nil {
		return Settle{}, false, err
	}
	logSettle(s.SettleNo, "", settlePending, order.PayOrderID)
	saved, err := submitSettle(ctx, sc, s, order.ChannelOrderNo, provider, sub)
	return saved, false, err
}

// submitSettle 用结算单号向渠道确认结算并落结果（首次提交与恢复重提共用）。
func submitSettle(ctx context.Context, sc channel.SettleChannel, s Settle, channelOrderNo string, provider, sub map[string]string) (Settle, error) {
	res, err := sc.SettleConfirm(ctx, channel.SettleIn{
		SettleNo: s.SettleNo, PayOrderID: s.PayOrderID, ChannelOrderNo: channelOrderNo, Amount: s.Amount,
		HoldForDivision: s.HoldForDivision, Provider: provider, Sub: sub,
	})
	if err != nil {
		// 适配器在发出请求前就拒绝了：渠道侧没有这笔结算，确定失败。
		res = channel.Result{State: channel.StateFail, ErrCode: "SYS", ErrMsg: err.Error()}
	}
	return applySettleResult(ctx, s, res)
}

// applySettleResult SUCCESS：结算单 success、订单 done、可提现台账贷方，同一事务；FAIL：释放订单；
// WAITING / UNKNOWN：留在 pending，由 worker 用同一结算单号重提。
func applySettleResult(ctx context.Context, s Settle, res channel.Result) (Settle, error) {
	from := s.State
	var err error
	switch res.State {
	case channel.StateSuccess:
		var advanced bool
		advanced, err = inTx(ctx, func(tx pgx.Tx) (bool, error) {
			tag, err := tx.Exec(ctx, `UPDATE pay_settle SET state='success', channel_order_no=COALESCE(NULLIF($3,''), channel_order_no),
				err_code='', err_msg=$4, updated_at=now() WHERE settle_no=$1 AND state=$2`, s.SettleNo, from, res.ChannelOrderNo, trunc(res.ErrMsg, 500))
			if err != nil || tag.RowsAffected() == 0 {
				return false, err
			}
			if _, err := tx.Exec(ctx, `UPDATE pay_order SET settle_state='done', updated_at=now() WHERE pay_order_id=$1`, s.PayOrderID); err != nil {
				return false, err
			}
			_, err = postLedger(ctx, tx, []LedgerEntry{{
				MchNo: s.MchNo, IsvNo: s.IsvNo, IfCode: s.IfCode, Kind: ledgerSettle, RefType: "settle", RefID: s.SettleNo,
				Amount: s.CreditAmount, Memo: "确认结算 " + s.PayOrderID, TenantID: s.TenantID,
			}})
			return err == nil, err
		})
		if advanced {
			logSettle(s.SettleNo, from, settleSuccess, res.ChannelOrderNo)
		}
	case channel.StateFail:
		var advanced bool
		advanced, err = inTx(ctx, func(tx pgx.Tx) (bool, error) {
			tag, err := tx.Exec(ctx, `UPDATE pay_settle SET state='failed', err_code=$3, err_msg=$4, updated_at=now()
				WHERE settle_no=$1 AND state=$2`, s.SettleNo, from, res.ErrCode, trunc(res.ErrMsg, 500))
			if err != nil || tag.RowsAffected() == 0 {
				return false, err
			}
			_, err = tx.Exec(ctx, `UPDATE pay_order SET settle_state='none', updated_at=now()
				WHERE pay_order_id=$1 AND settle_state='pending'`, s.PayOrderID)
			return err == nil, err
		})
		if advanced {
			logSettle(s.SettleNo, from, settleFailed, res.ErrCode)
		}
	default:
		_, err = db.Exec(ctx, `UPDATE pay_settle SET err_code=$2, err_msg=$3, updated_at=now() WHERE settle_no=$1 AND state='pending'`,
			s.SettleNo, res.ErrCode, trunc(res.ErrMsg, 500))
	}
	if err != nil {
		return s, err
	}
	return findSettle(ctx, s.SettleNo)
}

func logSettle(no, from, to, note string) {
	log.Printf(`{"event":"settle","settleNo":%q,"from":%q,"to":%q,"note":%q}`, no, from, to, note)
}

// resubmitSettle 结果未知的结算用原结算单号重提。
func resubmitSettle(ctx context.Context, s Settle) (Settle, error) {
	sc, err := channel.Settling(s.IfCode)
	if err != nil {
		return s, err
	}
	mch, err := loadMerchant(ctx, s.MchNo)
	if err != nil {
		return s, err
	}
	provider, sub, err := resolveParams(ctx, mch, s.IfCode)
	if err != nil {
		return s, err
	}
	order, err := findOrder(ctx, s.PayOrderID)
	if err != nil {
		return s, err
	}
	return submitSettle(ctx, sc, s, order.ChannelOrderNo, provider, sub)
}

// ── 提现 ────────────────────────────────────────────────────

// Withdrawal pay_withdrawal 一行。
type Withdrawal struct {
	WithdrawNo     string    `json:"withdrawNo"`
	MchNo          string    `json:"mchNo"`
	IsvNo          string    `json:"isvNo"`
	IfCode         string    `json:"ifCode"`
	MchWithdrawNo  string    `json:"mchWithdrawNo"`
	Amount         int64     `json:"amount"`
	Remark         string    `json:"remark"`
	State          string    `json:"state"`
	ChannelOrderNo string    `json:"channelOrderNo"`
	ErrCode        string    `json:"errCode"`
	ErrMsg         string    `json:"errMsg"`
	CreatedBy      string    `json:"createdBy"`
	TenantID       int       `json:"tenantId"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

const withdrawCols = `withdraw_no, mch_no, isv_no, if_code, mch_withdraw_no, amount, remark, state,
	channel_order_no, err_code, err_msg, created_by, tenant_id, created_at, updated_at`

func scanWithdraw(row pgx.Row) (Withdrawal, error) {
	var w Withdrawal
	err := row.Scan(&w.WithdrawNo, &w.MchNo, &w.IsvNo, &w.IfCode, &w.MchWithdrawNo, &w.Amount, &w.Remark, &w.State,
		&w.ChannelOrderNo, &w.ErrCode, &w.ErrMsg, &w.CreatedBy, &w.TenantID, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

func findWithdraw(ctx context.Context, no string) (Withdrawal, error) {
	return scanWithdraw(db.QueryRow(ctx, `SELECT `+withdrawCols+` FROM pay_withdrawal WHERE withdraw_no=$1`, no))
}

func findWithdrawByMch(ctx context.Context, mchNo, mchWithdrawNo string) (Withdrawal, error) {
	return scanWithdraw(db.QueryRow(ctx, `SELECT `+withdrawCols+` FROM pay_withdrawal WHERE mch_no=$1 AND mch_withdraw_no=$2`, mchNo, mchWithdrawNo))
}

// BalanceView 可提现余额：台账与渠道两边，以及本次最多可提的金额。
type BalanceView struct {
	MchNo        string `json:"mchNo"`
	IfCode       string `json:"ifCode"`
	Ledger       int64  `json:"ledger"`       // 台账可提现余额（发起的提现已预占）
	Available    int64  `json:"available"`    // 渠道报的可用余额
	Pending      int64  `json:"pending"`      // 渠道报的不可用（冻结）余额
	Submitting   int64  `json:"submitting"`   // 已发起、渠道尚未确认受理的提现
	Withdrawable int64  `json:"withdrawable"` // 本次最多可提 = min(ledger, available − submitting)
	Drift        int64  `json:"drift"`        // available − submitting − ledger；非 0 需对账核查，不阻断提现
}

// ledgerPosition 台账可提现余额与提交中（渠道尚未受理）的提现合计。
func ledgerPosition(ctx context.Context, q querier, mchNo, ifCode string) (ledger, submitting int64, err error) {
	err = q.QueryRow(ctx, `SELECT
		(SELECT COALESCE(SUM(amount),0) FROM pay_ledger WHERE mch_no=$1 AND if_code=$2 AND kind = ANY($3)),
		(SELECT COALESCE(SUM(amount),0) FROM pay_withdrawal WHERE mch_no=$1 AND if_code=$2 AND state='pending')`,
		mchNo, ifCode, withdrawableKinds).Scan(&ledger, &submitting)
	return ledger, submitting, err
}

func balanceView(mchNo, ifCode string, ledger, submitting int64, b channel.Balance) BalanceView {
	v := BalanceView{MchNo: mchNo, IfCode: ifCode, Ledger: ledger, Available: b.Available, Pending: b.Pending, Submitting: submitting}
	v.Withdrawable = max(min(ledger, b.Available-submitting), 0)
	v.Drift = b.Available - submitting - ledger
	return v
}

// merchantBalance 可提现余额。直清与直付通没有平台侧余额（400）。
func merchantBalance(ctx context.Context, mch Merchant, ifCode string) (BalanceView, error) {
	wc, err := channel.Withdrawing(ifCode)
	if err != nil {
		return BalanceView{}, badRequest("%v", err)
	}
	provider, sub, err := resolveParams(ctx, mch, ifCode)
	if err != nil {
		return BalanceView{}, errNotConfigured
	}
	b, err := wc.Balance(ctx, provider, sub)
	if err != nil {
		return BalanceView{}, err
	}
	ledger, submitting, err := ledgerPosition(ctx, db, mch.MchNo, ifCode)
	if err != nil {
		return BalanceView{}, err
	}
	return balanceView(mch.MchNo, ifCode, ledger, submitting, b), nil
}

// createWithdrawal 提现。同一 (mch_no, mch_withdraw_no) 返回原单（dup）。
// 同一商户同一渠道的提现串行：锁内计算额度、落单并立即记借方（预占），并发请求不能用同一笔余额；
// 失败（含银行退票）冲回。
func createWithdrawal(ctx context.Context, mch Merchant, ifCode, mchWithdrawNo string, amount int64, remark, by string) (Withdrawal, bool, error) {
	if amount <= 0 {
		return Withdrawal{}, false, badRequest("amount 必须为正")
	}
	if existing, err := findWithdrawByMch(ctx, mch.MchNo, mchWithdrawNo); err == nil {
		return existing, true, nil
	}
	wc, err := channel.Withdrawing(ifCode)
	if err != nil {
		return Withdrawal{}, false, badRequest("%v", err)
	}
	provider, sub, err := resolveParams(ctx, mch, ifCode)
	if err != nil {
		return Withdrawal{}, false, errNotConfigured
	}
	bal, err := wc.Balance(ctx, provider, sub)
	if err != nil {
		return Withdrawal{}, false, fmt.Errorf("查询渠道余额失败: %w", err)
	}
	w := Withdrawal{
		WithdrawNo: newOrderID("W"), MchNo: mch.MchNo, IsvNo: mch.IsvNo, IfCode: ifCode,
		MchWithdrawNo: mchWithdrawNo, Amount: amount, Remark: remark, State: withdrawPending,
		CreatedBy: by, TenantID: mch.TenantID,
	}
	dup := false
	_, err = inTx(ctx, func(tx pgx.Tx) (bool, error) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "svc-pay:withdraw:"+mch.MchNo+":"+ifCode); err != nil {
			return false, err
		}
		ledger, submitting, err := ledgerPosition(ctx, tx, mch.MchNo, ifCode)
		if err != nil {
			return false, err
		}
		if v := balanceView(mch.MchNo, ifCode, ledger, submitting, bal); amount > v.Withdrawable {
			return false, fmt.Errorf("%w：本次最多可提 %d（台账 %d，渠道可用 %d，提交中 %d）",
				errOverBalance, v.Withdrawable, ledger, bal.Available, submitting)
		}
		_, err = tx.Exec(ctx, `INSERT INTO pay_withdrawal (withdraw_no, mch_no, isv_no, if_code, mch_withdraw_no, amount, remark, created_by, tenant_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			w.WithdrawNo, w.MchNo, w.IsvNo, w.IfCode, w.MchWithdrawNo, w.Amount, w.Remark, w.CreatedBy, w.TenantID)
		if isUniqueViolation(err) {
			dup = true
			return false, nil
		}
		if err != nil {
			return false, err
		}
		_, err = postLedger(ctx, tx, []LedgerEntry{{
			MchNo: w.MchNo, IsvNo: w.IsvNo, IfCode: w.IfCode, Kind: ledgerWithdrawal, RefType: "withdrawal", RefID: w.WithdrawNo,
			Amount: -w.Amount, Memo: "提现 " + w.MchWithdrawNo, TenantID: w.TenantID,
		}})
		return err == nil, err
	})
	if dup {
		existing, ferr := findWithdrawByMch(ctx, mch.MchNo, mchWithdrawNo)
		return existing, true, ferr
	}
	if err != nil {
		return Withdrawal{}, false, err
	}
	logWithdraw(w.WithdrawNo, "", withdrawPending, mchWithdrawNo)
	saved, err := submitWithdraw(ctx, wc, w, provider, sub)
	return saved, false, err
}

// submitWithdraw 用提现单号向渠道发起提现并落结果（首次提交与恢复重提共用）。
func submitWithdraw(ctx context.Context, wc channel.WithdrawChannel, w Withdrawal, provider, sub map[string]string) (Withdrawal, error) {
	res, err := wc.Withdraw(ctx, channel.WithdrawIn{WithdrawNo: w.WithdrawNo, Amount: w.Amount, Remark: w.Remark, Provider: provider, Sub: sub})
	if err != nil {
		res = channel.Result{State: channel.StateFail, ErrCode: "SYS", ErrMsg: err.Error()}
	}
	return applyWithdrawResult(ctx, w, res)
}

// applyWithdrawResult 借方在发起时已记：SUCCESS 只推状态；FAIL（含成功后的银行退票）冲回借方，与状态同事务；
// WAITING：pending → accepted；UNKNOWN：不动，只记原因（pending 由 worker 用原单号重提）。
func applyWithdrawResult(ctx context.Context, w Withdrawal, res channel.Result) (Withdrawal, error) {
	from := w.State
	var err error
	switch res.State {
	case channel.StateSuccess:
		var tag interface{ RowsAffected() int64 }
		tag, err = db.Exec(ctx, `UPDATE pay_withdrawal SET state='success', channel_order_no=COALESCE(NULLIF($3,''), channel_order_no),
			err_code='', err_msg='', updated_at=now() WHERE withdraw_no=$1 AND state=$2`, w.WithdrawNo, from, res.ChannelOrderNo)
		if err == nil && tag.RowsAffected() == 1 && from != withdrawSuccess {
			logWithdraw(w.WithdrawNo, from, withdrawSuccess, res.ChannelOrderNo)
		}
	case channel.StateFail:
		var advanced bool
		advanced, err = inTx(ctx, func(tx pgx.Tx) (bool, error) {
			tag, err := tx.Exec(ctx, `UPDATE pay_withdrawal SET state='failed', err_code=$3, err_msg=$4, updated_at=now()
				WHERE withdraw_no=$1 AND state=$2`, w.WithdrawNo, from, res.ErrCode, trunc(res.ErrMsg, 500))
			if err != nil || tag.RowsAffected() == 0 {
				return false, err
			}
			memo := "提现失败冲回 " + w.MchWithdrawNo
			if from == withdrawSuccess {
				memo = "提现退回（银行退票）" + w.MchWithdrawNo
			}
			_, err = postLedger(ctx, tx, []LedgerEntry{{
				MchNo: w.MchNo, IsvNo: w.IsvNo, IfCode: w.IfCode, Kind: ledgerWithdrawalReversal, RefType: "withdrawal", RefID: w.WithdrawNo,
				Amount: w.Amount, Memo: memo, TenantID: w.TenantID,
			}})
			return err == nil, err
		})
		if advanced {
			logWithdraw(w.WithdrawNo, from, withdrawFailed, res.ErrCode)
		}
	case channel.StateWaiting:
		if from == withdrawPending {
			var tag interface{ RowsAffected() int64 }
			tag, err = db.Exec(ctx, `UPDATE pay_withdrawal SET state='accepted', channel_order_no=COALESCE(NULLIF($2,''), channel_order_no),
				updated_at=now() WHERE withdraw_no=$1 AND state='pending'`, w.WithdrawNo, res.ChannelOrderNo)
			if err == nil && tag.RowsAffected() == 1 {
				logWithdraw(w.WithdrawNo, from, withdrawAccepted, res.ChannelOrderNo)
			}
		} else {
			_, err = db.Exec(ctx, `UPDATE pay_withdrawal SET updated_at=now() WHERE withdraw_no=$1 AND state=$2`, w.WithdrawNo, from)
		}
	default:
		_, err = db.Exec(ctx, `UPDATE pay_withdrawal SET err_code=$2, err_msg=$3, updated_at=now() WHERE withdraw_no=$1 AND state=$4`,
			w.WithdrawNo, res.ErrCode, trunc(res.ErrMsg, 500), from)
	}
	if err != nil {
		return w, err
	}
	return findWithdraw(ctx, w.WithdrawNo)
}

func logWithdraw(no, from, to, note string) {
	log.Printf(`{"event":"withdrawal","withdrawNo":%q,"from":%q,"to":%q,"note":%q}`, no, from, to, note)
}

// withdrawTarget 提现单对应的渠道与参数。
func withdrawTarget(ctx context.Context, w Withdrawal) (channel.WithdrawChannel, map[string]string, map[string]string, error) {
	wc, err := channel.Withdrawing(w.IfCode)
	if err != nil {
		return nil, nil, nil, err
	}
	mch, err := loadMerchant(ctx, w.MchNo)
	if err != nil {
		return nil, nil, nil, err
	}
	provider, sub, err := resolveParams(ctx, mch, w.IfCode)
	return wc, provider, sub, err
}

// syncWithdrawal 查渠道推进提现（accepted 的终态；success 的银行退票）。
func syncWithdrawal(ctx context.Context, w Withdrawal) (Withdrawal, error) {
	wc, provider, sub, err := withdrawTarget(ctx, w)
	if err != nil {
		return w, err
	}
	res, err := wc.QueryWithdraw(ctx, channel.WithdrawRef{WithdrawNo: w.WithdrawNo, ChannelOrderNo: w.ChannelOrderNo, Provider: provider, Sub: sub})
	if err != nil {
		return w, err
	}
	return applyWithdrawResult(ctx, w, res)
}

// resubmitWithdrawal 提交结果未知的提现用原提现单号重提（渠道按 out_request_no 幂等）。
func resubmitWithdrawal(ctx context.Context, w Withdrawal) (Withdrawal, error) {
	wc, provider, sub, err := withdrawTarget(ctx, w)
	if err != nil {
		return w, err
	}
	return submitWithdraw(ctx, wc, w, provider, sub)
}

// withdrawalWorker 每分钟：pending 的用原单号重提；accepted 的查询终态；
// 成功后 10 天内每 6 小时复查一次（微信提现成功后仍可能银行退票，变为 REFUND）。
// 同一轮也恢复结果未知的确认结算。
func withdrawalWorker(ctx context.Context) {
	everyLeased(ctx, "controlled", 60*time.Second, sweepControlled)
}

func sweepControlled(ctx context.Context) error {
	rows, err := db.Query(ctx, `SELECT `+settleCols+` FROM pay_settle
		WHERE state='pending' AND updated_at < now() - interval '1 minute' ORDER BY updated_at LIMIT 50`)
	if err != nil {
		return err
	}
	var settles []Settle
	for rows.Next() {
		if s, serr := scanSettle(rows); serr == nil {
			settles = append(settles, s)
		}
	}
	rows.Close()
	for _, s := range settles {
		if _, err := resubmitSettle(ctx, s); err != nil {
			log.Printf("settle %s recover: %v", s.SettleNo, err)
			_, _ = db.Exec(ctx, `UPDATE pay_settle SET err_msg=$2, updated_at=now() WHERE settle_no=$1 AND state='pending'`, s.SettleNo, trunc(err.Error(), 500))
		}
	}

	rows, err = db.Query(ctx, `SELECT `+withdrawCols+` FROM pay_withdrawal
		WHERE (state='pending' AND updated_at < now() - interval '1 minute')
		   OR (state='accepted' AND updated_at < now() - interval '30 seconds')
		   OR (state='success' AND created_at > now() - interval '10 days' AND updated_at < now() - interval '6 hours')
		ORDER BY updated_at LIMIT 50`)
	if err != nil {
		return err
	}
	var batch []Withdrawal
	for rows.Next() {
		if w, serr := scanWithdraw(rows); serr == nil {
			batch = append(batch, w)
		}
	}
	rows.Close()
	for _, w := range batch {
		var err error
		if w.State == withdrawPending {
			_, err = resubmitWithdrawal(ctx, w)
		} else {
			_, err = syncWithdrawal(ctx, w)
		}
		if err != nil {
			log.Printf("withdrawal %s recover: %v", w.WithdrawNo, err)
			_, _ = db.Exec(ctx, `UPDATE pay_withdrawal SET err_msg=$2, updated_at=now() WHERE withdraw_no=$1`, w.WithdrawNo, trunc(err.Error(), 500))
		}
	}
	return nil
}

// ── 开放 API ────────────────────────────────────────────────

type withdrawIn struct {
	IfCode        string `json:"ifCode"`
	MchWithdrawNo string `json:"mchWithdrawNo"`
	Amount        int64  `json:"amount"`
	Remark        string `json:"remark"`
}

// handleCreateWithdrawal POST /v1/withdrawals（scope pay.withdrawal.create + 商户签名）。
func handleCreateWithdrawal(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyByte))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求失败"})
		return
	}
	app, mch, ok := signedApp(c, body)
	if !ok {
		return
	}
	var in withdrawIn
	if json.Unmarshal(body, &in) != nil || in.IfCode == "" || in.MchWithdrawNo == "" || in.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ifCode、mchWithdrawNo、amount 必填"})
		return
	}
	w, dup, err := createWithdrawal(c.Request.Context(), mch, in.IfCode, in.MchWithdrawNo, in.Amount, in.Remark, "app:"+app.AppID)
	if err != nil {
		c.JSON(moneyStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"withdrawal": w, "idempotent": dup})
}

// handleQueryWithdrawal GET /v1/withdrawals/:mchWithdrawNo。
func handleQueryWithdrawal(c *gin.Context) {
	app, _, ok := signedApp(c, nil)
	if !ok {
		return
	}
	w, err := findWithdrawByMch(c.Request.Context(), app.MchNo, c.Param("mchWithdrawNo"))
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "提现单不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"withdrawal": w})
}

// handleSettleOrder POST /v1/orders/:mchOrderNo/settle {holdForDivision}：归集模式确认结算。
func handleSettleOrder(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyByte))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求失败"})
		return
	}
	app, mch, ok := signedApp(c, body)
	if !ok {
		return
	}
	var in struct {
		HoldForDivision bool `json:"holdForDivision"`
	}
	if len(body) > 0 && json.Unmarshal(body, &in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	ctx := c.Request.Context()
	order, err := findOrderByMch(ctx, mch.MchNo, c.Param("mchOrderNo"))
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "支付订单不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if order.AppID != app.AppID {
		c.JSON(http.StatusForbidden, gin.H{"error": "订单不属于该应用"})
		return
	}
	s, dup, err := createSettle(ctx, mch, order, in.HoldForDivision)
	if err != nil {
		c.JSON(moneyStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"settle": s, "idempotent": dup})
}

// handleBalance GET /v1/balance?ifCode=。
func handleBalance(c *gin.Context) {
	_, mch, ok := signedApp(c, nil)
	if !ok {
		return
	}
	ifCode := c.Query("ifCode")
	if ifCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ifCode 必填"})
		return
	}
	writeBalance(c, mch, ifCode)
}

func writeBalance(c *gin.Context, mch Merchant, ifCode string) {
	v, err := merchantBalance(c.Request.Context(), mch, ifCode)
	var re reqError
	switch {
	case errors.As(err, &re):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, errNotConfigured):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, v)
	}
}

// ── 管理接口 ─────────────────────────────────────────────────

// handleAdminBalance GET /admin/balance?mchNo=&ifCode=
func handleAdminBalance(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	mchNo, ifCode := c.Query("mchNo"), c.Query("ifCode")
	if mchNo == "" || ifCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchNo、ifCode 必填"})
		return
	}
	mch, err := loadMerchant(c.Request.Context(), mchNo)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "商户不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if denyOtherTenant(c, claims, mch.TenantID, "商户") {
		return
	}
	writeBalance(c, mch, ifCode)
}

// handleListWithdrawals GET /admin/withdrawals?mchNo=&state=
func handleListWithdrawals(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	scope, args := tenantScope(claims)
	sql := `SELECT ` + withdrawCols + ` FROM pay_withdrawal WHERE ` + scope
	if m := c.Query("mchNo"); m != "" {
		args = append(args, m)
		sql += fmt.Sprintf(` AND mch_no=$%d`, len(args))
	}
	if s := c.Query("state"); s != "" {
		args = append(args, s)
		sql += fmt.Sprintf(` AND state=$%d`, len(args))
	}
	sql += ` ORDER BY created_at DESC LIMIT 200`
	rows, err := db.Query(c.Request.Context(), sql, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []Withdrawal{}
	for rows.Next() {
		if w, serr := scanWithdraw(rows); serr == nil {
			items = append(items, w)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// handleSyncWithdrawal POST /admin/withdrawals/:no/sync：pending 重提，其余查询。
func handleSyncWithdrawal(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	w, err := findWithdraw(c.Request.Context(), c.Param("no"))
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "提现单不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if denyOtherTenant(c, claims, w.TenantID, "提现单") {
		return
	}
	if w.State == withdrawFailed {
		c.JSON(http.StatusOK, gin.H{"withdrawal": w})
		return
	}
	if w.State == withdrawPending {
		w, err = resubmitWithdrawal(c.Request.Context(), w)
	} else {
		w, err = syncWithdrawal(c.Request.Context(), w)
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"withdrawal": w})
}

func registerControlledRoutes(admin *gin.RouterGroup) {
	admin.GET("/balance", handleAdminBalance)
	admin.GET("/withdrawals", handleListWithdrawals)
	admin.POST("/withdrawals/:no/sync", handleSyncWithdrawal)
}
