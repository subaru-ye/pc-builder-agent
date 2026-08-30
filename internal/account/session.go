package account

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const authSessionPrefix = "pcb:auth:session:"

type SessionRecord struct {
	UserID       string    `json:"user_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Version      int64     `json:"version"`
}

type SessionVault struct {
	rdb    *redis.Client
	aead   cipher.AEAD
	ttl    time.Duration
	macKey []byte
}

func NewSessionVault(rdb *redis.Client, encodedSecret string, ttl time.Duration) (*SessionVault, error) {
	secret, err := base64.RawURLEncoding.DecodeString(encodedSecret)
	if err != nil || len(secret) != 32 {
		return nil, fmt.Errorf("auth: AUTH_SESSION_SECRET 必须是 base64url 编码的 32 字节密钥")
	}
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, fmt.Errorf("auth: 创建 token 加密器: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("auth: 创建 GCM: %w", err)
	}
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	return &SessionVault{rdb: rdb, aead: aead, ttl: ttl, macKey: append([]byte(nil), secret...)}, nil
}

func NewOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: 生成会话标识: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (v *SessionVault) Put(ctx context.Context, token string, record SessionRecord) error {
	if v.rdb == nil {
		return ErrUnavailable
	}
	record.Version++
	plain, err := json.Marshal(record)
	if err != nil {
		return ErrProtocol
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ErrUnavailable
	}
	sealed := v.aead.Seal(nonce, nonce, plain, []byte(v.key(token)))
	if err := v.rdb.Set(ctx, v.key(token), base64.RawURLEncoding.EncodeToString(sealed), v.ttl).Err(); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (v *SessionVault) Get(ctx context.Context, token string) (SessionRecord, error) {
	if v.rdb == nil {
		return SessionRecord{}, ErrUnavailable
	}
	encoded, err := v.rdb.Get(ctx, v.key(token)).Result()
	if errors.Is(err, redis.Nil) {
		return SessionRecord{}, ErrSessionExpired
	}
	if err != nil {
		return SessionRecord{}, ErrUnavailable
	}
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < v.aead.NonceSize() {
		return SessionRecord{}, ErrSessionExpired
	}
	nonce := sealed[:v.aead.NonceSize()]
	plain, err := v.aead.Open(nil, nonce, sealed[v.aead.NonceSize():], []byte(v.key(token)))
	if err != nil {
		return SessionRecord{}, ErrSessionExpired
	}
	var record SessionRecord
	if err := json.Unmarshal(plain, &record); err != nil {
		return SessionRecord{}, ErrSessionExpired
	}
	return record, nil
}

func (v *SessionVault) Delete(ctx context.Context, token string) error {
	if v.rdb == nil {
		return ErrUnavailable
	}
	if err := v.rdb.Del(ctx, v.key(token)).Err(); err != nil {
		return ErrUnavailable
	}
	return nil
}

// RefreshLocked 串行化同一个浏览器会话的 refresh token 使用。未获得锁的请求
// 短暂等待锁持有者完成，再读取新版本，避免 refresh token 并发复用。
func (v *SessionVault) RefreshLocked(ctx context.Context, token string, current SessionRecord, refresh func(context.Context) (SessionRecord, error)) (SessionRecord, error) {
	if v.rdb == nil {
		return SessionRecord{}, ErrUnavailable
	}
	lockKey := v.key(token) + ":refresh"
	lockValue, err := NewOpaqueToken()
	if err != nil {
		return SessionRecord{}, err
	}
	locked, err := v.rdb.SetNX(ctx, lockKey, lockValue, 15*time.Second).Result()
	if err != nil {
		return SessionRecord{}, ErrUnavailable
	}
	if !locked {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case <-ctx.Done():
				return SessionRecord{}, ErrUnavailable
			case <-deadline.C:
				return SessionRecord{}, ErrUnavailable
			case <-ticker.C:
				record, err := v.Get(ctx, token)
				if err == nil && record.Version > current.Version {
					return record, nil
				}
			}
		}
	}
	defer func() {
		const release = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
		_ = v.rdb.Eval(context.Background(), release, []string{lockKey}, lockValue).Err()
	}()

	next, err := refresh(ctx)
	if err != nil {
		return SessionRecord{}, err
	}
	next.Version = current.Version
	if err := v.Put(ctx, token, next); err != nil {
		return SessionRecord{}, err
	}
	return v.Get(ctx, token)
}

func (v *SessionVault) key(token string) string {
	h := sha256.Sum256([]byte(token))
	return authSessionPrefix + hex.EncodeToString(h[:])
}

type idempotencyRecord struct {
	RequestMAC string `json:"request_mac"`
	Result     []byte `json:"result"`
}

// DoIdempotent 使用请求 HMAC 防止同 key 换正文，并加密保存成功结果。
func (v *SessionVault) DoIdempotent(ctx context.Context, operation, key string, payload []byte, fn func() ([]byte, error)) ([]byte, error) {
	if v.rdb == nil {
		return nil, ErrUnavailable
	}
	requestMAC := v.mac(operation + "\x00" + string(payload))
	recordKey := "pcb:auth:idempotency:" + operation + ":" + key
	read := func() ([]byte, bool, error) {
		encoded, err := v.rdb.Get(ctx, recordKey).Result()
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, ErrUnavailable
		}
		plain, err := v.open(recordKey, encoded)
		if err != nil {
			return nil, false, ErrProtocol
		}
		var record idempotencyRecord
		if json.Unmarshal(plain, &record) != nil {
			return nil, false, ErrProtocol
		}
		if !hmac.Equal([]byte(record.RequestMAC), []byte(requestMAC)) {
			return nil, false, ErrIdempotencyConflict
		}
		return record.Result, true, nil
	}
	if result, found, err := read(); err != nil || found {
		return result, err
	}
	lockKey := recordKey + ":lock"
	lockValue, _ := NewOpaqueToken()
	locked, err := v.rdb.SetNX(ctx, lockKey, lockValue, 15*time.Second).Result()
	if err != nil {
		return nil, ErrUnavailable
	}
	if !locked {
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil, ErrUnavailable
			case <-deadline.C:
				return nil, ErrUnavailable
			case <-ticker.C:
				if result, found, err := read(); err != nil || found {
					return result, err
				}
			}
		}
	}
	defer func() {
		const release = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
		_ = v.rdb.Eval(context.Background(), release, []string{lockKey}, lockValue).Err()
	}()
	result, err := fn()
	if err != nil {
		return nil, err
	}
	plain, _ := json.Marshal(idempotencyRecord{RequestMAC: requestMAC, Result: result})
	encoded, err := v.seal(recordKey, plain)
	if err != nil {
		return nil, err
	}
	if err := v.rdb.Set(ctx, recordKey, encoded, 15*time.Minute).Err(); err != nil {
		return nil, ErrUnavailable
	}
	return result, nil
}

func (v *SessionVault) mac(value string) string {
	h := hmac.New(sha256.New, v.macKey)
	_, _ = h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

func (v *SessionVault) seal(aad string, plain []byte) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(v.aead.Seal(nonce, nonce, plain, []byte(aad))), nil
}

func (v *SessionVault) open(aad, encoded string) ([]byte, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < v.aead.NonceSize() {
		return nil, ErrProtocol
	}
	nonce := sealed[:v.aead.NonceSize()]
	return v.aead.Open(nil, nonce, sealed[v.aead.NonceSize():], []byte(aad))
}
