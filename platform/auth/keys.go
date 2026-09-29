package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// keyID 由当前签名公钥派生（指纹前 16 hex）。轮换后新旧 token 的 kid 不同，
// 服务按 kid 在 jwks-extra 里找到旧公钥继续验在途 token。
func keyIDOf(pub *rsa.PublicKey) string {
	sum := sha256.Sum256(pub.N.Bytes())
	return hex.EncodeToString(sum[:8])
}

// loadOrCreateKeys 加载 RSA 私钥；不存在则生成并落盘（私钥 + 公钥 PEM）。
// pctl sync 也会做同样的生成（幂等，以文件存在为准），公钥供网关与服务验签。
func loadOrCreateKeys(privPath string) (*rsa.PrivateKey, error) {
	if raw, err := os.ReadFile(privPath); err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, fmt.Errorf("%s: 非法 PEM", privPath)
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析私钥: %w", err)
		}
		return key, nil
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("生成 RSA 密钥: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(privPath), 0o755); err != nil {
		return nil, err
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	if err := os.WriteFile(privPath+".pub", pubPEM, 0o644); err != nil {
		return nil, err
	}
	return key, nil
}

// handleJWKS 暴露公钥（RFC 7517），供网关外部/浏览器端验签与将来 kid 轮换。
func (s *server) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	pub := &s.key.PublicKey
	e := make([]byte, 8)
	binary.BigEndian.PutUint64(e, uint64(pub.E))
	for len(e) > 1 && e[0] == 0 {
		e = e[1:]
	}
	keys := []map[string]string{jwkOf(pub)}
	keys = append(keys, extraJWKs()...)
	writeJSON(w, 200, map[string]any{"keys": keys})
}

func jwkOf(pub *rsa.PublicKey) map[string]string {
	e := make([]byte, 8)
	binary.BigEndian.PutUint64(e, uint64(pub.E))
	for len(e) > 1 && e[0] == 0 {
		e = e[1:]
	}
	return map[string]string{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": keyIDOf(pub),
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(e),
	}
}

// extraJWKs 把轮换保留的旧公钥（jwks-extra/*.pub）一并公布，在途 token 仍可验。
func extraJWKs() []map[string]string {
	dir := filepath.Join(filepath.Dir(envOr("JWT_PRIVATE_KEY_FILE", "/keys/pf-auth.pem")), "jwks-extra")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []map[string]string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			continue
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			continue
		}
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			continue
		}
		out = append(out, jwkOf(pub))
	}
	return out
}
