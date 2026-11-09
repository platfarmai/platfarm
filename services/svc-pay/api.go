// 商户开放 API。身份来自 app token（网关已验 scope），资金类请求另验商户签名：
// 头 X-Pay-App-Id / X-Pay-Timestamp / X-Pay-Nonce / X-Pay-Signature。
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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"platfarm/svc-pay/channel"
)

const (
	sigWindow   = 5 * time.Minute
	nonceTTL    = 10 * time.Minute
	orderTTL    = 2 * time.Hour
	maxBodyByte = 1 << 20
)

// signedApp 校验商户签名并返回应用与其商户。失败已写响应。
func signedApp(c *gin.Context, body []byte) (App, Merchant, bool) {
	appID := c.GetHeader("X-Pay-App-Id")
	ts := c.GetHeader("X-Pay-Timestamp")
	nonce := c.GetHeader("X-Pay-Nonce")
	sig := c.GetHeader("X-Pay-Signature")
	if appID == "" || ts == "" || nonce == "" || sig == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少签名头"})
		return App{}, Merchant{}, false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "时间戳非法"})
		return App{}, Merchant{}, false
	}
	when := time.Unix(sec, 0)
	if d := time.Since(when); d > sigWindow || d < -time.Minute {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "时间戳超出窗口"})
		return App{}, Merchant{}, false
	}
	app, err := loadApp(c.Request.Context(), appID)
	if err != nil || app.State != stateActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "应用不存在或已停用"})
		return App{}, Merchant{}, false
	}
	secret, err := open(app.AppSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取应用密钥失败"})
		return App{}, Merchant{}, false
	}
	if signPayload(secret, appID, ts, nonce, string(body)) != sig {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "签名不匹配"})
		return App{}, Merchant{}, false
	}
	if !rememberNonce(c.Request.Context(), appID, nonce) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "重复请求"})
		return App{}, Merchant{}, false
	}
	mch, err := loadMerchant(c.Request.Context(), app.MchNo)
	if err != nil || mch.State != stateActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "商户不存在或已停用"})
		return App{}, Merchant{}, false
	}
	return app, mch, true
}

// rememberNonce 记录一次性随机串。无 Redis 时跳过（单副本 dev），返回 true。
func rememberNonce(ctx context.Context, appID, nonce string) bool {
	if rdb == nil {
		return true
	}
	ok, err := rdb.SetNX(ctx, "pay:nonce:"+appID+":"+nonce, "1", nonceTTL).Result()
	return err != nil || ok
}

type createOrderIn struct {
	MchOrderNo   string            `json:"mchOrderNo"`
	WayCode      string            `json:"wayCode"`
	Amount       int64             `json:"amount"`
	Currency     string            `json:"currency"`
	Subject      string            `json:"subject"`
	Body         string            `json:"body"`
	ClientIP     string            `json:"clientIp"`
	NotifyURL    string            `json:"notifyUrl"`
	ReturnURL    string            `json:"returnUrl"`
	ExtParam     string            `json:"extParam"`
	ChannelExtra map[string]string `json:"channelExtra"`
}

func handleCreateOrder(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyByte))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求失败"})
		return
	}
	app, mch, ok := signedApp(c, body)
	if !ok {
		return
	}
	var in createOrderIn
	if json.Unmarshal(body, &in) != nil || in.MchOrderNo == "" || in.WayCode == "" || in.Subject == "" || in.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchOrderNo、wayCode、subject、amount 必填且金额为正"})
		return
	}
	if in.NotifyURL != "" && !validNotifyURL(in.NotifyURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "notifyUrl 必须是公网 https 地址"})
		return
	}
	ctx := c.Request.Context()
	passage, err := loadPassage(ctx, app.AppID, in.WayCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该应用未配置此支付方式"})
		return
	}
	ch, err := channel.Get(passage.IfCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	provider, sub, err := resolveParams(ctx, mch, passage.IfCode)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "渠道参数未配置: " + err.Error()})
		return
	}

	exp := time.Now().Add(orderTTL)
	rebateBps := isvRebateRate(ctx, mch.IsvNo)
	order := Order{
		PayOrderID: newOrderID("P"), MchNo: mch.MchNo, IsvNo: mch.IsvNo, AppID: app.AppID,
		MchType: mch.MchType, MchOrderNo: in.MchOrderNo, IfCode: passage.IfCode, WayCode: in.WayCode,
		Amount: in.Amount, Currency: or(in.Currency, "CNY"), FeeRateBps: passage.RateBps,
		FeeAmount: feeAmount(in.Amount, passage.RateBps), Subject: in.Subject, Body: in.Body,
		ClientIP: in.ClientIP, NotifyURL: in.NotifyURL, ReturnURL: in.ReturnURL,
		ExtParam: in.ExtParam, ExpiredAt: &exp, TenantID: mch.TenantID,
		RebateRateBps: rebateBps, RebateAmount: feeAmount(in.Amount, rebateBps),
	}
	if len(in.ChannelExtra) > 0 {
		extra, _ := json.Marshal(in.ChannelExtra)
		order.ChannelExtra = string(extra)
	}
	saved, dup, err := insertOrder(ctx, order)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "建单失败"})
		return
	}
	// 幂等：同商户订单号已存在且已下过单，直接返回，不再次调用渠道。
	if dup && saved.State != orderCreated {
		c.JSON(http.StatusOK, gin.H{"order": saved, "idempotent": true})
		return
	}
	if dup {
		order = saved
		if order.ExpiredAt != nil && order.ExpiredAt.Before(time.Now()) {
			c.JSON(http.StatusConflict, gin.H{"error": "订单已过期，请换商户订单号重新下单", "order": saved})
			return
		}
	}
	var expireAt time.Time
	if order.ExpiredAt != nil {
		expireAt = *order.ExpiredAt
	}

	res, err := ch.Pay(ctx, channel.PayIn{
		PayOrderID: order.PayOrderID, WayCode: order.WayCode, Amount: order.Amount,
		Currency: order.Currency, Subject: order.Subject, Body: order.Body, ClientIP: order.ClientIP,
		NotifyURL: publicNotifyURL(order.IfCode, order.PayOrderID), ChannelExtra: in.ChannelExtra,
		ExpireAt: expireAt, Provider: provider, Sub: sub,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 同步即终态的渠道（如付款码）也在这里记台账 / 入通知，不能只靠回调。
	saved, err = applyOrderResult(ctx, order, res)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新订单失败"})
		return
	}
	if foldOrderState(res) == "" {
		// UNKNOWN：渠道超时或状态不明，不动订单，交给补单查询。
		c.JSON(http.StatusOK, gin.H{"order": saved, "pending": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": saved, "credential": res.Credential})
}

func handleCloseOrder(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyByte))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求失败"})
		return
	}
	app, mch, ok := signedApp(c, body)
	if !ok {
		return
	}
	mchOrderNo := c.Param("mchOrderNo")
	if mchOrderNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchOrderNo 必填"})
		return
	}
	ctx := c.Request.Context()
	order, err := findOrderByMch(ctx, app.MchNo, mchOrderNo)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "订单不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if order.State == orderClosed || order.State == orderFailed {
		c.JSON(http.StatusOK, gin.H{"order": order, "idempotent": true})
		return
	}
	if order.State != orderCreated && order.State != orderPaying {
		c.JSON(http.StatusConflict, gin.H{"error": "订单状态 " + order.State + " 不可关闭"})
		return
	}
	ch, err := channel.Get(order.IfCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	provider, sub, err := resolveParams(ctx, mch, order.IfCode)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "渠道参数未配置"})
		return
	}
	ref := channel.OrderRef{
		PayOrderID: order.PayOrderID, ChannelOrderNo: order.ChannelOrderNo, WayCode: order.WayCode,
		Provider: provider, Sub: sub,
	}
	if err := ch.Close(ctx, ref); err != nil {
		// 关单失败最常见的原因是已经付了：查一次，已支付就如实推进并告诉商户。
		if res, qerr := ch.Query(ctx, ref); qerr == nil && res.State == channel.StateSuccess {
			if paid, aerr := applyOrderResult(ctx, order, res); aerr == nil {
				c.JSON(http.StatusConflict, gin.H{"error": "订单已支付，不能关闭", "order": paid})
				return
			}
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if _, err := casOrder(ctx, order.PayOrderID, order.State, orderClosed, "", "", "CLOSED", "商户关单"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "关单失败"})
		return
	}
	order, _ = findOrder(ctx, order.PayOrderID)
	c.JSON(http.StatusOK, gin.H{"order": order})
}

// reconcileOrder 向渠道查一次未终态订单，按确定结果推进（见 applyOrderResult）。
func reconcileOrder(ctx context.Context, order Order) (Order, error) {
	if order.State != orderCreated && order.State != orderPaying {
		return order, nil
	}
	ch, err := channel.Get(order.IfCode)
	if err != nil {
		return order, err
	}
	mch, err := loadMerchant(ctx, order.MchNo)
	if err != nil {
		return order, err
	}
	provider, sub, err := resolveParams(ctx, mch, order.IfCode)
	if err != nil {
		return order, err
	}
	res, err := ch.Query(ctx, channel.OrderRef{
		PayOrderID: order.PayOrderID, ChannelOrderNo: order.ChannelOrderNo, WayCode: order.WayCode,
		Provider: provider, Sub: sub,
	})
	if err != nil {
		return order, err
	}
	return applyOrderResult(ctx, order, res)
}

var errAmountMismatch = errors.New("渠道金额与本地单据不一致")

// applyOrderResult 按渠道结果推进支付单，只认确定结果：
//   - UNKNOWN 不动；WAITING 只把 created 推到 paying；
//   - SUCCESS / FAIL 把未终态的单推到终态（台账、下游通知同事务）；
//   - 已关闭 / 已失败的单收到确定的 SUCCESS：钱已经收了，推进到 success 并标 LATE_PAYMENT，
//     商户会收到成功通知（可再走退款），而不是被静默吞掉。
//
// 渠道报告的成功金额与订单不符时不推进，返回 errAmountMismatch。
func applyOrderResult(ctx context.Context, o Order, res channel.Result) (Order, error) {
	if res.State == channel.StateSuccess && res.Amount != 0 && res.Amount != o.Amount {
		log.Printf(`{"event":"order_amount_mismatch","payOrderId":%q,"order":%d,"channel":%d}`, o.PayOrderID, o.Amount, res.Amount)
		return o, errAmountMismatch
	}
	to := foldOrderState(res)
	var err error
	switch {
	case to == "":
	case o.State == orderCreated || o.State == orderPaying:
		if to != orderPaying {
			_, err = finishOrder(ctx, o.PayOrderID, o.State, to, res.ChannelOrderNo, res.ChannelUser, res.ErrCode, res.ErrMsg)
		} else if o.State == orderCreated {
			_, err = casOrder(ctx, o.PayOrderID, orderCreated, orderPaying, res.ChannelOrderNo, res.ChannelUser, "", "")
		}
	case to == orderSuccess && (o.State == orderClosed || o.State == orderFailed):
		var late bool
		late, err = finishOrder(ctx, o.PayOrderID, o.State, orderSuccess, res.ChannelOrderNo, res.ChannelUser,
			"LATE_PAYMENT", "订单已"+o.State+"后渠道确认已支付")
		if late {
			log.Printf(`{"event":"late_payment","payOrderId":%q,"from":%q,"amount":%d}`, o.PayOrderID, o.State, o.Amount)
		}
	}
	if err != nil {
		return o, err
	}
	return findOrder(ctx, o.PayOrderID)
}

// applyRefundResult 按渠道结果推进退款单：UNKNOWN 保持原状态（created 由 worker 用原退款单号重提，
// 渠道按单号幂等），只记下原因；WAITING 把 created 推到 processing；SUCCESS / FAIL 进终态。
func applyRefundResult(ctx context.Context, r Refund, res channel.Result) (Refund, error) {
	if res.State == channel.StateSuccess && res.Amount != 0 && res.Amount != r.RefundAmount {
		log.Printf(`{"event":"refund_amount_mismatch","refundOrderId":%q,"refund":%d,"channel":%d}`, r.RefundOrderID, r.RefundAmount, res.Amount)
		return r, errAmountMismatch
	}
	to := foldRefundState(res)
	var err error
	switch {
	case r.State != refundCreated && r.State != refundProcessing:
	case to == "":
		_, err = db.Exec(ctx, `UPDATE pay_refund SET err_code=$2, err_msg=$3, updated_at=now()
			WHERE refund_order_id=$1 AND state IN ('created','processing')`, r.RefundOrderID, res.ErrCode, trunc(res.ErrMsg, 500))
	case to == refundProcessing:
		if r.State == refundCreated {
			_, err = casRefund(ctx, r.RefundOrderID, refundCreated, refundProcessing, res.ChannelOrderNo, res.ErrCode, trunc(res.ErrMsg, 500))
		}
	default:
		_, err = finishRefund(ctx, r.RefundOrderID, r.State, to, res.ChannelOrderNo, res.ErrCode, trunc(res.ErrMsg, 500))
	}
	if err != nil {
		return r, err
	}
	return findRefund(ctx, r.RefundOrderID)
}

func handleQueryOrder(c *gin.Context) {
	app, _, ok := signedApp(c, nil)
	if !ok {
		return
	}
	mchOrderNo := c.Param("mchOrderNo")
	o, err := findOrderByMch(c.Request.Context(), app.MchNo, mchOrderNo)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "订单不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": o})
}

type createRefundIn struct {
	MchOrderNo   string `json:"mchOrderNo"`
	MchRefundNo  string `json:"mchRefundNo"`
	RefundAmount int64  `json:"refundAmount"`
	Reason       string `json:"reason"`
	NotifyURL    string `json:"notifyUrl"`
}

func handleCreateRefund(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyByte))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求失败"})
		return
	}
	app, mch, ok := signedApp(c, body)
	if !ok {
		return
	}
	var in createRefundIn
	if json.Unmarshal(body, &in) != nil || in.MchOrderNo == "" || in.MchRefundNo == "" || in.RefundAmount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchOrderNo、mchRefundNo、refundAmount 必填"})
		return
	}
	if in.NotifyURL != "" && !validNotifyURL(in.NotifyURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "notifyUrl 必须是公网 https 地址"})
		return
	}
	ctx := c.Request.Context()
	if existing, ferr := findRefundByMch(ctx, mch.MchNo, in.MchRefundNo); ferr == nil {
		c.JSON(http.StatusOK, gin.H{"refund": existing, "idempotent": true})
		return
	}
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
	ch, err := channel.Get(order.IfCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	provider, sub, err := resolveParams(ctx, mch, order.IfCode)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "渠道参数未配置"})
		return
	}
	refund := Refund{
		RefundOrderID: newOrderID("R"), PayOrderID: order.PayOrderID, MchNo: mch.MchNo,
		IsvNo: mch.IsvNo, AppID: app.AppID, MchRefundNo: in.MchRefundNo, IfCode: order.IfCode,
		WayCode: order.WayCode, RefundAmount: in.RefundAmount, Reason: in.Reason,
		NotifyURL: in.NotifyURL, TenantID: mch.TenantID,
	}
	saved, err := createRefund(ctx, refund)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	res, err := ch.Refund(ctx, channel.RefundIn{
		RefundOrderID: saved.RefundOrderID, PayOrderID: order.PayOrderID,
		PayAmount: order.Amount, RefundAmount: saved.RefundAmount, Reason: saved.Reason,
		NotifyURL: publicNotifyURL(order.IfCode, saved.RefundOrderID), Provider: provider, Sub: sub,
	})
	if err != nil {
		// 适配器在发出请求前就拒绝了（参数或能力问题）：渠道侧没有这笔退款，确定失败并退回额度。
		_, _ = finishRefund(ctx, saved.RefundOrderID, refundCreated, refundFailed, "", "SYS", trunc(err.Error(), 500))
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	saved, err = applyRefundResult(ctx, saved, res)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新退款单失败"})
		return
	}
	if foldRefundState(res) == "" {
		c.JSON(http.StatusOK, gin.H{"refund": saved, "pending": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"refund": saved})
}

// foldOrderState 渠道四态 → 支付单状态。UNKNOWN 返回空，调用方不得推进。
func foldOrderState(res channel.Result) string {
	switch res.State {
	case channel.StateSuccess:
		return orderSuccess
	case channel.StateFail:
		return orderFailed
	case channel.StateWaiting:
		return orderPaying
	default:
		return ""
	}
}

// foldRefundState 渠道四态 → 退款单状态。
func foldRefundState(res channel.Result) string {
	switch res.State {
	case channel.StateSuccess:
		return refundSuccess
	case channel.StateFail:
		return refundFailed
	case channel.StateWaiting:
		return refundProcessing
	default:
		return ""
	}
}

// resolveParams 拼出服务商参数与子商户参数。
func resolveParams(ctx context.Context, mch Merchant, ifCode string) (map[string]string, map[string]string, error) {
	sub, err := loadChannelParams(ctx, "merchant", mch.MchNo, ifCode)
	if err != nil {
		return nil, nil, fmt.Errorf("商户 %s", mch.MchNo)
	}
	if mch.MchType == 1 || mch.IsvNo == "" {
		return sub, map[string]string{}, nil
	}
	provider, err := loadChannelParams(ctx, "isv", mch.IsvNo, ifCode)
	if err != nil {
		return nil, nil, fmt.Errorf("服务商 %s", mch.IsvNo)
	}
	return provider, sub, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// 保证 pgx 错误分类在本文件可见，避免漏导入。
var _ = errors.Is(pgx.ErrNoRows, pgx.ErrNoRows)
