package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	stateTTL = 600 * time.Second
	codeTTL  = 30 * time.Second
)

// store 保存 CSRF state 与一次性兑换码；有 PF_REDIS_URL 时用 Redis，否则内存。
type store struct {
	rdb    *redis.Client
	mu     sync.Mutex
	states map[string]time.Time
	codes  map[string]codeEntry
}

type codeEntry struct {
	exp    time.Time
	tokens map[string]any
}

func newStore() *store {
	s := &store{
		states: map[string]time.Time{},
		codes:  map[string]codeEntry{},
	}
	url := envOrEmpty("PF_REDIS_URL")
	if url == "" {
		return s
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		return s
	}
	c := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		return s
	}
	s.rdb = c
	return s
}

func (s *store) putState(state string) {
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.rdb.Set(ctx, "pf:oauth:state:"+state, "1", stateTTL).Err()
		return
	}
	s.mu.Lock()
	s.states[state] = time.Now().Add(stateTTL)
	s.mu.Unlock()
}

func (s *store) takeState(state string) bool {
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n, err := s.rdb.Del(ctx, "pf:oauth:state:"+state).Result()
		return err == nil && n > 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.states[state]
	delete(s.states, state)
	return ok && !time.Now().After(exp)
}

func (s *store) putCode(code string, tokens map[string]any) {
	if s.rdb != nil {
		raw, _ := json.Marshal(tokens)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.rdb.Set(ctx, "pf:oauth:code:"+code, raw, codeTTL).Err()
		return
	}
	s.mu.Lock()
	s.codes[code] = codeEntry{exp: time.Now().Add(codeTTL), tokens: tokens}
	s.mu.Unlock()
}

func (s *store) takeCode(code string) map[string]any {
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		raw, err := s.rdb.GetDel(ctx, "pf:oauth:code:"+code).Result()
		if err != nil || raw == "" {
			return nil
		}
		var tokens map[string]any
		if json.Unmarshal([]byte(raw), &tokens) != nil {
			return nil
		}
		return tokens
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.codes[code]
	delete(s.codes, code)
	if !ok || time.Now().After(e.exp) {
		return nil
	}
	return e.tokens
}
