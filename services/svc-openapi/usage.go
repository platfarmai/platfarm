package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// 用量视图（specs/013）：查 Loki（LOKI_URL）出按 app 的请求计数。admin-only。
// 未配 LOKI_URL → 返回 enabled=false，前端显示"未启用"。

func lokiURL() string { return os.Getenv("LOKI_URL") }

var httpClient = &http.Client{Timeout: 10 * time.Second}

// handleUsage GET /api/openapi/usage?window=24h
func handleUsage(c *gin.Context) {
	base := lokiURL()
	if base == "" {
		c.JSON(200, gin.H{"enabled": false, "hint": "启用 observability profile 并设 LOKI_URL"})
		return
	}
	window := c.DefaultQuery("window", "24h")
	dur, err := time.ParseDuration(window)
	if err != nil || dur <= 0 {
		dur = 24 * time.Hour
	}
	// LogQL：按 pf_app_key label 计数（promtail 已把 pf_app_key 提为 label）
	query := `sum by (pf_app_key) (count_over_time({container=~"platfarm-gateway.*", pf_app_key!=""} [` + window + `]))`
	end := time.Now()
	start := end.Add(-dur)

	q := url.Values{}
	q.Set("query", query)
	q.Set("start", fmt.Sprintf("%d", start.UnixNano()))
	q.Set("end", fmt.Sprintf("%d", end.UnixNano()))
	reqURL := base + "/loki/api/v1/query_range?" + q.Encode()

	resp, err := httpClient.Get(reqURL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"enabled": true, "error": "loki unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	var lq struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][2]any          `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&lq) != nil {
		c.JSON(http.StatusBadGateway, gin.H{"enabled": true, "error": "loki bad response"})
		return
	}
	type row struct {
		AppKey string  `json:"appKey"`
		Count  float64 `json:"count"`
	}
	out := []row{}
	for _, r := range lq.Data.Result {
		var sum float64
		for _, v := range r.Values {
			var f float64
			if s, ok := v[1].(string); ok {
				fmt.Sscanf(s, "%f", &f)
			}
			sum += f
		}
		out = append(out, row{AppKey: r.Metric["pf_app_key"], Count: sum})
	}
	c.JSON(200, gin.H{"enabled": true, "window": window, "usage": out})
}
