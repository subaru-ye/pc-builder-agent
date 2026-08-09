// Package runevents 持久化产品 run 的 SSE 业务事件。
package runevents

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

const memoryEventLimit = 512

type Event struct {
	ID   string
	Type string
	Data json.RawMessage
}

type Store interface {
	Append(context.Context, string, string, any) (string, error)
	History(context.Context, string, string) ([]Event, bool, error)
	Wait(context.Context, string, string, time.Duration) ([]Event, error)
	Has(context.Context, string) (bool, error)
	Degraded() bool
}

type envelope struct {
	SchemaVersion int       `json:"schema_version"`
	RunID         string    `json:"run_id"`
	Timestamp     time.Time `json:"timestamp"`
	Payload       any       `json:"payload"`
}

func encodeEnvelope(runID string, payload any) (json.RawMessage, error) {
	b, err := json.Marshal(envelope{
		SchemaVersion: 1,
		RunID:         runID,
		Timestamp:     time.Now().UTC(),
		Payload:       payload,
	})
	return b, err
}

func New(rdb *redis.Client, ttl time.Duration) Store {
	if rdb == nil {
		return NewMemory()
	}
	return NewRedis(rdb, ttl)
}
