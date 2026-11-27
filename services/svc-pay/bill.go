// 商户查账（开放 API，scope pay.bill.query）。
// 交易流水 = 支付成功的订单 + 退款成功的退款单，按成功时间归日（北京时间），只看调用方自己商户的数据。
// 手续费是本系统按通道费率记的账（与台账 fee / fee_refund 同一口径），不是渠道结算单；资金以渠道结算为准。
package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	billMaxDays     = 31
	billDefaultPage = 50
	billMaxPage     = 200
	billDateLayout  = "2006-01-02"
)

// billZone 账单按北京时间切日。固定偏移，不依赖镜像里有没有时区数据库。
var billZone = time.FixedZone("Asia/Shanghai", 8*3600)

// BillItem 一条交易流水。金额单位分。
type BillItem struct {
	Type           string    `json:"type"`           // pay 支付 | refund 退款
	TradeTime      time.Time `json:"tradeTime"`      // 支付 / 退款成功时间（北京时间）
	BillDate       string    `json:"billDate"`       // tradeTime 所在的账单日 YYYY-MM-DD
	AppID          string    `json:"appId"`          // 下单应用 ID
	MchOrderNo     string    `json:"mchOrderNo"`     // 商户订单号
	PayOrderID     string    `json:"payOrderId"`     // 平台订单号
	MchRefundNo    string    `json:"mchRefundNo"`    // 商户退款单号（仅退款）
	RefundOrderID  string    `json:"refundOrderId"`  // 平台退款单号（仅退款）
	ChannelOrderNo string    `json:"channelOrderNo"` // 渠道流水号（支付为渠道交易号，退款为渠道退款单号）
	IfCode         string    `json:"ifCode"`
	WayCode        string    `json:"wayCode"`
	Subject        string    `json:"subject"`
	Amount         int64     `json:"amount"`    // 交易金额：支付为订单金额，退款为退款金额，均为正数
	FeeAmount      int64     `json:"feeAmount"` // 支付为记的手续费，退款为冲回的手续费，均为正数
	NetAmount      int64     `json:"netAmount"` // 入账净额（有符号）：支付 = amount − feeAmount；退款 = −(amount − feeAmount)
}

// BillSummary 一段时间（或一天）的汇总。金额单位分。
type BillSummary struct {
	Date            string `json:"date,omitempty"`
	PayCount        int64  `json:"payCount"`
	PayAmount       int64  `json:"payAmount"`
	RefundCount     int64  `json:"refundCount"`
	RefundAmount    int64  `json:"refundAmount"`
	FeeAmount       int64  `json:"feeAmount"`       // 支付记的手续费合计
	FeeRefundAmount int64  `json:"feeRefundAmount"` // 退款冲回的手续费合计
	NetAmount       int64  `json:"netAmount"`       // payAmount − refundAmount − feeAmount + feeRefundAmount
}

func (s *BillSummary) add(o BillSummary) {
	s.PayCount += o.PayCount
	s.PayAmount += o.PayAmount
	s.RefundCount += o.RefundCount
	s.RefundAmount += o.RefundAmount
	s.FeeAmount += o.FeeAmount
	s.FeeRefundAmount += o.FeeRefundAmount
	s.NetAmount += o.NetAmount
}

// billRange 解析 startDate / endDate（北京时间自然日，闭区间），返回 [from, to) 时间范围。失败已写响应。
func billRange(c *gin.Context) (from, to time.Time, ok bool) {
	start := c.Query("startDate")
	if start == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startDate 必填，格式 YYYY-MM-DD"})
		return from, to, false
	}
	end := c.Query("endDate")
	if end == "" {
		end = start
	}
	from, err := time.ParseInLocation(billDateLayout, start, billZone)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startDate 格式须为 YYYY-MM-DD"})
		return from, to, false
	}
	last, err := time.ParseInLocation(billDateLayout, end, billZone)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "endDate 格式须为 YYYY-MM-DD"})
		return from, to, false
	}
	if last.Before(from) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "endDate 不能早于 startDate"})
		return from, to, false
	}
	to = last.AddDate(0, 0, 1)
	if to.Sub(from) > billMaxDays*24*time.Hour {
		c.JSON(http.StatusBadRequest, gin.H{"error": "一次最多查询 " + strconv.Itoa(billMaxDays) + " 天"})
		return from, to, false
	}
	return from, to, true
}

// billType 解析 type 过滤：空 = 全部。失败已写响应。
func billType(c *gin.Context) (string, bool) {
	switch t := c.Query("type"); t {
	case "", "pay", "refund":
		return t, true
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "type 只能是 pay 或 refund，不传为全部"})
		return "", false
	}
}

func positiveInt(raw string, def, max int) (int, bool) {
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, false
	}
	if max > 0 && n > max {
		n = max
	}
	return n, true
}

// feeRefundSQL 退款冲回的手续费，与 ledger.go 的 proportional(o.fee_amount, r.refund_amount, o.amount) 同一口径。
const feeRefundSQL = `(CASE WHEN o.amount <= 0 OR o.fee_amount = 0 THEN 0
	WHEN r.refund_amount >= o.amount THEN o.fee_amount
	ELSE (o.fee_amount * r.refund_amount + o.amount / 2) / o.amount END)`

// billRowsSQL 流水明细：$1 商户号，$2/$3 时间范围 [from, to)，$4 类型过滤（空 = 全部）。
const billRowsSQL = `
	SELECT 'pay' AS type, o.success_at AS trade_time, o.app_id, o.mch_order_no, o.pay_order_id, '' AS mch_refund_no, '' AS refund_order_id,
		o.channel_order_no, o.if_code, o.way_code, o.subject, o.amount, o.fee_amount AS fee
	FROM pay_order o
	WHERE o.mch_no=$1 AND o.state='success' AND o.success_at >= $2 AND o.success_at < $3 AND $4 IN ('', 'pay')
	UNION ALL
	SELECT 'refund', r.success_at, r.app_id, o.mch_order_no, r.pay_order_id, r.mch_refund_no, r.refund_order_id,
		r.channel_order_no, r.if_code, r.way_code, o.subject, r.refund_amount, ` + feeRefundSQL + `
	FROM pay_refund r JOIN pay_order o ON o.pay_order_id = r.pay_order_id
	WHERE r.mch_no=$1 AND r.state='success' AND r.success_at >= $2 AND r.success_at < $3 AND $4 IN ('', 'refund')`

// handleBillTransactions GET /v1/bills/transactions?startDate=&endDate=&type=&page=&pageSize=
// 按成功时间升序返回交易流水，分页。
func handleBillTransactions(c *gin.Context) {
	app, _, ok := signedApp(c, nil)
	if !ok {
		return
	}
	from, to, ok := billRange(c)
	if !ok {
		return
	}
	kind, ok := billType(c)
	if !ok {
		return
	}
	page, ok1 := positiveInt(c.Query("page"), 1, 0)
	size, ok2 := positiveInt(c.Query("pageSize"), billDefaultPage, billMaxPage)
	if !ok1 || !ok2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page、pageSize 须为正整数"})
		return
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRow(ctx, `SELECT count(*) FROM (`+billRowsSQL+`) t`, app.MchNo, from, to, kind).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	rows, err := db.Query(ctx, `SELECT * FROM (`+billRowsSQL+`) t
		ORDER BY trade_time, pay_order_id, refund_order_id LIMIT $5 OFFSET $6`,
		app.MchNo, from, to, kind, size, int64(page-1)*int64(size))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []BillItem{}
	for rows.Next() {
		var it BillItem
		if err := rows.Scan(&it.Type, &it.TradeTime, &it.AppID, &it.MchOrderNo, &it.PayOrderID, &it.MchRefundNo, &it.RefundOrderID,
			&it.ChannelOrderNo, &it.IfCode, &it.WayCode, &it.Subject, &it.Amount, &it.FeeAmount); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return
		}
		it.TradeTime = it.TradeTime.In(billZone)
		it.BillDate = it.TradeTime.Format(billDateLayout)
		it.NetAmount = it.Amount - it.FeeAmount
		if it.Type == "refund" {
			it.NetAmount = -it.NetAmount
		}
		items = append(items, it)
	}
	if rows.Err() != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"mchNo":     app.MchNo,
		"startDate": from.Format(billDateLayout),
		"endDate":   to.AddDate(0, 0, -1).Format(billDateLayout),
		"page":      page,
		"pageSize":  size,
		"total":     total,
		"hasMore":   int64(page)*int64(size) < total,
		"items":     items,
	})
}

// handleBillSummary GET /v1/bills/summary?startDate=&endDate=
// 按账单日汇总笔数与金额；没有交易的日期也给一行 0，方便下游逐日对账。
func handleBillSummary(c *gin.Context) {
	app, _, ok := signedApp(c, nil)
	if !ok {
		return
	}
	from, to, ok := billRange(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	byDay := map[string]*BillSummary{}
	days := []*BillSummary{}
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		s := &BillSummary{Date: d.Format(billDateLayout)}
		byDay[s.Date] = s
		days = append(days, s)
	}

	prows, err := db.Query(ctx, `SELECT to_char(o.success_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS d,
			count(*), COALESCE(sum(o.amount),0), COALESCE(sum(o.fee_amount),0)
		FROM pay_order o
		WHERE o.mch_no=$1 AND o.state='success' AND o.success_at >= $2 AND o.success_at < $3
		GROUP BY d`, app.MchNo, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	for prows.Next() {
		var day string
		var n, amount, fee int64
		if err := prows.Scan(&day, &n, &amount, &fee); err != nil {
			prows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return
		}
		if s := byDay[day]; s != nil {
			s.PayCount, s.PayAmount, s.FeeAmount = n, amount, fee
		}
	}
	prows.Close()
	if prows.Err() != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}

	rrows, err := db.Query(ctx, `SELECT to_char(r.success_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS d,
			count(*), COALESCE(sum(r.refund_amount),0), COALESCE(sum(`+feeRefundSQL+`),0)
		FROM pay_refund r JOIN pay_order o ON o.pay_order_id = r.pay_order_id
		WHERE r.mch_no=$1 AND r.state='success' AND r.success_at >= $2 AND r.success_at < $3
		GROUP BY d`, app.MchNo, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	for rrows.Next() {
		var day string
		var n, amount, fee int64
		if err := rrows.Scan(&day, &n, &amount, &fee); err != nil {
			rrows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return
		}
		if s := byDay[day]; s != nil {
			s.RefundCount, s.RefundAmount, s.FeeRefundAmount = n, amount, fee
		}
	}
	rrows.Close()
	if rrows.Err() != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}

	var total BillSummary
	for _, s := range days {
		s.NetAmount = s.PayAmount - s.RefundAmount - s.FeeAmount + s.FeeRefundAmount
		total.add(*s)
	}
	c.JSON(http.StatusOK, gin.H{
		"mchNo":     app.MchNo,
		"startDate": from.Format(billDateLayout),
		"endDate":   to.AddDate(0, 0, -1).Format(billDateLayout),
		"total":     total,
		"days":      days,
	})
}
