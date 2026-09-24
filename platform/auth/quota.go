// 开放平台日配额（specs/023）：用量真相源 = 网关计量日志（specs/013 → Loki）。
// 强制点在 /oauth/token 签发（app token TTL 1h，超额最多超放一个窗口）；
// LOKI_URL 未配置或查询失败 = fail-open（计量可选，不能因监控故障拒对外服务）。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

var (
	quotaHTTP     = &http.Client{Timeout: 3 * time.Second}
	quotaSkipOnce sync.Map // appKey → 最近告警时间：避免日志刷屏
)

func logQuotaSkip(appKey string, err error) {
	if v, ok := quotaSkipOnce.Load(appKey); ok {
		if t, _ := v.(time.Time); time.Since(t) < 10*time.Minute {
			return
		}
	}
	quotaSkipOnce.Store(appKey, time.Now())
	log.Printf("quota check skipped for %s (fail-open): %v", appKey, err)
}

// lokiAppUsage 查询该 appKey 当日（北京时区无关，按滚动 24h）经网关的请求数。
func lokiAppUsage(ctx context.Context, appKey string) (int, error) {
	base := os.Getenv("LOKI_URL")
	if base == "" {
		return 0, fmt.Errorf("LOKI_URL unset")
	}
	q := fmt.Sprintf(`sum(count_over_time({service_name="platfarm-gateway", pf_app_key=%q}[24h]))`, appKey)
	u := base + "/loki/api/v1/query?query=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := quotaHTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("loki status %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"` // [ts, "count"]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	if len(out.Data.Result) == 0 {
		return 0, nil // 无日志 = 零用量
	}
	sv, _ := out.Data.Result[0].Value[1].(string)
	n, err := strconv.Atoi(sv)
	if err != nil {
		return 0, fmt.Errorf("bad loki value %q", sv)
	}
	return n, nil
}
