// Platfarm 契约测试三件套：无 token 401 / 有效 token 200 且身份正确 / 篡改 token 401。
// 编译为 /contract-test，由 pctl check --e2e 在服务容器内执行，经网关内网地址访问。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

const target = "__MOUNT__/me"

func gateway() string {
	if v := os.Getenv("GATEWAY_URL"); v != "" {
		return v
	}
	return "http://gateway:8000"
}

var client = &http.Client{Timeout: 5 * time.Second}

func request(path, token string) (int, map[string]any) {
	req, _ := http.NewRequest(http.MethodGet, gateway()+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("request error:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func login() string {
	payload, _ := json.Marshal(map[string]string{"Username": "admin", "Password": "admin123"})
	resp, err := client.Post(gateway()+"/auth/login", "application/json", bytes.NewReader(payload))
	if err != nil {
		fmt.Println("login error:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || out.AccessToken == "" {
		fmt.Println("FAIL: 种子账号登录失败，无法继续")
		os.Exit(1)
	}
	return out.AccessToken
}

func main() {
	var failures []string

	if status, _ := request(target, ""); status != 401 {
		failures = append(failures, fmt.Sprintf("无 token 应 401，得到 %d", status))
	}

	token := login()
	status, body := request(target, token)
	switch {
	case status != 200:
		failures = append(failures, fmt.Sprintf("有效 token 应 200，得到 %d", status))
	case body["username"] != "admin":
		failures = append(failures, fmt.Sprintf("身份不匹配：期望 admin，得到 %v", body["username"]))
	}

	if status, _ := request(target, token[:len(token)-2]+"xx"); status != 401 {
		failures = append(failures, fmt.Sprintf("篡改 token 应 401，得到 %d", status))
	}

	if len(failures) > 0 {
		fmt.Println("FAIL:")
		for _, f := range failures {
			fmt.Println("  -", f)
		}
		os.Exit(1)
	}
	fmt.Println("PASS: 契约三件套全部通过")
}
