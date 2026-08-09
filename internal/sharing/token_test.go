package sharing

import (
	"encoding/base64"
	"testing"
)

func TestTokenCodecStableAndIsolated(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	codec, err := NewTokenCodec(secret)
	if err != nil {
		t.Fatal(err)
	}
	one := codec.Derive("owner", "session", 3, "request")
	if !ValidToken(one) || one != codec.Derive("owner", "session", 3, "request") {
		t.Fatalf("token 不稳定或格式无效:%q", one)
	}
	for _, other := range []string{
		codec.Derive("other", "session", 3, "request"),
		codec.Derive("owner", "other", 3, "request"),
		codec.Derive("owner", "session", 2, "request"),
		codec.Derive("owner", "session", 3, "other"),
	} {
		if other == one {
			t.Fatal("不同资源不应派生相同 token")
		}
	}
}

func TestTokenCodecRejectsMissingOrWeakSecret(t *testing.T) {
	for _, value := range []string{"", "not-base64", base64.RawURLEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := NewTokenCodec(value); err == nil {
			t.Fatalf("弱密钥 %q 未被拒绝", value)
		}
	}
}
