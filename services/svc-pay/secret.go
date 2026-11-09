// 渠道凭据保护：if_params 以 AES-256-GCM 密文落库。
// 主密钥来自 PAY_SECRET_KEY（32 字节 hex）。dev 未配置时自动生成并持久化到数据卷，
// 容器重建不丢密钥；非 dev 环境缺失即拒绝启动——丢密钥等于丢全部渠道凭据。
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
	raw := os.Getenv("PAY_SECRET_KEY")
	if raw == "" {
		if os.Getenv("PAY_ENV") == "dev" {
			raw = loadOrCreateKeyFile()
			log.Println("PAY_SECRET_KEY 未配置：dev 模式已自动生成并持久化（生产必须显式配置）")
		} else {
			log.Fatal("PAY_SECRET_KEY is required (32 bytes hex)")
		}
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		log.Fatal("PAY_SECRET_KEY must be 32 bytes hex (64 chars)")
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

// seal 加密；空串原样返回。
func seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	raw, err := sealBytes([]byte(plain))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// open 解密。
func open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := hex.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	plain, err := openBytes(raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func newGCM() (cipher.AEAD, error) {
	block, err := aes.NewCipher(secretKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealBytes 加密二进制（nonce || 密文），用于进件图片等大字段。
func sealBytes(plain []byte) ([]byte, error) {
	gcm, err := newGCM()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

// openBytes 解密 sealBytes 的输出。
func openBytes(raw []byte) ([]byte, error) {
	gcm, err := newGCM()
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errShortCipher
	}
	return gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
}

var errShortCipher = errStr("ciphertext too short")

type errStr string

func (e errStr) Error() string { return string(e) }
