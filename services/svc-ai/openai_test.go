package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeTest(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestToolRoundTrip(t *testing.T) {
	payload := decodeTest(t, `{
		"system":[{"type":"text","text":"Use tools"}],
		"messages":[
			{"role":"user","content":"Calculate"},
			{"role":"assistant","content":[{"type":"text","text":"Checking"},{"type":"tool_use","id":"call_1","name":"tax","input":{"income":100}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"20"}]},{"type":"text","text":"Explain"}]}
		],
		"tools":[{"name":"tax","description":"Calculate tax","input_schema":{"type":"object","properties":{"income":{"type":"number"}}}}],
		"tool_choice":{"type":"tool","name":"tax","disable_parallel_tool_use":true},
		"max_tokens":1024,"stop_sequences":["STOP"]
	}`)
	raw, err := toOpenAIRequest(payload, "kimi-test")
	if err != nil {
		t.Fatal(err)
	}
	out := decodeTest(t, string(raw))
	msgs := out["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("messages: %s", raw)
	}
	assistant := msgs[2].(map[string]any)
	call := assistant["tool_calls"].([]any)[0].(map[string]any)
	if call["id"] != "call_1" || call["function"].(map[string]any)["arguments"] != `{"income":100}` {
		t.Fatalf("tool call: %v", call)
	}
	result := msgs[3].(map[string]any)
	content := result["content"].([]any)[0].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "call_1" || content["text"] != "20" {
		t.Fatalf("result: %v", result)
	}
	if out["parallel_tool_calls"] != false || out["temperature"] != nil {
		t.Fatalf("options: %s", raw)
	}
	if out["tools"].([]any)[0].(map[string]any)["type"] != "function" {
		t.Fatalf("tools: %s", raw)
	}
	if out["tool_choice"].(map[string]any)["function"].(map[string]any)["name"] != "tax" {
		t.Fatalf("choice: %s", raw)
	}
	if out["stop"].([]any)[0] != "STOP" || out["max_tokens"] != float64(1024) {
		t.Fatalf("options: %s", raw)
	}
}

func TestToolChoices(t *testing.T) {
	for input, want := range map[string]string{"auto": "auto", "any": "required", "none": "none"} {
		raw, err := toOpenAIRequest(decodeTest(t, `{"tool_choice":{"type":"`+input+`"}}`), "test")
		if err != nil {
			t.Fatal(err)
		}
		if decodeTest(t, string(raw))["tool_choice"] != want {
			t.Fatalf("choice: %s", raw)
		}
	}
}

func TestToolResponse(t *testing.T) {
	raw, in, out, err := fromOpenAIResponse([]byte(`{"id":"msg1","choices":[{"message":{"content":null,"tool_calls":[{"id":"call1","type":"function","function":{"name":"tax","arguments":"{\"income\":100}"}},{"id":"call2","type":"function","function":{"name":"profile","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`), "alias")
	if err != nil {
		t.Fatal(err)
	}
	msg := decodeTest(t, string(raw))
	blocks := msg["content"].([]any)
	if in != 10 || out != 20 || msg["stop_reason"] != "tool_use" || msg["model"] != "alias" || len(blocks) != 2 {
		t.Fatalf("response: %s", raw)
	}
	if blocks[0].(map[string]any)["type"] != "tool_use" || blocks[0].(map[string]any)["input"].(map[string]any)["income"] != float64(100) {
		t.Fatalf("block: %v", blocks[0])
	}
}

func TestTextResponse(t *testing.T) {
	for finish, want := range map[string]string{"stop": "end_turn", "length": "max_tokens"} {
		raw, _, _, err := fromOpenAIResponse([]byte(`{"id":"msg1","choices":[{"message":{"content":"Hello"},"finish_reason":"`+finish+`"}]}`), "alias")
		if err != nil {
			t.Fatal(err)
		}
		msg := decodeTest(t, string(raw))
		if msg["stop_reason"] != want || msg["content"].([]any)[0].(map[string]any)["text"] != "Hello" {
			t.Fatalf("response: %s", raw)
		}
	}
}

func TestRejectLossyConversions(t *testing.T) {
	for _, raw := range []string{
		`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"unknown"}}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"tool_use","id":"c","name":"x","input":{}}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image"}]}]}]}`,
		`{"tool_choice":{"type":"unknown"}}`,
		`{"tools":[{"name":"missing_schema"}]}`,
	} {
		if _, err := toOpenAIRequest(decodeTest(t, raw), "test"); err == nil {
			t.Fatalf("accepted: %s", raw)
		}
	}
	for _, raw := range []string{
		`{"choices":[]}`,
		`{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"x","arguments":"not json"}}]}}]}`,
		`{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"x","arguments":"null"}}]}}]}`,
	} {
		if _, _, _, err := fromOpenAIResponse([]byte(raw), "test"); err == nil {
			t.Fatalf("accepted: %s", raw)
		}
	}
}

func TestRichContentAndThinking(t *testing.T) {
	raw, err := toOpenAIRequest(decodeTest(t, `{
		"thinking":{"type":"enabled","budget_tokens":1024},
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="},"cache_control":{"type":"ephemeral"}},
			{"type":"document","source":{"type":"text","data":"facts"},"title":"Plan"}
		]},{"role":"assistant","content":[
			{"type":"thinking","thinking":"check","signature":"sig"},
			{"type":"text","text":"Answer"}
		]}]
	}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	out := decodeTest(t, string(raw))
	user := out["messages"].([]any)[0].(map[string]any)["content"].([]any)
	image := user[0].(map[string]any)
	if image["type"] != "image_url" || image["image_url"].(map[string]any)["url"] != "data:image/png;base64,aGVsbG8=" || image["cache_control"] == nil {
		t.Fatalf("image: %s", raw)
	}
	if !strings.Contains(user[1].(map[string]any)["text"].(string), "<title>Plan</title>") {
		t.Fatalf("document: %s", raw)
	}
	assistant := out["messages"].([]any)[1].(map[string]any)
	if _, replayed := assistant["reasoning_content"]; replayed || out["reasoning_effort"] != "low" {
		t.Fatalf("unsigned thinking must not be replayed: %s", raw)
	}
	if _, err := toOpenAIRequest(decodeTest(t, `{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`), "test"); err == nil {
		t.Fatal("accepted server tool")
	}
}

func TestStreamTextAndTools(t *testing.T) {
	state := anthropicStream{model: "alias"}
	var events []string
	for _, chunk := range []string{
		`{"id":"msg1","choices":[{"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call1","function":{"name":"tax","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"income\":100}"}}]}}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	} {
		got, err := state.Events([]byte(chunk))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
	}
	done, err := state.Finish()
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, done...)
	joined := strings.Join(events, "")
	for _, want := range []string{"message_start", "text_delta", "input_json_delta", "content_block_stop", `"stop_reason":"tool_use"`, "message_stop"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
	if strings.Count(joined, "event: content_block_start") != 2 || strings.Count(joined, "event: content_block_stop") != 2 || state.input != 3 || state.output != 5 || state.blocks[0].text != "Hello" {
		t.Fatalf("stream state: %+v", state)
	}
}

func TestReasoningRoundTrip(t *testing.T) {
	raw, _, _, err := fromOpenAIResponse([]byte(`{"id":"msg1","choices":[{"message":{"content":"Answer","reasoning_content":"check"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`), "alias")
	if err != nil {
		t.Fatal(err)
	}
	msg := decodeTest(t, string(raw))
	block := msg["content"].([]any)[0].(map[string]any)
	thinking, _ := block["thinking"].(string)
	signature, _ := block["signature"].(string)
	if block["type"] != "thinking" || !validReasoningSignature(thinking, signature) {
		t.Fatalf("thinking block: %s", raw)
	}
	replay, err := toOpenAIRequest(decodeTest(t, `{
		"output_config":{"effort":"high"},
		"messages":[
			{"role":"user","content":"first"},
			{"role":"system","content":"stay here"},
			{"role":"assistant","content":[{"type":"thinking","thinking":"check","signature":"`+signature+`"},{"type":"text","text":"Answer"}]}
		]
	}`), "alias")
	if err != nil {
		t.Fatal(err)
	}
	out := decodeTest(t, string(replay))
	messages := out["messages"].([]any)
	if messages[1].(map[string]any)["role"] != "system" || messages[2].(map[string]any)["reasoning_content"] != "check" || out["reasoning_effort"] != "high" {
		t.Fatalf("replay: %s", replay)
	}
	if _, err := toOpenAIRequest(decodeTest(t, `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"check","signature":"foreign"}]}]}`), "alias"); err != nil {
		t.Fatal(err)
	}
}

func TestReasoningStream(t *testing.T) {
	state := anthropicStream{model: "alias"}
	events, err := state.Events([]byte(`{"id":"msg1","choices":[{"delta":{"reasoning_content":"che"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	more, err := state.Events([]byte(`{"choices":[{"delta":{"reasoning_content":"ck","content":"Hi"},"finish_reason":"stop"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	done, err := state.Finish()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(append(append(events, more...), done...), "")
	if !strings.Contains(joined, "thinking_delta") || !strings.Contains(joined, "signature_delta") || !strings.Contains(reasoningSignature("check"), "svc-ai:") {
		t.Fatalf("stream: %s", joined)
	}
	if !strings.Contains(joined, reasoningSignature("check")) {
		t.Fatalf("missing signature: %s", joined)
	}
}

func TestChatToAnthropic(t *testing.T) {
	raw, err := toAnthropicRequest(decodeTest(t, `{
		"messages":[
			{"role":"system","content":"Be precise"},
			{"role":"user","content":[{"type":"text","text":"Look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]},
			{"role":"assistant","content":"Using tool","reasoning_content":"check","reasoning_signature":"`+reasoningSignature("check")+`","tool_calls":[{"id":"call1","type":"function","function":{"name":"tax","arguments":"{\"income\":100}"}}]},
			{"role":"tool","tool_call_id":"call1","content":"20"}
		],
		"tools":[{"type":"function","function":{"name":"tax","parameters":{"type":"object"}}}],
		"tool_choice":"required","parallel_tool_calls":false,"reasoning_effort":"high","max_completion_tokens":300
	}`), "claude")
	if err != nil {
		t.Fatal(err)
	}
	out := decodeTest(t, string(raw))
	if out["max_tokens"] != float64(300) || out["output_config"].(map[string]any)["effort"] != "high" {
		t.Fatalf("options: %s", raw)
	}
	assistant := out["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if assistant[0].(map[string]any)["type"] != "thinking" || assistant[2].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("assistant: %s", raw)
	}
	if out["tool_choice"].(map[string]any)["disable_parallel_tool_use"] != true {
		t.Fatalf("choice: %s", raw)
	}
	response, in, outTokens, err := fromAnthropicResponse([]byte(`{"content":[{"type":"thinking","thinking":"check","signature":"provider-signature"},{"type":"text","text":"Answer"},{"type":"tool_use","id":"call1","name":"tax","input":{"income":100}}],"stop_reason":"tool_use","usage":{"input_tokens":4,"output_tokens":6}}`), "alias")
	if err != nil || in != 4 || outTokens != 6 {
		t.Fatal(err)
	}
	message := decodeTest(t, string(response))["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if message["reasoning_content"] != "check" || message["reasoning_signature"] != reasoningSignature("check") || message["tool_calls"] == nil {
		t.Fatalf("response: %s", response)
	}
}

func TestAnthropicToOpenAIStream(t *testing.T) {
	state := openAIStream{model: "alias"}
	var frames []string
	for _, item := range []struct{ event, data string }{
		{"message_start", `{"message":{"id":"msg1","usage":{"input_tokens":3}}}`},
		{"content_block_delta", `{"index":0,"delta":{"type":"thinking_delta","thinking":"check"}}`},
		{"content_block_delta", `{"index":0,"delta":{"type":"signature_delta","signature":"provider"}}`},
		{"content_block_delta", `{"index":1,"delta":{"type":"text_delta","text":"Hi"}}`},
		{"content_block_start", `{"index":2,"content_block":{"type":"tool_use","id":"call1","name":"tax"}}`},
		{"content_block_delta", `{"index":2,"delta":{"type":"input_json_delta","partial_json":"{\"income\":100}"}}`},
		{"message_delta", `{"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`},
		{"message_stop", `{}`},
	} {
		frame, err := state.Events(item.event, []byte(item.data))
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	done, err := state.Finish()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(append(frames, done), "")
	for _, want := range []string{`"reasoning_content":"check"`, `"content":"Hi"`, `"arguments":"{\"income\":100}"`, `"finish_reason":"tool_calls"`, "data: [DONE]"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
}

func TestToolErrorResult(t *testing.T) {
	raw, err := toOpenAIRequest(decodeTest(t, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","is_error":true,"content":"failed"}]}]}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	msg := decodeTest(t, string(raw))["messages"].([]any)[0].(map[string]any)
	if msg["content"] != "Tool error: failed" {
		t.Fatalf("result: %s", raw)
	}
}
