// TOTP（RFC 6238，SHA1/30s/6 位）标准库实现——auth 不引入新依赖（specs/018）。
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

func otpauthURL(username, secret string) string {
	return "otpauth://totp/Platfarm:" + url.PathEscape(username) +
		"?secret=" + secret + "&issuer=Platfarm&algorithm=SHA1&digits=6&period=30"
}

func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", code)
}

// verifyTOTP 允许 ±1 步（30s）时钟偏移。
func verifyTOTP(secret, code string) bool {
	if len(code) != 6 {
		return false
	}
	key, err := b32.DecodeString(secret)
	if err != nil {
		return false
	}
	counter := uint64(time.Now().Unix() / 30)
	for _, c := range []uint64{counter, counter - 1, counter + 1} {
		if subtle.ConstantTimeCompare([]byte(hotp(key, c)), []byte(code)) == 1 {
			return true
		}
	}
	return false
}
