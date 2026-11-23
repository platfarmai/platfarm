package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Image generation uses OpenAI's images API, without chat protocol translation.
func imageRequest(payload map[string]any, rt *route) (string, []byte, error) {
	if rt.Kind != "openai" {
		return "", nil, fmt.Errorf("image generation requires an OpenAI-compatible provider")
	}
	prompt, _ := payload["prompt"].(string)
	if strings.TrimSpace(prompt) == "" || len(prompt) > 32000 {
		return "", nil, fmt.Errorf("image prompt must be 1–32000 bytes")
	}
	payload["model"] = rt.Model
	body, err := json.Marshal(payload)
	return strings.TrimRight(rt.BaseURL, "/") + "/images/generations", body, err
}
