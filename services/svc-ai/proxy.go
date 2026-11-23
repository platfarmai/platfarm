// Anthropic Messages 透传网关：虚拟 key 鉴权 → 别名路由 → 上游转发 → 用量计量。
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

var upstream = &http.Client{Timeout: 180 * time.Second}

func hashKey(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

type route struct {
	Alias        string
	ProviderName string
	Kind         string // anthropic | openai
	BaseURL      string
	APIKey       string
	Model        string // 转发给上游的模型名
}

// resolveRoute：model 命中 enabled 别名 → 别名路由；否则透传原 model 至首个 enabled provider。
func resolveRoute(ctx context.Context, db *pgxpool.Pool, model string) (*route, error) {
	var r route
	err := db.QueryRow(ctx, `SELECT a.alias, p.name, p.kind, p.base_url, p.api_key, a.model
		FROM model_aliases a JOIN providers p ON p.id=a.provider_id
		WHERE a.alias=$1 AND a.enabled AND p.enabled`, model).
		Scan(&r.Alias, &r.ProviderName, &r.Kind, &r.BaseURL, &r.APIKey, &r.Model)
	if err == nil {
		return &r, nil
	}
	err = db.QueryRow(ctx, `SELECT '', name, kind, base_url, api_key, $1::text
		FROM providers WHERE enabled ORDER BY id LIMIT 1`, model).
		Scan(&r.Alias, &r.ProviderName, &r.Kind, &r.BaseURL, &r.APIKey, &r.Model)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func proxyMessages(db *pgxpool.Pool) gin.HandlerFunc {
	return proxyModel(db, "anthropic")
}

func proxyChatCompletions(db *pgxpool.Pool) gin.HandlerFunc {
	return proxyModel(db, "openai")
}

func proxyModel(db *pgxpool.Pool, client string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// 1. 虚拟 key 鉴权。Anthropic 客户端用 x-api-key，OpenAI 客户端用 Bearer。
		plain := c.GetHeader("x-api-key")
		if plain == "" {
			plain = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
			plain = strings.TrimSpace(plain)
		}
		if plain == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
			return
		}
		var keyName string
		if err := db.QueryRow(ctx,
			`SELECT name FROM ai_keys WHERE key_hash=$1 AND enabled`, hashKey(plain)).Scan(&keyName); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}
		_, _ = db.Exec(ctx, `UPDATE ai_keys SET last_used_at=now() WHERE key_hash=$1`, hashKey(plain))

		// 2. 解析请求，取 model
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 10<<20))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
			return
		}
		model, _ := payload["model"].(string)
		if model == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model required"})
			return
		}
		stream, _ := payload["stream"].(bool)
		delete(payload, "stream")

		// 3. 路由
		rt, err := resolveRoute(ctx, db, model)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "无可用 provider（请在 AI 中心配置）"})
			return
		}
		// 4. 按客户端协议和 provider 协议构造上游请求
		var upURL string
		var upBody []byte
		switch {
		case client == "anthropic" && rt.Kind == "openai":
			upURL = strings.TrimRight(rt.BaseURL, "/") + "/chat/completions"
			upBody, err = toOpenAIRequest(payload, rt.Model)
		case client == "openai" && rt.Kind == "anthropic":
			upURL = strings.TrimRight(rt.BaseURL, "/") + "/v1/messages"
			upBody, err = toAnthropicRequest(payload, rt.Model)
		case rt.Kind == "openai":
			upURL = strings.TrimRight(rt.BaseURL, "/") + "/chat/completions"
			payload["model"] = rt.Model
			upBody, err = json.Marshal(payload)
		default:
			upURL = strings.TrimRight(rt.BaseURL, "/") + "/v1/messages"
			payload["model"] = rt.Model
			upBody, err = json.Marshal(payload)
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "request build: " + err.Error()})
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(upBody))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if rt.Kind == "openai" {
			req.Header.Set("Authorization", "Bearer "+rt.APIKey)
			if stream && client == "openai" {
				var decoded map[string]any
				if err := json.Unmarshal(upBody, &decoded); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}
				decoded["stream"] = true
				decoded["stream_options"] = map[string]any{"include_usage": true}
				upBody, _ = json.Marshal(decoded)
				req.Body = io.NopCloser(bytes.NewReader(upBody))
				req.ContentLength = int64(len(upBody))
			}
			if stream && client != "openai" {
				var decoded map[string]any
				if err := json.Unmarshal(upBody, &decoded); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}
				decoded["stream"] = true
				decoded["stream_options"] = map[string]any{"include_usage": true}
				upBody, _ = json.Marshal(decoded)
				req.Body = io.NopCloser(bytes.NewReader(upBody))
				req.ContentLength = int64(len(upBody))
			}
		} else {
			req.Header.Set("x-api-key", rt.APIKey)
			ver := c.GetHeader("anthropic-version")
			if ver == "" {
				ver = "2023-06-01"
			}
			req.Header.Set("anthropic-version", ver)
			if stream && client == "anthropic" {
				var decoded map[string]any
				if err := json.Unmarshal(upBody, &decoded); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}
				decoded["stream"] = true
				upBody, _ = json.Marshal(decoded)
				req.Body = io.NopCloser(bytes.NewReader(upBody))
				req.ContentLength = int64(len(upBody))
			}
		}

		start := time.Now()
		resp, err := upstream.Do(req)
		if err != nil {
			recordUsage(db, keyName, rt, 0, 0, http.StatusBadGateway, time.Since(start))
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream: " + err.Error()})
			return
		}
		defer resp.Body.Close()
		if stream && resp.StatusCode == http.StatusOK && client != rt.Kind {
			if client == "anthropic" {
				proxyOpenAIStream(c, db, keyName, rt, model, resp.Body, start)
			} else {
				proxyAnthropicStream(c, db, keyName, rt, model, resp.Body, start)
			}
			return
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))

		// 5. 响应翻译 + 计量（成功解析 usage，失败记 0 token + 状态码）
		in, out := 0, 0
		if resp.StatusCode == http.StatusOK && client != rt.Kind {
			if client == "anthropic" {
				converted, pin, pout, cerr := fromOpenAIResponse(respBody, model)
				if cerr != nil {
					recordUsage(db, keyName, rt, 0, 0, http.StatusBadGateway, time.Since(start))
					c.JSON(http.StatusBadGateway, gin.H{"error": cerr.Error()})
					return
				}
				respBody, in, out = converted, pin, pout
			} else {
				converted, pin, pout, cerr := fromAnthropicResponse(respBody, model)
				if cerr != nil {
					recordUsage(db, keyName, rt, 0, 0, http.StatusBadGateway, time.Since(start))
					c.JSON(http.StatusBadGateway, gin.H{"error": cerr.Error()})
					return
				}
				respBody, in, out = converted, pin, pout
			}
		} else if resp.StatusCode == http.StatusOK {
			var u struct {
				Usage struct {
					InputTokens      int `json:"input_tokens"`
					OutputTokens     int `json:"output_tokens"`
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(respBody, &u) == nil {
				in, out = u.Usage.InputTokens, u.Usage.OutputTokens
				if in == 0 && out == 0 {
					in, out = u.Usage.PromptTokens, u.Usage.CompletionTokens
				}
			}
		}
		recordUsage(db, keyName, rt, in, out, resp.StatusCode, time.Since(start))

		c.Data(resp.StatusCode, "application/json", respBody)
	}
}

func proxyOpenAIStream(c *gin.Context, db *pgxpool.Pool, keyName string, rt *route, model string, body io.Reader, start time.Time) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	state := anthropicStream{model: model}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	var data strings.Builder
	fail := func(err error) {
		raw, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": err.Error()}})
		_, _ = c.Writer.Write([]byte("event: error\ndata: " + string(raw) + "\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			chunk := data.String()
			data.Reset()
			if chunk == "" || chunk == "[DONE]" {
				continue
			}
			events, err := state.Events([]byte(chunk))
			if err != nil {
				fail(err)
				recordUsage(db, keyName, rt, state.input, state.output, http.StatusBadGateway, time.Since(start))
				return
			}
			for _, event := range events {
				_, _ = io.WriteString(c.Writer, event)
			}
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		fail(err)
		recordUsage(db, keyName, rt, state.input, state.output, http.StatusBadGateway, time.Since(start))
		return
	}
	events, err := state.Finish()
	if err != nil {
		fail(err)
		recordUsage(db, keyName, rt, state.input, state.output, http.StatusBadGateway, time.Since(start))
		return
	}
	for _, event := range events {
		_, _ = io.WriteString(c.Writer, event)
	}
	if flusher != nil {
		flusher.Flush()
	}
	recordUsage(db, keyName, rt, state.input, state.output, http.StatusOK, time.Since(start))
}

func proxyAnthropicStream(c *gin.Context, db *pgxpool.Pool, keyName string, rt *route, model string, body io.Reader, start time.Time) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	state := openAIStream{model: model}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	var event string
	var data strings.Builder
	fail := func(err error) {
		raw, _ := json.Marshal(map[string]any{"error": map[string]any{"message": err.Error(), "type": "api_error"}})
		_, _ = c.Writer.Write([]byte("data: " + string(raw) + "\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
	flush := func() bool {
		chunk := data.String()
		data.Reset()
		if chunk == "" {
			return true
		}
		frame, err := state.Events(event, []byte(chunk))
		event = ""
		if err != nil {
			fail(err)
			recordUsage(db, keyName, rt, state.input, state.output, http.StatusBadGateway, time.Since(start))
			return false
		}
		if frame != "" {
			_, _ = io.WriteString(c.Writer, frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
		return true
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if !flush() {
				return
			}
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if !flush() {
		return
	}
	if err := scanner.Err(); err != nil {
		fail(err)
		recordUsage(db, keyName, rt, state.input, state.output, http.StatusBadGateway, time.Since(start))
		return
	}
	frame, err := state.Finish()
	if err != nil {
		fail(err)
		recordUsage(db, keyName, rt, state.input, state.output, http.StatusBadGateway, time.Since(start))
		return
	}
	_, _ = io.WriteString(c.Writer, frame)
	if flusher != nil {
		flusher.Flush()
	}
	recordUsage(db, keyName, rt, state.input, state.output, http.StatusOK, time.Since(start))
}

func recordUsage(db *pgxpool.Pool, keyName string, rt *route, in, out, status int, dur time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = db.Exec(ctx, `INSERT INTO usage (key_name, alias, provider, model, input_tokens,
		output_tokens, status, latency_ms) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		keyName, rt.Alias, rt.ProviderName, rt.Model, in, out, status, dur.Milliseconds())
}
