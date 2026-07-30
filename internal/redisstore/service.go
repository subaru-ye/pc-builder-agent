package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/redis/go-redis/v9"

	"google.golang.org/adk/v2/platform"
	"google.golang.org/adk/v2/session"
)

const (
	// sessKeyPrefix 下三类键靠段数/固定标记区分,普通 appName 不与 "app"/"user" 标记冲突。
	sessKeyPrefix         = "pcb:sess"
	appStatePrefix        = "pcb:sess:app"
	userStatePrefix       = "pcb:sess:user"
	scanCount       int64 = 200
)

// storedSession 是会话在 Redis 里的 JSON 表示(一个会话一个键,便于整体 TTL)。
// session.Event 内嵌 model.LLMResponse / EventActions.StateDelta / *genai.Content,
// 经 session/database 逐字段 marshal 实证可用 encoding/json 无损往返,故整体序列化。
type storedSession struct {
	AppName    string           `json:"appName"`
	UserID     string           `json:"userID"`
	ID         string           `json:"id"`
	State      map[string]any   `json:"state"` // session 级(无前缀)
	Events     []*session.Event `json:"events"`
	CreateTime time.Time        `json:"createTime"`
	UpdateTime time.Time        `json:"updateTime"`
}

// redisService 是 session.Service 的 Redis 实现。Redis 为唯一真值源:每次读改写都落库,
// 供 host / buildsvc 两进程共享;键按 appName 天然隔离。
type redisService struct {
	rdb *redis.Client
	ttl time.Duration // 会话/状态键 TTL,<=0 表示不过期
}

// NewSessionService 返回一个以 Redis 为后端的 session.Service。
func NewSessionService(rdb *redis.Client, ttl time.Duration) session.Service {
	return &redisService{rdb: rdb, ttl: ttl}
}

func sessKey(app, user, sid string) string {
	return fmt.Sprintf("%s:%s:%s:%s", sessKeyPrefix, app, user, sid)
}

func appStateKey(app string) string {
	return fmt.Sprintf("%s:%s", appStatePrefix, app)
}

func userStateKey(app, user string) string {
	return fmt.Sprintf("%s:%s:%s", userStatePrefix, app, user)
}

// setJSON 把 v 以 JSON 写入 key,并按 ttl 设置(滑动)过期。
func (s *redisService) setJSON(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	if err := s.rdb.Set(ctx, key, raw, s.ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}
	return nil
}

// getStateMap 读一个 state map 键;不存在返回空 map(非错误)。
func (s *redisService) getStateMap(ctx context.Context, key string) (map[string]any, error) {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return make(map[string]any), nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get %s: %w", key, err)
	}
	m := make(map[string]any)
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", key, err)
	}
	return m, nil
}

// loadSession 读一个会话键;不存在返回 (nil, nil) 由调用方决定报错。
func (s *redisService) loadSession(ctx context.Context, app, user, sid string) (*storedSession, error) {
	raw, err := s.rdb.Get(ctx, sessKey(app, user, sid)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get session: %w", err)
	}
	var st storedSession
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	if st.State == nil {
		st.State = make(map[string]any)
	}
	return &st, nil
}

func (s *redisService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req.AppName == "" || req.UserID == "" {
		return nil, fmt.Errorf("app_name and user_id are required, got app_name: %q, user_id: %q", req.AppName, req.UserID)
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = platform.NewUUID(ctx)
	}

	existing, err := s.loadSession(ctx, req.AppName, req.UserID, sessionID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("session %s already exists", sessionID)
	}

	appDelta, userDelta, sessionState := extractStateDeltas(req.State)

	appState, err := s.getStateMap(ctx, appStateKey(req.AppName))
	if err != nil {
		return nil, err
	}
	userState, err := s.getStateMap(ctx, userStateKey(req.AppName, req.UserID))
	if err != nil {
		return nil, err
	}

	if len(appDelta) > 0 {
		maps.Copy(appState, appDelta)
		if err := s.setJSON(ctx, appStateKey(req.AppName), appState); err != nil {
			return nil, err
		}
	}
	if len(userDelta) > 0 {
		maps.Copy(userState, userDelta)
		if err := s.setJSON(ctx, userStateKey(req.AppName, req.UserID), userState); err != nil {
			return nil, err
		}
	}

	now := platform.Now(ctx)
	st := &storedSession{
		AppName:    req.AppName,
		UserID:     req.UserID,
		ID:         sessionID,
		State:      sessionState,
		Events:     nil,
		CreateTime: now,
		UpdateTime: now,
	}
	if err := s.setJSON(ctx, sessKey(req.AppName, req.UserID, sessionID), st); err != nil {
		return nil, err
	}

	return &session.CreateResponse{
		Session: &redisSession{
			appName:   req.AppName,
			userID:    req.UserID,
			sessionID: sessionID,
			state:     mergeStates(appState, userState, sessionState),
			updatedAt: now,
		},
	}, nil
}

func (s *redisService) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	appName, userID, sessionID := req.AppName, req.UserID, req.SessionID
	if appName == "" || userID == "" || sessionID == "" {
		return nil, fmt.Errorf("app_name, user_id, session_id are required, got app_name: %q, user_id: %q, session_id: %q", appName, userID, sessionID)
	}

	st, err := s.loadSession(ctx, appName, userID, sessionID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		// Get miss 必须返回 error:runner/executor 靠 error 判断"不存在再 Create"。
		return nil, fmt.Errorf("session %q not found", sessionID)
	}

	appState, err := s.getStateMap(ctx, appStateKey(appName))
	if err != nil {
		return nil, err
	}
	userState, err := s.getStateMap(ctx, userStateKey(appName, userID))
	if err != nil {
		return nil, err
	}

	// 刷新 TTL(滑动过期),活跃会话读取即续命。
	if s.ttl > 0 {
		s.rdb.Expire(ctx, sessKey(appName, userID, sessionID), s.ttl)
	}

	return &session.GetResponse{
		Session: &redisSession{
			appName:   appName,
			userID:    userID,
			sessionID: sessionID,
			state:     mergeStates(appState, userState, st.State),
			events:    filterEvents(st.Events, req.NumRecentEvents, req.After),
			updatedAt: st.UpdateTime,
		},
	}, nil
}

// filterEvents 先按 After(Timestamp >= After)过滤,再取最近 NumRecentEvents 条。
func filterEvents(all []*session.Event, numRecent int, after time.Time) []*session.Event {
	filtered := all
	if !after.IsZero() {
		kept := make([]*session.Event, 0, len(filtered))
		for _, e := range filtered {
			if !e.Timestamp.Before(after) {
				kept = append(kept, e)
			}
		}
		filtered = kept
	}
	if numRecent > 0 && len(filtered) > numRecent {
		filtered = filtered[len(filtered)-numRecent:]
	}
	out := make([]*session.Event, len(filtered))
	copy(out, filtered)
	return out
}

func (s *redisService) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	appName, userID := req.AppName, req.UserID
	if appName == "" {
		return nil, fmt.Errorf("app_name is required, got app_name: %q", appName)
	}

	var match string
	if userID != "" {
		match = fmt.Sprintf("%s:%s:%s:*", sessKeyPrefix, appName, userID)
	} else {
		match = fmt.Sprintf("%s:%s:*", sessKeyPrefix, appName)
	}

	appState, err := s.getStateMap(ctx, appStateKey(appName))
	if err != nil {
		return nil, err
	}
	userStateCache := make(map[string]map[string]any)

	sessions := make([]session.Session, 0)
	var cursor uint64
	for {
		keys, next, err := s.rdb.Scan(ctx, cursor, match, scanCount).Result()
		if err != nil {
			return nil, fmt.Errorf("redis scan: %w", err)
		}
		for _, key := range keys {
			raw, err := s.rdb.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				continue // 已过期
			}
			if err != nil {
				return nil, fmt.Errorf("redis get %s: %w", key, err)
			}
			var st storedSession
			if err := json.Unmarshal(raw, &st); err != nil {
				return nil, fmt.Errorf("unmarshal %s: %w", key, err)
			}
			us, ok := userStateCache[st.UserID]
			if !ok {
				us, err = s.getStateMap(ctx, userStateKey(appName, st.UserID))
				if err != nil {
					return nil, err
				}
				userStateCache[st.UserID] = us
			}
			sessions = append(sessions, &redisSession{
				appName:   st.AppName,
				userID:    st.UserID,
				sessionID: st.ID,
				state:     mergeStates(appState, us, st.State),
				events:    st.Events,
				updatedAt: st.UpdateTime,
			})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	return &session.ListResponse{Sessions: sessions}, nil
}

func (s *redisService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	appName, userID, sessionID := req.AppName, req.UserID, req.SessionID
	if appName == "" || userID == "" || sessionID == "" {
		return fmt.Errorf("app_name, user_id, session_id are required, got app_name: %q, user_id: %q, session_id: %q", appName, userID, sessionID)
	}
	if err := s.rdb.Del(ctx, sessKey(appName, userID, sessionID)).Err(); err != nil {
		return fmt.Errorf("redis del session: %w", err)
	}
	return nil
}

func (s *redisService) AppendEvent(ctx context.Context, curSession session.Session, event *session.Event) error {
	if curSession == nil {
		return fmt.Errorf("session is nil")
	}
	if event == nil {
		return fmt.Errorf("event is nil")
	}
	if event.Partial {
		return nil
	}

	appName, userID, sessionID := curSession.AppName(), curSession.UserID(), curSession.ID()
	st, err := s.loadSession(ctx, appName, userID, sessionID)
	if err != nil {
		return err
	}
	if st == nil {
		return fmt.Errorf("session not found, cannot apply event")
	}

	// 持久化前 trim 掉 temp: 前缀的 StateDelta。
	event = trimTempDeltaState(event)
	appDelta, userDelta, sessionDelta := extractStateDeltas(event.Actions.StateDelta)

	if len(appDelta) > 0 {
		appState, err := s.getStateMap(ctx, appStateKey(appName))
		if err != nil {
			return err
		}
		maps.Copy(appState, appDelta)
		if err := s.setJSON(ctx, appStateKey(appName), appState); err != nil {
			return err
		}
	}
	if len(userDelta) > 0 {
		userState, err := s.getStateMap(ctx, userStateKey(appName, userID))
		if err != nil {
			return err
		}
		maps.Copy(userState, userDelta)
		if err := s.setJSON(ctx, userStateKey(appName, userID), userState); err != nil {
			return err
		}
	}
	if len(sessionDelta) > 0 {
		maps.Copy(st.State, sessionDelta)
	}

	st.Events = append(st.Events, event)
	st.UpdateTime = platform.Now(ctx)
	if err := s.setJSON(ctx, sessKey(appName, userID, sessionID), st); err != nil {
		return err
	}

	// 让调用方持有的会话对象也看到最新事件与更新时间(与官方实现一致)。
	if rs, ok := curSession.(*redisSession); ok {
		rs.mu.Lock()
		rs.events = append(rs.events, event)
		rs.updatedAt = st.UpdateTime
		if len(sessionDelta) > 0 {
			if rs.state == nil {
				rs.state = make(map[string]any)
			}
			maps.Copy(rs.state, sessionDelta)
		}
		rs.mu.Unlock()
	}
	return nil
}

var _ session.Service = (*redisService)(nil)
