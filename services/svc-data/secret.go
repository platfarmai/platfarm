// svc-data 密钥保护：webhook / 通知密钥以 AES-256-GCM 密文落库。
// 主密钥来自 DATA_SECRET_KEY（32 字节 hex）；缺失时自动生成并持久化到数据卷，
// 保证容器重建不丢密钥。库泄露本身不足以还原明文。
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
)

var secretKey []byte

func mustInitSecret() {
	raw := os.Getenv("DATA_SECRET_KEY")
	if raw == "" {
		raw = loadOrCreateKeyFile()
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		log.Fatal("DATA_SECRET_KEY must be 32 bytes hex (64 chars)")
	}
	secretKey = key
}

func loadOrCreateKeyFile() string {
	path := "/data/secret.key"
	if b, err := os.ReadFile(path); err == nil && len(b) == 64 {
		return string(b)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("rand: %v", err)
	}
	encoded := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
			log.Printf("persist secret key: %v（仅内存，容器重建后已存密文将无法解密）", err)
		}
	}
	return encoded
}

// seal 加密；空串原样返回（未配置的可选密钥）。
func seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(secretKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return hex.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// open 解密；兼容升级前的明文存量（非合法密文时原样返回）。
func open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := hex.DecodeString(sealed)
	if err != nil {
		return sealed, nil // 存量明文
	}
	block, err := aes.NewCipher(secretKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return sealed, nil
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return sealed, nil // 非本密钥所加密文（如存量明文恰好是十六进制）按明文兼容
	}
	return string(plain), nil
}
