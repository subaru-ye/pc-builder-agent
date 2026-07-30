// Package redisstore 提供 P6 的 Redis 后端:ADK session.Service 的 Redis 实现
// (会话热上下文两进程共享 + TTL),以及 query 向量化的 embedding 缓存装饰器。
package redisstore

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Dial 建立 Redis 连接并做一次 Ping 探活。addr 为空视为未配置,由调用方决定降级。
func Dial(ctx context.Context, addr string) (*redis.Client, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis addr is empty")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping %s: %w", addr, err)
	}
	return rdb, nil
}
