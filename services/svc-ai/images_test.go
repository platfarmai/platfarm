package main

import (
	"encoding/json"
	"testing"
)

func TestImageRequest(t *testing.T) {
	rt := &route{Kind: "openai", Model: "gpt-image-1", BaseURL: "https://example.test/v1/"}
	url, raw, err := imageRequest(map[string]any{"model": "cover", "prompt": "A landscape", "n": 1}, rt)
	if err != nil || url != "https://example.test/v1/images/generations" {
		t.Fatalf("%s %v", url, err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-image-1" || body["prompt"] != "A landscape" {
		t.Fatalf("%v", body)
	}
	rt.Kind = "anthropic"
	if _, _, err := imageRequest(map[string]any{"prompt": "test"}, rt); err == nil {
		t.Fatal("accepted non-image protocol")
	}
	rt.Kind = "openai"
	if _, _, err := imageRequest(map[string]any{"prompt": " "}, rt); err == nil {
		t.Fatal("accepted empty prompt")
	}
}
