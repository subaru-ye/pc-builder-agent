package redisstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/subaru-ye/pc-builder-agent/internal/evalmetrics"
)

// embedder 是 query 向量化的最小面(与 tools.QueryEmbedder 结构一致,不 import 以避免耦合)。
type embedder interface {
	EmbedOne(ctx context.Context, text string) ([]float32, error)
}

// Embedder 是给底层 embedder 加 Redis 缓存的装饰器:同一模型 + 同一 query 文本 → 同一向量,
// 天然可缓存;短查询文本在多轮/跨会话高频复现,命中率真实。
type Embedder struct {
	inner    embedder
	rdb      *redis.Client
	identity string
	provider string
	model    string
	ttl      time.Duration
}

// NewEmbedder 用 Redis 包一层缓存。rdb 为 nil 时不启用缓存(直接透传底层),
// 保证无 Redis 时 host/buildsvc 仍可运行。
func NewEmbedder(inner embedder, rdb *redis.Client, identity, provider, model string, ttl time.Duration) *Embedder {
	return &Embedder{inner: inner, rdb: rdb, identity: identity, provider: provider, model: model, ttl: ttl}
}

func (e *Embedder) cacheKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	identitySum := sha256.Sum256([]byte(e.identity))
	return fmt.Sprintf("pcb:emb:%s:%s", hex.EncodeToString(identitySum[:8]), hex.EncodeToString(sum[:]))
}

// EmbedOne 命中缓存直接返回;未命中回落底层 client 并写回、设 TTL。
func (e *Embedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	if e.rdb == nil {
		return e.inner.EmbedOne(ctx, text)
	}

	key := e.cacheKey(text)
	raw, err := e.rdb.Get(ctx, key).Bytes()
	switch {
	case err == nil:
		var vec []float32
		if uerr := json.Unmarshal(raw, &vec); uerr == nil {
			log.Printf("[cache] embedding 命中 model=%s len(text)=%d", e.model, len(text))
			evalmetrics.Record("buildsvc", "embedding.cache", map[string]any{
				"provider": e.provider, "role": "embedding", "model": e.model,
				"status": "hit", "text_length": len(text),
			})
			return vec, nil
		}
		// 缓存值损坏:当作未命中,回落重算并覆盖。
	case errors.Is(err, redis.Nil):
		// 未命中,继续。
	default:
		// Redis 读故障不应拖垮主流程,降级为直算(不写回)。
		log.Printf("[cache] embedding 读缓存失败(降级直算): %v", err)
		evalmetrics.Record("buildsvc", "embedding.cache", map[string]any{
			"provider": e.provider, "role": "embedding", "model": e.model,
			"status": "read_error", "text_length": len(text),
		})
		return e.inner.EmbedOne(ctx, text)
	}

	log.Printf("[cache] embedding 未命中 model=%s len(text)=%d", e.model, len(text))
	evalmetrics.Record("buildsvc", "embedding.cache", map[string]any{
		"provider": e.provider, "role": "embedding", "model": e.model,
		"status": "miss", "text_length": len(text),
	})
	vec, err := e.inner.EmbedOne(ctx, text)
	if err != nil {
		return nil, err
	}
	if encoded, merr := json.Marshal(vec); merr == nil {
		if serr := e.rdb.Set(ctx, key, encoded, e.ttl).Err(); serr != nil {
			log.Printf("[cache] embedding 写回失败(不影响结果): %v", serr)
		}
	}
	return vec, nil
}
