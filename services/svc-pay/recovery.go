// 结果未知的资金请求的恢复。渠道超时、5xx、系统繁忙时请求可能已生效：不能判失败，也不能换单号重来；
// 用原单号重提（微信、支付宝都按单号幂等），处理中的再查询推进。所有后台扫描都先拿租约，多副本只跑一份。
package main

import (
	"context"
	"log"
	"time"

	"platfarm/svc-pay/channel"
)

// instanceID 本进程的租约持有者标识。
var instanceID = newOrderID("I")

// claimLease 取得或续上名为 name 的租约。持有者每轮续租；租约过期后其他实例才能接手。
func claimLease(ctx context.Context, name, holder string, ttl time.Duration) bool {
	tag, err := db.Exec(ctx, `INSERT INTO pay_worker_lease (name, holder, until) VALUES ($1,$2,now()+$3::interval)
		ON CONFLICT (name) DO UPDATE SET holder=$2, until=now()+$3::interval
		WHERE pay_worker_lease.until < now() OR pay_worker_lease.holder=$2`, name, holder, ttl.String())
	return err == nil && tag.RowsAffected() == 1
}

// everyLeased 每隔 interval 在持有租约时执行一次 fn。
func everyLeased(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		if !claimLease(ctx, name, instanceID, 2*interval+30*time.Second) {
			continue
		}
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			log.Printf("%s worker: %v", name, err)
		}
	}
}

// refundWorker 每分钟：created（提交结果未知或尚未提交）的退款用原退款单号重提；processing 的查询推进。
func refundWorker(ctx context.Context) {
	everyLeased(ctx, "refunds", time.Minute, sweepRefunds)
}

func sweepRefunds(ctx context.Context) error {
	rows, err := db.Query(ctx, `SELECT `+refundCols+` FROM pay_refund
		WHERE state IN ('created','processing') AND updated_at < now() - interval '1 minute'
		ORDER BY updated_at LIMIT 50`)
	if err != nil {
		return err
	}
	var batch []Refund
	for rows.Next() {
		if r, serr := scanRefund(rows); serr == nil {
			batch = append(batch, r)
		}
	}
	rows.Close()
	for _, r := range batch {
		var err error
		if r.State == refundCreated {
			_, err = resubmitRefund(ctx, r)
		} else {
			_, err = syncRefund(ctx, r)
		}
		if err != nil {
			// 连渠道都没到（参数缺失等）：记下原因并刷新 updated_at，一分钟后再试。
			log.Printf("refund %s recover: %v", r.RefundOrderID, err)
			_, _ = db.Exec(ctx, `UPDATE pay_refund SET err_msg=$2, updated_at=now()
				WHERE refund_order_id=$1 AND state IN ('created','processing')`, r.RefundOrderID, trunc(err.Error(), 500))
		}
	}
	return nil
}

// refundTarget 退款单对应的渠道、原支付单与渠道参数。
func refundTarget(ctx context.Context, r Refund) (channel.PayChannel, Order, map[string]string, map[string]string, error) {
	ch, err := channel.Get(r.IfCode)
	if err != nil {
		return nil, Order{}, nil, nil, err
	}
	order, err := findOrder(ctx, r.PayOrderID)
	if err != nil {
		return nil, Order{}, nil, nil, err
	}
	mch, err := loadMerchant(ctx, r.MchNo)
	if err != nil {
		return nil, Order{}, nil, nil, err
	}
	provider, sub, err := resolveParams(ctx, mch, r.IfCode)
	return ch, order, provider, sub, err
}

// resubmitRefund 用原退款单号重提（渠道按单号只退一笔），按结果推进。
func resubmitRefund(ctx context.Context, r Refund) (Refund, error) {
	ch, order, provider, sub, err := refundTarget(ctx, r)
	if err != nil {
		return r, err
	}
	res, err := ch.Refund(ctx, channel.RefundIn{
		RefundOrderID: r.RefundOrderID, PayOrderID: r.PayOrderID, PayAmount: order.Amount, RefundAmount: r.RefundAmount,
		Reason: r.Reason, NotifyURL: publicNotifyURL(r.IfCode, r.RefundOrderID), Provider: provider, Sub: sub,
	})
	if err != nil {
		return r, err
	}
	return applyRefundResult(ctx, r, res)
}

// syncRefund 查询处理中的退款并推进；渠道不支持查询时只能等回调。
func syncRefund(ctx context.Context, r Refund) (Refund, error) {
	ch, _, provider, sub, err := refundTarget(ctx, r)
	if err != nil {
		return r, err
	}
	q, ok := ch.(channel.RefundQuerier)
	if !ok {
		return r, nil
	}
	res, err := q.QueryRefund(ctx, channel.RefundRef{RefundOrderID: r.RefundOrderID, PayOrderID: r.PayOrderID,
		ChannelOrderNo: r.ChannelOrderNo, Provider: provider, Sub: sub})
	if err != nil {
		return r, err
	}
	if res.State == channel.StateUnknown {
		// 查询失败也刷新 updated_at，按分钟退避。
		_, err = db.Exec(ctx, `UPDATE pay_refund SET updated_at=now() WHERE refund_order_id=$1`, r.RefundOrderID)
		return r, err
	}
	return applyRefundResult(ctx, r, res)
}
