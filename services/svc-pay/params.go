package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// parseParams 渠道参数以 JSON 对象存储，值一律字符串。
func parseParams(plain string) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal([]byte(plain), &out)
	return out
}

func mustSealParams(v map[string]string) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return seal(string(raw))
}

// signPayload 商户请求签名：HMAC-SHA256(app_secret, app_id\ntimestamp\nnonce\nMETHOD\nrequest_uri\nbody)。
// 方法与请求 URI（路径 + 查询串）参与签名：截获的签名不能挪到别的接口、别的单号上用。
func signPayload(secret, appID, ts, nonce, method, uri, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s\n%s\n%s\n%s\n%s\n%s", appID, ts, nonce, method, uri, body)
	return hex.EncodeToString(mac.Sum(nil))
}

// signFields 下游通知签名：按 key 排序拼 k=v&…，HMAC-SHA256，十六进制小写。
func signFields(secret string, fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k == "sign" || fields[k] == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fields[k])
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}
