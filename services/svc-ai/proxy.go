// Anthropic Messages 透传网关：虚拟 key 鉴权 → 别名路由 → 上游转发 → 用量计量。
package main

import (
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
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// 1. 虚拟 key 鉴权
		plain := c.GetHeader("x-api-key")
		if plain == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing x-api-key"})
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
		if stream, _ := payload["stream"].(bool); stream {
			c.JSON(http.StatusBadRequest, gin.H{"error": "v1 暂不支持 stream"})
			return
		}

		// 3. 路由
		rt, err := resolveRoute(ctx, db, model)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "无可用 provider（请在 AI 中心配置）"})
			return
		}
		// 4. 按 provider 协议构造上游请求
		var upURL string
		var upBody []byte
		if rt.Kind == "openai" {
			upURL = strings.TrimRight(rt.BaseURL, "/") + "/chat/completions"
			upBody, err = toOpenAIRequest(payload, rt.Model)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "openai request build: " + err.Error()})
				return
			}
		} else {
			upURL = strings.TrimRight(rt.BaseURL, "/") + "/v1/messages"
			if rt.Model != model {
				payload["model"] = rt.Model
				body, _ = json.Marshal(payload)
			}
			upBody = body
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(upBody))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if rt.Kind == "openai" {
			req.Header.Set("Authorization", "Bearer "+rt.APIKey)
		} else {
			req.Header.Set("x-api-key", rt.APIKey)
			ver := c.GetHeader("anthropic-version")
			if ver == "" {
				ver = "2023-06-01"
			}
			req.Header.Set("anthropic-version", ver)
		}

		start := time.Now()
		resp, err := upstream.Do(req)
		if err != nil {
			recordUsage(db, keyName, rt, 0, 0, http.StatusBadGateway, time.Since(start))
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream: " + err.Error()})
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))

		// 5. 响应翻译 + 计量（成功解析 usage，失败记 0 token + 状态码）
		in, out := 0, 0
		if resp.StatusCode == http.StatusOK {
			if rt.Kind == "openai" {
				converted, pin, pout, cerr := fromOpenAIResponse(respBody, model)
				if cerr != nil {
					recordUsage(db, keyName, rt, 0, 0, http.StatusBadGateway, time.Since(start))
					c.JSON(http.StatusBadGateway, gin.H{"error": cerr.Error()})
					return
				}
				respBody, in, out = converted, pin, pout
			} else {
				var u struct {
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				}
				if json.Unmarshal(respBody, &u) == nil {
					in, out = u.Usage.InputTokens, u.Usage.OutputTokens
				}
			}
		}
		recordUsage(db, keyName, rt, in, out, resp.StatusCode, time.Since(start))

		c.Data(resp.StatusCode, "application/json", respBody)
	}
}

func recordUsage(db *pgxpool.Pool, keyName string, rt *route, in, out, status int, dur time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = db.Exec(ctx, `INSERT INTO usage (key_name, alias, provider, model, input_tokens,
		output_tokens, status, latency_ms) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		keyName, rt.Alias, rt.ProviderName, rt.Model, in, out, status, dur.Milliseconds())
}
