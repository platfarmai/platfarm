package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

func openRedis() *redis.Client {
	url := os.Getenv("PF_REDIS_URL")
	if url == "" {
		return nil
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		log.Fatalf("PF_REDIS_URL: %v", err)
	}
	c := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis ping: %v", err)
	}
	log.Println("token revocation using redis")
	return c
}

func (s *server) revoke(c *Claims) {
	if c.ExpiresAt == nil {
		return
	}
	ttl := time.Until(c.ExpiresAt.Time)
	if ttl <= 0 {
		return
	}
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.rdb.Set(ctx, "pf:revoke:"+c.TokenId, "1", ttl).Err()
		return
	}
	s.mu.Lock()
	s.revoked[c.TokenId] = c.ExpiresAt.Time
	s.mu.Unlock()
}

func (s *server) isRevoked(tokenID string) bool {
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n, err := s.rdb.Exists(ctx, "pf:revoke:"+tokenID).Result()
		return err == nil && n > 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.revoked[tokenID]
	return ok
}

func (s *server) janitor() {
	if s.rdb != nil {
		return
	}
	for range time.Tick(10 * time.Minute) {
		now := time.Now()
		s.mu.Lock()
		for id, exp := range s.revoked {
			if now.After(exp) {
				delete(s.revoked, id)
			}
		}
		s.mu.Unlock()
	}
}
