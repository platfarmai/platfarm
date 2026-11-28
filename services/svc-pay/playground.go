// 商户中心的调试工具：替商户把一个开放 API 请求拼好、签好名、真的打一次，
// 再把「签名串 / 签名 / 请求头 / curl / 响应」原样摆出来，对照着就能写出自己的接入代码。
//
// 签名在服务端完成：签名密钥库里是密封的，这里解封后只用来算签名，明文不出进程、不进响应。
// 请求在进程内直接交给开放 API 的同一组处理函数（main.go registerOpenRoutes），
// 验签、防重放、幂等、渠道调用与对外完全一致；只是不经网关，所以不需要 app token。
//
// 会动钱的操作只作用于调试单（商户订单号以 DBG 开头）：
// 这个工具凭登录密码就能用，不能让它成为绕过 appSecret 给任意真实订单退款的后门。
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	qrcode "github.com/skip2/go-qrcode"

	"platfarm/svc-pay/channel/sandbox"
)

const (
	debugPrefix  = "DBG"            // 调试单的商户订单号 / 退款单号前缀
	debugRealMax = 100              // 走真实渠道时单笔上限（分）：会真实扣款
	debugSandMax = 100_000_000      // 沙箱单笔上限（分）
	openPrefix   = mount + "/v1"    // 开放 API 路径前缀
	tokenHint    = "<accessToken>"  // curl 示例里的占位：appSecret 平台只存哈希，替商户换不出 token
	billDateHint = "格式 YYYY-MM-DD" // 账单日期提示
)

type playgroundParams struct {
	MchOrderNo   string            `json:"mchOrderNo"`
	WayCode      string            `json:"wayCode"`
	Amount       int64             `json:"amount"`
	Subject      string            `json:"subject"`
	NotifyURL    string            `json:"notifyUrl"`
	ChannelExtra map[string]string `json:"channelExtra"`
	State        string            `json:"state"`
	MchRefundNo  string            `json:"mchRefundNo"`
	RefundAmount int64             `json:"refundAmount"`
	Reason       string            `json:"reason"`
	StartDate    string            `json:"startDate"`
	EndDate      string            `json:"endDate"`
	Type         string            `json:"type"`
	Page         int               `json:"page"`
	PageSize     int               `json:"pageSize"`
}

// openCall 一次拼好的开放 API 调用。
type openCall struct {
	Method string
	URI    string // 路径 + 查询串，原样参与签名
	Body   string // 原始请求体，原样参与签名
	Scope  string // 对外调用时 app token 需要的 scope
	Writes bool   // 会改动单据（记审计日志）
}

type badParam string

func (b badParam) Error() string { return string(b) }

func debugNo() string {
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	return debugPrefix + time.Now().In(billZone).Format("20060102150405") + hex.EncodeToString(buf)
}

func isDebugNo(no string) bool { return strings.HasPrefix(no, debugPrefix) }

// rawJSON 序列化成要发出去的请求体：不转义 <>&，没有结尾换行。
func rawJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimRight(buf.String(), "\n")
}

// buildOpenCall 把调试表单折成一次开放 API 调用。参数不合规返回 badParam。
func buildOpenCall(ctx context.Context, app App, op string, p *playgroundParams) (openCall, error) {
	orderPath := func(suffix string) (string, error) {
		if p.MchOrderNo == "" {
			return "", badParam("mchOrderNo 必填")
		}
		return openPrefix + "/orders/" + url.PathEscape(p.MchOrderNo) + suffix, nil
	}
	needDebug := func(what string) error {
		if !isDebugNo(p.MchOrderNo) {
			return badParam("调试工具只能" + what + "调试单（商户订单号以 " + debugPrefix + " 开头）。正式订单请由你的业务系统调用开放 API")
		}
		return nil
	}
	switch op {
	case "order.create":
		if p.MchOrderNo == "" {
			p.MchOrderNo = debugNo()
		}
		if !isDebugNo(p.MchOrderNo) {
			return openCall{}, badParam("调试单的商户订单号须以 " + debugPrefix + " 开头（留空自动生成）")
		}
		if p.WayCode == "" || p.Amount <= 0 {
			return openCall{}, badParam("wayCode、amount 必填且金额为正")
		}
		limit, where := int64(debugRealMax), "走真实渠道（会真实扣款）"
		if passage, err := loadPassage(ctx, app.AppID, p.WayCode); err == nil && passage.IfCode == sandbox.IfCode {
			limit, where = debugSandMax, "走沙箱"
		}
		if p.Amount > limit {
			return openCall{}, badParam(fmt.Sprintf("调试单%s时单笔最多 %d 分", where, limit))
		}
		if p.Subject == "" {
			p.Subject = "接口调试"
		}
		body := struct {
			MchOrderNo   string            `json:"mchOrderNo"`
			WayCode      string            `json:"wayCode"`
			Amount       int64             `json:"amount"`
			Subject      string            `json:"subject"`
			NotifyURL    string            `json:"notifyUrl,omitempty"`
			ChannelExtra map[string]string `json:"channelExtra,omitempty"`
		}{p.MchOrderNo, p.WayCode, p.Amount, p.Subject, p.NotifyURL, p.ChannelExtra}
		return openCall{http.MethodPost, openPrefix + "/orders", rawJSON(body), "pay.order.create", true}, nil
	case "order.query":
		path, err := orderPath("")
		return openCall{http.MethodGet, path, "", "pay.order.query", false}, err
	case "order.close":
		path, err := orderPath("/close")
		if err == nil {
			err = needDebug("关闭")
		}
		return openCall{http.MethodPost, path, "", "pay.order.create", true}, err
	case "order.mock":
		path, err := orderPath("/mock")
		if err == nil {
			err = needDebug("模拟支付")
		}
		if p.State == "" {
			p.State = orderSuccess
		}
		return openCall{http.MethodPost, path, rawJSON(map[string]string{"state": p.State}), "pay.order.create", true}, err
	case "refund.create":
		if p.MchOrderNo == "" || p.RefundAmount <= 0 {
			return openCall{}, badParam("mchOrderNo、refundAmount 必填且金额为正")
		}
		if err := needDebug("退款"); err != nil {
			return openCall{}, err
		}
		if p.MchRefundNo == "" {
			p.MchRefundNo = debugNo()
		}
		body := struct {
			MchOrderNo   string `json:"mchOrderNo"`
			MchRefundNo  string `json:"mchRefundNo"`
			RefundAmount int64  `json:"refundAmount"`
			Reason       string `json:"reason,omitempty"`
			NotifyURL    string `json:"notifyUrl,omitempty"`
		}{p.MchOrderNo, p.MchRefundNo, p.RefundAmount, p.Reason, p.NotifyURL}
		return openCall{http.MethodPost, openPrefix + "/refunds", rawJSON(body), "pay.refund.create", true}, nil
	case "refund.query":
		if p.MchRefundNo == "" {
			return openCall{}, badParam("mchRefundNo 必填")
		}
		return openCall{http.MethodGet, openPrefix + "/refunds/" + url.PathEscape(p.MchRefundNo), "", "pay.refund.query", false}, nil
	case "bill.transactions", "bill.summary":
		if p.StartDate == "" {
			return openCall{}, badParam("startDate 必填，" + billDateHint)
		}
		// 手工拼查询串：参数顺序就是签名时的顺序，照抄即可。
		q := "startDate=" + url.QueryEscape(p.StartDate)
		if p.EndDate != "" {
			q += "&endDate=" + url.QueryEscape(p.EndDate)
		}
		if op == "bill.summary" {
			return openCall{http.MethodGet, openPrefix + "/bills/summary?" + q, "", "pay.bill.query", false}, nil
		}
		if p.Type != "" {
			q += "&type=" + url.QueryEscape(p.Type)
		}
		if p.Page > 0 {
			q += "&page=" + strconv.Itoa(p.Page)
		}
		if p.PageSize > 0 {
			q += "&pageSize=" + strconv.Itoa(p.PageSize)
		}
		return openCall{http.MethodGet, openPrefix + "/bills/transactions?" + q, "", "pay.bill.query", false}, nil
	}
	return openCall{}, badParam("不支持的调试操作 " + op)
}

// openEngine 只挂开放 API 的引擎，调试工具在进程内向它发请求。
var openEngine = sync.OnceValue(func() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	registerOpenRoutes(r.Group(openPrefix))
	return r
})

// memResponse 接住进程内调用的响应。
type memResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (m *memResponse) Header() http.Header { return m.header }
func (m *memResponse) WriteHeader(code int) {
	if m.status == 0 {
		m.status = code
	}
}
func (m *memResponse) Write(b []byte) (int, error) {
	if m.status == 0 {
		m.status = http.StatusOK
	}
	return m.body.Write(b)
}

type headerKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func curlFor(base string, call openCall, headers []headerKV) string {
	var b strings.Builder
	b.WriteString("curl -X " + call.Method + " " + shellQuote(base+call.URI))
	for _, h := range headers {
		b.WriteString(" \\\n  -H " + shellQuote(h.Name+": "+h.Value))
	}
	if call.Body != "" {
		b.WriteString(" \\\n  --data-raw " + shellQuote(call.Body))
	}
	return b.String()
}

// qrDataURI 把扫码支付的链接画成二维码（PNG data URI），调试时直接拿手机扫。
func qrDataURI(text string) string {
	png, err := qrcode.Encode(text, qrcode.Medium, 240)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// handlePlayground POST /portal/playground {appId, op, params, dryRun}
// dryRun=true 只签名不发送：随机串没有被用掉，5 分钟内把 curl 里的 <accessToken> 换成真的就能原样重放。
func handlePlayground(c *gin.Context) {
	mch, _, ok := portalCaller(c)
	if !ok {
		return
	}
	var in struct {
		AppID  string           `json:"appId"`
		Op     string           `json:"op"`
		DryRun bool             `json:"dryRun"`
		Params playgroundParams `json:"params"`
	}
	if c.ShouldBindJSON(&in) != nil || in.AppID == "" || in.Op == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "appId、op 必填"})
		return
	}
	app, ok := portalApp(c, mch, in.AppID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	call, err := buildOpenCall(ctx, app, in.Op, &in.Params)
	if err != nil {
		if bad, isBad := err.(badParam); isBad {
			c.JSON(http.StatusBadRequest, gin.H{"error": string(bad)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成请求失败"})
		return
	}
	secret, err := open(app.AppSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取应用密钥失败"})
		return
	}

	var rdr *strings.Reader = strings.NewReader(call.Body)
	req, err := http.NewRequestWithContext(ctx, call.Method, call.URI, rdr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求地址不合法"})
		return
	}
	nonceBuf := make([]byte, 16)
	_, _ = rand.Read(nonceBuf)
	ts, nonce := strconv.FormatInt(time.Now().Unix(), 10), hex.EncodeToString(nonceBuf)
	uri := req.URL.RequestURI() // 与验签时取的值同源
	toSign := signingString(app.AppID, ts, nonce, call.Method, uri, call.Body)
	signature := signPayload(secret, app.AppID, ts, nonce, call.Method, uri, call.Body)

	headers := []headerKV{{"Authorization", "Bearer " + tokenHint}}
	if call.Body != "" {
		headers = append(headers, headerKV{"Content-Type", "application/json"})
	}
	headers = append(headers, headerKV{"X-Pay-App-Id", app.AppID}, headerKV{"X-Pay-Timestamp", ts},
		headerKV{"X-Pay-Nonce", nonce}, headerKV{"X-Pay-Signature", signature})
	base := publicBase(c)
	out := gin.H{
		"op":     in.Op,
		"dryRun": in.DryRun,
		"scope":  call.Scope,
		"request": gin.H{"method": call.Method, "uri": uri, "url": base + uri, "headers": headers, "body": call.Body},
		"signing": gin.H{"stringToSign": toSign, "signature": signature},
		"curl":    curlFor(base, call, headers),
	}
	if in.DryRun {
		c.JSON(http.StatusOK, out)
		return
	}

	for _, h := range headers[1:] { // 进程内调用不经网关，不带 Authorization
		req.Header.Set(h.Name, h.Value)
	}
	rec := &memResponse{header: http.Header{}}
	start := time.Now()
	openEngine().ServeHTTP(rec, req)
	out["elapsedMs"] = time.Since(start).Milliseconds()
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	resp := gin.H{"status": rec.status}
	if raw := rec.body.Bytes(); json.Valid(raw) {
		resp["body"] = json.RawMessage(raw)
		// 扫码支付：把二维码链接画出来，手机直接扫。沙箱的链接是占位符，扫不了，不画。
		var parsed struct {
			Credential map[string]string `json:"credential"`
		}
		if json.Unmarshal(raw, &parsed) == nil && parsed.Credential["sandbox"] != "true" {
			for _, key := range []string{"codeUrl", "qrCode"} {
				if link := parsed.Credential[key]; link != "" {
					resp["qr"] = qrDataURI(link)
					break
				}
			}
		}
	} else {
		resp["body"] = rec.body.String()
	}
	out["response"] = resp
	if call.Writes {
		log.Printf(`{"event":"portal_playground","mchNo":%q,"appId":%q,"op":%q,"uri":%q,"status":%d,"by":%q}`,
			mch.MchNo, app.AppID, in.Op, uri, rec.status, claimsOf(c).Username)
	}
	c.JSON(http.StatusOK, out)
}
