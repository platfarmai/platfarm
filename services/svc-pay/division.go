// 分账（specs/030 M3）。接收方先在渠道绑定；分账时按比例计算金额（向下取整，永不超额），
// 比例逐个不超过子商户授权上限、合计不超过商户渠道参数 max_division_ratio_bps（默认 30%）。
// 一笔支付单只有一条未失败的分账单：重复请求返回原单（no-op）。状态 pending → accepted → success | failed。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"platfarm/svc-pay/channel"
)

const (
	divisionPending  = "pending"
	divisionAccepted = "accepted"
	divisionSuccess  = "success"
	divisionFailed   = "failed"

	orderDivisionNone    = "none"
	orderDivisionPending = "pending"
	orderDivisionDone    = "done"

	defaultMaxDivisionBps = 3000
)

// Receiver pay_division_receiver 一行（name 密封存储，对外脱敏）。
type Receiver struct {
	ID              int64  `json:"id"`
	MchNo           string `json:"mchNo"`
	IfCode          string `json:"ifCode"`
	Type            string `json:"type"`
	Account         string `json:"account"`
	Name            string `json:"name"`
	RelationType    string `json:"relationType"`
	MaxRatioBps     int    `json:"maxRatioBps"`
	DefaultRatioBps int    `json:"defaultRatioBps"`
	BindState       string `json:"bindState"`
	BindMsg         string `json:"bindMsg"`
	State           string `json:"state"`
	TenantID        int    `json:"tenantId"`
}

// Division pay_division 一行。
type Division struct {
	DivisionNo      string                     `json:"divisionNo"`
	PayOrderID      string                     `json:"payOrderId"`
	MchNo           string                     `json:"mchNo"`
	IsvNo           string                     `json:"isvNo"`
	AppID           string                     `json:"appId"`
	IfCode          string                     `json:"ifCode"`
	WayCode         string                     `json:"wayCode"`
	OrderAmount     int64                      `json:"orderAmount"`
	DivisionAmount  int64                      `json:"divisionAmount"`
	Receivers       []channel.DivisionReceiver `json:"receivers"`
	UnfreezeUnsplit bool                       `json:"unfreezeUnsplit"`
	State           string                     `json:"state"`
	ChannelOrderNo  string                     `json:"channelOrderNo"`
	ErrCode         string                     `json:"errCode"`
	ErrMsg          string                     `json:"errMsg"`
	CreatedBy       string                     `json:"createdBy"`
	TenantID        int                        `json:"tenantId"`
	CreatedAt       time.Time                  `json:"createdAt"`
	UpdatedAt       time.Time                  `json:"updatedAt"`
}

const divisionCols = `division_no, pay_order_id, mch_no, isv_no, app_id, if_code, way_code, order_amount, division_amount,
	receivers, unfreeze_unsplit, state, channel_order_no, err_code, err_msg, created_by, tenant_id, created_at, updated_at`

func scanDivision(row pgx.Row) (Division, error) {
	var d Division
	var receivers []byte
	err := row.Scan(&d.DivisionNo, &d.PayOrderID, &d.MchNo, &d.IsvNo, &d.AppID, &d.IfCode, &d.WayCode, &d.OrderAmount,
		&d.DivisionAmount, &receivers, &d.UnfreezeUnsplit, &d.State, &d.ChannelOrderNo, &d.ErrCode, &d.ErrMsg,
		&d.CreatedBy, &d.TenantID, &d.CreatedAt, &d.UpdatedAt)
	if err == nil {
		_ = json.Unmarshal(receivers, &d.Receivers)
	}
	return d, err
}

func findDivision(ctx context.Context, divisionNo string) (Division, error) {
	return scanDivision(db.QueryRow(ctx, `SELECT `+divisionCols+` FROM pay_division WHERE division_no=$1`, divisionNo))
}

// findActiveDivisionByOrder 该支付单的未失败分账单。
func findActiveDivisionByOrder(ctx context.Context, payOrderID string) (Division, error) {
	return scanDivision(db.QueryRow(ctx, `SELECT `+divisionCols+` FROM pay_division
		WHERE pay_order_id=$1 AND state<>'failed'`, payOrderID))
}

func loadReceivers(ctx context.Context, mchNo, ifCode string) ([]Receiver, error) {
	rows, err := db.Query(ctx, `SELECT id, mch_no, if_code, receiver_type, account, name, relation_type, max_ratio_bps,
		default_ratio_bps, bind_state, bind_msg, state, tenant_id FROM pay_division_receiver
		WHERE mch_no=$1 AND ($2='' OR if_code=$2) ORDER BY id`, mchNo, ifCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Receiver
	for rows.Next() {
		var r Receiver
		if rows.Scan(&r.ID, &r.MchNo, &r.IfCode, &r.Type, &r.Account, &r.Name, &r.RelationType, &r.MaxRatioBps,
			&r.DefaultRatioBps, &r.BindState, &r.BindMsg, &r.State, &r.TenantID) == nil {
			if plain, err := open(r.Name); err == nil {
				r.Name = plain
			}
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// ── 分账计划与校验（纯函数，可测） ──────────────────────────

type divisionReq struct {
	Account  string `json:"account"`
	RatioBps int    `json:"ratioBps"`
}

// planDivision 把请求映射到接收方快照：比例校验、金额向下取整。
// 请求为空时用所有已绑定且 default_ratio_bps>0 的接收方。
func planDivision(orderAmount int64, receivers []Receiver, reqs []divisionReq, maxTotalBps int) ([]channel.DivisionReceiver, error) {
	byAccount := map[string]Receiver{}
	for _, r := range receivers {
		byAccount[r.Account] = r
	}
	if len(reqs) == 0 {
		// 默认名单只取已绑定且启用的接收方；显式指定时未绑定会报错，让调用方知道。
		for _, r := range receivers {
			if r.DefaultRatioBps > 0 && r.BindState == "bound" && r.State == stateActive {
				reqs = append(reqs, divisionReq{Account: r.Account, RatioBps: r.DefaultRatioBps})
			}
		}
	}
	if len(reqs) == 0 {
		return nil, fmt.Errorf("没有可分账的接收方")
	}
	total := 0
	seen := map[string]bool{}
	var out []channel.DivisionReceiver
	for _, q := range reqs {
		r, ok := byAccount[q.Account]
		if !ok {
			return nil, fmt.Errorf("接收方 %s 未绑定", q.Account)
		}
		if r.BindState != "bound" || r.State != stateActive {
			return nil, fmt.Errorf("接收方 %s 未完成渠道绑定或已停用", q.Account)
		}
		if seen[q.Account] {
			return nil, fmt.Errorf("接收方 %s 重复", q.Account)
		}
		seen[q.Account] = true
		if q.RatioBps <= 0 {
			return nil, fmt.Errorf("接收方 %s 比例必须为正", q.Account)
		}
		if q.RatioBps > r.MaxRatioBps {
			return nil, fmt.Errorf("接收方 %s 比例 %d 超过授权上限 %d（万分位）", q.Account, q.RatioBps, r.MaxRatioBps)
		}
		total += q.RatioBps
		amount := orderAmount * int64(q.RatioBps) / 10000 // 向下取整：永不超过应分额
		if amount <= 0 {
			return nil, fmt.Errorf("接收方 %s 分账金额为 0", q.Account)
		}
		out = append(out, channel.DivisionReceiver{
			Type: r.Type, Account: r.Account, RatioBps: q.RatioBps, Amount: amount, Description: r.RelationType + "分账",
		})
	}
	if total > maxTotalBps {
		return nil, fmt.Errorf("分账比例合计 %d 超过商户上限 %d（万分位）", total, maxTotalBps)
	}
	return out, nil
}

func sumAmount(rs []channel.DivisionReceiver) int64 {
	var s int64
	for _, r := range rs {
		s += r.Amount
	}
	return s
}

// ── 核心流程 ────────────────────────────────────────────────

var errDivisionConflict = errors.New("division conflict")

// createDivision 建分账单并发起渠道分账。重复请求返回已有分账单（dup=true）。
func createDivision(ctx context.Context, app App, mch Merchant, order Order, reqs []divisionReq, unfreeze bool, by string) (Division, bool, error) {
	if order.State != orderSuccess {
		return Division{}, false, fmt.Errorf("订单状态 %s 不可分账", order.State)
	}
	// refund_amount 含处理中的退款占用的额度：退款在途时分账会和退款抢同一笔冻结资金。
	if order.RefundState != refundNone || order.RefundAmount > 0 {
		return Division{}, false, fmt.Errorf("已退款或退款处理中的订单不可分账")
	}
	if existing, err := findActiveDivisionByOrder(ctx, order.PayOrderID); err == nil {
		return existing, true, nil
	}
	if channel.IsControlled(order.IfCode) {
		if channel.Withdrawable(order.IfCode) {
			// 收付通：分账在确认结算（完结分账）之前；分账时不解冻，剩余资金统一由确认结算释放并记入可提现台账。
			if order.SettleState != orderSettleNone {
				return Division{}, false, fmt.Errorf("收付通订单确认结算后剩余资金已解冻，不能再分账")
			}
			unfreeze = false
		} else if s, err := findActiveSettle(ctx, order.PayOrderID); err != nil || s.State != settleSuccess || !s.HoldForDivision {
			// 直付通：结算前不支持分账；结算时须带 holdForDivision 冻结，资金才不会被挪走。
			return Division{}, false, fmt.Errorf("直付通须先以 holdForDivision=true 确认结算成功后再分账")
		}
	}
	sh, err := channel.Sharing(order.IfCode)
	if err != nil {
		return Division{}, false, err
	}
	provider, sub, err := resolveParams(ctx, mch, order.IfCode)
	if err != nil {
		return Division{}, false, fmt.Errorf("渠道参数未配置")
	}
	maxTotal := defaultMaxDivisionBps
	if v, perr := strconv.Atoi(sub["max_division_ratio_bps"]); perr == nil && v > 0 {
		maxTotal = v
	}
	receivers, err := loadReceivers(ctx, mch.MchNo, order.IfCode)
	if err != nil {
		return Division{}, false, err
	}
	plan, err := planDivision(order.Amount, receivers, reqs, maxTotal)
	if err != nil {
		return Division{}, false, err
	}
	snapshot, _ := json.Marshal(plan)
	d := Division{
		DivisionNo: newOrderID("D"), PayOrderID: order.PayOrderID, MchNo: mch.MchNo, IsvNo: mch.IsvNo, AppID: app.AppID,
		IfCode: order.IfCode, WayCode: order.WayCode, OrderAmount: order.Amount, DivisionAmount: sumAmount(plan),
		Receivers: plan, UnfreezeUnsplit: unfreeze, State: divisionPending, TenantID: mch.TenantID, CreatedBy: by,
	}
	_, err = db.Exec(ctx, `INSERT INTO pay_division (division_no, pay_order_id, mch_no, isv_no, app_id, if_code, way_code,
		order_amount, division_amount, receivers, unfreeze_unsplit, created_by, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		d.DivisionNo, d.PayOrderID, d.MchNo, d.IsvNo, d.AppID, d.IfCode, d.WayCode, d.OrderAmount, d.DivisionAmount,
		snapshot, d.UnfreezeUnsplit, d.CreatedBy, d.TenantID)
	if isUniqueViolation(err) {
		existing, ferr := findActiveDivisionByOrder(ctx, order.PayOrderID)
		return existing, true, ferr
	}
	if err != nil {
		return Division{}, false, err
	}
	_, _ = db.Exec(ctx, `UPDATE pay_order SET division_state='pending', updated_at=now()
		WHERE pay_order_id=$1 AND division_state='none'`, order.PayOrderID)
	logTransitionDivision(d.DivisionNo, "", divisionPending, order.PayOrderID)

	res, err := sh.Share(ctx, channel.DivisionIn{
		DivisionNo: d.DivisionNo, PayOrderID: order.PayOrderID, ChannelOrderNo: order.ChannelOrderNo,
		Receivers: plan, UnfreezeUnsplit: unfreeze, Provider: provider, Sub: sub,
	})
	if err != nil {
		// 适配器在发出请求前就拒绝了：渠道侧没有这笔分账，确定失败并释放订单。
		res = channel.DivisionResult{State: channel.StateFail, ErrCode: "SYS", ErrMsg: err.Error()}
	}
	saved, aerr := applyDivisionResult(ctx, d, res)
	return saved, false, aerr
}

// applyDivisionResult 把渠道四态落到分账单：SUCCESS 记台账 + 订单 done；FAIL 释放订单；WAITING → accepted；
// UNKNOWN 不动（pending 由 worker 用原分账单号重提）。终态推进与台账、订单状态同事务。
func applyDivisionResult(ctx context.Context, d Division, res channel.DivisionResult) (Division, error) {
	from := d.State
	receivers := d.Receivers
	if len(res.Receivers) > 0 {
		receivers = mergeReceiverResults(d.Receivers, res.Receivers)
	}
	snapshot, _ := json.Marshal(receivers)
	switch res.State {
	case channel.StateSuccess:
		var entries []LedgerEntry
		for _, r := range receivers {
			if r.Result != "" && r.Result != "SUCCESS" {
				continue // 渠道逐个失败的接收方不记账
			}
			entries = append(entries, LedgerEntry{MchNo: d.MchNo, IsvNo: d.IsvNo, IfCode: d.IfCode, Kind: ledgerSharing,
				RefType: "division", RefID: d.DivisionNo, RefSub: r.Account, Amount: -r.Amount,
				Memo: "分账给 " + r.Account, TenantID: d.TenantID})
		}
		advanced, err := inTx(ctx, func(tx pgx.Tx) (bool, error) {
			tag, err := tx.Exec(ctx, `UPDATE pay_division SET state='success', channel_order_no=COALESCE(NULLIF($3,''), channel_order_no),
				receivers=$4, err_code='', err_msg='', updated_at=now() WHERE division_no=$1 AND state=$2`,
				d.DivisionNo, from, res.ChannelOrderNo, snapshot)
			if err != nil || tag.RowsAffected() == 0 {
				return false, err
			}
			if _, err := tx.Exec(ctx, `UPDATE pay_order SET division_state='done', updated_at=now() WHERE pay_order_id=$1`, d.PayOrderID); err != nil {
				return false, err
			}
			_, err = postLedger(ctx, tx, entries)
			return err == nil, err
		})
		if err != nil {
			return d, err
		}
		if advanced {
			logTransitionDivision(d.DivisionNo, from, divisionSuccess, res.ChannelOrderNo)
		}
	case channel.StateFail:
		advanced, err := inTx(ctx, func(tx pgx.Tx) (bool, error) {
			tag, err := tx.Exec(ctx, `UPDATE pay_division SET state='failed', receivers=$3, err_code=$4, err_msg=$5, updated_at=now()
				WHERE division_no=$1 AND state=$2`, d.DivisionNo, from, snapshot, res.ErrCode, trunc(res.ErrMsg, 500))
			if err != nil || tag.RowsAffected() == 0 {
				return false, err
			}
			_, err = tx.Exec(ctx, `UPDATE pay_order SET division_state='none', updated_at=now()
				WHERE pay_order_id=$1 AND division_state='pending'`, d.PayOrderID)
			return err == nil, err
		})
		if err != nil {
			return d, err
		}
		if advanced {
			logTransitionDivision(d.DivisionNo, from, divisionFailed, res.ErrCode)
		}
	case channel.StateWaiting:
		if from == divisionPending {
			_, err := db.Exec(ctx, `UPDATE pay_division SET state='accepted', channel_order_no=COALESCE(NULLIF($2,''), channel_order_no),
				receivers=$3, updated_at=now() WHERE division_no=$1 AND state='pending'`, d.DivisionNo, res.ChannelOrderNo, snapshot)
			if err != nil {
				return d, err
			}
			logTransitionDivision(d.DivisionNo, from, divisionAccepted, res.ChannelOrderNo)
		} else {
			_, _ = db.Exec(ctx, `UPDATE pay_division SET receivers=$2, updated_at=now() WHERE division_no=$1`, d.DivisionNo, snapshot)
		}
	default:
		// UNKNOWN：不动状态，只记录渠道信息供排障。
		_, _ = db.Exec(ctx, `UPDATE pay_division SET err_code=$2, err_msg=$3, updated_at=now() WHERE division_no=$1`,
			d.DivisionNo, res.ErrCode, trunc(res.ErrMsg, 500))
	}
	return findDivision(ctx, d.DivisionNo)
}

// mergeReceiverResults 把渠道返回的逐个结果并回快照（按 account 匹配，保留我们的比例）。
func mergeReceiverResults(snapshot, channelSide []channel.DivisionReceiver) []channel.DivisionReceiver {
	out := make([]channel.DivisionReceiver, len(snapshot))
	copy(out, snapshot)
	for i := range out {
		for _, c := range channelSide {
			if c.Account == out[i].Account {
				out[i].Result, out[i].Detail = c.Result, c.Detail
			}
		}
	}
	return out
}

func logTransitionDivision(no, from, to, note string) {
	log.Printf(`{"event":"division","divisionNo":%q,"from":%q,"to":%q,"note":%q}`, no, from, to, note)
}

// syncDivision 查渠道推进 accepted 分账单。
func syncDivision(ctx context.Context, d Division) (Division, error) {
	sh, err := channel.Sharing(d.IfCode)
	if err != nil {
		return d, err
	}
	mch, err := loadMerchant(ctx, d.MchNo)
	if err != nil {
		return d, err
	}
	provider, sub, err := resolveParams(ctx, mch, d.IfCode)
	if err != nil {
		return d, err
	}
	order, err := findOrder(ctx, d.PayOrderID)
	if err != nil {
		return d, err
	}
	res, err := sh.QueryShare(ctx, channel.DivisionRef{DivisionNo: d.DivisionNo, ChannelOrderNo: order.ChannelOrderNo, Provider: provider, Sub: sub})
	if err != nil {
		return d, err
	}
	return applyDivisionResult(ctx, d, res)
}

// divisionWorker 每分钟：accepted 的查询推进；pending（提交结果未知）的用原分账单号重提——
// 微信同一 out_order_no 多次请求等同一次，支付宝 ACQ.SYSTEM_ERROR 要求原请求号重试。
func divisionWorker(ctx context.Context) {
	everyLeased(ctx, "divisions", 60*time.Second, sweepDivisions)
}

func sweepDivisions(ctx context.Context) error {
	rows, err := db.Query(ctx, `SELECT `+divisionCols+` FROM pay_division
		WHERE (state='accepted' AND updated_at < now() - interval '30 seconds')
		   OR (state='pending' AND updated_at < now() - interval '1 minute')
		ORDER BY updated_at LIMIT 50`)
	if err != nil {
		return err
	}
	var batch []Division
	for rows.Next() {
		if d, serr := scanDivision(rows); serr == nil {
			batch = append(batch, d)
		}
	}
	rows.Close()
	for _, d := range batch {
		var err error
		if d.State == divisionPending {
			_, err = resubmitDivision(ctx, d)
		} else {
			_, err = syncDivision(ctx, d)
		}
		if err != nil {
			log.Printf("division %s recover: %v", d.DivisionNo, err)
			_, _ = db.Exec(ctx, `UPDATE pay_division SET err_msg=$2, updated_at=now() WHERE division_no=$1 AND state IN ('pending','accepted')`,
				d.DivisionNo, trunc(err.Error(), 500))
		}
	}
	return nil
}

// resubmitDivision 用原分账单号与原接收方快照重提，按结果推进。
func resubmitDivision(ctx context.Context, d Division) (Division, error) {
	sh, err := channel.Sharing(d.IfCode)
	if err != nil {
		return d, err
	}
	mch, err := loadMerchant(ctx, d.MchNo)
	if err != nil {
		return d, err
	}
	provider, sub, err := resolveParams(ctx, mch, d.IfCode)
	if err != nil {
		return d, err
	}
	order, err := findOrder(ctx, d.PayOrderID)
	if err != nil {
		return d, err
	}
	res, err := sh.Share(ctx, channel.DivisionIn{
		DivisionNo: d.DivisionNo, PayOrderID: d.PayOrderID, ChannelOrderNo: order.ChannelOrderNo,
		Receivers: d.Receivers, UnfreezeUnsplit: d.UnfreezeUnsplit, Provider: provider, Sub: sub,
	})
	if err != nil {
		return d, err
	}
	return applyDivisionResult(ctx, d, res)
}

// ── 开放 API ────────────────────────────────────────────────

type createDivisionIn struct {
	MchOrderNo      string        `json:"mchOrderNo"`
	Receivers       []divisionReq `json:"receivers"`
	UnfreezeUnsplit *bool         `json:"unfreezeUnsplit"`
}

// handleCreateDivision POST /v1/divisions（scope pay.division.create + 商户签名）。
func handleCreateDivision(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyByte))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求失败"})
		return
	}
	app, mch, ok := signedApp(c, body)
	if !ok {
		return
	}
	var in createDivisionIn
	if json.Unmarshal(body, &in) != nil || in.MchOrderNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchOrderNo 必填"})
		return
	}
	ctx := c.Request.Context()
	order, err := findOrderByMch(ctx, mch.MchNo, in.MchOrderNo)
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
	unfreeze := true
	if in.UnfreezeUnsplit != nil {
		unfreeze = *in.UnfreezeUnsplit
	}
	d, dup, err := createDivision(ctx, app, mch, order, in.Receivers, unfreeze, "app:"+app.AppID)
	if err != nil {
		if d.DivisionNo != "" {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "division": d})
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"division": d, "idempotent": dup})
}

// handleQueryDivision GET /v1/divisions/:mchOrderNo（scope pay.division.query）。
func handleQueryDivision(c *gin.Context) {
	app, _, ok := signedApp(c, nil)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	order, err := findOrderByMch(ctx, app.MchNo, c.Param("mchOrderNo"))
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "支付订单不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	d, err := findActiveDivisionByOrder(ctx, order.PayOrderID)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "该订单没有分账单"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"division": d})
}

// ── 管理接口 ─────────────────────────────────────────────────

// handleAddReceiver POST /admin/division/receivers：在渠道绑定接收方并落库。
func handleAddReceiver(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	var in struct {
		MchNo           string `json:"mchNo"`
		IfCode          string `json:"ifCode"`
		Type            string `json:"type"`
		Account         string `json:"account"`
		Name            string `json:"name"`
		RelationType    string `json:"relationType"`
		MaxRatioBps     int    `json:"maxRatioBps"`
		DefaultRatioBps int    `json:"defaultRatioBps"`
	}
	if c.ShouldBindJSON(&in) != nil || in.MchNo == "" || in.IfCode == "" || in.Type == "" || in.Account == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchNo、ifCode、type、account 必填"})
		return
	}
	if in.MaxRatioBps <= 0 {
		in.MaxRatioBps = defaultMaxDivisionBps
	}
	if in.MaxRatioBps > 10000 || in.DefaultRatioBps < 0 || in.DefaultRatioBps > in.MaxRatioBps {
		c.JSON(http.StatusBadRequest, gin.H{"error": "比例须在 0..10000 且默认比例不超过上限"})
		return
	}
	if in.RelationType == "" {
		in.RelationType = "PARTNER"
	}
	ctx := c.Request.Context()
	mch, err := loadMerchant(ctx, in.MchNo)
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
	sh, err := channel.Sharing(in.IfCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	provider, sub, err := resolveParams(ctx, mch, in.IfCode)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "渠道参数未配置: " + err.Error()})
		return
	}
	sealedName, err := seal(in.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密封失败"})
		return
	}
	bindState, bindMsg := "bound", ""
	if berr := sh.AddReceiver(ctx, channel.ReceiverIn{Type: in.Type, Account: in.Account, Name: in.Name,
		RelationType: in.RelationType, Provider: provider, Sub: sub}); berr != nil {
		bindState, bindMsg = "failed", trunc(berr.Error(), 500)
	}
	_, err = db.Exec(ctx, `INSERT INTO pay_division_receiver (mch_no, if_code, receiver_type, account, name, relation_type,
		max_ratio_bps, default_ratio_bps, bind_state, bind_msg, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (mch_no, if_code, account) DO UPDATE SET receiver_type=$3, name=$5, relation_type=$6, max_ratio_bps=$7,
			default_ratio_bps=$8, bind_state=$9, bind_msg=$10, state='active', updated_at=now()`,
		in.MchNo, in.IfCode, in.Type, in.Account, sealedName, in.RelationType, in.MaxRatioBps, in.DefaultRatioBps,
		bindState, bindMsg, mch.TenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	if bindState == "failed" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "渠道绑定失败: " + bindMsg, "bindState": bindState})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "bindState": bindState})
}

// handleListReceivers GET /admin/division/receivers?mchNo=
func handleListReceivers(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	mchNo := c.Query("mchNo")
	if mchNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchNo 必填"})
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
	items, err := loadReceivers(c.Request.Context(), mchNo, c.Query("ifCode"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if items == nil {
		items = []Receiver{}
	}
	for i := range items {
		items[i].Name = maskName(items[i].Name)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// maskName 接收方姓名/名称对外脱敏：只留第一个字。
func maskName(s string) string {
	r := []rune(s)
	if len(r) <= 1 {
		return strings.Repeat("*", len(r))
	}
	return string(r[0]) + strings.Repeat("*", len(r)-1)
}

// handleListDivisions GET /admin/divisions?mchNo=&state=
func handleListDivisions(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	scope, args := tenantScope(claims)
	sql := `SELECT ` + divisionCols + ` FROM pay_division WHERE ` + scope
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
	items := []Division{}
	for rows.Next() {
		if d, serr := scanDivision(rows); serr == nil {
			items = append(items, d)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// handleSyncDivision POST /admin/divisions/:no/sync
func handleSyncDivision(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	d, err := findDivision(c.Request.Context(), c.Param("no"))
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "分账单不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if denyOtherTenant(c, claims, d.TenantID, "分账单") {
		return
	}
	if d.State != divisionAccepted && d.State != divisionPending {
		c.JSON(http.StatusOK, gin.H{"division": d})
		return
	}
	d, err = syncDivision(c.Request.Context(), d)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"division": d})
}

// handleLedger GET /admin/ledger?mchNo=|isvNo=&kind=
func handleLedger(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	scope, args := tenantScope(claims)
	sql := `SELECT id, mch_no, isv_no, if_code, kind, ref_type, ref_id, ref_sub, amount, memo, tenant_id, created_at::text
		FROM pay_ledger WHERE ` + scope
	if m := c.Query("mchNo"); m != "" {
		args = append(args, m)
		sql += fmt.Sprintf(` AND mch_no=$%d`, len(args))
	}
	if i := c.Query("isvNo"); i != "" {
		args = append(args, i)
		sql += fmt.Sprintf(` AND isv_no=$%d`, len(args))
	}
	if k := c.Query("kind"); k != "" {
		args = append(args, k)
		sql += fmt.Sprintf(` AND kind=$%d`, len(args))
	}
	sql += ` ORDER BY id DESC LIMIT 500`
	rows, err := db.Query(c.Request.Context(), sql, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []LedgerEntry{}
	var total int64
	for rows.Next() {
		var e LedgerEntry
		if rows.Scan(&e.ID, &e.MchNo, &e.IsvNo, &e.IfCode, &e.Kind, &e.RefType, &e.RefID, &e.RefSub, &e.Amount, &e.Memo, &e.TenantID, &e.CreatedAt) == nil {
			items = append(items, e)
			total += e.Amount
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}
