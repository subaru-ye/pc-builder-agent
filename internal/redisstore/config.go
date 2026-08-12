package redisstore

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"google.golang.org/adk/v2/session"
)

const (
	defaultSessionTTL = 24 * time.Hour
	defaultCacheTTL   = 168 * time.Hour
)

// Backend 聚合 Redis 连接与 TTL 配置;rdb 为 nil 表示未配置/连接失败(降级)。
// 供 cmd/host 与 cmd/buildsvc 共用同一份装配 + 降级逻辑。
type Backend struct {
	rdb        *redis.Client
	SessionTTL time.Duration
	CacheTTL   time.Duration
}

// Open 按环境变量装配 Redis 后端。REDIS_ADDR 未设置或连接失败时返回一个 rdb=nil 的
// Backend 并告警一行——调用方据此降级(会话回退 InMemory、embedding 缓存停用),
// 保证无 Redis 的 CI / go test 仍可运行(与 PG_TEST_DSN 门控同风格)。
func Open(ctx context.Context) *Backend {
	b := &Backend{
		SessionTTL: durationEnv("REDIS_SESSION_TTL", defaultSessionTTL),
		CacheTTL:   durationEnv("REDIS_CACHE_TTL", defaultCacheTTL),
	}
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		log.Printf("[redis] REDIS_ADDR 未设置:会话回退进程内 InMemory、embedding 缓存停用")
		return b
	}
	rdb, err := Dial(ctx, addr)
	if err != nil {
		log.Printf("[redis] 连接 %s 失败,降级(会话 InMemory、缓存停用): %v", addr, err)
		return b
	}
	log.Printf("[redis] 已连接 %s(会话 TTL=%s、缓存 TTL=%s)", addr, b.SessionTTL, b.CacheTTL)
	b.rdb = rdb
	return b
}

// SessionService 返回会话服务:有 Redis 用 Redis 后端(两进程共享 + TTL),否则回退 InMemory。
func (b *Backend) SessionService() session.Service {
	if b.rdb == nil {
		return session.InMemoryService()
	}
	return NewSessionService(b.rdb, b.SessionTTL)
}

// Available 表示当前进程已连接 Redis；产品 API 据此标记降级状态。
func (b *Backend) Available() bool { return b.rdb != nil }

// Client 返回共享 Redis 客户端；nil 表示已降级。调用方不得关闭它，由 Backend.Close 统一管理。
func (b *Backend) Client() *redis.Client { return b.rdb }

// Ping 供 readyz 检查当前 Redis 连接。
func (b *Backend) Ping(ctx context.Context) error {
	if b.rdb == nil {
		return fmt.Errorf("redis unavailable")
	}
	return b.rdb.Ping(ctx).Err()
}

// WrapEmbedder 给底层 embedder 套一层 Redis 缓存;无 Redis 时返回透传装饰器。
func (b *Backend) WrapEmbedder(inner embedder, identity, provider, model string) *Embedder {
	return NewEmbedder(inner, b.rdb, identity, provider, model, b.CacheTTL)
}

// Close 关闭底层连接(若有)。
func (b *Backend) Close() error {
	if b.rdb != nil {
		return b.rdb.Close()
	}
	return nil
}

func durationEnv(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("[redis] %s=%q 无效,使用默认 %s", key, v, def)
		return def
	}
	return d
}
