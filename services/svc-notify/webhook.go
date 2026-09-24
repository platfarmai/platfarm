package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// 与 svc-jobs 同一条 SSRF 边界：通知 webhook 也从 core-net 发出。
var blockedWebhookHosts = map[string]bool{
	"localhost": true, "postgres": true, "redis": true, "plugin-pg": true,
	"minio": true, "loki": true, "gateway": true, "grafana": true, "promtail": true,
	"auth": true, "metadata.google.internal": true,
}

func validateWebhook(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("webhook must be an http(s) URL")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || blockedWebhookHosts[host] || strings.HasPrefix(host, "svc-") {
		return fmt.Errorf("webhook host %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return fmt.Errorf("webhook must not target a raw IP address")
	}
	return nil
}
