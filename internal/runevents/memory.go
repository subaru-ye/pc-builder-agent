package runevents

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type memoryRun struct {
	next   uint64
	events []Event
	notify chan struct{}
}

type MemoryStore struct {
	mu   sync.Mutex
	runs map[string]*memoryRun
}

func NewMemory() *MemoryStore { return &MemoryStore{runs: make(map[string]*memoryRun)} }

func (s *MemoryStore) Append(_ context.Context, runID, eventType string, payload any) (string, error) {
	data, err := encodeEnvelope(runID, payload)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[runID]
	if r == nil {
		r = &memoryRun{notify: make(chan struct{})}
		s.runs[runID] = r
	}
	r.next++
	id := fmt.Sprintf("mem-%d", r.next)
	r.events = append(r.events, Event{ID: id, Type: eventType, Data: data})
	if len(r.events) > memoryEventLimit {
		r.events = append([]Event(nil), r.events[len(r.events)-memoryEventLimit:]...)
	}
	close(r.notify)
	r.notify = make(chan struct{})
	return id, nil
}

func (s *MemoryStore) History(_ context.Context, runID, after string) ([]Event, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[runID]
	if r == nil {
		return nil, false, nil
	}
	return eventsAfter(r.events, after), true, nil
}

func (s *MemoryStore) Wait(ctx context.Context, runID, after string, block time.Duration) ([]Event, error) {
	s.mu.Lock()
	r := s.runs[runID]
	if r == nil {
		r = &memoryRun{notify: make(chan struct{})}
		s.runs[runID] = r
	}
	if events := eventsAfter(r.events, after); len(events) > 0 {
		s.mu.Unlock()
		return events, nil
	}
	notify := r.notify
	s.mu.Unlock()

	timer := time.NewTimer(block)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	case <-notify:
		items, _, err := s.History(ctx, runID, after)
		return items, err
	}
}

func eventsAfter(events []Event, after string) []Event {
	start := 0
	if after != "" {
		for i := range events {
			if events[i].ID == after {
				start = i + 1
				break
			}
		}
	}
	out := make([]Event, len(events)-start)
	copy(out, events[start:])
	return out
}

func (s *MemoryStore) Has(_ context.Context, runID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.runs[runID]
	return ok, nil
}

func (s *MemoryStore) Degraded() bool { return true }
