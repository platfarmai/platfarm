// 下游商户通知。pay_notify 是 outbox：每笔订单一行，投递由本服务的 worker 完成，
// 多副本用 FOR UPDATE SKIP LOCKED 取件。商户回复正文 SUCCESS 才算送达。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"platfarm/svc-pay/channel"
)

// 重试阶梯：立即、30 秒、60 秒、150 秒、10 分、30 分、1 小时，共 7 次。
var retryLadder = []time.Duration{
	0, 30 * time.Second, 60 * time.Second, 150 * time.Second,
	10 * time.Minute, 30 * time.Minute, time.Hour,
}

var notifyClient = &http.Client{Timeout: 10 * time.Second}

// enqueueNotify 按单号重读后入通知队列（终态动作之外的补发入口）。
func enqueueNotify(ctx context.Context, orderType, orderID string) error {
	if orderType == "refund" {
		r, err := findRefund(ctx, orderID)
		if err != nil {
			return err
		}
		return enqueueNotifyFields(ctx, db, "refund", refundNotifyFields(r), r.TenantID)
	}
	o, err := findOrder(ctx, orderID)
	if err != nil {
		return err
	}
	return enqueueNotifyFields(ctx, db, "pay", orderNotifyFields(o), o.TenantID)
}

// enqueueNotifyFields 签名并写 outbox（每单一行）。同一单再次入队时：内容没变是空操作（回调重放）；
// 内容变了（如关单后又确认支付成功）就换成新内容并重置投递，商户一定能收到最终状态。
func enqueueNotifyFields(ctx context.Context, q querier, orderType string, fields map[string]string, tenantID int) error {
	if fields["notifyUrl"] == "" {
		return nil
	}
	app, err := loadApp(ctx, fields["appId"])
	if err != nil {
		return err
	}
	secret, err := open(app.AppSecret)
	if err != nil {
		return err
	}
	fields["sign"] = signFields(secret, fields)
	payload, _ := json.Marshal(fields)
	_, err = q.Exec(ctx, `INSERT INTO pay_notify
		(order_id, order_type, mch_no, mch_order_no, app_id, notify_url, payload, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (order_id, order_type) DO UPDATE SET payload=EXCLUDED.payload, notify_url=EXCLUDED.notify_url,
			state='notifying', notify_count=0, res_result='', next_retry_at=now(), updated_at=now()
		WHERE pay_notify.payload IS DISTINCT FROM EXCLUDED.payload`,
		fields["orderId"], orderType, fields["mchNo"], fields["mchOrderNo"], fields["appId"],
		fields["notifyUrl"], string(payload), tenantID)
	return err
}

func orderNotifyFields(o Order) map[string]string {
	return map[string]string{
		"orderType": "pay", "orderId": o.PayOrderID, "mchNo": o.MchNo, "mchOrderNo": o.MchOrderNo,
		"appId": o.AppID, "state": o.State, "amount": fmt.Sprintf("%d", o.Amount),
		"channelOrderNo": o.ChannelOrderNo, "errCode": o.ErrCode, "errMsg": o.ErrMsg, "notifyUrl": o.NotifyURL,
	}
}

func refundNotifyFields(r Refund) map[string]string {
	return map[string]string{
		"orderType": "refund", "orderId": r.RefundOrderID, "payOrderId": r.PayOrderID, "mchNo": r.MchNo,
		"mchOrderNo": r.MchRefundNo, "appId": r.AppID, "state": r.State, "amount": fmt.Sprintf("%d", r.RefundAmount),
		"channelOrderNo": r.ChannelOrderNo, "errCode": r.ErrCode, "errMsg": r.ErrMsg, "notifyUrl": r.NotifyURL,
	}
}

// expireGrace 过期后仍查不到渠道交易（或渠道不可达）时，再等多久本地关单。下单时已把失效时间传给渠道，
// 过期后渠道拒绝支付；宽限只覆盖过期前一刻已经在付款中的单。
const expireGrace = 10 * time.Minute

// reconcileWorker 补单：回调丢失时，对未终态订单主动查渠道；过期的单先在渠道关单再本地关闭。
func reconcileWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		if err := sweepOrders(ctx); err != nil && ctx.Err() == nil {
			log.Printf("reconcile: %v", err)
		}
	}
}

func sweepOrders(ctx context.Context) error {
	rows, err := db.Query(ctx, `SELECT `+orderCols+` FROM pay_order
		WHERE state IN ('created','paying') AND created_at < now() - interval '30 seconds'
		ORDER BY created_at LIMIT 50`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var batch []Order
	for rows.Next() {
		o, serr := scanOrder(rows)
		if serr == nil {
			batch = append(batch, o)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now()
	for _, o := range batch {
		var err error
		if o.ExpiredAt != nil && o.ExpiredAt.Before(now) {
			err = expireOrder(ctx, o)
		} else {
			_, err = reconcileOrder(ctx, o)
		}
		if err != nil {
			log.Printf("reconcile %s: %v", o.PayOrderID, err)
		}
	}
	return nil
}

// expireOrder 过期未付的单：先查渠道（可能刚付成功）；渠道仍是待支付就在渠道关单，关成功才本地关闭；
// 渠道查无此单或不可达时，过了宽限期再本地关闭。之后若仍收到支付成功，按迟到支付推进（applyOrderResult）。
func expireOrder(ctx context.Context, o Order) error {
	ch, err := channel.Get(o.IfCode)
	if err != nil {
		return err
	}
	ref := channel.OrderRef{PayOrderID: o.PayOrderID, ChannelOrderNo: o.ChannelOrderNo, WayCode: o.WayCode}
	mch, err := loadMerchant(ctx, o.MchNo)
	if err == nil {
		ref.Provider, ref.Sub, err = resolveParams(ctx, mch, o.IfCode)
	}
	if err == nil {
		res, qerr := ch.Query(ctx, ref)
		switch {
		case qerr != nil:
		case res.State == channel.StateSuccess || res.State == channel.StateFail:
			_, err := applyOrderResult(ctx, o, res)
			return err
		case res.State == channel.StateWaiting:
			if cerr := ch.Close(ctx, ref); cerr != nil {
				// 下一轮再试；若期间已付款，下一轮查询会拿到 SUCCESS。
				return fmt.Errorf("渠道关单: %w", cerr)
			}
			_, err := casOrder(ctx, o.PayOrderID, o.State, orderClosed, "", "", "EXPIRED", "超时未支付，已在渠道关单")
			return err
		}
	}
	if o.ExpiredAt == nil || time.Since(*o.ExpiredAt) < expireGrace {
		return nil
	}
	_, err = casOrder(ctx, o.PayOrderID, o.State, orderClosed, "", "", "EXPIRED", "超时未支付（渠道查无此单或不可达）")
	return err
}

func notifyWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		if err := deliverDue(ctx); err != nil && ctx.Err() == nil {
			log.Printf("notify worker: %v", err)
		}
	}
}

type dueNotify struct {
	id      int64
	url     string
	payload string
	count   int
	limit   int
}

func deliverDue(ctx context.Context) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `SELECT id, notify_url, payload, notify_count, notify_limit FROM pay_notify
		WHERE state='notifying' AND next_retry_at <= now()
		ORDER BY id LIMIT 20 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	var batch []dueNotify
	for rows.Next() {
		var d dueNotify
		if rows.Scan(&d.id, &d.url, &d.payload, &d.count, &d.limit) == nil {
			batch = append(batch, d)
		}
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	for _, d := range batch {
		res, ok := postNotify(ctx, d.url, d.payload)
		next := d.count + 1
		switch {
		case ok:
			_, _ = db.Exec(ctx, `UPDATE pay_notify SET state='success', notify_count=$2, res_result=$3, updated_at=now()
				WHERE id=$1`, d.id, next, trunc(res, 500))
		case next >= d.limit:
			_, _ = db.Exec(ctx, `UPDATE pay_notify SET state='failed', notify_count=$2, res_result=$3, updated_at=now()
				WHERE id=$1`, d.id, next, trunc(res, 500))
		default:
			delay := retryLadder[len(retryLadder)-1]
			if next < len(retryLadder) {
				delay = retryLadder[next]
			}
			_, _ = db.Exec(ctx, `UPDATE pay_notify SET notify_count=$2, res_result=$3,
				next_retry_at=now()+$4::interval, updated_at=now() WHERE id=$1`,
				d.id, next, trunc(res, 500), delay.String())
		}
	}
	return nil
}

// postNotify 把 payload POST 给商户。仅 https，且拒绝内网地址——notify_url 来自外部输入。
func postNotify(ctx context.Context, target, payload string) (string, bool) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "notify_url 必须是 https", false
	}
	if hostIsPrivate(u.Hostname()) {
		return "notify_url 指向内网地址，已拒绝", false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(payload))
	if err != nil {
		return err.Error(), false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := notifyClient.Do(req)
	if err != nil {
		return err.Error(), false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(body))
	return text, resp.StatusCode/100 == 2 && strings.EqualFold(text, "SUCCESS")
}

func hostIsPrivate(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
	}
	return false
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
