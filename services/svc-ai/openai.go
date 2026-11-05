// kind=openai 的协议翻译：对外仍是 Anthropic Messages，内部转成 OpenAI chat/completions。
// 这样任意 OpenAI 兼容上游（apihub/one-api/litellm 等）都能挂进 AI 中心，调用方零改动。
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// anthropicContentText：content 可能是 string 或 [{type:"text",text:"..."}] 块数组，统一取纯文本。
func anthropicContentText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if s, _ := m["text"].(string); s != "" {
						sb.WriteString(s)
					}
				}
			}
		}
		return sb.String()
	}
	return ""
}

// toOpenAIRequest：Anthropic Messages 请求体 → OpenAI chat/completions 请求体。
func toOpenAIRequest(payload map[string]any, model string) ([]byte, error) {
	msgs := []map[string]any{}
	if sys := anthropicContentText(payload["system"]); sys != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": sys})
	}
	rawMsgs, _ := payload["messages"].([]any)
	for _, rm := range rawMsgs {
		m, ok := rm.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role != "user" && role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, map[string]any{"role": role, "content": anthropicContentText(m["content"])})
	}
	out := map[string]any{"model": model, "messages": msgs}
	if mt, ok := payload["max_tokens"].(float64); ok && mt > 0 {
		out["max_tokens"] = int(mt)
	}
	if tp, ok := payload["temperature"].(float64); ok {
		out["temperature"] = tp
	}
	return json.Marshal(out)
}

// fromOpenAIResponse：OpenAI chat/completions 响应 → Anthropic Messages 响应。
// 返回 (转换后的 body, input_tokens, output_tokens, 错误)。
func fromOpenAIResponse(body []byte, model string) ([]byte, int, int, error) {
	var in struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, 0, 0, fmt.Errorf("openai response parse: %w", err)
	}
	text := ""
	stop := "end_turn"
	if len(in.Choices) > 0 {
		text = in.Choices[0].Message.Content
		if in.Choices[0].FinishReason == "length" {
			stop = "max_tokens"
		}
	}
	out := map[string]any{
		"id":    in.ID,
		"type":  "message",
		"role":  "assistant",
		"model": model,
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
		"stop_reason": stop,
		"usage": map[string]int{
			"input_tokens":  in.Usage.PromptTokens,
			"output_tokens": in.Usage.CompletionTokens,
		},
	}
	raw, err := json.Marshal(out)
	return raw, in.Usage.PromptTokens, in.Usage.CompletionTokens, err
}
