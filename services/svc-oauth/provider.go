package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// ExternalProfile 已由 provider 验证的外部身份，交给 auth external-login 兑换。
type ExternalProfile struct {
	Provider    string
	Subject     string // externalId：unionid / openid / github id 等
	DisplayName string
}

// Provider 第三方登录提供方接口：Start 发起授权，Callback 用 code 换身份。
type Provider interface {
	ID() string
	Start(state string) (redirectURL string, err error)
	Callback(code string) (*ExternalProfile, error)
}

// ErrNotConfigured 凭据缺失：provider 仍注册，但 Start 返回此错误 → HTTP 501。
var ErrNotConfigured = errors.New("provider not configured")

var (
	providersMu sync.RWMutex
	providers   = map[string]Provider{}
)

func registerProvider(p Provider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers[p.ID()] = p
}

func lookupProvider(id string) (Provider, bool) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	p, ok := providers[id]
	return p, ok
}

func selfURL() string {
	if v := os.Getenv("SELF_URL"); v != "" {
		return v
	}
	return "http://localhost:18000"
}

func envOrEmpty(name string) string { return os.Getenv(name) }

func misconfig(id string) error {
	return fmt.Errorf("%s: %w", id, ErrNotConfigured)
}
