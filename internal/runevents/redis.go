package runevents

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewRedis(rdb *redis.Client, ttl time.Duration) *RedisStore {
	return &RedisStore{rdb: rdb, ttl: ttl}
}

func eventKey(runID string) string { return "pcb:run:" + runID + ":events" }

func (s *RedisStore) Append(ctx context.Context, runID, eventType string, payload any) (string, error) {
	data, err := encodeEnvelope(runID, payload)
	if err != nil {
		return "", fmt.Errorf("run events: encode %s: %w", eventType, err)
	}
	key := eventKey(runID)
	id, err := s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"event": eventType, "data": string(data)},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("run events: xadd: %w", err)
	}
	if s.ttl > 0 {
		if err := s.rdb.Expire(ctx, key, s.ttl).Err(); err != nil {
			return "", fmt.Errorf("run events: expire: %w", err)
		}
	}
	return id, nil
}

func (s *RedisStore) History(ctx context.Context, runID, after string) ([]Event, bool, error) {
	key := eventKey(runID)
	exists, err := s.rdb.Exists(ctx, key).Result()
	if err != nil {
		return nil, false, fmt.Errorf("run events: exists: %w", err)
	}
	if exists == 0 {
		return nil, false, nil
	}
	start := "-"
	if after != "" {
		start = "(" + after
	}
	msgs, err := s.rdb.XRange(ctx, key, start, "+").Result()
	if err != nil {
		return nil, true, fmt.Errorf("run events: xrange: %w", err)
	}
	return decodeMessages(msgs), true, nil
}

func (s *RedisStore) Wait(ctx context.Context, runID, after string, block time.Duration) ([]Event, error) {
	if after == "" {
		after = "0-0"
	}
	streams, err := s.rdb.XRead(ctx, &redis.XReadArgs{
		Streams: []string{eventKey(runID), after}, Count: 100, Block: block,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("run events: xread: %w", err)
	}
	if len(streams) == 0 {
		return nil, nil
	}
	return decodeMessages(streams[0].Messages), nil
}

func decodeMessages(messages []redis.XMessage) []Event {
	out := make([]Event, 0, len(messages))
	for _, msg := range messages {
		eventType, okType := msg.Values["event"].(string)
		data, okData := msg.Values["data"].(string)
		if !okType || !okData {
			continue
		}
		out = append(out, Event{ID: msg.ID, Type: eventType, Data: []byte(data)})
	}
	return out
}

func (s *RedisStore) Has(ctx context.Context, runID string) (bool, error) {
	n, err := s.rdb.Exists(ctx, eventKey(runID)).Result()
	return n > 0, err
}

func (s *RedisStore) Degraded() bool { return false }
