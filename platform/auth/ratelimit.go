package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// 注册按来源 IP 限流：共享的网关 60/min 挡不住邮箱枚举和灌库。
const (
	registerWindow = time.Hour
	registerMax    = 10
)

type registerLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
}

func newRegisterLimiter() *registerLimiter {
	return &registerLimiter{hits: map[string][]time.Time{}}
}

func (l *registerLimiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-registerWindow)
	kept := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= registerMax {
		l.hits[ip] = kept
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

func (s *server) allowRegister(ip string) bool {
	if s.regLimit == nil {
		return true
	}
	return s.regLimit.allow(ip)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
