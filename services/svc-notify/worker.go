// outbox 投递 worker：FOR UPDATE SKIP LOCKED 取件（多副本安全），指数退避，5 次后置 failed。
// SMTP_HOST 未配置 = dev 模式：email 只记日志即算送达（本地开发不外发）。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"
)

const (
	maxAttempts  = 5
	pollInterval = 5 * time.Second
)

var webhookClient = &http.Client{Timeout: 10 * time.Second}

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
		`SELECT id, channel, recipient, subject, body, attempts FROM notifications
		 WHERE status='pending' AND next_attempt_at <= now()
		 ORDER BY id LIMIT 10 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type job struct {
		id                               int64
		channel, recipient, subject, body string
		attempts                          int
	}
	var jobs []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.channel, &j.recipient, &j.subject, &j.body, &j.attempts) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()

	for _, j := range jobs {
		var derr error
		switch j.channel {
		case "email":
			derr = sendEmail(j.recipient, j.subject, j.body)
		case "webhook":
			derr = sendWebhook(ctx, j.recipient, j.subject, j.body)
		default:
			derr = fmt.Errorf("unknown channel %q", j.channel)
		}
		if derr == nil {
			_, err = tx.Exec(ctx,
				`UPDATE notifications SET status='sent', sent_at=now(), last_error='' WHERE id=$1`, j.id)
		} else if j.attempts+1 >= maxAttempts {
			_, err = tx.Exec(ctx,
				`UPDATE notifications SET status='failed', attempts=$2, last_error=$3 WHERE id=$1`,
				j.id, j.attempts+1, derr.Error())
		} else {
			backoff := time.Duration(1<<uint(j.attempts)) * time.Minute
			_, err = tx.Exec(ctx,
				`UPDATE notifications SET attempts=$2, last_error=$3, next_attempt_at=now()+$4::interval WHERE id=$1`,
				j.id, j.attempts+1, derr.Error(), backoff.String())
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func sendEmail(to, subject, body string) error {
	host := os.Getenv("SMTP_HOST")
	if host == "" { // dev 模式：不外发，日志即送达
		log.Printf("[dev-mail] to=%s subject=%q body=%q", to, subject, body)
		return nil
	}
	port := os.Getenv("SMTP_PORT")
	if port == "" {
		port = "587"
	}
	from := os.Getenv("SMTP_FROM")
	if from == "" {
		from = os.Getenv("SMTP_USER")
	}
	var auth smtp.Auth
	if u := os.Getenv("SMTP_USER"); u != "" {
		auth = smtp.PlainAuth("", u, os.Getenv("SMTP_PASS"), host)
	}
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")
	return smtp.SendMail(host+":"+port, auth, from, []string{to}, []byte(msg))
}

func sendWebhook(ctx context.Context, url, subject, body string) error {
	payload, _ := json.Marshal(map[string]string{"subject": subject, "body": body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
