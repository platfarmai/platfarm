// 数据访问与状态机。所有状态流转都是 CAS：WHERE state=旧态，
// 影响行数为 0 即并发冲突，调用方当无事发生。金额单位分。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 订单状态。
const (
	orderCreated = "created"
	orderPaying  = "paying"
	orderSuccess = "success"
	orderFailed  = "failed"
	orderClosed  = "closed"

	refundNone    = "none"
	refundPartial = "partial"
	refundFull    = "full"

	refundCreated    = "created"
	refundProcessing = "processing"
	refundSuccess    = "success"
	refundFailed     = "failed"

	stateActive   = "active"
	stateDisabled = "disabled"
)

// Order 支付订单。
type Order struct {
	PayOrderID     string     `json:"payOrderId"`
	MchNo          string     `json:"mchNo"`
	IsvNo          string     `json:"isvNo"`
	AppID          string     `json:"appId"`
	MchType        int        `json:"mchType"`
	MchOrderNo     string     `json:"mchOrderNo"`
	IfCode         string     `json:"ifCode"`
	WayCode        string     `json:"wayCode"`
	Amount         int64      `json:"amount"`
	Currency       string     `json:"currency"`
	FeeRateBps     int        `json:"feeRateBps"`
	FeeAmount      int64      `json:"feeAmount"`
	State          string     `json:"state"`
	RefundState    string     `json:"refundState"`
	RefundAmount   int64      `json:"refundAmount"`
	Subject        string     `json:"subject"`
	Body           string     `json:"body"`
	ClientIP       string     `json:"clientIp"`
	ChannelExtra   string     `json:"-"`
	ChannelUser    string     `json:"channelUser"`
	ChannelOrderNo string     `json:"channelOrderNo"`
	ErrCode        string     `json:"errCode"`
	ErrMsg         string     `json:"errMsg"`
	NotifyURL      string     `json:"notifyUrl"`
	ReturnURL      string     `json:"returnUrl"`
	NotifyState    string     `json:"notifyState"`
	ExtParam       string     `json:"extParam"`
	ExpiredAt      *time.Time `json:"expiredAt"`
	SuccessAt      *time.Time `json:"successAt"`
	TenantID       int        `json:"tenantId"`
	CreatedAt      time.Time  `json:"createdAt"`
	DivisionState  string     `json:"divisionState"` // none|pending|done（M3）
	RebateRateBps  int        `json:"rebateRateBps"` // 服务商返佣费率快照（M3）
	RebateAmount   int64      `json:"rebateAmount"`
}

// Refund 退款订单。
type Refund struct {
	RefundOrderID  string     `json:"refundOrderId"`
	PayOrderID     string     `json:"payOrderId"`
	MchNo          string     `json:"mchNo"`
	IsvNo          string     `json:"isvNo"`
	AppID          string     `json:"appId"`
	MchRefundNo    string     `json:"mchRefundNo"`
	IfCode         string     `json:"ifCode"`
	WayCode        string     `json:"wayCode"`
	PayAmount      int64      `json:"payAmount"`
	RefundAmount   int64      `json:"refundAmount"`
	State          string     `json:"state"`
	Reason         string     `json:"reason"`
	ChannelOrderNo string     `json:"channelOrderNo"`
	ErrCode        string     `json:"errCode"`
	ErrMsg         string     `json:"errMsg"`
	NotifyURL      string     `json:"notifyUrl"`
	SuccessAt      *time.Time `json:"successAt"`
	TenantID       int        `json:"tenantId"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// Merchant 商户。
type Merchant struct {
	MchNo        string `json:"mchNo"`
	MchName      string `json:"mchName"`
	MchShortName string `json:"mchShortName"`
	MchType      int    `json:"mchType"`
	IsvNo        string `json:"isvNo"`
	State        string `json:"state"`
	TenantID     int    `json:"tenantId"`
}

// App 商户应用。
type App struct {
	AppID     string `json:"appId"`
	AppName   string `json:"appName"`
	MchNo     string `json:"mchNo"`
	AppSecret string `json:"-"`
	State     string `json:"state"`
	TenantID  int    `json:"tenantId"`
}

// Passage 支付方式路由。
type Passage struct {
	AppID   string
	MchNo   string
	IfCode  string
	WayCode string
	RateBps int
	State   string
}

const orderCols = `pay_order_id, mch_no, isv_no, app_id, mch_type, mch_order_no, if_code, way_code,
	amount, currency, fee_rate_bps, fee_amount, state, refund_state, refund_amount, subject, body,
	client_ip, channel_extra, channel_user, channel_order_no, err_code, err_msg, notify_url, return_url,
	notify_state, ext_param, expired_at, success_at, tenant_id, created_at,
	division_state, rebate_rate_bps, rebate_amount`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.PayOrderID, &o.MchNo, &o.IsvNo, &o.AppID, &o.MchType, &o.MchOrderNo,
		&o.IfCode, &o.WayCode, &o.Amount, &o.Currency, &o.FeeRateBps, &o.FeeAmount, &o.State,
		&o.RefundState, &o.RefundAmount, &o.Subject, &o.Body, &o.ClientIP, &o.ChannelExtra,
		&o.ChannelUser, &o.ChannelOrderNo, &o.ErrCode, &o.ErrMsg, &o.NotifyURL, &o.ReturnURL,
		&o.NotifyState, &o.ExtParam, &o.ExpiredAt, &o.SuccessAt, &o.TenantID, &o.CreatedAt,
		&o.DivisionState, &o.RebateRateBps, &o.RebateAmount)
	return o, err
}

// isvRebateRate 服务商返佣费率（万分位）；无服务商或未配置为 0。
func isvRebateRate(ctx context.Context, isvNo string) int {
	if isvNo == "" {
		return 0
	}
	var bps int
	_ = db.QueryRow(ctx, `SELECT rebate_rate_bps FROM pay_isv WHERE isv_no=$1`, isvNo).Scan(&bps)
	return bps
}

// findOrderByMch 按商户订单号查（幂等返回用）。
func findOrderByMch(ctx context.Context, mchNo, mchOrderNo string) (Order, error) {
	return scanOrder(db.QueryRow(ctx,
		`SELECT `+orderCols+` FROM pay_order WHERE mch_no=$1 AND mch_order_no=$2`, mchNo, mchOrderNo))
}

// findOrder 按平台订单号查。
func findOrder(ctx context.Context, payOrderID string) (Order, error) {
	return scanOrder(db.QueryRow(ctx,
		`SELECT `+orderCols+` FROM pay_order WHERE pay_order_id=$1`, payOrderID))
}

// insertOrder 建单。撞上 (mch_no, mch_order_no) 唯一键时返回已存在的订单与 dup=true。
func insertOrder(ctx context.Context, o Order) (Order, bool, error) {
	row := db.QueryRow(ctx, `INSERT INTO pay_order
		(pay_order_id, mch_no, isv_no, app_id, mch_type, mch_order_no, if_code, way_code, amount, currency,
		 fee_rate_bps, fee_amount, subject, body, client_ip, channel_extra, notify_url, return_url,
		 ext_param, expired_at, tenant_id, rebate_rate_bps, rebate_amount)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		RETURNING `+orderCols,
		o.PayOrderID, o.MchNo, o.IsvNo, o.AppID, o.MchType, o.MchOrderNo, o.IfCode, o.WayCode,
		o.Amount, o.Currency, o.FeeRateBps, o.FeeAmount, o.Subject, o.Body, o.ClientIP, o.ChannelExtra,
		o.NotifyURL, o.ReturnURL, o.ExtParam, o.ExpiredAt, o.TenantID, o.RebateRateBps, o.RebateAmount)
	saved, err := scanOrder(row)
	if isUniqueViolation(err) {
		existing, ferr := findOrderByMch(ctx, o.MchNo, o.MchOrderNo)
		return existing, true, ferr
	}
	return saved, false, err
}

// casOrder 把订单从 from 推进到 to，并写渠道结果。返回 false 表示状态已被别人推进。
func casOrder(ctx context.Context, payOrderID, from, to, channelOrderNo, channelUser, errCode, errMsg string) (bool, error) {
	successAt := any(nil)
	if to == orderSuccess {
		successAt = time.Now()
	}
	tag, err := db.Exec(ctx, `UPDATE pay_order SET state=$3, channel_order_no=COALESCE(NULLIF($4,''), channel_order_no),
		channel_user=COALESCE(NULLIF($5,''), channel_user), err_code=$6, err_msg=$7,
		success_at=COALESCE($8, success_at), updated_at=now()
		WHERE pay_order_id=$1 AND state=$2`,
		payOrderID, from, to, channelOrderNo, channelUser, errCode, errMsg, successAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const refundCols = `refund_order_id, pay_order_id, mch_no, isv_no, app_id, mch_refund_no, if_code, way_code,
	pay_amount, refund_amount, state, reason, channel_order_no, err_code, err_msg, notify_url, success_at,
	tenant_id, created_at`

func scanRefund(row pgx.Row) (Refund, error) {
	var r Refund
	err := row.Scan(&r.RefundOrderID, &r.PayOrderID, &r.MchNo, &r.IsvNo, &r.AppID, &r.MchRefundNo,
		&r.IfCode, &r.WayCode, &r.PayAmount, &r.RefundAmount, &r.State, &r.Reason, &r.ChannelOrderNo,
		&r.ErrCode, &r.ErrMsg, &r.NotifyURL, &r.SuccessAt, &r.TenantID, &r.CreatedAt)
	return r, err
}

// findRefundByMch 按商户退款单号查。
func findRefundByMch(ctx context.Context, mchNo, mchRefundNo string) (Refund, error) {
	return scanRefund(db.QueryRow(ctx,
		`SELECT `+refundCols+` FROM pay_refund WHERE mch_no=$1 AND mch_refund_no=$2`, mchNo, mchRefundNo))
}

// findRefund 按平台退款单号查。
func findRefund(ctx context.Context, refundOrderID string) (Refund, error) {
	return scanRefund(db.QueryRow(ctx,
		`SELECT `+refundCols+` FROM pay_refund WHERE refund_order_id=$1`, refundOrderID))
}

// createRefund 在一个事务里占住退款额度：支付单 refund_amount 累加，退款单落 created。
// 超额、订单非 success、重复商户退款单号都返回错误。
func createRefund(ctx context.Context, r Refund) (Refund, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return Refund{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var state string
	var payAmount, refunded int64
	err = tx.QueryRow(ctx, `SELECT state, amount, refund_amount FROM pay_order
		WHERE pay_order_id=$1 AND mch_no=$2 FOR UPDATE`, r.PayOrderID, r.MchNo).
		Scan(&state, &payAmount, &refunded)
	if errors.Is(err, pgx.ErrNoRows) {
		return Refund{}, fmt.Errorf("支付订单不存在")
	}
	if err != nil {
		return Refund{}, err
	}
	if state != orderSuccess {
		return Refund{}, fmt.Errorf("订单状态 %s 不可退款", state)
	}
	if refunded+r.RefundAmount > payAmount {
		return Refund{}, fmt.Errorf("退款金额超出可退余额")
	}
	if _, err = tx.Exec(ctx, `UPDATE pay_order SET refund_amount=refund_amount+$2, updated_at=now()
		WHERE pay_order_id=$1`, r.PayOrderID, r.RefundAmount); err != nil {
		return Refund{}, err
	}
	row := tx.QueryRow(ctx, `INSERT INTO pay_refund
		(refund_order_id, pay_order_id, mch_no, isv_no, app_id, mch_refund_no, if_code, way_code,
		 pay_amount, refund_amount, reason, notify_url, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING `+refundCols,
		r.RefundOrderID, r.PayOrderID, r.MchNo, r.IsvNo, r.AppID, r.MchRefundNo, r.IfCode, r.WayCode,
		payAmount, r.RefundAmount, r.Reason, r.NotifyURL, r.TenantID)
	saved, err := scanRefund(row)
	if isUniqueViolation(err) {
		return Refund{}, fmt.Errorf("商户退款单号已存在")
	}
	if err != nil {
		return Refund{}, err
	}
	return saved, tx.Commit(ctx)
}

// casRefund 推进退款单状态。
func casRefund(ctx context.Context, refundOrderID, from, to, channelOrderNo, errCode, errMsg string) (bool, error) {
	successAt := any(nil)
	if to == refundSuccess {
		successAt = time.Now()
	}
	tag, err := db.Exec(ctx, `UPDATE pay_refund SET state=$3,
		channel_order_no=COALESCE(NULLIF($4,''), channel_order_no), err_code=$5, err_msg=$6,
		success_at=COALESCE($7, success_at), updated_at=now()
		WHERE refund_order_id=$1 AND state=$2`,
		refundOrderID, from, to, channelOrderNo, errCode, errMsg, successAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// rollupRefund 按退款单终态回写支付单的 refund_state。失败则把占住的额度退回。
func rollupRefund(ctx context.Context, payOrderID string, amount int64, success bool) error {
	if !success {
		_, err := db.Exec(ctx, `UPDATE pay_order SET refund_amount=refund_amount-$2, updated_at=now()
			WHERE pay_order_id=$1`, payOrderID, amount)
		return err
	}
	_, err := db.Exec(ctx, `UPDATE pay_order SET refund_state = CASE
		WHEN refund_amount >= amount THEN 'full' ELSE 'partial' END, updated_at=now()
		WHERE pay_order_id=$1`, payOrderID)
	return err
}

// ── 商户 / 应用 / 路由 / 渠道参数 ──────────────────────────

func loadMerchant(ctx context.Context, mchNo string) (Merchant, error) {
	var m Merchant
	err := db.QueryRow(ctx, `SELECT mch_no, mch_name, mch_short_name, mch_type, isv_no, state, tenant_id
		FROM pay_merchant WHERE mch_no=$1`, mchNo).
		Scan(&m.MchNo, &m.MchName, &m.MchShortName, &m.MchType, &m.IsvNo, &m.State, &m.TenantID)
	return m, err
}

func loadApp(ctx context.Context, appID string) (App, error) {
	var a App
	err := db.QueryRow(ctx, `SELECT app_id, app_name, mch_no, app_secret, state, tenant_id
		FROM pay_app WHERE app_id=$1`, appID).
		Scan(&a.AppID, &a.AppName, &a.MchNo, &a.AppSecret, &a.State, &a.TenantID)
	return a, err
}

func loadPassage(ctx context.Context, appID, wayCode string) (Passage, error) {
	var p Passage
	err := db.QueryRow(ctx, `SELECT app_id, mch_no, if_code, way_code, rate_bps, state
		FROM pay_passage WHERE app_id=$1 AND way_code=$2 AND state='active'`, appID, wayCode).
		Scan(&p.AppID, &p.MchNo, &p.IfCode, &p.WayCode, &p.RateBps, &p.State)
	return p, err
}

// loadChannelParams 读取并解密渠道参数。
func loadChannelParams(ctx context.Context, ownerType, ownerID, ifCode string) (map[string]string, error) {
	var sealed string
	err := db.QueryRow(ctx, `SELECT if_params FROM pay_channel_config
		WHERE owner_type=$1 AND owner_id=$2 AND if_code=$3 AND state='active'`,
		ownerType, ownerID, ifCode).Scan(&sealed)
	if err != nil {
		return nil, err
	}
	plain, err := open(sealed)
	if err != nil {
		return nil, fmt.Errorf("解密渠道参数: %w", err)
	}
	return parseParams(plain), nil
}

// ── 小工具 ──────────────────────────────────────────────────

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// newOrderID 生成 20 位 hex 订单号（80 bit 随机，碰撞可忽略）。
func newOrderID(prefix string) string {
	buf := make([]byte, 10)
	_, _ = rand.Read(buf)
	return prefix + hex.EncodeToString(buf)
}

// feeAmount 手续费：万分位费率，四舍五入到分。
func feeAmount(amount int64, rateBps int) int64 {
	if rateBps <= 0 {
		return 0
	}
	return (amount*int64(rateBps) + 5000) / 10000
}

// 编译期断言连接池类型，防止重构时签名漂移。
var _ *pgxpool.Pool = db
