package wxpay

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLive_SignatureAcceptedByWeChat 用真实商户凭据打一次只读接口 GET /v3/certificates，
// 证明请求签名链路（私钥 / 证书序列号 / Authorization 头格式）被微信接受，并用微信支付公钥
// 校验响应签名。默认跳过；设置 WXPAY_LIVE_KEY_DIR + WXPAY_LIVE_MCHID 才运行。
// 不打印任何密钥内容，只输出状态码、Wechatpay-Serial 与证书数量。
func TestLive_SignatureAcceptedByWeChat(t *testing.T) {
	dir := os.Getenv("WXPAY_LIVE_KEY_DIR")
	mchid := os.Getenv("WXPAY_LIVE_MCHID")
	if dir == "" || mchid == "" {
		t.Skip("WXPAY_LIVE_KEY_DIR / WXPAY_LIVE_MCHID 未设置，跳过真实渠道联调")
	}
	keyPEM := mustReadFile(t, filepath.Join(dir, "apiclient_key.pem"))
	certPEM := mustReadFile(t, filepath.Join(dir, "apiclient_cert.pem"))
	pubPEM := ""
	if p := os.Getenv("WXPAY_LIVE_PUBKEY_FILE"); p != "" {
		pubPEM = mustReadFile(t, p)
	}
	serial := os.Getenv("WXPAY_LIVE_SERIAL")
	if serial == "" {
		serial = certSerial(t, certPEM)
	}
	prov := map[string]string{
		"sp_mchid": mchid, "mch_serial_no": serial, "private_key": keyPEM,
		"wx_public_key": pubPEM, "wx_public_key_id": os.Getenv("WXPAY_LIVE_PUBKEY_ID"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, raw, err := doSigned(ctx, http.MethodGet, "/v3/certificates", nil, prov)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Logf("status=%d Wechatpay-Serial=%s", resp.StatusCode, resp.Header.Get("Wechatpay-Serial"))
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("微信拒绝签名（检查私钥与证书序列号是否匹配）: %s", redact(raw))
	}
	if resp.StatusCode/100 != 2 {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, redact(raw))
	}
	var out struct {
		Data []struct {
			SerialNo    string `json:"serial_no"`
			EncryptCert struct {
				Algorithm  string `json:"algorithm"`
				Nonce      string `json:"nonce"`
				Associated string `json:"associated_data"`
				Ciphertext string `json:"ciphertext"`
			} `json:"encrypt_certificate"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &out)
	t.Logf("platform certificates returned: %d", len(out.Data))

	// 平台证书用 APIv3 密钥 AES-GCM 加密下发：解开即同时验证了回调解密路径。
	if v3 := os.Getenv("WXPAY_LIVE_V3_FILE"); v3 != "" && len(out.Data) > 0 {
		apiV3Key := strings.TrimSpace(mustReadFile(t, v3))
		for _, d := range out.Data {
			plain, err := decryptAESGCM(apiV3Key, d.EncryptCert.Associated, d.EncryptCert.Nonce, d.EncryptCert.Ciphertext)
			if err != nil {
				t.Fatalf("APIv3 密钥解密平台证书失败（密钥不匹配？）: %v", err)
			}
			block, _ := pem.Decode(plain)
			if block == nil {
				t.Fatal("解密结果不是 PEM 证书")
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				t.Fatalf("parse platform cert: %v", err)
			}
			der, _ := x509.MarshalPKIXPublicKey(cert.PublicKey)
			certPub := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
			same := pubPEM != "" && normalizePEM(certPub) == normalizePEM(pubPEM)
			t.Logf("platform cert serial=%s notAfter=%s pub_key.pem matches cert public key=%v",
				strings.ToUpper(cert.SerialNumber.Text(16)), cert.NotAfter.Format("2006-01-02"), same)
			// 用刚解出的平台证书公钥验响应签名：这是证书模式的标准引导流程，
			// 配置方式与生产一致（wx_platform_cert + wx_platform_cert_serial，serial 决定选钥）。
			serial := strings.ToUpper(cert.SerialNumber.Text(16))
			if resp.Header.Get("Wechatpay-Serial") == serial {
				p2 := map[string]string{
					"wx_public_key": pubPEM, "wx_public_key_id": prov["wx_public_key_id"],
					"wx_platform_cert": certPub, "wx_platform_cert_serial": serial,
				}
				if err := verifyResponse(resp, raw, p2); err != nil {
					t.Fatalf("用平台证书验响应签名失败: %v", err)
				}
				t.Log("response signature verified with decrypted platform certificate (serial-selected key)")
			}
		}
	}
}

func normalizePEM(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	return string(raw)
}

// certSerial 从商户证书 PEM 提取大写十六进制序列号（Authorization 头 serial_no）。
func certSerial(t *testing.T, certPEM string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("apiclient_cert.pem 无法解析")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return strings.ToUpper(cert.SerialNumber.Text(16))
}

func redact(raw []byte) string {
	s := string(raw)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
