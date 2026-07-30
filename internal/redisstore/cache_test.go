package redisstore

import (
	"context"
	"testing"
	"time"
)

// fakeEmbedder 记录被调用次数,并对每个 text 返回稳定向量。
type fakeEmbedder struct {
	calls int
}

func (f *fakeEmbedder) EmbedOne(_ context.Context, text string) ([]float32, error) {
	f.calls++
	return []float32{float32(len(text)), 1.5, -2.25}, nil
}

// 不带 Redis(rdb=nil)时装饰器应直接透传底层,不缓存。
func TestEmbedder_NilRedisPassthrough(t *testing.T) {
	inner := &fakeEmbedder{}
	e := NewEmbedder(inner, nil, "m", time.Hour)

	ctx := context.Background()
	if _, err := e.EmbedOne(ctx, "hello"); err != nil {
		t.Fatalf("EmbedOne 1: %v", err)
	}
	if _, err := e.EmbedOne(ctx, "hello"); err != nil {
		t.Fatalf("EmbedOne 2: %v", err)
	}
	if inner.calls != 2 {
		t.Errorf("nil Redis 应每次透传,inner.calls = %d, want 2", inner.calls)
	}
}

// 带 Redis 时:首次未命中落底层并写回,二次命中不再调用底层;向量一致。
func TestEmbedder_RedisHitMiss(t *testing.T) {
	rdb := newTestClient(t)
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("FlushDB: %v", err)
	}

	inner := &fakeEmbedder{}
	e := NewEmbedder(inner, rdb, "m", time.Hour)
	ctx := context.Background()

	v1, err := e.EmbedOne(ctx, "黑神话")
	if err != nil {
		t.Fatalf("EmbedOne miss: %v", err)
	}
	v2, err := e.EmbedOne(ctx, "黑神话")
	if err != nil {
		t.Fatalf("EmbedOne hit: %v", err)
	}
	if inner.calls != 1 {
		t.Errorf("二次应命中缓存,inner.calls = %d, want 1", inner.calls)
	}
	if len(v1) != len(v2) {
		t.Fatalf("命中向量长度不一致: %d vs %d", len(v1), len(v2))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("命中向量[%d] = %v, want %v", i, v2[i], v1[i])
		}
	}

	// 不同文本应各自未命中一次。
	if _, err := e.EmbedOne(ctx, "光追"); err != nil {
		t.Fatalf("EmbedOne other: %v", err)
	}
	if inner.calls != 2 {
		t.Errorf("不同文本应再落底层,inner.calls = %d, want 2", inner.calls)
	}
}
