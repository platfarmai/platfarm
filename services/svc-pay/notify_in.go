// 渠道回调入口。网关放行（public_routes），验签在渠道适配器内。
// 回调地址形如 /notify/{ifCode}/{单号}：按单号找到商户，再取该商户在该渠道的服务商参数验签——
// 每个服务商一套密钥，不能任取一个服务商去验。
// 验签失败回 400；状态推进用 CAS，同一回调来多次只生效一次。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

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
	ctx := c.Request.Context()
	pathID := strings.Trim(c.Param("orderId"), "/")
	provider, err := providerForNotify(ctx, ifCode, pathID)
	if err != nil {
		log.Printf("notify %s/%s: %v", ifCode, pathID, err)
		c.String(http.StatusBadRequest, "unknown order")
		return
	}
	n, err := ch.ParseNotify(c.Request, provider)
	if err != nil {
		log.Printf("notify %s/%s: 验签失败: %v", ifCode, pathID, err)
		c.String(http.StatusBadRequest, "signature rejected")
		return
	}
	if n.OrderType == "refund" {
		applyRefundNotify(c, n, pathID)
		return
	}
	if n.OrderID != pathID {
		log.Printf("notify %s: 回调单号 %s 与回调地址单号 %s 不一致", ifCode, n.OrderID, pathID)
		c.String(http.StatusBadRequest, "order mismatch")
		return
	}
	order, err := findOrder(ctx, n.OrderID)
	if err != nil {
		c.String(http.StatusInternalServerError, "query failed")
		return
	}
	if _, err := applyOrderResult(ctx, order, n.Result); err != nil {
		notApplied(c, ifCode, order.PayOrderID, err)
		return
	}
	ack(c, n)
}

func applyRefundNotify(c *gin.Context, n channel.Notify, pathID string) {
	ctx := c.Request.Context()
	refund, err := findRefund(ctx, n.OrderID)
	if notFound(err) {
		// 不是我们发起的退款（或支付宝通知没带退款请求号）：确认收到，免得渠道无意义重推。
		ack(c, n)
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, "query failed")
		return
	}
	// 微信退款回调打到退款单自己的地址；支付宝的退款状态通知打到原支付单地址。
	if refund.RefundOrderID != pathID && refund.PayOrderID != pathID {
		log.Printf("notify refund %s: 回调地址单号 %s 与退款单不符", refund.RefundOrderID, pathID)
		c.String(http.StatusBadRequest, "refund mismatch")
		return
	}
	if _, err := applyRefundResult(ctx, refund, n.Result); err != nil {
		notApplied(c, refund.IfCode, refund.RefundOrderID, err)
		return
	}
	ack(c, n)
}

// notApplied 回调验签通过但没有落库：不确认，渠道会重推。金额不符的需人工核查。
func notApplied(c *gin.Context, ifCode, id string, err error) {
	log.Printf("notify %s/%s: 未生效: %v", ifCode, id, err)
	if errors.Is(err, errAmountMismatch) {
		c.String(http.StatusBadRequest, "amount mismatch")
		return
	}
	c.String(http.StatusInternalServerError, "not applied")
}

func ack(c *gin.Context, n channel.Notify) {
	if n.AckBody != "" {
		c.String(n.Ack, n.AckBody)
		return
	}
	c.Status(n.Ack)
}

// providerForNotify 回调地址里的单号（支付单或退款单）→ 商户 → 该商户在该渠道的服务商参数。
func providerForNotify(ctx context.Context, ifCode, id string) (map[string]string, error) {
	if id == "" {
		return nil, errors.New("回调地址缺少单号")
	}
	var mchNo, orderIfCode string
	if o, err := findOrder(ctx, id); err == nil {
		mchNo, orderIfCode = o.MchNo, o.IfCode
	} else if r, rerr := findRefund(ctx, id); rerr == nil {
		mchNo, orderIfCode = r.MchNo, r.IfCode
	} else {
		return nil, fmt.Errorf("单号 %s 不存在", id)
	}
	if orderIfCode != ifCode {
		return nil, fmt.Errorf("单号 %s 属于渠道 %s", id, orderIfCode)
	}
	mch, err := loadMerchant(ctx, mchNo)
	if err != nil {
		return nil, err
	}
	provider, _, err := resolveParams(ctx, mch, ifCode)
	return provider, err
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
