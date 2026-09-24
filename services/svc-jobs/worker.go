// jobs 投递 worker：FOR UPDATE SKIP LOCKED 取件（多副本安全），指数退避，max_attempts 后置 dead。
// cron 调度 worker：每 30s 扫描 enabled 计划，Next<=now 则派生一条 job。
// service token 用 client_credentials 换取并缓存（4 分钟，TTL 5 分钟）；未配置 JOBS_CLIENT_ID = dev 降级。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	pollInterval      = 5 * time.Second
	schedulerInterval = 30 * time.Second
)

var (
	webhookClient = &http.Client{Timeout: 10 * time.Second}

	tokenMu        sync.Mutex
	tokenVal       string
	tokenExp       time.Time
	devTokenLogged bool
)

func deliveryWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
		if err := deliverBatch(ctx); err != nil && ctx.Err() == nil {
			log.Printf("worker: %v", err)
		}
	}
}

func deliverBatch(ctx context.Context) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, type, payload, webhook, attempts, max_attempts FROM jobs
		 WHERE status='pending' AND run_at <= now()
		 ORDER BY id LIMIT 10 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type job struct {
		id                        int64
		jobType, payload, webhook string
		attempts, maxAttempts     int
	}
	var jobs []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.jobType, &j.payload, &j.webhook, &j.attempts, &j.maxAttempts) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()

	for _, j := range jobs {
		derr := deliver(ctx, j.id, j.jobType, j.payload, j.webhook)
		if derr == nil {
			_, err = tx.Exec(ctx,
				`UPDATE jobs SET status='done', done_at=now(), last_error='' WHERE id=$1`, j.id)
		} else if j.attempts+1 >= j.maxAttempts {
			_, err = tx.Exec(ctx,
				`UPDATE jobs SET status='dead', attempts=$2, last_error=$3 WHERE id=$1`,
				j.id, j.attempts+1, derr.Error())
		} else {
			backoff := time.Duration(1<<uint(j.attempts+1)) * time.Minute
			_, err = tx.Exec(ctx,
				`UPDATE jobs SET attempts=$2, last_error=$3, run_at=now()+$4::interval WHERE id=$1`,
				j.id, j.attempts+1, derr.Error(), backoff.String())
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// deliver POST job.webhook，body {"jobId","type","payload"}，带 service token（若可得）。
func deliver(ctx context.Context, id int64, jobType, payload, webhook string) error {
	body, _ := json.Marshal(map[string]any{"jobId": id, "type": jobType, "payload": payload})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok, terr := serviceToken(); terr == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := webhookClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

// serviceToken client_credentials 换取并缓存（4 分钟）。JOBS_CLIENT_ID 未配置 = dev 降级：返回空、不带鉴权。
func serviceToken() (string, error) {
	clientID := os.Getenv("JOBS_CLIENT_ID")
	if clientID == "" {
		if !devTokenLogged {
			log.Println("JOBS_CLIENT_ID unset: delivering webhooks without Authorization (dev degradation)")
			devTokenLogged = true
		}
		return "", nil
	}
	tokenMu.Lock()
	defer tokenMu.Unlock()
	if tokenVal != "" && time.Now().Before(tokenExp) {
		return tokenVal, nil
	}
	authURL := envOr("AUTH_URL", "http://auth:8080")
	body := `{"clientId":"` + clientID + `","clientSecret":"` + os.Getenv("JOBS_CLIENT_SECRET") + `"}`
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(authURL+"/auth/service-token", "application/json", bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if json.Unmarshal(raw, &out) != nil || out.AccessToken == "" {
		return "", fmt.Errorf("service-token failed: %s", string(raw))
	}
	tokenVal = out.AccessToken
	tokenExp = time.Now().Add(4 * time.Minute) // service TTL is 5min
	return tokenVal, nil
}

// schedulerWorker 每 30s 扫描 enabled 计划；Next(last_run_at 或 created_at) <= now 则派生 job 并更新 last_run_at。
func schedulerWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(schedulerInterval):
		}
		if err := runSchedules(ctx); err != nil && ctx.Err() == nil {
			log.Printf("scheduler: %v", err)
		}
	}
}

func runSchedules(ctx context.Context) error {
	rows, err := db.Query(ctx,
		`SELECT id, name, cron, type, payload, webhook,
		        COALESCE(last_run_at, created_at)
		 FROM schedules WHERE enabled=true`)
	if err != nil {
		return err
	}
	type sched struct {
		id                                        int64
		name, cronExpr, jobType, payload, webhook string
		base                                      time.Time
	}
	var scheds []sched
	for rows.Next() {
		var s sched
		if rows.Scan(&s.id, &s.name, &s.cronExpr, &s.jobType, &s.payload, &s.webhook, &s.base) == nil {
			scheds = append(scheds, s)
		}
	}
	rows.Close()

	now := time.Now()
	for _, s := range scheds {
		schedule, perr := cron.ParseStandard(s.cronExpr)
		if perr != nil {
			log.Printf("scheduler: schedule %q invalid cron %q: %v", s.name, s.cronExpr, perr)
			continue
		}
		if !schedule.Next(s.base).After(now) {
			tx, terr := db.Begin(ctx)
			if terr != nil {
				return terr
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO jobs (type, payload, webhook, created_by)
				 VALUES ($1,$2,$3,$4)`,
				s.jobType, s.payload, s.webhook, "schedule:"+s.name); err != nil {
				_ = tx.Rollback(ctx)
				return err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE schedules SET last_run_at=now() WHERE id=$1`, s.id); err != nil {
				_ = tx.Rollback(ctx)
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
