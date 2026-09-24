package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// authClient calls the auth internal apps API with a service token (client_credentials)
// and forwards the admin's user token (OBO). service token is cached until near expiry.
type authClient struct {
	authURL      string
	clientID     string
	clientSecret string
	mu           sync.Mutex
	token        string
	exp          time.Time
	http         *http.Client
}

func newAuthClient() *authClient {
	return &authClient{
		authURL:      envOr("AUTH_URL", "http://auth:8080"),
		clientID:     os.Getenv("USERS_CLIENT_ID"),
		clientSecret: os.Getenv("USERS_CLIENT_SECRET"),
		http:         &http.Client{Timeout: 8 * time.Second},
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (a *authClient) serviceToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Before(a.exp) {
		return a.token, nil
	}
	body := `{"clientId":"` + a.clientID + `","clientSecret":"` + a.clientSecret + `"}`
	resp, err := a.http.Post(a.authURL+"/auth/service-token", "application/json", bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	// minimal parse to avoid a struct: find "accessToken":"..."
	tok := extractJSONString(raw, "accessToken")
	if tok == "" {
		return "", errFromBody(raw)
	}
	a.token = tok
	a.exp = time.Now().Add(4 * time.Minute) // service TTL is 5min
	return tok, nil
}

// call proxies METHOD /internal/auth/apps... to auth with service token + user OBO header.
func (a *authClient) call(method, path, userToken string, body []byte) (int, []byte, error) {
	st, err := a.serviceToken()
	if err != nil {
		return 0, nil, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, a.authURL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+st)
	req.Header.Set("X-PF-User-Token", userToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, nil
}

// callService calls auth internal APIs with the service token only (no user OBO) —
// password-reset flow has no user token (specs/018).
func (a *authClient) callService(method, path string, body []byte) (int, []byte, error) {
	st, err := a.serviceToken()
	if err != nil {
		return 0, nil, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, a.authURL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+st)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, nil
}

// callNotify posts to svc-notify /send with the service token (first-party direct DNS on core-net).
func (a *authClient) callNotify(body []byte) (int, []byte, error) {
	st, err := a.serviceToken()
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost,
		envOr("NOTIFY_URL", "http://svc-notify:8080")+"/api/notify/send", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+st)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, nil
}

// callUser proxies with the user's own token as bearer (self change-password; no service token/OBO).
func (a *authClient) callUser(method, path, userToken string, body []byte) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, a.authURL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, nil
}
