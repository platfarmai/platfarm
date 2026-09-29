// svc-data 下游承接：通用 webhook 接收端。
// POST /api/data/ingest/{dataset}：网关放行（public_routes），鉴权 = 数据集各自密钥的 HMAC-SHA256 验签。
// 报文契约与《D4 装备属性推送对接说明》一致：X-MMOM-* 头 + {source, version, items:[...]}。
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxWebhookBody = 8 << 20 // 8MB；每批 ≤200 条富余

type ingestPayload struct {
	Source  string             `json:"source"`
	Version string             `json:"version"`
	Items   *[]json.RawMessage `json:"items"`
}

// verifySignature 验签：signature = HMAC_SHA256(secret, timestamp + "." + raw_body)，
// 原始字节、±300s 防重放、定长比较。
func verifySignature(c *gin.Context, secret string, raw []byte) bool {
	if secret == "" {
		return false
	}
	sig := c.GetHeader("X-MMOM-Signature")
	ts := c.GetHeader("X-MMOM-Timestamp")
	tsv, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := time.Now().Unix() - tsv; d > 300 || d < -300 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(raw)
	expect := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(sig), []byte(expect)) == 1
}

// handleIngest 验签 → 校验结构 → 逐条按主键覆盖写入 + 按规则抽索引。
// 单条失败不影响整批，记入 rejected。
func handleIngest(c *gin.Context) {
	ds, err := loadDataset(c.Request.Context(), c.Param("dataset"))
	if err != nil || !ds.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown or disabled dataset"})
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body failed"})
		return
	}
	if !verifySignature(c, ds.Secret, raw) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}
	// 投递去重：同一 X-MMOM-Delivery 重试时直接返回首次结果。
	delivery := strings.TrimSpace(c.GetHeader("X-MMOM-Delivery"))
	if prev, ok := findDelivery(c.Request.Context(), ds.ID, delivery); ok {
		c.Data(http.StatusOK, "application/json", prev)
		return
	}
	var payload ingestPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Items == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "malformed payload: items required"})
		return
	}

	accepted := 0
	rejected := make([]gin.H, 0)
	for _, item := range *payload.Items {
		pk, ok := itemPK(item, ds.PKField)
		if !ok {
			rejected = append(rejected, gin.H{"pk": "", "reason": "missing_" + ds.PKField})
			continue
		}
		if err := upsertRecord(c.Request.Context(), ds, pk, item, extractEntries(ds.Rules, item)); err != nil {
			log.Printf("ingest %s pk=%s: %v", ds.ID, pk, err)
			rejected = append(rejected, gin.H{"pk": pk, "reason": "db_error"})
			continue
		}
		accepted++
	}
	result := mustJSON(gin.H{"accepted": accepted, "rejected": rejected})
	saveDelivery(c.Request.Context(), ds.ID, delivery, result)
	if accepted > 0 {
		notifyDataset(ds, accepted)
	}
	c.Data(http.StatusOK, "application/json", result)
}

// itemPK 从单条原文中取主键（pk_field 后台可配，支持点路径）。数字与字符串均可。
func itemPK(item json.RawMessage, pkField string) (string, bool) {
	var doc map[string]any
	if json.Unmarshal(item, &doc) != nil {
		return "", false
	}
	switch v := walk(doc, pkField).(type) {
	case string:
		return v, v != ""
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	}
	return "", false
}
