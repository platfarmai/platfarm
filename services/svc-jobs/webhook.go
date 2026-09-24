// webhook 目标约束（审计 H1）：回调从 core-net 发出，裸 IP、localhost、
// 云元数据地址和平台基础设施主机名一律拒绝，避免把内网面暴露成 SSRF。
package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

var blockedWebhookHosts = map[string]bool{
	"localhost": true, "postgres": true, "redis": true, "plugin-pg": true,
	"minio": true, "loki": true, "gateway": true, "grafana": true, "promtail": true,
	"metadata.google.internal": true,
}

func validateWebhook(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("webhook must be an http(s) URL")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || blockedWebhookHosts[host] {
		return fmt.Errorf("webhook host %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("webhook must not target a raw IP address")
		}
	}
	if host == "169.254.169.254" {
		return fmt.Errorf("webhook host is not allowed")
	}
	return nil
}

// platformWebhook 平台内服务名才配得上附带 service token。
func platformWebhook(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "auth" || strings.HasPrefix(host, "svc-")
}
