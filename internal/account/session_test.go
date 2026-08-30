package account

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func authRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR 未设置")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 14})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("Redis 已配置但不可达: %v", err)
	}
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestSessionVaultRefreshLockedUsesRefreshTokenOnce(t *testing.T) {
	rdb := authRedis(t)
	secret := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	vault, err := NewSessionVault(rdb, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	token := strings.Repeat("r", 43)
	if err := vault.Put(ctx, token, SessionRecord{UserID: "user-id", AccessToken: "old-access", RefreshToken: "single-use-refresh", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	current, err := vault.Get(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	refresh := func(context.Context) (SessionRecord, error) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		return SessionRecord{UserID: "user-id", AccessToken: "new-access", RefreshToken: "rotated-refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	start := make(chan struct{})
	results := make(chan SessionRecord, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := vault.RefreshLocked(ctx, token, current, refresh)
			results <- result
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result.AccessToken != "new-access" || result.Version <= current.Version {
			t.Fatalf("refresh result=%+v current=%+v", result, current)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh 调用次数=%d，期望 1", calls.Load())
	}
}

func TestSessionVaultEncryptsTamperRejectsAndIdempotencyConflicts(t *testing.T) {
	rdb := authRedis(t)
	secret := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	vault, err := NewSessionVault(rdb, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	token := strings.Repeat("t", 43)
	record := SessionRecord{UserID: "user-id", AccessToken: "access-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(time.Hour)}
	if err := vault.Put(ctx, token, record); err != nil {
		t.Fatal(err)
	}
	raw, _ := rdb.Get(ctx, vault.key(token)).Result()
	if strings.Contains(raw, "access-secret") || strings.Contains(raw, "refresh-secret") {
		t.Fatal("Redis 中出现明文 token")
	}
	if got, err := vault.Get(ctx, token); err != nil || got.UserID != "user-id" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if err := rdb.Set(ctx, vault.key(token), raw+"x", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get(ctx, token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("篡改后应失效: %v", err)
	}

	calls := 0
	first, err := vault.DoIdempotent(ctx, "login", "key", []byte("same"), func() ([]byte, error) { calls++; return []byte("result"), nil })
	if err != nil || string(first) != "result" {
		t.Fatal(err)
	}
	second, err := vault.DoIdempotent(ctx, "login", "key", []byte("same"), func() ([]byte, error) { calls++; return []byte("other"), nil })
	if err != nil || string(second) != "result" || calls != 1 {
		t.Fatalf("second=%q calls=%d err=%v", second, calls, err)
	}
	if _, err := vault.DoIdempotent(ctx, "login", "key", []byte("different"), func() ([]byte, error) { return nil, nil }); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("同 key 不同正文应冲突: %v", err)
	}
}
