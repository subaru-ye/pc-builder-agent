package runevents

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func testStoreContract(t *testing.T, s Store, runID string) {
	t.Helper()
	ctx := context.Background()
	id1, err := s.Append(ctx, runID, "run.started", map[string]any{"kind": "screening"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.Append(ctx, runID, "run.progress", map[string]any{"stage": "screening"})
	if err != nil {
		t.Fatal(err)
	}
	all, exists, err := s.History(ctx, runID, "")
	if err != nil || !exists || len(all) != 2 || all[0].ID != id1 || all[1].ID != id2 {
		t.Fatalf("History all=%+v exists=%v err=%v", all, exists, err)
	}
	var env map[string]any
	if err := json.Unmarshal(all[0].Data, &env); err != nil || env["run_id"] != runID || env["schema_version"] != float64(1) {
		t.Fatalf("event envelope=%v err=%v", env, err)
	}
	after, exists, err := s.History(ctx, runID, id1)
	if err != nil || !exists || len(after) != 1 || after[0].ID != id2 {
		t.Fatalf("History after=%+v exists=%v err=%v", after, exists, err)
	}
}

func TestMemoryStore(t *testing.T) {
	s := NewMemory()
	testStoreContract(t, s, "memory-run")
	ctx := context.Background()
	all, _, _ := s.History(ctx, "memory-run", "")
	done := make(chan []Event, 1)
	go func() {
		events, _ := s.Wait(ctx, "memory-run", all[len(all)-1].ID, time.Second)
		done <- events
	}()
	time.Sleep(10 * time.Millisecond)
	_, _ = s.Append(ctx, "memory-run", "run.completed", map[string]any{"status": "succeeded"})
	select {
	case events := <-done:
		if len(events) != 1 || events[0].Type != "run.completed" {
			t.Fatalf("Wait=%+v", events)
		}
	case <-time.After(time.Second):
		t.Fatal("memory Wait 未被唤醒")
	}
}

func TestRedisStore(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR 未设置,跳过 Redis Stream 集成测试")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis 不可达:%v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	runID := "redis-test-" + time.Now().Format("150405.000000000")
	t.Cleanup(func() { _ = rdb.Del(ctx, eventKey(runID)).Err() })
	s := NewRedis(rdb, time.Hour)
	testStoreContract(t, s, runID)
	ttl, err := rdb.TTL(ctx, eventKey(runID)).Result()
	if err != nil || ttl <= 0 {
		t.Fatalf("stream TTL=%s err=%v", ttl, err)
	}
}
