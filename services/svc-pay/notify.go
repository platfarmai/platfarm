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
)

// 重试阶梯：立即、30 秒、60 秒、150 秒、10 分、30 分、1 小时，共 7 次。
var retryLadder = []time.Duration{
	0, 30 * time.Second, 60 * time.Second, 150 * time.Second,
	10 * time.Minute, 30 * time.Minute, time.Hour,
}

var notifyClient = &http.Client{Timeout: 10 * time.Second}

// enqueueNotify 终态订单入通知队列。已存在则直接返回（回调重放不产生第二条）。
func enqueueNotify(ctx context.Context, orderType, orderID string) error {
	fields, err := notifyFields(ctx, orderType, orderID)
	if err != nil {
		return err
	}
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
	_, err = db.Exec(ctx, `INSERT INTO pay_notify
		(order_id, order_type, mch_no, mch_order_no, app_id, notify_url, payload, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (order_id, order_type) DO NOTHING`,
		orderID, orderType, fields["mchNo"], fields["mchOrderNo"], fields["appId"],
		fields["notifyUrl"], string(payload), 0)
	return err
}

func notifyFields(ctx context.Context, orderType, orderID string) (map[string]string, error) {
	f := map[string]string{"orderType": orderType}
	if orderType == "refund" {
		r, err := findRefund(ctx, orderID)
		if err != nil {
			return nil, err
		}
		f["orderId"] = r.RefundOrderID
		f["payOrderId"] = r.PayOrderID
		f["mchNo"] = r.MchNo
		f["mchOrderNo"] = r.MchRefundNo
		f["appId"] = r.AppID
		f["state"] = r.State
		f["amount"] = fmt.Sprintf("%d", r.RefundAmount)
		f["channelOrderNo"] = r.ChannelOrderNo
		f["errCode"] = r.ErrCode
		f["errMsg"] = r.ErrMsg
		f["notifyUrl"] = r.NotifyURL
		return f, nil
	}
	o, err := findOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	f["orderId"] = o.PayOrderID
	f["mchNo"] = o.MchNo
	f["mchOrderNo"] = o.MchOrderNo
	f["appId"] = o.AppID
	f["state"] = o.State
	f["amount"] = fmt.Sprintf("%d", o.Amount)
	f["channelOrderNo"] = o.ChannelOrderNo
	f["errCode"] = o.ErrCode
	f["errMsg"] = o.ErrMsg
	f["notifyUrl"] = o.NotifyURL
	return f, nil
}

// reconcileWorker 补单：回调丢失时，对未终态订单主动查渠道。
// 超过有效期仍未支付的，直接关单，不再打扰渠道。
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
		if o.ExpiredAt != nil && o.ExpiredAt.Before(now) {
			_, _ = casOrder(ctx, o.PayOrderID, o.State, orderClosed, "", "", "EXPIRED", "超时未支付")
			continue
		}
		if _, err := reconcileOrder(ctx, o); err != nil {
			log.Printf("reconcile %s: %v", o.PayOrderID, err)
		}
	}
	return nil
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
