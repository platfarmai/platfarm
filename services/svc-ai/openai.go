// kind=openai 的协议翻译：对外保持 Anthropic Messages，内部使用 OpenAI chat/completions。
// 原生 Anthropic 上游不经过这里，由代理原样透传。
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const openaiReasoningProvider = "svc-ai"

var openaiPassthroughKeys = []string{
	"metadata", "service_tier", "user", "frequency_penalty", "presence_penalty", "seed", "n", "logprobs",
}

func anthropicContentText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok && m["type"] == "text" {
				if s, _ := m["text"].(string); s != "" {
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	}
	return ""
}

func toOpenAIRequest(payload map[string]any, model string) ([]byte, error) {
	msgs := []map[string]any{}
	system, err := convertSystem(payload["system"])
	if err != nil {
		return nil, err
	}
	msgs = append(msgs, system...)
	rawMsgs, _ := payload["messages"].([]any)
	for _, rm := range rawMsgs {
		m, ok := rm.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("message must be an object")
		}
		role, _ := m["role"].(string)
		if role == "system" {
			system, err := convertSystem(m["content"])
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, system...)
			continue
		}
		converted, err := convertMessage(m)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, converted...)
	}
	out := map[string]any{"model": model, "messages": msgs}
	if err := convertRequestOptions(payload, out); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func convertSystem(raw any) ([]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	if text, ok := raw.(string); ok {
		if text == "" {
			return nil, nil
		}
		return []map[string]any{{"role": "system", "content": text}}, nil
	}
	blocks, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("system must be a string or text block array")
	}
	parts := []map[string]any{}
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "text" {
			return nil, fmt.Errorf("system blocks must be text")
		}
		text, _ := block["text"].(string)
		part := map[string]any{"type": "text", "text": text}
		if err := preserveCacheControl(block, part); err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return []map[string]any{{"role": "system", "content": parts}}, nil
}

func convertMessage(m map[string]any) ([]map[string]any, error) {
	role, _ := m["role"].(string)
	if role != "user" && role != "assistant" {
		return nil, fmt.Errorf("unsupported message role %q", role)
	}
	if text, ok := m["content"].(string); ok {
		return []map[string]any{{"role": role, "content": text}}, nil
	}
	blocks, ok := m["content"].([]any)
	if !ok {
		return nil, fmt.Errorf("message content must be a string or block array")
	}
	parts := []map[string]any{}
	calls := []map[string]any{}
	results := []map[string]any{}
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("content block must be an object")
		}
		switch block["type"] {
		case "text":
			part := map[string]any{"type": "text", "text": anthropicContentText([]any{block})}
			if err := preserveCacheControl(block, part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case "image":
			if role != "user" {
				return nil, fmt.Errorf("image requires user role")
			}
			url, err := imageURL(block["source"])
			if err != nil {
				return nil, err
			}
			part := map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
			if err := preserveCacheControl(block, part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case "document":
			if role != "user" {
				return nil, fmt.Errorf("document requires user role")
			}
			text, err := documentText(block)
			if err != nil {
				return nil, err
			}
			part := map[string]any{"type": "text", "text": text}
			if err := preserveCacheControl(block, part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case "thinking":
			if role != "assistant" {
				return nil, fmt.Errorf("thinking requires assistant role")
			}
			thinking, _ := block["thinking"].(string)
			signature, _ := block["signature"].(string)
			if thinking == "" || !validReasoningSignature(thinking, signature) {
				continue
			}
			parts = append(parts, map[string]any{"type": "thinking", "text": thinking})
		case "redacted_thinking":
			if role != "assistant" {
				return nil, fmt.Errorf("redacted_thinking requires assistant role")
			}
			continue
		case "tool_use":
			if role != "assistant" {
				return nil, fmt.Errorf("tool_use requires assistant role")
			}
			call, err := toolCall(block)
			if err != nil {
				return nil, err
			}
			calls = append(calls, call)
		case "tool_result":
			if role != "user" {
				return nil, fmt.Errorf("tool_result requires user role")
			}
			result, err := toolResult(block)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		default:
			return nil, fmt.Errorf("unsupported content block type %v", block["type"])
		}
	}
	out := append([]map[string]any{}, results...)
	if len(parts) > 0 || len(calls) > 0 || len(results) == 0 {
		content, reasoning := splitReasoning(parts)
		message := map[string]any{"role": role, "content": messageContent(content)}
		if reasoning != "" {
			message["reasoning_content"] = reasoning
		}
		if len(calls) > 0 {
			message["tool_calls"] = calls
		}
		out = append(out, message)
	}
	return out, nil
}

func splitReasoning(parts []map[string]any) ([]map[string]any, string) {
	content := make([]map[string]any, 0, len(parts))
	var reasoning strings.Builder
	for _, part := range parts {
		if part["type"] == "thinking" {
			if text, _ := part["text"].(string); text != "" {
				reasoning.WriteString(text)
			}
			continue
		}
		content = append(content, part)
	}
	return content, reasoning.String()
}

func messageContent(parts []map[string]any) any {
	if len(parts) == 1 {
		if text, ok := parts[0]["text"].(string); ok && len(parts[0]) == 2 {
			return text
		}
	}
	return parts
}

func toolCall(block map[string]any) (map[string]any, error) {
	id, _ := block["id"].(string)
	name, _ := block["name"].(string)
	input, ok := block["input"].(map[string]any)
	if id == "" || name == "" || !ok {
		return nil, fmt.Errorf("invalid tool_use block")
	}
	args, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	call := map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(args)}}
	if cache, ok := block["cache_control"]; ok {
		call["cache_control"] = cache
	}
	return call, nil
}

func toolResult(block map[string]any) (map[string]any, error) {
	id, _ := block["tool_use_id"].(string)
	if id == "" {
		return nil, fmt.Errorf("tool_result requires tool_use_id")
	}
	content, err := toolResultContent(block["content"])
	if err != nil {
		return nil, err
	}
	if failed, _ := block["is_error"].(bool); failed {
		switch value := content.(type) {
		case string:
			content = "Tool error: " + value
		case []map[string]any:
			content = append([]map[string]any{{"type": "text", "text": "Tool error:"}}, value...)
		}
	}
	result := map[string]any{"role": "tool", "tool_call_id": id, "content": content}
	if cache, ok := block["cache_control"]; ok {
		result["cache_control"] = cache
	}
	return result, nil
}

func toolResultContent(content any) (any, error) {
	if content == nil {
		return "", nil
	}
	if text, ok := content.(string); ok {
		return text, nil
	}
	blocks, ok := content.([]any)
	if !ok {
		return nil, fmt.Errorf("unsupported tool_result content")
	}
	parts := []map[string]any{}
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool_result content block must be an object")
		}
		switch block["type"] {
		case "text":
			part := map[string]any{"type": "text", "text": anthropicContentText([]any{block})}
			if err := preserveCacheControl(block, part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case "image":
			url, err := imageURL(block["source"])
			if err != nil {
				return nil, err
			}
			part := map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
			if err := preserveCacheControl(block, part); err != nil {
				return nil, err
			}
			parts = append(parts, part)
		default:
			return nil, fmt.Errorf("unsupported tool_result content type %v", block["type"])
		}
	}
	return parts, nil
}

func imageURL(source any) (string, error) {
	s, ok := source.(map[string]any)
	if !ok {
		return "", fmt.Errorf("image source must be an object")
	}
	switch s["type"] {
	case "base64":
		media, _ := s["media_type"].(string)
		data, _ := s["data"].(string)
		if media == "" || data == "" || !strings.HasPrefix(media, "image/") {
			return "", fmt.Errorf("invalid base64 image source")
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return "", fmt.Errorf("invalid base64 image data")
		}
		return "data:" + media + ";base64," + data, nil
	case "url":
		url, _ := s["url"].(string)
		if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
			return "", fmt.Errorf("image URL must use http or https")
		}
		return url, nil
	default:
		return "", fmt.Errorf("unsupported image source type %v", s["type"])
	}
}

func documentText(block map[string]any) (string, error) {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("document source must be an object")
	}
	var body string
	switch source["type"] {
	case "text":
		body, _ = source["data"].(string)
		if body == "" {
			return "", fmt.Errorf("text document requires data")
		}
	case "content":
		raw, _ := source["content"].([]any)
		parts := []string{}
		for _, item := range raw {
			part, ok := item.(map[string]any)
			if !ok || part["type"] != "text" {
				return "", fmt.Errorf("document content source only supports text blocks")
			}
			parts = append(parts, anthropicContentText([]any{part}))
		}
		body = strings.Join(parts, "")
	case "base64":
		if source["media_type"] != "application/pdf" {
			return "", fmt.Errorf("base64 document must be application/pdf")
		}
		data, _ := source["data"].(string)
		if _, err := base64.StdEncoding.DecodeString(data); err != nil || data == "" {
			return "", fmt.Errorf("invalid base64 PDF data")
		}
		body = "[PDF document base64]\n" + data
	case "url":
		url, _ := source["url"].(string)
		if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
			return "", fmt.Errorf("document URL must use http or https")
		}
		body = "[Document URL]\n" + url
	default:
		return "", fmt.Errorf("unsupported document source type %v", source["type"])
	}
	title, _ := block["title"].(string)
	context, _ := block["context"].(string)
	text := "<document>"
	if title != "" {
		text += "\n<title>" + title + "</title>"
	}
	if context != "" {
		text += "\n<context>" + context + "</context>"
	}
	return text + "\n<content>\n" + body + "\n</content>\n</document>", nil
}

func preserveCacheControl(from, to map[string]any) error {
	cache, ok := from["cache_control"]
	if !ok {
		return nil
	}
	value, ok := cache.(map[string]any)
	if !ok || value["type"] != "ephemeral" {
		return fmt.Errorf("unsupported cache_control")
	}
	to["cache_control"] = value
	return nil
}

func convertRequestOptions(payload, out map[string]any) error {
	if tools, ok := payload["tools"].([]any); ok {
		converted := []map[string]any{}
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("tool must be an object")
			}
			if toolType, _ := tool["type"].(string); toolType != "" && toolType != "custom" {
				return fmt.Errorf("unsupported_tool: %s", toolType)
			}
			name, _ := tool["name"].(string)
			schema, ok := tool["input_schema"].(map[string]any)
			if name == "" || !ok {
				return fmt.Errorf("tool requires name and input_schema")
			}
			fn := map[string]any{"name": name, "parameters": schema}
			if desc, ok := tool["description"].(string); ok {
				fn["description"] = desc
			}
			item := map[string]any{"type": "function", "function": fn}
			if cache, ok := tool["cache_control"]; ok {
				item["cache_control"] = cache
			}
			converted = append(converted, item)
		}
		out["tools"] = converted
	}
	if choice, ok := payload["tool_choice"].(map[string]any); ok {
		switch choice["type"] {
		case "auto":
			out["tool_choice"] = "auto"
		case "any":
			out["tool_choice"] = "required"
		case "none":
			out["tool_choice"] = "none"
		case "tool":
			name, _ := choice["name"].(string)
			if name == "" {
				return fmt.Errorf("tool_choice requires name")
			}
			out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": name}}
		default:
			return fmt.Errorf("unsupported tool_choice type")
		}
		if disabled, ok := choice["disable_parallel_tool_use"].(bool); ok {
			out["parallel_tool_calls"] = !disabled
		}
	}
	if effort, err := reasoningEffort(payload); err != nil {
		return err
	} else if effort != "" {
		out["reasoning_effort"] = effort
	}
	for _, key := range []string{"stop_sequences", "top_p", "top_k"} {
		if value, ok := payload[key]; ok {
			target := key
			if key == "stop_sequences" {
				target = "stop"
			}
			out[target] = value
		}
	}
	if maxTokens, ok := payload["max_tokens"].(float64); ok && maxTokens > 0 {
		out["max_tokens"] = int(maxTokens)
	}
	if temperature, ok := payload["temperature"].(float64); ok {
		out["temperature"] = temperature
	}
	for _, key := range openaiPassthroughKeys {
		if value, ok := payload[key]; ok {
			out[key] = value
		}
	}
	return nil
}

func reasoningEffort(payload map[string]any) (string, error) {
	effort := ""
	if output, ok := payload["output_config"].(map[string]any); ok {
		if value, _ := output["effort"].(string); strings.TrimSpace(value) != "" {
			effort = strings.ToLower(strings.TrimSpace(value))
		}
	}
	thinking, _ := payload["thinking"].(map[string]any)
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(thinking["type"]))) {
	case "disabled":
		return "none", nil
	case "adaptive":
		return effort, nil
	case "enabled":
		if effort != "" {
			return effort, nil
		}
		return reasoningEffortForBudget(int64(numberValue(thinking["budget_tokens"]))), nil
	case "<nil>", "":
		return effort, nil
	default:
		return "", fmt.Errorf("unsupported thinking type")
	}
}

func reasoningEffortForBudget(budget int64) string {
	switch {
	case budget <= 0:
		return "none"
	case budget <= 2048:
		return "low"
	case budget <= 8192:
		return "medium"
	case budget <= 16384:
		return "high"
	default:
		return "xhigh"
	}
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	default:
		return 0
	}
}

func reasoningSignature(thinking string) string {
	sum := sha256.Sum256([]byte(thinking))
	return openaiReasoningProvider + ":" + hex.EncodeToString(sum[:])
}

func validReasoningSignature(thinking, signature string) bool {
	return thinking != "" && signature == reasoningSignature(thinking)
}

func toAnthropicRequest(payload map[string]any, model string) ([]byte, error) {
	messages, _ := payload["messages"].([]any)
	system := []map[string]any{}
	turns := []map[string]any{}
	pending := []map[string]any{}
	flush := func() {
		if len(pending) == 0 {
			return
		}
		turns = append(turns, map[string]any{"role": "user", "content": pending})
		pending = nil
	}
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("message must be an object")
		}
		role, _ := message["role"].(string)
		switch role {
		case "system", "developer":
			flush()
			text := openAIText(message["content"])
			if text != "" {
				system = append(system, map[string]any{"type": "text", "text": text})
			}
		case "tool":
			id, _ := message["tool_call_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("tool message requires tool_call_id")
			}
			pending = append(pending, map[string]any{"type": "tool_result", "tool_use_id": id, "content": openAIText(message["content"])})
		case "assistant":
			flush()
			blocks, err := anthropicAssistantBlocks(message)
			if err != nil {
				return nil, err
			}
			if len(blocks) > 0 {
				turns = append(turns, map[string]any{"role": "assistant", "content": blocks})
			}
		case "user":
			flush()
			blocks, err := anthropicUserBlocks(message["content"])
			if err != nil {
				return nil, err
			}
			turns = append(turns, map[string]any{"role": "user", "content": blocks})
		default:
			return nil, fmt.Errorf("unsupported message role %q", role)
		}
	}
	flush()
	out := map[string]any{"model": model, "messages": turns, "max_tokens": 1024}
	if len(system) > 0 {
		out["system"] = system
	}
	if maxTokens := int(numberValue(payload["max_tokens"])); maxTokens > 0 {
		out["max_tokens"] = maxTokens
	}
	if maxTokens := int(numberValue(payload["max_completion_tokens"])); maxTokens > int(numberValue(out["max_tokens"])) {
		out["max_tokens"] = maxTokens
	}
	for _, key := range []string{"temperature", "top_p"} {
		if value, ok := payload[key]; ok {
			out[key] = value
		}
	}
	if stop, ok := payload["stop"]; ok {
		out["stop_sequences"] = stop
	}
	if effort, _ := payload["reasoning_effort"].(string); strings.TrimSpace(effort) != "" {
		out["output_config"] = map[string]any{"effort": strings.ToLower(strings.TrimSpace(effort))}
	}
	if tools, ok := payload["tools"]; ok {
		converted, err := anthropicTools(tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = converted
		choice, err := anthropicToolChoice(payload["tool_choice"], payload["parallel_tool_calls"])
		if err != nil {
			return nil, err
		}
		out["tool_choice"] = choice
	}
	return json.Marshal(out)
}

func anthropicAssistantBlocks(message map[string]any) ([]map[string]any, error) {
	blocks := []map[string]any{}
	if reasoning, _ := message["reasoning_content"].(string); reasoning != "" {
		signature, _ := message["reasoning_signature"].(string)
		if validReasoningSignature(reasoning, signature) {
			blocks = append(blocks, map[string]any{"type": "thinking", "thinking": reasoning, "signature": signature})
		}
	}
	if text := openAIText(message["content"]); text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	calls, _ := message["tool_calls"].([]any)
	for _, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool call must be an object")
		}
		id, _ := call["id"].(string)
		function, _ := call["function"].(map[string]any)
		name, _ := function["name"].(string)
		arguments, _ := function["arguments"].(string)
		if id == "" || name == "" {
			return nil, fmt.Errorf("tool call requires id and name")
		}
		input := map[string]any{}
		if strings.TrimSpace(arguments) != "" && json.Unmarshal([]byte(arguments), &input) != nil {
			return nil, fmt.Errorf("tool call arguments must be a JSON object")
		}
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
	}
	return blocks, nil
}

func anthropicUserBlocks(content any) ([]map[string]any, error) {
	switch value := content.(type) {
	case string:
		return []map[string]any{{"type": "text", "text": value}}, nil
	case []any:
		blocks := []map[string]any{}
		for _, raw := range value {
			part, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("content part must be an object")
			}
			switch part["type"] {
			case "text", "input_text":
				text, _ := part["text"].(string)
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			case "image_url":
				source, err := anthropicImageSource(part["image_url"])
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, map[string]any{"type": "image", "source": source})
			default:
				return nil, fmt.Errorf("unsupported_content_block: %v", part["type"])
			}
		}
		return blocks, nil
	default:
		return nil, fmt.Errorf("message content must be a string or content array")
	}
}

func anthropicImageSource(value any) (map[string]any, error) {
	raw := ""
	switch typed := value.(type) {
	case string:
		raw = typed
	case map[string]any:
		raw, _ = typed["url"].(string)
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "data:") {
		meta, data, ok := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
		media, _, _ := strings.Cut(meta, ";")
		if !ok || !strings.Contains(meta, ";base64") || media == "" || data == "" {
			return nil, fmt.Errorf("invalid image data URI")
		}
		return map[string]any{"type": "base64", "media_type": media, "data": data}, nil
	}
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return map[string]any{"type": "url", "url": raw}, nil
	}
	return nil, fmt.Errorf("image_url must be an http(s) URL or data URI")
}

func anthropicTools(value any) ([]map[string]any, error) {
	rawTools, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("tools must be an array")
	}
	tools := []map[string]any{}
	for _, raw := range rawTools {
		tool, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool must be an object")
		}
		if kind, _ := tool["type"].(string); kind != "" && kind != "function" {
			return nil, fmt.Errorf("unsupported_tool: %s", kind)
		}
		function, _ := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("tool name is required")
		}
		parameters, _ := function["parameters"].(map[string]any)
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		item := map[string]any{"name": name, "input_schema": parameters}
		if description, _ := function["description"].(string); description != "" {
			item["description"] = description
		}
		tools = append(tools, item)
	}
	return tools, nil
}

func anthropicToolChoice(value, parallel any) (map[string]any, error) {
	choice := map[string]any{"type": "auto"}
	switch typed := value.(type) {
	case nil:
	case string:
		switch typed {
		case "auto":
			choice["type"] = "auto"
		case "required":
			choice["type"] = "any"
		case "none":
			choice["type"] = "none"
		default:
			return nil, fmt.Errorf("unsupported tool_choice %q", typed)
		}
	case map[string]any:
		function, _ := typed["function"].(map[string]any)
		name, _ := function["name"].(string)
		if typed["type"] != "function" || name == "" {
			return nil, fmt.Errorf("tool_choice function requires name")
		}
		choice["type"] = "tool"
		choice["name"] = name
	default:
		return nil, fmt.Errorf("tool_choice must be a string or object")
	}
	if enabled, ok := parallel.(bool); ok && !enabled {
		choice["disable_parallel_tool_use"] = true
	}
	return choice, nil
}

func openAIText(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		var text strings.Builder
		for _, raw := range typed {
			part, _ := raw.(map[string]any)
			if part["type"] == "text" || part["type"] == "input_text" {
				if value, _ := part["text"].(string); value != "" {
					text.WriteString(value)
				}
			}
		}
		return text.String()
	default:
		return ""
	}
}

func fromAnthropicResponse(body []byte, model string) ([]byte, int, int, error) {
	var in struct {
		Content []struct {
			Type      string         `json:"type"`
			Text      string         `json:"text"`
			Thinking  string         `json:"thinking"`
			Signature string         `json:"signature"`
			ID        string         `json:"id"`
			Name      string         `json:"name"`
			Input     map[string]any `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, 0, 0, fmt.Errorf("anthropic response parse: %w", err)
	}
	message := map[string]any{"role": "assistant", "content": nil}
	text := strings.Builder{}
	calls := []map[string]any{}
	for _, block := range in.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			message["reasoning_content"] = block.Thinking
			message["reasoning_signature"] = reasoningSignature(block.Thinking)
		case "tool_use":
			raw, _ := json.Marshal(block.Input)
			calls = append(calls, map[string]any{"id": block.ID, "type": "function", "function": map[string]any{"name": block.Name, "arguments": string(raw)}})
		}
	}
	if text.Len() > 0 {
		message["content"] = text.String()
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	finish := "stop"
	switch in.StopReason {
	case "max_tokens":
		finish = "length"
	case "tool_use":
		finish = "tool_calls"
	case "refusal":
		finish = "content_filter"
	}
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	out := map[string]any{
		"id": "chatcmpl-local", "object": "chat.completion", "model": model,
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
		"usage":   map[string]int{"prompt_tokens": in.Usage.InputTokens, "completion_tokens": in.Usage.OutputTokens, "total_tokens": in.Usage.InputTokens + in.Usage.OutputTokens},
	}
	raw, err := json.Marshal(out)
	return raw, in.Usage.InputTokens, in.Usage.OutputTokens, err
}

func fromOpenAIResponse(body []byte, model string) ([]byte, int, int, error) {
	var in struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content          any    `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
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
	if len(in.Choices) == 0 {
		return nil, 0, 0, fmt.Errorf("openai response has no choices")
	}
	message := in.Choices[0].Message
	content := []map[string]any{}
	if message.ReasoningContent != "" {
		content = append(content, map[string]any{
			"type": "thinking", "thinking": message.ReasoningContent, "signature": reasoningSignature(message.ReasoningContent),
		})
	}
	text, err := responseContent(message.Content)
	if err != nil {
		return nil, 0, 0, err
	}
	content = append(content, text...)
	for _, call := range message.ToolCalls {
		var input map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil || input == nil || call.ID == "" || call.Function.Name == "" {
			return nil, 0, 0, fmt.Errorf("invalid openai tool call")
		}
		content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": input})
	}
	stop := "end_turn"
	switch in.Choices[0].FinishReason {
	case "length":
		stop = "max_tokens"
	case "tool_calls":
		stop = "tool_use"
	case "content_filter":
		stop = "refusal"
	}
	if len(message.ToolCalls) > 0 && stop == "end_turn" {
		stop = "tool_use"
	}
	out := map[string]any{
		"id": in.ID, "type": "message", "role": "assistant", "model": model, "content": content,
		"stop_reason": stop, "stop_sequence": nil,
		"usage": map[string]int{"input_tokens": in.Usage.PromptTokens, "output_tokens": in.Usage.CompletionTokens},
	}
	raw, err := json.Marshal(out)
	return raw, in.Usage.PromptTokens, in.Usage.CompletionTokens, err
}

func responseContent(raw any) ([]map[string]any, error) {
	switch value := raw.(type) {
	case nil:
		return []map[string]any{}, nil
	case string:
		if value == "" {
			return []map[string]any{}, nil
		}
		return []map[string]any{{"type": "text", "text": value}}, nil
	case []any:
		out := []map[string]any{}
		for _, item := range value {
			part, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unsupported openai content part")
			}
			switch part["type"] {
			case "text":
				text, _ := part["text"].(string)
				out = append(out, map[string]any{"type": "text", "text": text})
			default:
				return nil, fmt.Errorf("unsupported openai content type %v", part["type"])
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported openai content")
	}
}

type anthropicStream struct {
	id, model    string
	blocks       []streamBlock
	input        int
	output       int
	stop         string
	sequence     string
	reasoning    strings.Builder
	reasoningIdx int
	started      bool
}

type streamBlock struct {
	kind, id, name, text string
	args                 strings.Builder
}

func (s *anthropicStream) Events(chunk []byte) ([]string, error) {
	var in struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content          any    `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Role             string `json:"role"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(chunk, &in); err != nil {
		return nil, fmt.Errorf("openai stream parse: %w", err)
	}
	events := []string{}
	if in.ID != "" {
		s.id = in.ID
	}
	if s.model == "" {
		s.model = in.Model
	}
	if in.Usage.PromptTokens > 0 {
		s.input = in.Usage.PromptTokens
	}
	if in.Usage.CompletionTokens > 0 {
		s.output = in.Usage.CompletionTokens
	}
	if !s.started && (s.id != "" || len(in.Choices) > 0) {
		s.started = true
		events = append(events, sse("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": s.id, "type": "message", "role": "assistant", "model": s.model, "content": []any{},
			"stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": s.input, "output_tokens": 0},
		}}))
	}
	if len(in.Choices) == 0 {
		return events, nil
	}
	choice := in.Choices[0]
	if choice.Delta.ReasoningContent != "" {
		ev, err := s.reasoningDelta(choice.Delta.ReasoningContent)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	if text, ok := choice.Delta.Content.(string); ok && text != "" {
		ev, err := s.textDelta(text)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	base := len(s.blocks)
	for i := len(s.blocks) - 1; i >= 0 && s.blocks[i].kind == "tool_use"; i-- {
		base = i
	}
	for _, call := range choice.Delta.ToolCalls {
		evs, err := s.toolDelta(base+call.Index, call.ID, call.Function.Name, call.Function.Arguments)
		if err != nil {
			return nil, err
		}
		events = append(events, evs...)
	}
	switch choice.FinishReason {
	case "stop":
		s.stop = "end_turn"
	case "length":
		s.stop = "max_tokens"
	case "tool_calls":
		s.stop = "tool_use"
	case "content_filter":
		s.stop = "refusal"
	}
	return events, nil
}

func (s *anthropicStream) reasoningDelta(text string) (string, error) {
	if s.reasoning.Len() == 0 {
		if len(s.blocks) > 0 {
			return "", fmt.Errorf("reasoning content arrived after another content block")
		}
		s.blocks = append(s.blocks, streamBlock{kind: "thinking"})
		s.reasoningIdx = len(s.blocks) - 1
		s.reasoning.WriteString(text)
		return sse("content_block_start", map[string]any{"type": "content_block_start", "index": s.reasoningIdx,
			"content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}}) +
			sse("content_block_delta", map[string]any{"type": "content_block_delta", "index": s.reasoningIdx,
				"delta": map[string]any{"type": "thinking_delta", "thinking": text}}), nil
	}
	s.reasoning.WriteString(text)
	return sse("content_block_delta", map[string]any{"type": "content_block_delta", "index": s.reasoningIdx,
		"delta": map[string]any{"type": "thinking_delta", "thinking": text}}), nil
}

func (s *anthropicStream) textDelta(text string) (string, error) {
	if len(s.blocks) == 0 || s.blocks[len(s.blocks)-1].kind != "text" {
		s.blocks = append(s.blocks, streamBlock{kind: "text"})
		index := len(s.blocks) - 1
		s.blocks[index].text = text
		return sse("content_block_start", map[string]any{"type": "content_block_start", "index": index,
			"content_block": map[string]any{"type": "text", "text": ""}}) + sse("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": text}}), nil
	}
	index := len(s.blocks) - 1
	s.blocks[index].text += text
	return sse("content_block_delta", map[string]any{"type": "content_block_delta", "index": index,
		"delta": map[string]any{"type": "text_delta", "text": text}}), nil
}

func (s *anthropicStream) toolDelta(index int, id, name, args string) ([]string, error) {
	if index != len(s.blocks) && index != len(s.blocks)-1 {
		return nil, fmt.Errorf("noncontiguous tool stream index")
	}
	if index == len(s.blocks) {
		s.blocks = append(s.blocks, streamBlock{})
	}
	block := &s.blocks[index]
	events := []string{}
	if block.kind == "" {
		if id == "" || name == "" {
			return nil, fmt.Errorf("tool stream is missing id or name")
		}
		*block = streamBlock{kind: "tool_use", id: id, name: name}
		events = append(events, sse("content_block_start", map[string]any{"type": "content_block_start", "index": index,
			"content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}}))
	}
	if args != "" {
		block.args.WriteString(args)
		events = append(events, sse("content_block_delta", map[string]any{"type": "content_block_delta", "index": index,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": args}}))
	}
	return events, nil
}

func (s *anthropicStream) Finish() ([]string, error) {
	if !s.started {
		return nil, fmt.Errorf("openai stream ended before message_start")
	}
	events := []string{}
	if s.reasoning.Len() > 0 {
		events = append(events, sse("content_block_delta", map[string]any{"type": "content_block_delta", "index": s.reasoningIdx,
			"delta": map[string]any{"type": "signature_delta", "signature": reasoningSignature(s.reasoning.String())}}))
	}
	for i, block := range s.blocks {
		if block.kind == "tool_use" {
			var input map[string]any
			if err := json.Unmarshal([]byte(block.args.String()), &input); err != nil || input == nil {
				return nil, fmt.Errorf("streamed tool arguments are not a JSON object")
			}
		}
		events = append(events, sse("content_block_stop", map[string]any{"type": "content_block_stop", "index": i}))
	}
	if s.stop == "" {
		s.stop = "end_turn"
		for _, block := range s.blocks {
			if block.kind == "tool_use" {
				s.stop = "tool_use"
			}
		}
	}
	events = append(events,
		sse("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": s.stop, "stop_sequence": s.sequence},
			"usage": map[string]int{"output_tokens": s.output}}),
		sse("message_stop", map[string]any{"type": "message_stop"}))
	return events, nil
}

type openAIStream struct {
	id, model string
	toolSlot  map[int]int
	nextSlot  int
	text      strings.Builder
	reasoning strings.Builder
	signature strings.Builder
	arguments map[int]*strings.Builder
	stop      string
	input     int
	output    int
	started   bool
	tools     bool
}

func (s *openAIStream) Events(event string, data []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("anthropic stream parse: %w", err)
	}
	if event == "" {
		event, _ = payload["type"].(string)
	}
	if s.toolSlot == nil {
		s.toolSlot = map[int]int{}
		s.arguments = map[int]*strings.Builder{}
	}
	switch event {
	case "message_start":
		s.started = true
		if message, _ := payload["message"].(map[string]any); message != nil {
			s.id, _ = message["id"].(string)
			if usage, _ := message["usage"].(map[string]any); usage != nil {
				s.input = int(numberValue(usage["input_tokens"]))
			}
		}
		return s.chunk(map[string]any{"role": "assistant"}, ""), nil
	case "content_block_start":
		block, _ := payload["content_block"].(map[string]any)
		if block["type"] != "tool_use" {
			return "", nil
		}
		index := int(numberValue(payload["index"]))
		s.toolSlot[index] = s.nextSlot
		s.arguments[index] = &strings.Builder{}
		s.nextSlot++
		s.tools = true
		id, _ := block["id"].(string)
		name, _ := block["name"].(string)
		return s.chunk(map[string]any{"tool_calls": []map[string]any{{
			"index": s.toolSlot[index], "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": ""},
		}}}, ""), nil
	case "content_block_delta":
		delta, _ := payload["delta"].(map[string]any)
		index := int(numberValue(payload["index"]))
		switch delta["type"] {
		case "text_delta":
			text, _ := delta["text"].(string)
			s.text.WriteString(text)
			return s.chunk(map[string]any{"content": text}, ""), nil
		case "thinking_delta":
			text, _ := delta["thinking"].(string)
			s.reasoning.WriteString(text)
			return s.chunk(map[string]any{"reasoning_content": text}, ""), nil
		case "signature_delta":
			text, _ := delta["signature"].(string)
			s.signature.WriteString(text)
			return "", nil
		case "input_json_delta":
			slot, ok := s.toolSlot[index]
			if !ok {
				return "", fmt.Errorf("tool arguments for unopened block")
			}
			partial, _ := delta["partial_json"].(string)
			s.arguments[index].WriteString(partial)
			return s.chunk(map[string]any{"tool_calls": []map[string]any{{
				"index": slot, "function": map[string]any{"arguments": partial},
			}}}, ""), nil
		}
	case "message_delta":
		if delta, _ := payload["delta"].(map[string]any); delta != nil {
			s.stop, _ = delta["stop_reason"].(string)
		}
		if usage, _ := payload["usage"].(map[string]any); usage != nil {
			s.output = int(numberValue(usage["output_tokens"]))
		}
	case "error":
		detail, _ := payload["error"].(map[string]any)
		message, _ := detail["message"].(string)
		return "", fmt.Errorf("%s", message)
	}
	return "", nil
}

func (s *openAIStream) Finish() (string, error) {
	if !s.started {
		return "", fmt.Errorf("anthropic stream ended before message_start")
	}
	for _, args := range s.arguments {
		if strings.TrimSpace(args.String()) == "" {
			continue
		}
		var input map[string]any
		if json.Unmarshal([]byte(args.String()), &input) != nil {
			return "", fmt.Errorf("streamed tool arguments are not a JSON object")
		}
	}
	finish := "stop"
	switch s.stop {
	case "max_tokens":
		finish = "length"
	case "tool_use":
		finish = "tool_calls"
	case "refusal":
		finish = "content_filter"
	}
	if s.tools {
		finish = "tool_calls"
	}
	done := s.chunk(map[string]any{}, finish)
	if s.reasoning.Len() > 0 && s.signature.Len() > 0 {
		done += s.chunk(map[string]any{"reasoning_signature": reasoningSignature(s.reasoning.String())}, "")
	}
	usage := map[string]any{
		"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []any{},
		"usage": map[string]int{"prompt_tokens": s.input, "completion_tokens": s.output, "total_tokens": s.input + s.output},
	}
	raw, _ := json.Marshal(usage)
	return done + "data: " + string(raw) + "\n\ndata: [DONE]\n\n", nil
}

func (s *openAIStream) chunk(delta map[string]any, finish string) string {
	var reason any
	if finish != "" {
		reason = finish
	}
	raw, _ := json.Marshal(map[string]any{
		"id": s.id, "object": "chat.completion.chunk", "model": s.model,
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": reason}},
	})
	return "data: " + string(raw) + "\n\n"
}

func sse(event string, data any) string {
	raw, _ := json.Marshal(data)
	return "event: " + event + "\ndata: " + string(raw) + "\n\n"
}
