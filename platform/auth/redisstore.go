package main

import (
	"context"
	"log"
	"os"
	"strconv"
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

// revokeUserTokens 记录用户级吊销截止（specs/011）：此刻之前签发的 access token 全部作废。
// 覆盖 refresh 有效期(30d)，保证禁用/改密后旧 token 立即失效。
func (s *server) revokeUserTokens(userID int) {
	cutoff := time.Now()
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.rdb.Set(ctx, "pf:userkill:"+strconv.Itoa(userID),
			strconv.FormatInt(cutoff.Unix(), 10), refreshTTL).Err()
		return
	}
	s.mu.Lock()
	s.userKill[userID] = cutoff
	s.mu.Unlock()
}

// userKilledBefore 返回该用户的吊销截止时间（零值表示无）。
func (s *server) userKilledBefore(userID int) time.Time {
	if s.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		v, err := s.rdb.Get(ctx, "pf:userkill:"+strconv.Itoa(userID)).Result()
		if err != nil {
			return time.Time{}
		}
		sec, _ := strconv.ParseInt(v, 10, 64)
		return time.Unix(sec, 0)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userKill[userID]
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
