package redisstore

import (
	"iter"
	"maps"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/session"
)

// redisSession 是 session.Session 的具体实现,作为 Create/Get/List 的返回类型。
// runner/executor 会把它原样回传给 AppendEvent,故必须是本包的具体类型。
type redisSession struct {
	appName   string
	userID    string
	sessionID string

	mu        sync.RWMutex
	events    []*session.Event
	state     map[string]any // 已合并 app:/user: 前缀的对外快照
	updatedAt time.Time
}

func (s *redisSession) ID() string      { return s.sessionID }
func (s *redisSession) AppName() string { return s.appName }
func (s *redisSession) UserID() string  { return s.userID }

func (s *redisSession) State() session.State {
	return &redisState{mu: &s.mu, state: s.state}
}

func (s *redisSession) Events() session.Events {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return redisEvents(s.events)
}

func (s *redisSession) LastUpdateTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

type redisEvents []*session.Event

func (e redisEvents) All() iter.Seq[*session.Event] {
	return func(yield func(*session.Event) bool) {
		for _, event := range e {
			if !yield(event) {
				return
			}
		}
	}
}

func (e redisEvents) Len() int { return len(e) }

func (e redisEvents) At(i int) *session.Event {
	if i >= 0 && i < len(e) {
		return e[i]
	}
	return nil
}

type redisState struct {
	mu    *sync.RWMutex
	state map[string]any
}

func (s *redisState) Get(key string) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	val, ok := s.state[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return val, nil
}

func (s *redisState) All() iter.Seq2[string, any] {
	s.mu.RLock()
	stateCopy := maps.Clone(s.state)
	s.mu.RUnlock()

	return func(yield func(key string, val any) bool) {
		for k, v := range stateCopy {
			if !yield(k, v) {
				return
			}
		}
	}
}

func (s *redisState) Set(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state[key] = value
	return nil
}

// trimTempDeltaState 移除 event 里 temp: 前缀的 StateDelta(照 session/database 复制,
// 因 internal/sessionutils 是 internal 包不可 import)。
func trimTempDeltaState(event *session.Event) *session.Event {
	if len(event.Actions.StateDelta) == 0 {
		return event
	}
	filteredStateDelta := make(map[string]any)
	for key, value := range event.Actions.StateDelta {
		if !strings.HasPrefix(key, session.KeyPrefixTemp) {
			filteredStateDelta[key] = value
		}
	}
	if len(filteredStateDelta) == len(event.Actions.StateDelta) {
		return event
	}
	eventCopy := *event
	eventCopy.Actions.StateDelta = filteredStateDelta
	return &eventCopy
}

// extractStateDeltas 按前缀把一份 StateDelta 拆成 app / user / session 三层;temp: 丢弃。
func extractStateDeltas(delta map[string]any) (appStateDelta, userStateDelta, sessionStateDelta map[string]any) {
	appStateDelta = make(map[string]any)
	userStateDelta = make(map[string]any)
	sessionStateDelta = make(map[string]any)
	if delta == nil {
		return appStateDelta, userStateDelta, sessionStateDelta
	}
	for key, value := range delta {
		if cleanKey, found := strings.CutPrefix(key, session.KeyPrefixApp); found {
			appStateDelta[cleanKey] = value
		} else if cleanKey, found := strings.CutPrefix(key, session.KeyPrefixUser); found {
			userStateDelta[cleanKey] = value
		} else if !strings.HasPrefix(key, session.KeyPrefixTemp) {
			sessionStateDelta[key] = value
		}
	}
	return appStateDelta, userStateDelta, sessionStateDelta
}

// mergeStates 把 app / user / session 三层 state 合并成对外单一 map,并加回前缀。
func mergeStates(appState, userState, sessionState map[string]any) map[string]any {
	mergedState := make(map[string]any, len(appState)+len(userState)+len(sessionState))
	maps.Copy(mergedState, sessionState)
	for key, value := range appState {
		mergedState[session.KeyPrefixApp+key] = value
	}
	for key, value := range userState {
		mergedState[session.KeyPrefixUser+key] = value
	}
	return mergedState
}

var (
	_ session.Session = (*redisSession)(nil)
	_ session.Events  = (redisEvents)(nil)
	_ session.State   = (*redisState)(nil)
)
