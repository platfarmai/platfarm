package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const mount = "/api/oauth"

func gateway() string {
	if v := os.Getenv("GATEWAY_URL"); v != "" {
		return v
	}
	return "http://gateway:8000"
}

var client = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func main() {
	var failures []string

	status, cb, err := followAuthorize("/api/oauth/mock/authorize")
	if err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
	if status != 200 || cb["exchangeCode"] == nil {
		fmt.Printf("FAIL: mock authorize/callback 流程失败 (%d) %v\n", status, cb)
		os.Exit(1)
	}

	code := fmt.Sprint(cb["exchangeCode"])
	status, tokens := postJSON("/api/oauth/exchange", map[string]string{"code": code})
	if status != 200 || tokens["accessToken"] == nil {
		failures = append(failures, fmt.Sprintf("兑换码换 token 失败 (%d)", status))
	} else {
		req, _ := http.NewRequest(http.MethodGet, gateway()+"/auth/me", nil)
		req.Header.Set("Authorization", "Bearer "+fmt.Sprint(tokens["accessToken"]))
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			failures = append(failures, "me: "+err.Error())
		} else {
			defer resp.Body.Close()
			var me map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&me)
			u := fmt.Sprint(me["username"])
			if !strings.HasPrefix(u, "mock_") {
				failures = append(failures, "外部身份用户名异常: "+u)
			}
		}
		status, _ = postJSON("/api/oauth/exchange", map[string]string{"code": code})
		if status != 401 {
			failures = append(failures, fmt.Sprintf("兑换码复用应 401，得到 %d", status))
		}
	}

	status, _ = getNoFollow("/api/oauth/mock/callback?code=x&state=forged")
	if status != 401 {
		failures = append(failures, fmt.Sprintf("伪造 state 应 401，得到 %d", status))
	}

	if len(failures) > 0 {
		fmt.Println("FAIL:")
		for _, f := range failures {
			fmt.Println("  -", f)
		}
		os.Exit(1)
	}
	fmt.Println("PASS: svc-oauth 契约测试全部通过")
}

func followAuthorize(path string) (int, map[string]any, error) {
	url := gateway() + path
	self := os.Getenv("SELF_URL")
	if self == "" {
		self = "http://localhost:18000"
	}
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == 301 || resp.StatusCode == 302 {
			loc := resp.Header.Get("Location")
			url = strings.Replace(loc, self, gateway(), 1)
			continue
		}
		var out map[string]any
		_ = json.Unmarshal(body, &out)
		return resp.StatusCode, out, nil
	}
	return 0, nil, fmt.Errorf("too many redirects")
}

func getNoFollow(path string) (int, map[string]any) {
	req, _ := http.NewRequest(http.MethodGet, gateway()+path, nil)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func postJSON(path string, body any) (int, map[string]any) {
	raw, _ := json.Marshal(body)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post(gateway()+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// silence unused
var _ = mount
