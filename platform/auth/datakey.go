package main

import (
	"crypto/sha256"
	"encoding/base64"
	"os"

	"golang.org/x/crypto/hkdf"
)

// dataKey 按附录 F 派生会话数据密钥：HKDF-SHA256(PF_DATA_SECRET, salt=tokenId, info=pf-data)。
// 未配置 PF_DATA_SECRET 时返回空串，登录响应不带 dataKey，网关也不加密。
func dataKey(tokenID string) string {
	secret := os.Getenv("PF_DATA_SECRET")
	if secret == "" || tokenID == "" {
		return ""
	}
	r := hkdf.New(sha256.New, []byte(secret), []byte(tokenID), []byte("pf-data"))
	buf := make([]byte, 32)
	if _, err := r.Read(buf); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf)
}
