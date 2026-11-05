// 商户开放 API。身份来自 app token（网关已验 scope），资金类请求另验商户签名：
// 头 X-Pay-App-Id / X-Pay-Timestamp / X-Pay-Nonce / X-Pay-Signature。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	if in.NotifyURL != "" && !httpsURL(in.NotifyURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "notifyUrl 必须是 https"})
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
	order := Order{
		PayOrderID: newOrderID("P"), MchNo: mch.MchNo, IsvNo: mch.IsvNo, AppID: app.AppID,
		MchType: mch.MchType, MchOrderNo: in.MchOrderNo, IfCode: passage.IfCode, WayCode: in.WayCode,
		Amount: in.Amount, Currency: or(in.Currency, "CNY"), FeeRateBps: passage.RateBps,
		FeeAmount: feeAmount(in.Amount, passage.RateBps), Subject: in.Subject, Body: in.Body,
		ClientIP: in.ClientIP, NotifyURL: in.NotifyURL, ReturnURL: in.ReturnURL,
		ExtParam: in.ExtParam, ExpiredAt: &exp, TenantID: mch.TenantID,
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
	}

	res, err := ch.Pay(ctx, channel.PayIn{
		PayOrderID: order.PayOrderID, WayCode: order.WayCode, Amount: order.Amount,
		Currency: order.Currency, Subject: order.Subject, Body: order.Body, ClientIP: order.ClientIP,
		NotifyURL: publicNotifyURL(order.IfCode, order.PayOrderID), ChannelExtra: in.ChannelExtra,
		Provider: provider, Sub: sub,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	to := foldOrderState(res)
	if to == "" {
		// UNKNOWN：渠道超时或状态不明，不动订单，交给补单查询。
		saved, _ = findOrder(ctx, order.PayOrderID)
		c.JSON(http.StatusOK, gin.H{"order": saved, "pending": true})
		return
	}
	advanced, _ := casOrder(ctx, order.PayOrderID, orderCreated, to,
		res.ChannelOrderNo, res.ChannelUser, res.ErrCode, res.ErrMsg)
	if !advanced {
		saved, _ = findOrder(ctx, order.PayOrderID)
		c.JSON(http.StatusOK, gin.H{"order": saved, "idempotent": true})
		return
	}
	saved, _ = findOrder(ctx, order.PayOrderID)
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
	if err := ch.Close(ctx, channel.OrderRef{
		PayOrderID: order.PayOrderID, ChannelOrderNo: order.ChannelOrderNo, WayCode: order.WayCode,
		Provider: provider, Sub: sub,
	}); err != nil {
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

// reconcileOrder 向渠道查一次未终态订单。成功或失败才推进并入通知队列。
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
	to := foldOrderState(res)
	if to != orderSuccess && to != orderFailed {
		return order, nil
	}
	advanced, err := casOrder(ctx, order.PayOrderID, order.State, to,
		res.ChannelOrderNo, res.ChannelUser, res.ErrCode, res.ErrMsg)
	if err != nil || !advanced {
		return findOrder(ctx, order.PayOrderID)
	}
	_ = enqueueNotify(ctx, "pay", order.PayOrderID)
	return findOrder(ctx, order.PayOrderID)
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
		_, _ = casRefund(ctx, saved.RefundOrderID, refundCreated, refundFailed, "", "SYS", err.Error())
		_ = rollupRefund(ctx, order.PayOrderID, saved.RefundAmount, false)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	to := foldRefundState(res)
	if to == "" {
		saved, _ = findRefund(ctx, saved.RefundOrderID)
		c.JSON(http.StatusOK, gin.H{"refund": saved, "pending": true})
		return
	}
	if _, err := casRefund(ctx, saved.RefundOrderID, refundCreated, to, res.ChannelOrderNo, res.ErrCode, res.ErrMsg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新退款单失败"})
		return
	}
	if to == refundSuccess || to == refundFailed {
		_ = rollupRefund(ctx, order.PayOrderID, saved.RefundAmount, to == refundSuccess)
		if saved.NotifyURL != "" {
			_ = enqueueNotify(ctx, "refund", saved.RefundOrderID)
		}
	}
	saved, _ = findRefund(ctx, saved.RefundOrderID)
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

func httpsURL(s string) bool { return len(s) > 8 && s[:8] == "https://" }

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// 保证 pgx 错误分类在本文件可见，避免漏导入。
var _ = errors.Is(pgx.ErrNoRows, pgx.ErrNoRows)
