// Package sharing 实现 P9 不可变配置分享、公开最小披露与 token 生命周期。
package sharing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const tokenBytes = 32

type TokenCodec struct{ secret []byte }

// NewTokenCodec 校验独立的 base64url 分享密钥。密钥值不得进入错误或日志。
func NewTokenCodec(encoded string) (*TokenCodec, error) {
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(secret) < tokenBytes {
		return nil, fmt.Errorf("SHARE_TOKEN_SECRET 必须是 base64url 编码的至少 32 字节随机值")
	}
	return &TokenCodec{secret: secret}, nil
}

// Derive 对同一资源和幂等键稳定派生 256-bit token，数据库只保存其 Hash。
func (c *TokenCodec) Derive(ownerID, sessionID string, version int, requestID string) string {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte("p9:v1\x00"))
	for _, value := range []string{ownerID, sessionID, strconv.Itoa(version), requestID} {
		_, _ = mac.Write([]byte(value))
		_, _ = mac.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func ValidToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == tokenBytes
}
