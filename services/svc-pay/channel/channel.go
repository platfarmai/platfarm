// Package channel 是支付渠道适配层。业务层只依赖本文件的类型，
// 渠道差异（微信 v3、支付宝）全部封在各自的子包里。
package channel

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// 渠道返回四态。UNKNOWN（超时、验签失败）永远不推动订单状态。
const (
	StateSuccess = "SUCCESS"
	StateFail    = "FAIL"
	StateWaiting = "WAITING"
	StateUnknown = "UNKNOWN"
)

const (
	FundDirect     = "direct"     // 直清：渠道结算给子商户
	FundControlled = "controlled" // 归集：资金由平台控制（M4）
)

// Result 渠道调用的归一结果。
type Result struct {
	State            string // SUCCESS|FAIL|WAITING|UNKNOWN
	ChannelOrderNo   string
	ChannelUser      string
	ErrCode          string
	ErrMsg           string
	Credential       any    // 返回给下游继续支付的数据（如 code_url、prepay_id）
	Raw              string // 上游原文，仅排障，不回传下游
}

// PayIn 下单输入。渠道参数由业务层解析好后传入，适配器不碰数据库。
type PayIn struct {
	PayOrderID   string
	WayCode      string
	Amount       int64
	Currency     string
	Subject      string
	Body         string
	ClientIP     string
	NotifyURL    string
	ChannelExtra map[string]string
	// Provider 服务商参数，Sub 子商户参数，均为已解密的明文 map。
	Provider map[string]string
	Sub      map[string]string
}

// OrderRef 查单/关单定位。
type OrderRef struct {
	PayOrderID     string
	ChannelOrderNo string
	WayCode        string
	Provider       map[string]string
	Sub            map[string]string
}

// RefundIn 退款输入。
type RefundIn struct {
	RefundOrderID string
	PayOrderID    string
	PayAmount     int64
	RefundAmount  int64
	Reason        string
	NotifyURL     string
	Provider      map[string]string
	Sub           map[string]string
}

// Notify 渠道回调验签解密后的结果。
type Notify struct {
	OrderID        string
	OrderType      string // pay|refund
	Result         Result
	Ack            int    // 回给渠道的 HTTP 状态
	AckBody        string // 回给渠道的正文
}

// PayChannel 一个渠道实现。实现必须并发安全。
type PayChannel interface {
	IfCode() string
	FundMode() string
	Ways() []string
	Pay(ctx context.Context, in PayIn) (Result, error)
	Query(ctx context.Context, ref OrderRef) (Result, error)
	Close(ctx context.Context, ref OrderRef) error
	Refund(ctx context.Context, in RefundIn) (Result, error)
	ParseNotify(r *http.Request, provider map[string]string) (Notify, error)
}

var (
	mu       sync.RWMutex
	registry = map[string]PayChannel{}
)

// Register 启动期注册。重复 ifCode 直接 panic——那是编程错误。
func Register(c PayChannel) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[c.IfCode()]; ok {
		panic("duplicate channel " + c.IfCode())
	}
	registry[c.IfCode()] = c
}

// Get 按 ifCode 取适配器。
func Get(ifCode string) (PayChannel, error) {
	mu.RLock()
	defer mu.RUnlock()
	c, ok := registry[ifCode]
	if !ok {
		return nil, fmt.Errorf("unknown channel %q", ifCode)
	}
	return c, nil
}

// Ways 返回某渠道支持的支付方式；渠道不存在返回 nil。
func Ways(ifCode string) []string {
	mu.RLock()
	defer mu.RUnlock()
	if c, ok := registry[ifCode]; ok {
		return c.Ways()
	}
	return nil
}
