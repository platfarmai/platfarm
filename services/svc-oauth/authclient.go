package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// authHTTP 可被测试替换。
var authHTTP = &http.Client{Timeout: 10 * time.Second}

func authURL() string {
	if v := envOrEmpty("AUTH_URL"); v != "" {
		return v
	}
	return "http://auth:8080"
}

func serviceToken() (string, error) {
	body, _ := json.Marshal(map[string]string{
		"ClientId":     envOrEmpty("PF_CLIENT_ID"),
		"ClientSecret": envOrEmpty("PF_CLIENT_SECRET"),
	})
	resp, err := authHTTP.Post(authURL()+"/auth/service-token", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("service-token: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("service-token read: %w", err)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("service-token: status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("service-token decode: %w", err)
	}
	return out.AccessToken, nil
}

// exchangeExternal 已验证的外部身份 → 平台 token 对（auth 内网端点）。
func exchangeExternal(profile *ExternalProfile) (map[string]any, error) {
	tok, err := serviceToken()
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{
		"Provider":    profile.Provider,
		"ExternalId":  profile.Subject,
		"DisplayName": profile.DisplayName,
	})
	req, err := http.NewRequest(http.MethodPost, authURL()+"/internal/auth/external-login", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("external-login: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := authHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("external-login: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("external-login read: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("external-login: status %d: %s", resp.StatusCode, raw)
	}
	var tokens map[string]any
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return nil, fmt.Errorf("external-login decode: %w", err)
	}
	return tokens, nil
}
