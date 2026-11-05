// 渠道回调入口。网关放行（public_routes），验签在渠道适配器内。
// 验签失败回 400；状态推进用 CAS，同一回调来多次只生效一次。
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"platfarm/svc-pay/channel"
)

func handleChannelNotify(c *gin.Context) {
	ifCode := c.Param("ifCode")
	ch, err := channel.Get(ifCode)
	if err != nil {
		c.String(http.StatusNotFound, "unknown channel")
		return
	}
	provider, err := providerForNotify(c, ifCode)
	if err != nil {
		log.Printf("notify %s: 找服务商参数失败: %v", ifCode, err)
		c.String(http.StatusBadRequest, "unknown merchant")
		return
	}
	n, err := ch.ParseNotify(c.Request, provider)
	if err != nil {
		log.Printf("notify %s: 验签失败: %v", ifCode, err)
		c.String(http.StatusBadRequest, "signature rejected")
		return
	}
	ctx := c.Request.Context()
	if n.OrderType == "refund" {
		applyRefundNotify(c, n)
		return
	}
	order, ferr := findOrder(ctx, n.OrderID)
	if notFound(ferr) {
		// 不是我们的订单：回 204 让渠道停止重推，避免无意义重试。
		c.Status(http.StatusNoContent)
		return
	}
	if ferr != nil {
		c.String(http.StatusInternalServerError, "query failed")
		return
	}
	to := foldOrderState(n.Result)
	if to != "" && (order.State == orderCreated || order.State == orderPaying) {
		if advanced, _ := casOrder(ctx, order.PayOrderID, order.State, to,
			n.Result.ChannelOrderNo, n.Result.ChannelUser, n.Result.ErrCode, n.Result.ErrMsg); advanced &&
			(to == orderSuccess || to == orderFailed) {
			_ = enqueueNotify(ctx, "pay", order.PayOrderID)
		}
	}
	ack(c, n)
}

func applyRefundNotify(c *gin.Context, n channel.Notify) {
	ctx := c.Request.Context()
	refund, err := findRefund(ctx, n.OrderID)
	if notFound(err) {
		c.Status(http.StatusNoContent)
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, "query failed")
		return
	}
	to := foldRefundState(n.Result)
	if to == refundProcessing {
		// 渠道仍在处理：保持原状态，等下一次回调或补单。
		ack(c, n)
		return
	}
	if to != "" && (refund.State == refundCreated || refund.State == refundProcessing) {
		if advanced, _ := casRefund(ctx, refund.RefundOrderID, refund.State, to,
			n.Result.ChannelOrderNo, n.Result.ErrCode, n.Result.ErrMsg); advanced &&
			(to == refundSuccess || to == refundFailed) {
			_ = rollupRefund(ctx, refund.PayOrderID, refund.RefundAmount, to == refundSuccess)
			_ = enqueueNotify(ctx, "refund", refund.RefundOrderID)
		}
	}
	ack(c, n)
}

func ack(c *gin.Context, n channel.Notify) {
	if n.AckBody != "" {
		c.String(n.Ack, n.AckBody)
		return
	}
	c.Status(n.Ack)
}

// providerForNotify 回调只带渠道订单号，验签要用服务商参数。
// M1 一个渠道对应一个服务商：取该 ifCode 下唯一的 isv 配置。
func providerForNotify(c *gin.Context, ifCode string) (map[string]string, error) {
	var ownerID string
	err := db.QueryRow(c.Request.Context(), `SELECT owner_id FROM pay_channel_config
		WHERE owner_type='isv' AND if_code=$1 AND state='active' LIMIT 1`, ifCode).Scan(&ownerID)
	if err != nil {
		return nil, err
	}
	return loadChannelParams(c.Request.Context(), "isv", ownerID, ifCode)
}

// publicNotifyURL 渠道回调地址，必须公网可达。PAY_PUBLIC_BASE 未配置时退回占位，
// 仅用于本地联调（渠道会回调失败，靠补单查询兜底）。
func publicNotifyURL(ifCode, orderID string) string {
	base := os.Getenv("PAY_PUBLIC_BASE")
	if base == "" {
		base = "https://pay.local"
	}
	return base + "/api/pay/notify/" + ifCode + "/" + orderID
}
