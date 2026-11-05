// Anthropic Messages API 极简客户端（无 SDK 依赖）。未配置 ANTHROPIC_API_KEY 时降级为禁用，
// 规则引擎照常工作，AI 报告/语义匹配跳过。
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
	"strings"
	"time"
)

type aiClient struct {
	key   string
	base  string
	model string
	hc    *http.Client
}

func newAIClient() *aiClient {
	base := os.Getenv("ANTHROPIC_BASE_URL")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	model := os.Getenv("GITPULSE_AI_MODEL")
	if model == "" {
		model = "claude-sonnet-4-5"
	}
	return &aiClient{
		key:   os.Getenv("ANTHROPIC_API_KEY"),
		base:  strings.TrimRight(base, "/"),
		model: model,
		hc:    &http.Client{Timeout: 120 * time.Second},
	}
}

func (a *aiClient) enabled() bool { return a.key != "" }

func (a *aiClient) complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	if !a.enabled() {
		return "", fmt.Errorf("AI 未配置（缺 ANTHROPIC_API_KEY）")
	}
	payload := map[string]any{
		"model":      a.model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := a.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic: %d %s", resp.StatusCode, truncate(string(body), 300))
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

// completeJSON：要求模型返回纯 JSON 并解析到 out；容忍 ```json 围栏。
func (a *aiClient) completeJSON(ctx context.Context, system, user string, maxTokens int, out any) error {
	text, err := a.complete(ctx, system, user, maxTokens)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "```"); i >= 0 {
		text = text[i:]
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		if j := strings.Index(text, "```"); j >= 0 {
			text = text[:j]
		}
	}
	// 兜底：截取首个 { 到末个 }
	if s, e := strings.Index(text, "{"), strings.LastIndex(text, "}"); s >= 0 && e > s {
		text = text[s : e+1]
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		log.Printf("ai json parse failed: %v raw=%s", err, truncate(text, 300))
		return err
	}
	return nil
}
