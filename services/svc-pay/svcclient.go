// 平台内其它服务的客户端：服务 token（client_credentials）与 svc-file 文件拉取。
// 首方服务走 core-net 直连 DNS，不经网关（与 svc-users → svc-notify 同一做法）。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

var svcHTTP = &http.Client{Timeout: 10 * time.Second}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	tokenMu        sync.Mutex
	tokenVal       string
	tokenExp       time.Time
	devTokenLogged bool
)

// serviceToken client_credentials 换取并缓存 4 分钟（TTL 5 分钟）。PAY_CLIENT_ID 未配置 = dev 降级：返回空。
func serviceToken(ctx context.Context) (string, error) {
	clientID := os.Getenv("PAY_CLIENT_ID")
	secret := os.Getenv("PAY_CLIENT_SECRET")
	if clientID == "" || secret == "" {
		if !devTokenLogged {
			log.Println("PAY_CLIENT_ID/PAY_CLIENT_SECRET 未配置：不向其它服务携带服务 token（dev 降级）")
			devTokenLogged = true
		}
		return "", nil
	}
	tokenMu.Lock()
	defer tokenMu.Unlock()
	if tokenVal != "" && time.Now().Before(tokenExp) {
		return tokenVal, nil
	}
	body, _ := json.Marshal(map[string]string{"clientId": clientID, "clientSecret": secret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, envOr("AUTH_URL", "http://auth:8080")+"/auth/service-token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := svcHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if json.Unmarshal(raw, &out) != nil || out.AccessToken == "" {
		return "", fmt.Errorf("service-token failed: %s", trunc(string(raw), 200))
	}
	tokenVal, tokenExp = out.AccessToken, time.Now().Add(4*time.Minute)
	return tokenVal, nil
}

// ── svc-file ────────────────────────────────────────────────

const maxMediaBytes = 2 << 20 // 微信进件图片上限 2MB

type fetchedFile struct {
	Filename string
	Mime     string
	Size     int64
	Content  []byte
}

// fetchFile 可被测试替换。
var fetchFile = fetchFileViaSvcFile

// proxyUploadURL 替控制台向 svc-file 申请预签名上传链接（带操作员自己的 token）。
// 控制台不直接打 svc-file：经网关的 svc-file 响应是加密的（附录 F），而 core-net 直连是明文。
func proxyUploadURL(ctx context.Context, bearer string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		envOr("FILE_URL", "http://svc-file:8080")+"/api/file/upload-url", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := svcHTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, raw, nil
}

// fetchFileViaSvcFile 用调用者自己的 token 向 svc-file 换下载链接（归属判定在 svc-file），再拉字节。
// 直连 svc-file 不经网关，响应是明文 JSON（附录 F 的响应加密只发生在网关）。
func fetchFileViaSvcFile(ctx context.Context, bearer, fileID string) (fetchedFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		envOr("FILE_URL", "http://svc-file:8080")+"/api/file/"+fileID+"/download-url", nil)
	if err != nil {
		return fetchedFile{}, err
	}
	req.Header.Set("Authorization", bearer)
	resp, err := svcHTTP.Do(req)
	if err != nil {
		return fetchedFile{}, fmt.Errorf("svc-file: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fetchedFile{}, fmt.Errorf("svc-file %d: %s", resp.StatusCode, trunc(string(raw), 200))
	}
	var out struct {
		DownloadURL string `json:"downloadUrl"`
		File        struct {
			Filename string `json:"filename"`
			Mime     string `json:"mime"`
			Size     int64  `json:"size"`
		} `json:"file"`
	}
	if json.Unmarshal(raw, &out) != nil || out.DownloadURL == "" {
		return fetchedFile{}, fmt.Errorf("svc-file: bad download-url response")
	}
	if out.File.Size > maxMediaBytes {
		return fetchedFile{}, fmt.Errorf("文件 %s 超过 2MB", out.File.Filename)
	}
	dreq, err := http.NewRequestWithContext(ctx, http.MethodGet, out.DownloadURL, nil)
	if err != nil {
		return fetchedFile{}, err
	}
	dresp, err := svcHTTP.Do(dreq)
	if err != nil {
		return fetchedFile{}, fmt.Errorf("download: %w", err)
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		return fetchedFile{}, fmt.Errorf("download %d", dresp.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(dresp.Body, maxMediaBytes+1))
	if err != nil {
		return fetchedFile{}, err
	}
	if len(content) > maxMediaBytes {
		return fetchedFile{}, fmt.Errorf("文件 %s 超过 2MB", out.File.Filename)
	}
	return fetchedFile{Filename: out.File.Filename, Mime: out.File.Mime, Size: int64(len(content)), Content: content}, nil
}
