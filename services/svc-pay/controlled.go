// 归集模式（specs/030 M4）：确认结算 + 提现 + 余额。
// 直清渠道没有这些动作：channel.Controlled() 对直清渠道返回错误，接口回 400。
// 余额 = 台账合计（settle 贷方、withdrawal 借方、withdrawal_reversal 冲回）。
// 提现前用渠道报的余额与台账核对：不一致拒绝，避免在账不平的时候把钱划走。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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

// Settle pay_settle 一行。
type Settle struct {
	SettleNo       string    `json:"settleNo"`
	PayOrderID     string    `json:"payOrderId"`
	MchNo          string    `json:"mchNo"`
	IsvNo          string    `json:"isvNo"`
	IfCode         string    `json:"ifCode"`
	OrderAmount    int64     `json:"orderAmount"`
	SharedAmount   int64     `json:"sharedAmount"`
	Amount         int64     `json:"amount"`
	State          string    `json:"state"`
	ChannelOrderNo string    `json:"channelOrderNo"`
	ErrCode        string    `json:"errCode"`
	ErrMsg         string    `json:"errMsg"`
	TenantID       int       `json:"tenantId"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

const settleCols = `settle_no, pay_order_id, mch_no, isv_no, if_code, order_amount, shared_amount, amount,
	state, channel_order_no, err_code, err_msg, tenant_id, created_at, updated_at`

func scanSettle(row pgx.Row) (Settle, error) {
	var s Settle
	err := row.Scan(&s.SettleNo, &s.PayOrderID, &s.MchNo, &s.IsvNo, &s.IfCode, &s.OrderAmount, &s.SharedAmount,
		&s.Amount, &s.State, &s.ChannelOrderNo, &s.ErrCode, &s.ErrMsg, &s.TenantID, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func findSettle(ctx context.Context, no string) (Settle, error) {
	return scanSettle(db.QueryRow(ctx, `SELECT `+settleCols+` FROM pay_settle WHERE settle_no=$1`, no))
}

func findActiveSettle(ctx context.Context, payOrderID string) (Settle, error) {
	return scanSettle(db.QueryRow(ctx, `SELECT `+settleCols+` FROM pay_settle WHERE pay_order_id=$1 AND state<>'failed'`, payOrderID))
}

// sharedAmount 订单上已成功分出的金额（台账 sharing 为负数，取绝对值）。
func sharedAmount(ctx context.Context, payOrderID string) (int64, error) {
	var sum int64
	err := db.QueryRow(ctx, `SELECT COALESCE(SUM(-l.amount),0) FROM pay_ledger l
		JOIN pay_division d ON d.division_no=l.ref_id
		WHERE l.kind=$1 AND l.ref_type='division' AND d.pay_order_id=$2 AND d.state='success'`,
		ledgerSharing, payOrderID).Scan(&sum)
	return sum, err
}

// ── 结算确认 ────────────────────────────────────────────────

// createSettle 确认结算。已有未失败确认单则原样返回（dup）。
// 金额 = 订单金额 - 已分账金额；归集模式下分账是从这笔订单里划走的，剩下的才归子商户。
func createSettle(ctx context.Context, mch Merchant, order Order) (Settle, bool, error) {
	if !channel.IsControlled(order.IfCode) {
		return Settle{}, false, fmt.Errorf("channel %s 是直清模式，没有结算确认", order.IfCode)
	}
	if order.State != orderSuccess {
		return Settle{}, false, fmt.Errorf("订单状态 %s 不可确认结算", order.State)
	}
	if existing, err := findActiveSettle(ctx, order.PayOrderID); err == nil {
		return existing, true, nil
	}
	cc, err := channel.Controlled(order.IfCode)
	if err != nil {
		return Settle{}, false, err
	}
	provider, sub, err := resolveParams(ctx, mch, order.IfCode)
	if err != nil {
		return Settle{}, false, fmt.Errorf("渠道参数未配置")
	}
	shared, err := sharedAmount(ctx, order.PayOrderID)
	if err != nil {
		return Settle{}, false, err
	}
	amount := order.Amount - shared
	if amount < 0 {
		return Settle{}, false, fmt.Errorf("已分账 %d 超过订单金额 %d", shared, order.Amount)
	}
	s := Settle{
		SettleNo: newOrderID("S"), PayOrderID: order.PayOrderID, MchNo: mch.MchNo, IsvNo: mch.IsvNo,
		IfCode: order.IfCode, OrderAmount: order.Amount, SharedAmount: shared, Amount: amount,
		State: settlePending, TenantID: mch.TenantID,
	}
	_, err = db.Exec(ctx, `INSERT INTO pay_settle (settle_no, pay_order_id, mch_no, isv_no, if_code, order_amount,
		shared_amount, amount, tenant_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		s.SettleNo, s.PayOrderID, s.MchNo, s.IsvNo, s.IfCode, s.OrderAmount, s.SharedAmount, s.Amount, s.TenantID)
	if isUniqueViolation(err) {
		existing, ferr := findActiveSettle(ctx, order.PayOrderID)
		return existing, true, ferr
	}
	if err != nil {
		return Settle{}, false, err
	}
	_, _ = db.Exec(ctx, `UPDATE pay_order SET settle_state='pending', updated_at=now()
		WHERE pay_order_id=$1 AND settle_state='none'`, order.PayOrderID)
	logSettle(s.SettleNo, "", settlePending, order.PayOrderID)

	res, err := cc.SettleConfirm(ctx, channel.SettleIn{
		PayOrderID: order.PayOrderID, ChannelOrderNo: order.ChannelOrderNo, Amount: amount, Provider: provider, Sub: sub,
	})
	if err != nil {
		return s, false, err
	}
	saved, aerr := applySettleResult(ctx, s, res)
	return saved, false, aerr
}

// applySettleResult SUCCESS 记 settle 台账（贷方，子商户可用余额增加）并把订单标 done；FAIL 释放订单。
func applySettleResult(ctx context.Context, s Settle, res channel.Result) (Settle, error) {
	from := s.State
	switch res.State {
	case channel.StateSuccess:
		tag, err := db.Exec(ctx, `UPDATE pay_settle SET state='success', channel_order_no=COALESCE(NULLIF($3,''), channel_order_no),
			err_code='', err_msg='', updated_at=now() WHERE settle_no=$1 AND state=$2`, s.SettleNo, from, res.ChannelOrderNo)
		if err != nil {
			return s, err
		}
		if tag.RowsAffected() == 1 {
			logSettle(s.SettleNo, from, settleSuccess, res.ChannelOrderNo)
			_, _ = db.Exec(ctx, `UPDATE pay_order SET settle_state='done', updated_at=now() WHERE pay_order_id=$1`, s.PayOrderID)
			if _, err := postLedger(ctx, nil, []LedgerEntry{{
				MchNo: s.MchNo, IsvNo: s.IsvNo, IfCode: s.IfCode, Kind: ledgerSettle, RefType: "settle", RefID: s.SettleNo,
				Amount: s.Amount, Memo: "确认结算 " + s.PayOrderID, TenantID: s.TenantID,
			}}); err != nil {
				log.Printf("ledger settle %s: %v", s.SettleNo, err)
			}
		}
	case channel.StateFail:
		tag, err := db.Exec(ctx, `UPDATE pay_settle SET state='failed', err_code=$3, err_msg=$4, updated_at=now()
			WHERE settle_no=$1 AND state=$2`, s.SettleNo, from, res.ErrCode, trunc(res.ErrMsg, 500))
		if err != nil {
			return s, err
		}
		if tag.RowsAffected() == 1 {
			logSettle(s.SettleNo, from, settleFailed, res.ErrCode)
			_, _ = db.Exec(ctx, `UPDATE pay_order SET settle_state='none', updated_at=now()
				WHERE pay_order_id=$1 AND settle_state='pending'`, s.PayOrderID)
		}
	default:
		_, _ = db.Exec(ctx, `UPDATE pay_settle SET err_code=$2, err_msg=$3, updated_at=now() WHERE settle_no=$1`,
			s.SettleNo, res.ErrCode, trunc(res.ErrMsg, 500))
	}
	return findSettle(ctx, s.SettleNo)
}

func logSettle(no, from, to, note string) {
	log.Printf(`{"event":"settle","settleNo":%q,"from":%q,"to":%q,"note":%q}`, no, from, to, note)
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

// BalanceView 台账余额与渠道余额的对照。Invariant 为真表示两边一致。
type BalanceView struct {
	MchNo     string `json:"mchNo"`
	IfCode    string `json:"ifCode"`
	Ledger    int64  `json:"ledger"`    // 台账合计（settle - withdrawal + reversal）
	Available int64  `json:"available"` // 渠道报的可用余额
	Pending   int64  `json:"pending"`
	Invariant bool   `json:"invariant"`
}

// merchantBalance 台账余额与渠道余额。渠道查不到时 ChannelErr 非空，Invariant 为假。
func merchantBalance(ctx context.Context, mch Merchant, ifCode string) (BalanceView, error) {
	sum, err := ledgerSum(ctx, mch.MchNo, ifCode, []string{ledgerSettle, ledgerWithdrawal, ledgerWithdrawalReversal})
	if err != nil {
		return BalanceView{}, err
	}
	v := BalanceView{MchNo: mch.MchNo, IfCode: ifCode, Ledger: sum}
	cc, err := channel.Controlled(ifCode)
	if err != nil {
		return v, err
	}
	provider, sub, err := resolveParams(ctx, mch, ifCode)
	if err != nil {
		return v, fmt.Errorf("渠道参数未配置")
	}
	b, err := cc.Balance(ctx, provider, sub)
	if err != nil {
		return v, err
	}
	v.Available, v.Pending = b.Available, b.Pending
	v.Invariant = v.Ledger == v.Available
	return v, nil
}

var errBalanceMismatch = errors.New("balance mismatch")

// createWithdrawal 提现。同一 (mch_no, mch_withdraw_no) 返回原单（dup）。
// 提现前核对台账与渠道余额：不一致拒绝，不向渠道发请求。
func createWithdrawal(ctx context.Context, mch Merchant, ifCode, mchWithdrawNo string, amount int64, remark, by string) (Withdrawal, bool, error) {
	if amount <= 0 {
		return Withdrawal{}, false, fmt.Errorf("amount 必须为正")
	}
	if existing, err := findWithdrawByMch(ctx, mch.MchNo, mchWithdrawNo); err == nil {
		return existing, true, nil
	}
	cc, err := channel.Controlled(ifCode)
	if err != nil {
		return Withdrawal{}, false, err
	}
	v, err := merchantBalance(ctx, mch, ifCode)
	if err != nil {
		return Withdrawal{}, false, fmt.Errorf("查询余额失败: %w", err)
	}
	if !v.Invariant {
		return Withdrawal{}, false, fmt.Errorf("%w: 台账 %d 与渠道可用余额 %d 不一致，拒绝提现", errBalanceMismatch, v.Ledger, v.Available)
	}
	if amount > v.Available {
		return Withdrawal{}, false, fmt.Errorf("提现金额 %d 超过可用余额 %d", amount, v.Available)
	}
	provider, sub, err := resolveParams(ctx, mch, ifCode)
	if err != nil {
		return Withdrawal{}, false, fmt.Errorf("渠道参数未配置")
	}
	w := Withdrawal{
		WithdrawNo: newOrderID("W"), MchNo: mch.MchNo, IsvNo: mch.IsvNo, IfCode: ifCode,
		MchWithdrawNo: mchWithdrawNo, Amount: amount, Remark: remark, State: withdrawPending,
		CreatedBy: by, TenantID: mch.TenantID,
	}
	_, err = db.Exec(ctx, `INSERT INTO pay_withdrawal (withdraw_no, mch_no, isv_no, if_code, mch_withdraw_no, amount, remark, created_by, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		w.WithdrawNo, w.MchNo, w.IsvNo, w.IfCode, w.MchWithdrawNo, w.Amount, w.Remark, w.CreatedBy, w.TenantID)
	if isUniqueViolation(err) {
		existing, ferr := findWithdrawByMch(ctx, mch.MchNo, mchWithdrawNo)
		return existing, true, ferr
	}
	if err != nil {
		return Withdrawal{}, false, err
	}
	logWithdraw(w.WithdrawNo, "", withdrawPending, mchWithdrawNo)

	res, err := cc.Withdraw(ctx, channel.WithdrawIn{WithdrawNo: w.WithdrawNo, Amount: amount, Remark: remark, Provider: provider, Sub: sub})
	if err != nil {
		return w, false, err
	}
	saved, aerr := applyWithdrawResult(ctx, w, res)
	return saved, false, aerr
}

// applyWithdrawResult SUCCESS 记 withdrawal（借方）；FAIL 不记账；WAITING → accepted。
// 渠道把一笔已成功的提现退回（FAIL 且当前是 success）记 withdrawal_reversal 冲回。
func applyWithdrawResult(ctx context.Context, w Withdrawal, res channel.Result) (Withdrawal, error) {
	from := w.State
	switch res.State {
	case channel.StateSuccess:
		tag, err := db.Exec(ctx, `UPDATE pay_withdrawal SET state='success', channel_order_no=COALESCE(NULLIF($3,''), channel_order_no),
			err_code='', err_msg='', updated_at=now() WHERE withdraw_no=$1 AND state=$2`, w.WithdrawNo, from, res.ChannelOrderNo)
		if err != nil {
			return w, err
		}
		if tag.RowsAffected() == 1 {
			logWithdraw(w.WithdrawNo, from, withdrawSuccess, res.ChannelOrderNo)
			if _, err := postLedger(ctx, nil, []LedgerEntry{{
				MchNo: w.MchNo, IsvNo: w.IsvNo, IfCode: w.IfCode, Kind: ledgerWithdrawal, RefType: "withdrawal", RefID: w.WithdrawNo,
				Amount: -w.Amount, Memo: "提现 " + w.MchWithdrawNo, TenantID: w.TenantID,
			}}); err != nil {
				log.Printf("ledger withdrawal %s: %v", w.WithdrawNo, err)
			}
		}
	case channel.StateFail:
		tag, err := db.Exec(ctx, `UPDATE pay_withdrawal SET state='failed', err_code=$3, err_msg=$4, updated_at=now()
			WHERE withdraw_no=$1 AND state=$2`, w.WithdrawNo, from, res.ErrCode, trunc(res.ErrMsg, 500))
		if err != nil {
			return w, err
		}
		if tag.RowsAffected() == 1 {
			logWithdraw(w.WithdrawNo, from, withdrawFailed, res.ErrCode)
			if from == withdrawSuccess {
				if _, err := postLedger(ctx, nil, []LedgerEntry{{
					MchNo: w.MchNo, IsvNo: w.IsvNo, IfCode: w.IfCode, Kind: ledgerWithdrawalReversal, RefType: "withdrawal", RefID: w.WithdrawNo,
					Amount: w.Amount, Memo: "提现退回 " + w.MchWithdrawNo, TenantID: w.TenantID,
				}}); err != nil {
					log.Printf("ledger withdrawal reversal %s: %v", w.WithdrawNo, err)
				}
			}
		}
	case channel.StateWaiting:
		if from == withdrawPending {
			_, err := db.Exec(ctx, `UPDATE pay_withdrawal SET state='accepted', channel_order_no=COALESCE(NULLIF($2,''), channel_order_no),
				updated_at=now() WHERE withdraw_no=$1 AND state='pending'`, w.WithdrawNo, res.ChannelOrderNo)
			if err != nil {
				return w, err
			}
			logWithdraw(w.WithdrawNo, from, withdrawAccepted, res.ChannelOrderNo)
		}
	default:
		_, _ = db.Exec(ctx, `UPDATE pay_withdrawal SET err_code=$2, err_msg=$3, updated_at=now() WHERE withdraw_no=$1`,
			w.WithdrawNo, res.ErrCode, trunc(res.ErrMsg, 500))
	}
	return findWithdraw(ctx, w.WithdrawNo)
}

func logWithdraw(no, from, to, note string) {
	log.Printf(`{"event":"withdrawal","withdrawNo":%q,"from":%q,"to":%q,"note":%q}`, no, from, to, note)
}

// syncWithdrawal 查渠道推进 accepted（或 pending）的提现。
func syncWithdrawal(ctx context.Context, w Withdrawal) (Withdrawal, error) {
	cc, err := channel.Controlled(w.IfCode)
	if err != nil {
		return w, err
	}
	mch, err := loadMerchant(ctx, w.MchNo)
	if err != nil {
		return w, err
	}
	provider, sub, err := resolveParams(ctx, mch, w.IfCode)
	if err != nil {
		return w, err
	}
	res, err := cc.QueryWithdraw(ctx, channel.WithdrawRef{WithdrawNo: w.WithdrawNo, ChannelOrderNo: w.ChannelOrderNo, Provider: provider, Sub: sub})
	if err != nil {
		return w, err
	}
	return applyWithdrawResult(ctx, w, res)
}

// withdrawalWorker 每分钟补查 accepted 提现。
func withdrawalWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
		rows, err := db.Query(ctx, `SELECT `+withdrawCols+` FROM pay_withdrawal
			WHERE state='accepted' AND updated_at < now() - interval '30 seconds' ORDER BY updated_at LIMIT 50`)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("withdrawal worker: %v", err)
			}
			continue
		}
		var batch []Withdrawal
		for rows.Next() {
			if w, serr := scanWithdraw(rows); serr == nil {
				batch = append(batch, w)
			}
		}
		rows.Close()
		for _, w := range batch {
			if _, err := syncWithdrawal(ctx, w); err != nil {
				log.Printf("withdrawal %s sync: %v", w.WithdrawNo, err)
			}
		}
	}
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
		// 直清渠道、金额非法是请求错误；余额与台账不一致、超额是拒绝动账。
		code := http.StatusConflict
		if !channel.IsControlled(in.IfCode) || strings.Contains(err.Error(), "必须为正") {
			code = http.StatusBadRequest
		}
		c.JSON(code, gin.H{"error": err.Error()})
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

// handleSettleOrder POST /v1/orders/:mchOrderNo/settle：归集模式确认结算。
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
	if !channel.IsControlled(order.IfCode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "直清模式没有结算确认"})
		return
	}
	s, dup, err := createSettle(ctx, mch, order)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
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
	if !channel.IsControlled(ifCode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "直清模式没有平台余额"})
		return
	}
	v, err := merchantBalance(c.Request.Context(), mch, ifCode)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, v)
}

// ── 管理接口 ─────────────────────────────────────────────────

// handleAdminBalance GET /admin/balance?mchNo=&ifCode=
func handleAdminBalance(c *gin.Context) {
	if _, ok := requireRole(c, roleOperator); !ok {
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
	v, err := merchantBalance(c.Request.Context(), mch, ifCode)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, v)
}

// handleListWithdrawals GET /admin/withdrawals?mchNo=&state=
func handleListWithdrawals(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	sql := `SELECT ` + withdrawCols + ` FROM pay_withdrawal WHERE (tenant_id=$1 OR $1=0)`
	args := []any{claims.TenantId}
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

// handleSyncWithdrawal POST /admin/withdrawals/:no/sync
func handleSyncWithdrawal(c *gin.Context) {
	if _, ok := requireRole(c, roleOperator); !ok {
		return
	}
	w, err := findWithdraw(c.Request.Context(), c.Param("no"))
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "提现单不存在"})
		return
	}
	if w.State == withdrawSuccess || w.State == withdrawFailed {
		c.JSON(http.StatusOK, gin.H{"withdrawal": w})
		return
	}
	w, err = syncWithdrawal(c.Request.Context(), w)
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
