package redisstore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/sessiontestsuite"
)

// testRedisDB 是一个约定的隔离 DB(测试会 FLUSHDB 它);REDIS_TEST_ADDR 须指向可丢弃实例。
const testRedisDB = 15

// newTestClient 按 REDIS_TEST_ADDR 连接测试实例(未设置则 skip)。
func newTestClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR 未设置,跳过 Redis 集成测试")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: testRedisDB})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("REDIS_TEST_ADDR=%s 不可达,跳过: %v", addr, err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestRedisService_Suite(t *testing.T) {
	shared := newTestClient(t)

	sessiontestsuite.RunServiceTests(t, sessiontestsuite.SuiteOptions{
		SupportsUserProvidedSessionID: true,
		ProvidesServerAssignedEventID: false,
		AppName:                       "testApp",
	}, func(t *testing.T) session.Service {
		// 每个子测试前清空隔离 DB,保证互不干扰(与官方 database 套件的 fresh DB 同效)。
		if err := shared.FlushDB(context.Background()).Err(); err != nil {
			t.Fatalf("FlushDB 失败: %v", err)
		}
		return NewSessionService(shared, time.Hour)
	})
}
