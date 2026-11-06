// 对账（specs/030 M3）：按账单日拉渠道交易账单，与本地终态订单/退款逐笔比对，差异落 pay_recon_diff。
// 批次按 (if_code, isv_no, bill_date, bill_type) 幂等；每日 cron 由 svc-jobs 回调 /internal/recon/daily 触发。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
)

var cst = time.FixedZone("Asia/Shanghai", 8*3600)

// localTrade 本地一笔终态支付或退款（对账输入）。
type localTrade struct {
	Kind           string // pay|refund
	ID             string // pay_order_id / refund_order_id
	ChannelOrderNo string
	Amount         int64
	State          string
}

// reconDiff 一条差异（纯数据，便于测试）。
type reconDiff struct {
	Type           string `json:"diffType"`
	Kind           string `json:"kind"`
	LocalID        string `json:"localId"`
	ChannelOrderNo string `json:"channelOrderNo"`
	SubMchID       string `json:"subMchid"`
	LocalAmount    int64  `json:"localAmount"`
	ChannelAmount  int64  `json:"channelAmount"`
	LocalState     string `json:"localState"`
	ChannelState   string `json:"channelState"`
}

// diffBill 渠道账单 vs 本地。支付行按 OutTradeNo 对 pay_order_id，退款行按 OutRefundNo 对 refund_order_id。
// 渠道成功而本地无 → missing_local；本地成功而渠道无 → missing_channel；金额不同 → amount_mismatch；
// 渠道成功但本地非成功 → state_mismatch。
func diffBill(rows []channel.BillRow, local []localTrade) []reconDiff {
	localByID := map[string]localTrade{}
	for _, t := range local {
		localByID[t.Kind+":"+t.ID] = t
	}
	seen := map[string]bool{}
	var diffs []reconDiff
	for _, r := range rows {
		kind, id, amount, state := "pay", r.OutTradeNo, r.Amount, r.TradeState
		if r.IsRefund() {
			kind, id, amount, state = "refund", r.OutRefundNo, r.RefundAmount, r.RefundState
		}
		if id == "" {
			continue
		}
		key := kind + ":" + id
		seen[key] = true
		channelOK := state == "SUCCESS" || (kind == "pay" && state == "REFUND")
		if !channelOK {
			continue // 渠道侧未成功的行不参与
		}
		t, ok := localByID[key]
		switch {
		case !ok:
			diffs = append(diffs, reconDiff{Type: "missing_local", Kind: kind, LocalID: id, ChannelOrderNo: r.ChannelOrderNo,
				SubMchID: r.SubMchID, ChannelAmount: amount, ChannelState: state})
		case t.State != "success":
			diffs = append(diffs, reconDiff{Type: "state_mismatch", Kind: kind, LocalID: id, ChannelOrderNo: r.ChannelOrderNo,
				SubMchID: r.SubMchID, LocalAmount: t.Amount, ChannelAmount: amount, LocalState: t.State, ChannelState: state})
		case t.Amount != amount:
			diffs = append(diffs, reconDiff{Type: "amount_mismatch", Kind: kind, LocalID: id, ChannelOrderNo: r.ChannelOrderNo,
				SubMchID: r.SubMchID, LocalAmount: t.Amount, ChannelAmount: amount, LocalState: t.State, ChannelState: state})
		}
	}
	for _, t := range local {
		if t.State == "success" && !seen[t.Kind+":"+t.ID] {
			diffs = append(diffs, reconDiff{Type: "missing_channel", Kind: t.Kind, LocalID: t.ID, ChannelOrderNo: t.ChannelOrderNo,
				LocalAmount: t.Amount, LocalState: t.State})
		}
	}
	return diffs
}

// loadLocalTrades 账单日（北京时间）内成功的支付与退款。退款行同时覆盖账单里 REFUND 状态的支付行。
func loadLocalTrades(ctx context.Context, ifCode, isvNo string, billDate time.Time) ([]localTrade, error) {
	start := time.Date(billDate.Year(), billDate.Month(), billDate.Day(), 0, 0, 0, 0, cst)
	end := start.Add(24 * time.Hour)
	var out []localTrade
	rows, err := db.Query(ctx, `SELECT pay_order_id, channel_order_no, amount, state FROM pay_order
		WHERE if_code=$1 AND isv_no=$2 AND state='success' AND success_at >= $3 AND success_at < $4`, ifCode, isvNo, start, end)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t localTrade
		if rows.Scan(&t.ID, &t.ChannelOrderNo, &t.Amount, &t.State) == nil {
			t.Kind = "pay"
			out = append(out, t)
		}
	}
	rows.Close()
	rows, err = db.Query(ctx, `SELECT refund_order_id, channel_order_no, refund_amount, state FROM pay_refund
		WHERE if_code=$1 AND isv_no=$2 AND state='success' AND success_at >= $3 AND success_at < $4`, ifCode, isvNo, start, end)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t localTrade
		if rows.Scan(&t.ID, &t.ChannelOrderNo, &t.Amount, &t.State) == nil {
			t.Kind = "refund"
			out = append(out, t)
		}
	}
	rows.Close()
	return out, nil
}

// ReconBatch pay_recon_batch 一行。
type ReconBatch struct {
	ID            int64  `json:"id"`
	IfCode        string `json:"ifCode"`
	IsvNo         string `json:"isvNo"`
	BillDate      string `json:"billDate"`
	BillType      string `json:"billType"`
	State         string `json:"state"`
	ChannelCount  int    `json:"channelCount"`
	ChannelAmount int64  `json:"channelAmount"`
	LocalCount    int    `json:"localCount"`
	LocalAmount   int64  `json:"localAmount"`
	DiffCount     int    `json:"diffCount"`
	ErrMsg        string `json:"errMsg"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

const batchCols = `id, if_code, isv_no, bill_date::text, bill_type, state, channel_count, channel_amount, local_count,
	local_amount, diff_count, err_msg, created_at::text, updated_at::text`

func scanBatch(row interface{ Scan(...any) error }) (ReconBatch, error) {
	var b ReconBatch
	err := row.Scan(&b.ID, &b.IfCode, &b.IsvNo, &b.BillDate, &b.BillType, &b.State, &b.ChannelCount, &b.ChannelAmount,
		&b.LocalCount, &b.LocalAmount, &b.DiffCount, &b.ErrMsg, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

// runRecon 执行一次对账。已 done 的批次不重跑（force=true 清掉差异重跑）。
func runRecon(ctx context.Context, ifCode, isvNo string, billDate time.Time, billType string, force bool) (ReconBatch, error) {
	if billType == "" {
		billType = "ALL"
	}
	date := billDate.In(cst).Format("2006-01-02")
	var id int64
	err := db.QueryRow(ctx, `INSERT INTO pay_recon_batch (if_code, isv_no, bill_date, bill_type)
		VALUES ($1,$2,$3,$4) ON CONFLICT (if_code, isv_no, bill_date, bill_type) DO UPDATE SET updated_at=now()
		RETURNING id`, ifCode, isvNo, date, billType).Scan(&id)
	if err != nil {
		return ReconBatch{}, err
	}
	batch, err := scanBatch(db.QueryRow(ctx, `SELECT `+batchCols+` FROM pay_recon_batch WHERE id=$1`, id))
	if err != nil {
		return ReconBatch{}, err
	}
	if (batch.State == "done" || batch.State == "no_bill") && !force {
		return batch, nil
	}
	if force {
		_, _ = db.Exec(ctx, `DELETE FROM pay_recon_diff WHERE batch_id=$1`, id)
	}
	fail := func(msg string) (ReconBatch, error) {
		_, _ = db.Exec(ctx, `UPDATE pay_recon_batch SET state='failed', err_msg=$2, updated_at=now() WHERE id=$1`, id, trunc(msg, 500))
		return scanBatch(db.QueryRow(ctx, `SELECT `+batchCols+` FROM pay_recon_batch WHERE id=$1`, id))
	}
	bc, err := channel.Bill(ifCode)
	if err != nil {
		return fail(err.Error())
	}
	provider, err := loadChannelParams(ctx, "isv", isvNo, ifCode)
	if err != nil {
		return fail("服务商渠道参数未配置")
	}
	rows, err := bc.TradeBill(ctx, date, billType, provider, nil)
	noBill := errors.Is(err, channel.ErrNoBill)
	if noBill {
		rows = nil
	} else if err != nil {
		return fail(err.Error())
	}
	local, err := loadLocalTrades(ctx, ifCode, isvNo, billDate.In(cst))
	if err != nil {
		return fail(err.Error())
	}
	diffs := diffBill(rows, local)
	var channelAmount, localAmount int64
	for _, r := range rows {
		if r.IsRefund() {
			channelAmount -= r.RefundAmount
		} else if r.TradeState == "SUCCESS" || r.TradeState == "REFUND" {
			channelAmount += r.Amount
		}
	}
	for _, t := range local {
		if t.Kind == "pay" {
			localAmount += t.Amount
		} else {
			localAmount -= t.Amount
		}
	}
	for _, d := range diffs {
		if _, err := db.Exec(ctx, `INSERT INTO pay_recon_diff (batch_id, diff_type, kind, local_id, channel_order_no, sub_mchid,
			local_amount, channel_amount, local_state, channel_state) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			id, d.Type, d.Kind, d.LocalID, d.ChannelOrderNo, d.SubMchID, d.LocalAmount, d.ChannelAmount, d.LocalState, d.ChannelState); err != nil {
			return fail(err.Error())
		}
	}
	state := "done"
	if noBill {
		state = "no_bill"
	}
	_, err = db.Exec(ctx, `UPDATE pay_recon_batch SET state=$2, channel_count=$3, channel_amount=$4, local_count=$5,
		local_amount=$6, diff_count=$7, err_msg='', updated_at=now() WHERE id=$1`,
		id, state, len(rows), channelAmount, len(local), localAmount, len(diffs))
	if err != nil {
		return ReconBatch{}, err
	}
	log.Printf(`{"event":"recon","ifCode":%q,"isvNo":%q,"billDate":%q,"channel":%d,"local":%d,"diffs":%d}`,
		ifCode, isvNo, date, len(rows), len(local), len(diffs))
	return scanBatch(db.QueryRow(ctx, `SELECT `+batchCols+` FROM pay_recon_batch WHERE id=$1`, id))
}

// runDailyRecon 对所有配置了账单渠道参数的服务商跑昨日对账。
func runDailyRecon(ctx context.Context, day time.Time) ([]ReconBatch, []string) {
	rows, err := db.Query(ctx, `SELECT DISTINCT owner_id, if_code FROM pay_channel_config WHERE owner_type='isv' AND state='active'`)
	if err != nil {
		return nil, []string{err.Error()}
	}
	type pair struct{ isv, ifCode string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if rows.Scan(&p.isv, &p.ifCode) == nil {
			pairs = append(pairs, p)
		}
	}
	rows.Close()
	var batches []ReconBatch
	var errs []string
	for _, p := range pairs {
		if _, err := channel.Bill(p.ifCode); err != nil {
			continue
		}
		b, err := runRecon(ctx, p.ifCode, p.isv, day, "ALL", false)
		if err != nil {
			errs = append(errs, p.ifCode+"/"+p.isv+": "+err.Error())
			continue
		}
		batches = append(batches, b)
	}
	return batches, errs
}

// ── 接口 ────────────────────────────────────────────────────

// handleRunRecon POST /admin/recon/run {ifCode, isvNo, billDate, billType, force}
func handleRunRecon(c *gin.Context) {
	if _, ok := requireRole(c, roleOperator); !ok {
		return
	}
	var in struct {
		IfCode   string `json:"ifCode"`
		IsvNo    string `json:"isvNo"`
		BillDate string `json:"billDate"`
		BillType string `json:"billType"`
		Force    bool   `json:"force"`
	}
	if c.ShouldBindJSON(&in) != nil || in.IfCode == "" || in.IsvNo == "" || in.BillDate == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ifCode、isvNo、billDate 必填"})
		return
	}
	day, err := time.ParseInLocation("2006-01-02", in.BillDate, cst)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "billDate 格式 YYYY-MM-DD"})
		return
	}
	b, err := runRecon(c.Request.Context(), in.IfCode, in.IsvNo, day, in.BillType, in.Force)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"batch": b})
}

// handleListReconBatches GET /admin/recon/batches
func handleListReconBatches(c *gin.Context) {
	if _, ok := requireRole(c, roleOperator); !ok {
		return
	}
	rows, err := db.Query(c.Request.Context(), `SELECT `+batchCols+` FROM pay_recon_batch ORDER BY bill_date DESC, id DESC LIMIT 200`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []ReconBatch{}
	for rows.Next() {
		if b, serr := scanBatch(rows); serr == nil {
			items = append(items, b)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// handleListReconDiffs GET /admin/recon/batches/:id/diffs
func handleListReconDiffs(c *gin.Context) {
	if _, ok := requireRole(c, roleOperator); !ok {
		return
	}
	rows, err := db.Query(c.Request.Context(), `SELECT id, diff_type, kind, local_id, channel_order_no, sub_mchid, local_amount,
		channel_amount, local_state, channel_state, resolved, note FROM pay_recon_diff WHERE batch_id=$1 ORDER BY id`, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	type diffRow struct {
		ID int64 `json:"id"`
		reconDiff
		Resolved bool   `json:"resolved"`
		Note     string `json:"note"`
	}
	items := []diffRow{}
	for rows.Next() {
		var d diffRow
		if rows.Scan(&d.ID, &d.Type, &d.Kind, &d.LocalID, &d.ChannelOrderNo, &d.SubMchID, &d.LocalAmount, &d.ChannelAmount,
			&d.LocalState, &d.ChannelState, &d.Resolved, &d.Note) == nil {
			items = append(items, d)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// handleResolveReconDiff POST /admin/recon/diffs/:id/resolve {note}
func handleResolveReconDiff(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&in)
	tag, err := db.Exec(c.Request.Context(), `UPDATE pay_recon_diff SET resolved=true, note=$2 WHERE id=$1`,
		c.Param("id"), trunc(claims.Username+": "+in.Note, 500))
	if err != nil || tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "差异不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleDailyRecon POST /internal/recon/daily：svc-jobs cron 回调（服务 token）或 admin 手动触发。
// 默认对昨日（北京时间）；body 可带 {"date":"YYYY-MM-DD"}。
func handleDailyRecon(c *gin.Context) {
	claims, err := identity(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if !(claims.TokenType == "service" && claims.Svc == "svc-jobs") && claims.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "需要 svc-jobs 服务 token 或平台管理员"})
		return
	}
	day := time.Now().In(cst).AddDate(0, 0, -1)
	var in struct {
		Date    string `json:"date"`
		Payload string `json:"payload"` // svc-jobs 投递体
	}
	_ = c.ShouldBindJSON(&in)
	if in.Date == "" && in.Payload != "" {
		var p struct {
			Date string `json:"date"`
		}
		_ = json.Unmarshal([]byte(in.Payload), &p)
		in.Date = p.Date
	}
	if in.Date != "" {
		if d, perr := time.ParseInLocation("2006-01-02", in.Date, cst); perr == nil {
			day = d
		}
	}
	batches, errs := runDailyRecon(c.Request.Context(), day)
	if batches == nil {
		batches = []ReconBatch{}
	}
	c.JSON(http.StatusOK, gin.H{"date": day.Format("2006-01-02"), "batches": batches, "errors": errs})
}

// handleEnsureReconSchedule POST /admin/recon/schedule {cron}：用调用者（须 admin）的 token 在 svc-jobs 建每日计划。
// svc-jobs 的计划接口只认 admin 用户，所以这里不能用服务 token，必须转发操作者的身份。
func handleEnsureReconSchedule(c *gin.Context) {
	if _, ok := requireRole(c, roleOperator); !ok {
		return
	}
	var in struct {
		Cron string `json:"cron"`
	}
	_ = c.ShouldBindJSON(&in)
	if in.Cron == "" {
		in.Cron = "30 9 * * *" // 微信账单次日 10 点前生成（北京时间）；容器多为 UTC，9:30 UTC = 17:30 CST 稍保守
	}
	ctx := c.Request.Context()
	jobs := envOr("JOBS_URL", "http://svc-jobs:8080")
	bearer := c.GetHeader("Authorization")
	// 已存在同名计划则直接返回。
	if code, raw, err := jobsCall(ctx, http.MethodGet, jobs+"/api/jobs/schedules", bearer, nil); err == nil && code == http.StatusOK {
		var out struct {
			Items []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		}
		_ = json.Unmarshal(raw, &out)
		for _, s := range out.Items {
			if s.Name == "svc-pay-recon-daily" {
				c.JSON(http.StatusOK, gin.H{"ok": true, "scheduleId": s.ID, "existing": true})
				return
			}
		}
	}
	body, _ := json.Marshal(map[string]any{
		"name": "svc-pay-recon-daily", "cron": in.Cron, "type": "pay.recon", "payload": "{}",
		"webhook": envOr("PAY_INTERNAL_BASE", "http://svc-pay:8080") + mount + "/internal/recon/daily",
	})
	code, raw, err := jobsCall(ctx, http.MethodPost, jobs+"/api/jobs/schedules", bearer, body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "svc-jobs 不可用: " + err.Error()})
		return
	}
	c.Data(code, "application/json", raw)
}

func jobsCall(ctx context.Context, method, url, bearer string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := svcHTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, raw, nil
}

func registerDivisionReconRoutes(admin *gin.RouterGroup) {
	admin.POST("/division/receivers", handleAddReceiver)
	admin.GET("/division/receivers", handleListReceivers)
	admin.GET("/divisions", handleListDivisions)
	admin.POST("/divisions/:no/sync", handleSyncDivision)
	admin.GET("/ledger", handleLedger)
	admin.POST("/recon/run", handleRunRecon)
	admin.GET("/recon/batches", handleListReconBatches)
	admin.GET("/recon/batches/:id/diffs", handleListReconDiffs)
	admin.POST("/recon/diffs/:id/resolve", handleResolveReconDiff)
	admin.POST("/recon/schedule", handleEnsureReconSchedule)
}

var _ = fmt.Sprintf
