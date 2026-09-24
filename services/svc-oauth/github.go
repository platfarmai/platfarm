package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// githubHTTP 可被测试替换，避免真实出网。
var githubHTTP = &http.Client{Timeout: 10 * time.Second}

type githubProvider struct {
	clientID     string
	clientSecret string
}

func newGitHubProvider() *githubProvider {
	return &githubProvider{
		clientID:     envOrEmpty("GITHUB_CLIENT_ID"),
		clientSecret: envOrEmpty("GITHUB_CLIENT_SECRET"),
	}
}

func (p *githubProvider) ID() string { return "github" }

func (p *githubProvider) Start(state string) (string, error) {
	if p.clientID == "" || p.clientSecret == "" {
		return "", misconfig(p.ID())
	}
	q := url.Values{
		"client_id":    {p.clientID},
		"redirect_uri": {selfURL() + "/api/oauth/github/callback"},
		"state":        {state},
	}
	return "https://github.com/login/oauth/authorize?" + q.Encode(), nil
}

func (p *githubProvider) Callback(code string) (*ExternalProfile, error) {
	if p.clientID == "" || p.clientSecret == "" {
		return nil, misconfig(p.ID())
	}
	form := url.Values{
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"code":          {code},
	}
	req, err := http.NewRequest(http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("github token read: %w", err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("github token decode: %w", err)
	}
	if tok.Error != "" {
		return nil, fmt.Errorf("github token: %s: %s", tok.Error, tok.ErrorDesc)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("github token: empty access_token")
	}

	ureq, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, fmt.Errorf("github user: %w", err)
	}
	ureq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	ureq.Header.Set("Accept", "application/json")
	uresp, err := githubHTTP.Do(ureq)
	if err != nil {
		return nil, fmt.Errorf("github user: %w", err)
	}
	defer uresp.Body.Close()
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(uresp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("github user decode: %w", err)
	}
	if u.ID == 0 {
		return nil, fmt.Errorf("github user: missing id")
	}
	return &ExternalProfile{
		Provider:    "github",
		Subject:     fmt.Sprintf("%d", u.ID),
		DisplayName: u.Login,
	}, nil
}
