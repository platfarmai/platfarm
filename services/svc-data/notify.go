// svc-data 出站变更通知：数据集配置 notify_url 后，每次成功入库异步 POST 通知下游。
// 签名与入站同一方案（HMAC-SHA256，密钥为 notify_secret），下游可复用验签逻辑。
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"time"
)

var notifyClient = &http.Client{Timeout: 5 * time.Second}

type notifyPayload struct {
	Dataset  string `json:"dataset"`
	Accepted int    `json:"accepted"`
	At       string `json:"at"`
}

// notifyDataset 异步投递；失败只记日志（下游可改用增量拉取 updated_since 兜底）。
func notifyDataset(d *Dataset, accepted int) {
	if d.NotifyURL == "" {
		return
	}
	go deliverNotify(d.NotifyURL, d.NotifySecret, notifyPayload{
		Dataset: d.ID, Accepted: accepted, At: time.Now().UTC().Format(time.RFC3339),
	})
}

func deliverNotify(url, secret string, payload notifyPayload) {
	body := mustJSON(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("notify %s: %v", payload.Dataset, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts))
		mac.Write([]byte("."))
		mac.Write(body)
		req.Header.Set("X-MMOM-Timestamp", ts)
		req.Header.Set("X-MMOM-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := notifyClient.Do(req)
	if err != nil {
		log.Printf("notify %s: %v", payload.Dataset, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("notify %s: downstream status %d", payload.Dataset, resp.StatusCode)
	}
}
