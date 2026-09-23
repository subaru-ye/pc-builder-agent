package product

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

type fakeProductStore struct {
	mu           sync.Mutex
	session      store.WebSession
	runs         map[string]store.AgentRun
	messages     []store.WebMessage
	latest       int
	fingerprints map[string]string
}

func newFakeProductStore() *fakeProductStore {
	return &fakeProductStore{
		session:      store.WebSession{ID: "session-1", OwnerID: "owner-1", Phase: store.PhaseCollecting},
		runs:         make(map[string]store.AgentRun),
		fingerprints: make(map[string]string),
	}
}

func (f *fakeProductStore) CreateWebSession(context.Context, string, string, string) (store.WebSession, error) {
	return f.session, nil
}
func (f *fakeProductStore) WebSessionsByOwner(context.Context, string) ([]store.WebSession, error) {
	return []store.WebSession{f.session}, nil
}
func (f *fakeProductStore) WebSessionByOwner(_ context.Context, owner, session string) (store.WebSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if owner != f.session.OwnerID || session != f.session.ID {
		return store.WebSession{}, store.ErrWebSessionNotFound
	}
	return f.session, nil
}
func (f *fakeProductStore) WebMessages(context.Context, string) ([]store.WebMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.WebMessage(nil), f.messages...), nil
}
func (f *fakeProductStore) ScreeningMessages(_ context.Context, _ string, kind store.RunKind) ([]store.WebMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.WebMessage, 0)
	for _, message := range f.messages {
		if message.RunID != nil && f.runs[*message.RunID].Kind == kind {
			out = append(out, message)
		}
	}
	return out, nil
}
func (f *fakeProductStore) ActiveRun(context.Context, string) (*store.AgentRun, error) {
	return nil, nil
}
func (f *fakeProductStore) RunByOwner(_ context.Context, _ string, id string) (store.AgentRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[id], nil
}
func (f *fakeProductStore) MessageRunByRequest(_ context.Context, owner, session, request, text string, fingerprints ...string) (store.AgentRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if owner != f.session.OwnerID || session != f.session.ID {
		return store.AgentRun{}, false, nil
	}
	for _, run := range f.runs {
		if run.ClientRequestID != request {
			continue
		}
		fingerprint := ""
		if len(fingerprints) > 0 {
			fingerprint = fingerprints[0]
		}
		if f.fingerprints[run.ID] != fingerprint {
			return store.AgentRun{}, false, store.ErrIdempotencyConflict
		}
		for _, message := range f.messages {
			if message.RunID != nil && *message.RunID == run.ID && message.Role == "user" {
				if fingerprint == "" && message.Content != text {
					return store.AgentRun{}, false, store.ErrIdempotencyConflict
				}
				return run, true, nil
			}
		}
	}
	return store.AgentRun{}, false, nil
}
func (f *fakeProductStore) ReplacePendingRequirement(_ context.Context, _, _ string, spec json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.session.PendingRequirement = spec
	f.session.Phase = store.PhaseRequirementReady
	return nil
}
func (f *fakeProductStore) StartMessageRun(_ context.Context, p store.StartMessageRunParams) (store.AgentRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kind := store.RunScreening
	if f.session.Phase == store.PhaseReady {
		kind = store.RunChange
		f.session.Phase = store.PhaseChanging
	}
	r := store.AgentRun{ID: p.RunID, SessionID: p.SessionID, ClientRequestID: p.RequestID,
		Kind: kind, Status: store.RunRunning, StartedAt: time.Now()}
	f.runs[r.ID] = r
	f.fingerprints[r.ID] = p.RequestFingerprint
	f.messages = append(f.messages, store.WebMessage{
		ID: p.MessageID, SessionID: p.SessionID, Role: "user", Content: p.Text, RunID: &r.ID, CreatedAt: time.Now(),
	})
	return r, false, nil
}
func (f *fakeProductStore) StartConfirmRun(_ context.Context, p store.StartConfirmRunParams) (store.AgentRun, json.RawMessage, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := store.AgentRun{ID: p.RunID, SessionID: p.SessionID, ClientRequestID: p.RequestID,
		Kind: store.RunBuild, Status: store.RunRunning, StartedAt: time.Now()}
	f.runs[r.ID] = r
	f.session.Phase = store.PhaseBuilding
	return r, f.session.PendingRequirement, false, nil
}
func (f *fakeProductStore) CompleteRun(_ context.Context, p store.CompleteRunParams) (*store.WebMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[p.RunID]
	r.Status = p.Status
	f.runs[p.RunID] = r
	f.session.Phase = p.Phase
	f.session.RecoveryPhase = p.RecoveryPhase
	f.session.LastError = p.Error
	if p.SetPending {
		f.session.PendingRequirement = p.PendingRequirement
	}
	if p.AssistantContent == "" {
		return nil, nil
	}
	m := store.WebMessage{ID: p.AssistantMessageID, SessionID: p.SessionID, Role: "assistant",
		Content: p.AssistantContent, DisplayContent: p.DisplayContent, RunID: &p.RunID, CreatedAt: time.Now()}
	f.messages = append(f.messages, m)
	return &m, nil
}
func (f *fakeProductStore) LatestBuildVersion(context.Context, string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latest, f.latest > 0, nil
}
func (f *fakeProductStore) InterruptRunning(context.Context, json.RawMessage) ([]store.InterruptedRun, error) {
	return nil, nil
}
func (f *fakeProductStore) RequestRunCancel(_ context.Context, _, _, runID string) (store.AgentRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[runID]
	if !ok {
		return store.AgentRun{}, false, store.ErrRunNotFound
	}
	if r.Status != store.RunRunning {
		return r, false, nil
	}
	if r.CancelRequestedAt == nil {
		now := time.Now()
		r.CancelRequestedAt = &now
		f.runs[runID] = r
	}
	return r, true, nil
}
func (f *fakeProductStore) ReclaimStaleRuns(context.Context, string) ([]store.InterruptedRun, error) {
	return nil, nil
}

// planningFakeStore 在基础假 store 上补齐增量协议能力（proposalStore +
// ContinueScreeningRun），模拟生产 Store 的能力面；旧协议测试继续用基础版。
type planningFakeStore struct{ *fakeProductStore }

func (f *planningFakeStore) LatestProposal(context.Context, string) (json.RawMessage, error) {
	return nil, nil
}
func (f *planningFakeStore) BuildByVersion(_ context.Context, _ string, version int) (store.BuildVersion, error) {
	return store.BuildVersion{Version: version}, nil
}
func (f *planningFakeStore) CompletePlanningRun(ctx context.Context, p store.CompletePlanningParams) (store.PlanningCompletion, error) {
	if _, err := f.CompleteRun(ctx, p.Completion); err != nil {
		return store.PlanningCompletion{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latest++
	return store.PlanningCompletion{Result: p.Result, Version: f.latest}, nil
}

type fakeAgent struct {
	store            *fakeProductStore
	screen           ScreenResult
	contextAvailable bool
	remotePayload    json.RawMessage
	screenCalls      int
	screenInput      ScreenInput
	screenErr        error
}

func (f *fakeAgent) Screen(_ context.Context, _, _ string, input ScreenInput) (ScreenResult, error) {
	f.screenCalls++
	f.screenInput = input
	return f.screen, f.screenErr
}
func (f *fakeAgent) Remote(_ context.Context, _, _ string, payload json.RawMessage) (RemoteResult, error) {
	f.remotePayload = append(json.RawMessage(nil), payload...)
	f.store.mu.Lock()
	f.store.latest++
	f.store.mu.Unlock()
	return RemoteResult{Text: "配置已生成并通过校验。"}, nil
}
func (f *fakeAgent) ContextAvailable(context.Context, string, string) (bool, error) {
	return f.contextAvailable, nil
}

type recordedEvent struct{ name string }
type fakeSink struct {
	mu        sync.Mutex
	events    []recordedEvent
	completed chan struct{}
}

func newFakeSink() *fakeSink { return &fakeSink{completed: make(chan struct{}, 8)} }
func (f *fakeSink) Append(_ context.Context, _ string, name string, _ any) (string, error) {
	f.mu.Lock()
	f.events = append(f.events, recordedEvent{name: name})
	f.mu.Unlock()
	if name == "run.completed" {
		f.completed <- struct{}{}
	}
	return "1-0", nil
}
func (f *fakeSink) Has(context.Context, string) (bool, error) { return true, nil }
func (f *fakeSink) Degraded() bool                            { return false }
func (f *fakeSink) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.completed:
	case <-time.After(2 * time.Second):
		t.Fatal("等待 run.completed 超时")
	}
}
func (f *fakeSink) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.events))
	for i := range f.events {
		out[i] = f.events[i].name
	}
	return out
}

func TestServiceRequirementConfirmBuild(t *testing.T) {
	st := newFakeProductStore()
	spec := json.RawMessage(`{"schema_version": 2, "configuration_scope": ["tower"],"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"}}`)
	agent := &fakeAgent{store: st, screen: ScreenResult{Kind: ScreenRequirement, Payload: spec}, contextAvailable: true}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000001", "8000 元 2K 玩黑神话")
	if err != nil || started.Run.Kind != store.RunScreening {
		t.Fatalf("StartMessage=%+v err=%v", started, err)
	}
	sink.wait(t)
	ws, _ := st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseRequirementReady || len(ws.PendingRequirement) == 0 {
		t.Fatalf("screening 后状态不正确:%+v", ws)
	}
	edited := json.RawMessage(`{"schema_version": 2, "configuration_scope": ["tower"],"budget_cny":8500,"noise_pref":"silent","use_case":{"type":"gaming","resolution":"2K"}}`)
	if err := svc.ReplaceRequirement(context.Background(), "owner-1", "session-1", edited); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartConfirm(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000002"); err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	ws, _ = st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseReady || st.latest != 1 {
		t.Fatalf("confirm 后状态不正确:session=%+v latest=%d", ws, st.latest)
	}
	confirmed, err := schemas.DecodeRequirementSpec(agent.remotePayload)
	if err != nil || confirmed.BudgetCNY != 8500 || confirmed.NoisePref != schemas.NoisePrefSilent {
		t.Fatalf("Remote 应收到编辑后的 pending requirement,得到 %s err=%v", agent.remotePayload, err)
	}
	wantOrder := []string{"run.started", "run.progress", "assistant.completed", "requirement.ready", "run.completed",
		"run.started", "run.progress", "run.progress", "assistant.completed", "build.saved", "run.completed"}
	got := sink.names()
	if len(got) != len(wantOrder) {
		t.Fatalf("事件数量=%d want=%d:%v", len(got), len(wantOrder), got)
	}
	for i := range wantOrder {
		if got[i] != wantOrder[i] {
			t.Fatalf("事件[%d]=%s want=%s,all=%v", i, got[i], wantOrder[i], got)
		}
	}
}

// 零模型回归:首句模糊需求(缺分辨率/已有件)不得因模型 next_action=confirm
// 提前 ready;必须被确定性 readiness 拦在 collecting。
func TestFirstVagueMessageIsBlockedByReadiness(t *testing.T) {
	st := &planningFakeStore{newFakeProductStore()}
	update := &schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_cny", Value: json.RawMessage("8000"), Kind: "constraint", Strength: "must", Scope: "session", Evidence: "stated", Quote: "预算8000"},
			{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "游戏"},
		},
	}
	agent := &fakeAgent{store: st.fakeProductStore, screen: ScreenResult{RequirementUpdate: update, Reply: "需求已经整理好，请确认。"}, contextAvailable: true}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000009", "预算8000，配一台游戏主机"); err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	ws, _ := st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseCollecting || len(ws.PendingRequirement) != 0 {
		t.Fatalf("不完整首句应被 readiness 拦截: %+v", ws)
	}
	messages, _ := st.WebMessages(context.Background(), "session-1")
	last := messages[len(messages)-1]
	if last.Role != "assistant" || !strings.Contains(last.Content, "分辨率") || !strings.Contains(last.Content, "已有配件") {
		t.Fatalf("追问应覆盖缺失的分辨率与已有件: %s", last.Content)
	}
}

// v1 一次性切换:旧格式需求状态在读取边界被明确拒绝,不回退为空 v2 状态。
func TestLegacyV1RequirementStateIsRejected(t *testing.T) {
	st := newFakeProductStore()
	v1State := json.RawMessage(`{"schema_version":1,"revision":2,"reply":"旧协议","next_action":"confirm","fields":{},"alternatives":[],"changes":[],"history":[]}`)
	st.session.RequirementState = v1State
	agent := &fakeAgent{store: st, contextAvailable: true}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetSession(context.Background(), "owner-1", "session-1"); err == nil ||
		!strings.Contains(err.Error(), "不支持的 schema_version") && !strings.Contains(err.Error(), "旧版需求格式") {
		// GetSession 统一转为 problem;底层必须是稳定拒绝而不是空状态。
		problem, ok := err.(Problem)
		if !ok || problem.Code != "requirement_state_unsupported" {
			t.Fatalf("v1 状态应以稳定问题拒绝: %v", err)
		}
	}
	if _, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000010", "再改改预算"); err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	ws, _ := st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseError || !bytes.Equal(ws.RequirementState, v1State) {
		t.Fatalf("v1 会话应失败并保持原状态不变,不得静默重建: %+v", ws)
	}
}

// 仅 must 冲突(无缺失)时必须保持 collecting:不发布待确认草稿、不发
// requirement.ready、不启动 Builder,提示指向冲突本身。
func TestMustConflictOnlyKeepsCollecting(t *testing.T) {
	st := &planningFakeStore{newFakeProductStore()}
	update := &schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage("8000"), Kind: "constraint", Strength: "must", Scope: "session", Evidence: "stated", Quote: "预算8000"},
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "游戏"},
		{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "1080p"},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage("[]"), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "全部新买"},
		{Op: "conflict", Field: "brand_pref.gpu", Strength: "must", Evidence: "uncertain", Quote: "只要N卡，但绝不要N卡"},
	}}
	agent := &fakeAgent{store: st.fakeProductStore, screen: ScreenResult{RequirementUpdate: update}, contextAvailable: true}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000021", "预算8000，配1080p游戏主机全部新买，只要N卡，但绝不要N卡"); err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	if agent.remotePayload != nil || st.latest != 0 {
		t.Fatalf("must 冲突不得启动 Builder: payload=%s latest=%d", agent.remotePayload, st.latest)
	}
	ws, _ := st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseCollecting || len(ws.PendingRequirement) != 0 {
		t.Fatalf("must 冲突应保持 collecting 且无待确认草稿: %+v", ws)
	}
	for _, name := range sink.names() {
		if name == "requirement.ready" {
			t.Fatalf("must 冲突不得发布 requirement.ready: %v", sink.names())
		}
	}
	messages, _ := st.WebMessages(context.Background(), "session-1")
	if last := messages[len(messages)-1]; last.Role != "assistant" || !strings.Contains(last.Content, "显卡品牌") {
		t.Fatalf("追问应指向冲突字段: %s", last.Content)
	}
}

// 仅 unsupported 阻塞(其余齐备)时同样保持 collecting,并提示当前 tower 边界。
func TestUnsupportedOnlyKeepsCollecting(t *testing.T) {
	st := &planningFakeStore{newFakeProductStore()}
	update := &schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_cny", Value: json.RawMessage("8000"), Kind: "constraint", Strength: "must", Scope: "session", Evidence: "stated", Quote: "预算8000"},
			{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "游戏"},
			{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "1080p"},
			{Op: "set", Field: "existing_parts", Value: json.RawMessage("[]"), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "全部新买"},
		},
		Observations: []schemas.RequirementObservationInput{{Quote: "还要配显示器", Reason: "unsupported_capability:monitor"}},
	}
	agent := &fakeAgent{store: st.fakeProductStore, screen: ScreenResult{RequirementUpdate: update}, contextAvailable: true}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000022", "预算8000，配1080p游戏主机全部新买，还要配显示器"); err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	if agent.remotePayload != nil || st.latest != 0 {
		t.Fatalf("unsupported 阻塞不得启动 Builder: payload=%s latest=%d", agent.remotePayload, st.latest)
	}
	ws, _ := st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseCollecting || len(ws.PendingRequirement) != 0 {
		t.Fatalf("unsupported 阻塞应保持 collecting 且无待确认草稿: %+v", ws)
	}
	messages, _ := st.WebMessages(context.Background(), "session-1")
	if last := messages[len(messages)-1]; last.Role != "assistant" || !strings.Contains(last.Content, "主机") || !strings.Contains(last.Content, "显示器") {
		t.Fatalf("追问应说明 tower 边界与显示器阻塞: %s", last.Content)
	}
}

func TestFirstExecutionMessageDoesNotStartBuilder(t *testing.T) {
	st := &planningFakeStore{newFakeProductStore()}
	update := &schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_cny", Value: json.RawMessage("8000"), Kind: "constraint", Strength: "must", Scope: "session", Evidence: "stated", Quote: "预算8000"},
			{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "游戏"},
			{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "1080p"},
			{Op: "set", Field: "existing_parts", Value: json.RawMessage("[]"), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "全部新买"},
		},
	}
	agent := &fakeAgent{store: st.fakeProductStore, screen: ScreenResult{RequirementUpdate: update, Reply: "开始为本轮选配。"}, contextAvailable: true}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000003", "预算8000，直接开始配一台1080p游戏主机，全部新买")
	if err != nil || started.Run.Kind != store.RunScreening {
		t.Fatalf("StartMessage=%+v err=%v", started, err)
	}
	sink.wait(t)
	// v2:模型 next_action=plan 不再有权威,聊天文字不能替代确认 API 启动 Builder。
	if agent.remotePayload != nil {
		t.Fatalf("聊天不得启动 Builder: %s", agent.remotePayload)
	}
	if st.latest != 0 {
		t.Fatalf("builder 不应运行: latest=%d", st.latest)
	}
	ws, _ := st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseRequirementReady || len(ws.PendingRequirement) == 0 {
		t.Fatalf("readiness 完整时应停在 requirement_ready 待确认: %+v", ws)
	}
	pending, err := schemas.DecodeRequirementSpec(ws.PendingRequirement)
	if err != nil || pending.SchemaVersion != schemas.RequirementSpecSchemaVersion || len(pending.ConfigurationScope) != 1 || pending.ConfigurationScope[0] != schemas.ConfigurationScopeTower {
		t.Fatalf("待确认草稿应是 RequirementSpec v2: %s err=%v", ws.PendingRequirement, err)
	}
	messages, _ := st.WebMessages(context.Background(), "session-1")
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || messages[len(messages)-1].Content != "开始为本轮选配。" {
		t.Fatalf("legacy reply 应作为助手文案展示: %+v", messages)
	}
}

func TestServiceCanContinueWithoutRemoteContext(t *testing.T) {
	st := newFakeProductStore()
	st.session.Phase = store.PhaseReady
	agent := &fakeAgent{store: st, contextAvailable: false}
	svc, _ := NewService(context.Background(), st, agent, newFakeSink())
	_, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000003", "换成 A 卡")
	if err != nil {
		t.Fatalf("已持久化的会话不应被远端上下文过期阻断: %v", err)
	}
}

func TestServiceDeterministicBudgetChangeBypassesScreening(t *testing.T) {
	st := newFakeProductStore()
	st.session.Phase = store.PhaseReady
	st.latest = 1
	agent := &fakeAgent{store: st, contextAvailable: true}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000006", "降 500")
	if err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	change, err := schemas.DecodeChangeRequest(agent.remotePayload)
	if err != nil {
		t.Fatalf("Remote payload 不是合法 ChangeRequest:%s err=%v", agent.remotePayload, err)
	}
	if agent.screenCalls != 0 || change.BaseBuildRef != "v1" || change.BudgetDeltaCNY == nil || *change.BudgetDeltaCNY != -500 {
		t.Fatalf("确定性改单不正确:screen_calls=%d change=%+v", agent.screenCalls, change)
	}
	ws, _ := st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseReady || st.latest != 2 {
		t.Fatalf("改单后状态不正确:session=%+v latest=%d", ws, st.latest)
	}
}

func TestServiceDeterministicGPUBrandChangeBypassesScreening(t *testing.T) {
	for _, tc := range []struct {
		text, target string
	}{
		{text: "换成 A 卡", target: "AMD 显卡"},
		{text: "把显卡改为 NVIDIA", target: "NVIDIA 显卡"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			st := newFakeProductStore()
			st.session.Phase = store.PhaseReady
			st.latest = 1
			agent := &fakeAgent{store: st, contextAvailable: true}
			sink := newFakeSink()
			svc, err := NewService(context.Background(), st, agent, sink)
			if err != nil {
				t.Fatal(err)
			}
			_, err = svc.StartMessage(context.Background(), "owner-1", "session-1",
				"00000000-0000-4000-8000-000000000016", tc.text)
			if err != nil {
				t.Fatal(err)
			}
			sink.wait(t)
			change, err := schemas.DecodeChangeRequest(agent.remotePayload)
			if err != nil {
				t.Fatalf("Remote payload 不是合法 ChangeRequest:%s err=%v", agent.remotePayload, err)
			}
			if agent.screenCalls != 0 || change.BaseBuildRef != "v1" || change.Swap == nil ||
				change.Swap.Category != schemas.CategoryGPU || change.Swap.TargetHint != tc.target {
				t.Fatalf("确定性显卡改单不正确:screen_calls=%d change=%+v", agent.screenCalls, change)
			}
			if got := change.HardLocked(); len(got) != len(schemas.AllCategories)-1 {
				t.Fatalf("锁定品类=%v", got)
			}
		})
	}
}

func TestParseDeterministicBudgetDelta(t *testing.T) {
	tests := []struct {
		text  string
		want  int
		match bool
	}{
		{text: "降 500", want: -500, match: true},
		{text: "预算减少500元", want: -500, match: true},
		{text: "再加 300", want: 300, match: true},
		{text: "预算提高1000元", want: 1000, match: true},
		{text: "降 500，显卡别动", match: false},
		{text: "预算改成 7500", match: false},
	}
	for _, tt := range tests {
		got, matched := parseDeterministicBudgetDelta(tt.text)
		if got != tt.want || matched != tt.match {
			t.Errorf("parseDeterministicBudgetDelta(%q)=(%d,%v),want (%d,%v)", tt.text, got, matched, tt.want, tt.match)
		}
	}
}

func TestParseDeterministicGPUBrandSwap(t *testing.T) {
	for _, tc := range []struct {
		text, want string
		match      bool
	}{
		{text: "换成 A 卡", want: "AMD 显卡", match: true},
		{text: "显卡换为AMD", want: "AMD 显卡", match: true},
		{text: "把显卡改为 N 卡", want: "NVIDIA 显卡", match: true},
		{text: "换成NVIDIA显卡", want: "NVIDIA 显卡", match: true},
		{text: "不要换成 A 卡", match: false},
		{text: "换成 A 卡还是 N 卡", match: false},
		{text: "换成 RX 9070", match: false},
	} {
		got, matched := parseDeterministicGPUBrandSwap(tc.text)
		if got != tc.want || matched != tc.match {
			t.Errorf("parseDeterministicGPUBrandSwap(%q)=(%q,%v),want (%q,%v)",
				tc.text, got, matched, tc.want, tc.match)
		}
	}
}

func TestScreenInputUsesOnlyRecentSameKindStableMessages(t *testing.T) {
	st := newFakeProductStore()
	for i := 0; i < 8; i++ {
		runID := "screen-" + strconv.Itoa(i)
		st.runs[runID] = store.AgentRun{ID: runID, Kind: store.RunScreening}
		st.messages = append(st.messages, store.WebMessage{Role: "user", Content: "screening-" + strconv.Itoa(i), RunID: &runID})
	}
	buildRunID := "build-output"
	st.runs[buildRunID] = store.AgentRun{ID: buildRunID, Kind: store.RunBuild}
	st.messages = append(st.messages, store.WebMessage{Role: "assistant", Content: "不应进入初筛的完整配置", RunID: &buildRunID})
	svc, _ := NewService(context.Background(), st, &fakeAgent{}, newFakeSink())
	input, err := svc.screenInput(context.Background(), store.AgentRun{SessionID: st.session.ID, Kind: store.RunScreening}, "当前消息")
	if err != nil {
		t.Fatal(err)
	}
	if input.HasBuild {
		t.Fatal("screening invented a build")
	}
	changeInput, err := svc.screenInput(context.Background(), store.AgentRun{SessionID: st.session.ID, Kind: store.RunChange}, "改预算")
	if err != nil || !changeInput.HasBuild {
		t.Fatal("change run lost authoritative build state")
	}
	if strings.Contains(input.Context, "完整配置") || strings.Contains(input.Context, "screening-0") || strings.Contains(input.Context, "screening-1") {
		t.Fatalf("上下文未正确过滤/截断:%s", input.Context)
	}
	if strings.Count(input.Context, "用户：screening-") != maxScreenMessages-1 || !strings.HasSuffix(input.Context, "用户：当前消息") {
		t.Fatalf("上下文消息数不正确:%s", input.Context)
	}
	if input.Text != "当前消息" || utf8.RuneCountInString(input.Context) > maxScreenRunes {
		t.Fatalf("input=%+v", input)
	}
	if len(input.UserSources) != maxScreenMessages || input.UserSources[len(input.UserSources)-1] != "当前消息" {
		t.Fatalf("核验来源与用户上下文不一致: %v", input.UserSources)
	}
}

func TestScreenInputGroundingExcludesAssistantExamples(t *testing.T) {
	st := newFakeProductStore()
	runID := "prior-screen"
	st.runs[runID] = store.AgentRun{ID: runID, Kind: store.RunScreening}
	st.messages = append(st.messages,
		store.WebMessage{Role: "user", Content: "已有CPU，办公。", RunID: &runID},
		store.WebMessage{Role: "assistant", Content: "例如 AMD Ryzen 5 7600；新增预算是多少？", RunID: &runID})
	svc, _ := NewService(context.Background(), st, &fakeAgent{}, newFakeSink())
	input, err := svc.screenInput(context.Background(), store.AgentRun{SessionID: st.session.ID, Kind: store.RunScreening}, "预算6000元")
	if err != nil {
		t.Fatal(err)
	}
	if len(input.UserSources) != 2 || strings.Contains(strings.Join(input.UserSources, ""), "7600") {
		t.Fatalf("助手示例混入来源: %v", input.UserSources)
	}
}

func TestScreenInputGroundingDoesNotRestoreTruncatedText(t *testing.T) {
	st := newFakeProductStore()
	svc, _ := NewService(context.Background(), st, &fakeAgent{}, newFakeSink())
	input, err := svc.screenInput(context.Background(), store.AgentRun{SessionID: st.session.ID, Kind: store.RunScreening}, strings.Repeat("长", maxScreenRunes+1))
	if err != nil {
		t.Fatal(err)
	}
	if input.UserSources == nil || len(input.UserSources) != 0 {
		t.Fatal("截断后的空来源不能回退到完整原文")
	}
}

func TestScreeningQuotaErrorIsExplicitAndDoesNotFallback(t *testing.T) {
	st := newFakeProductStore()
	agent := &fakeAgent{store: st, screenErr: upstream.New("bailian", "screening", upstream.KindQuota,
		403, "AllocationQuota.FreeTierOnly", errors.New("secret provider body"))}
	sink := newFakeSink()
	svc, _ := NewService(context.Background(), st, agent, sink)
	_, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000026", "8000 元装机")
	if err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	var problem Problem
	if err := json.Unmarshal(st.session.LastError, &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "model_quota_exhausted" || agent.screenCalls != 1 {
		t.Fatalf("problem=%+v calls=%d", problem, agent.screenCalls)
	}
}

func TestServiceDuplicateMessagePrecedesContextCheck(t *testing.T) {
	st := newFakeProductStore()
	st.session.Phase = store.PhaseReady
	requestID := "00000000-0000-4000-8000-000000000004"
	runID := "00000000-0000-4000-8000-000000000005"
	st.runs[runID] = store.AgentRun{ID: runID, SessionID: st.session.ID, ClientRequestID: requestID,
		Kind: store.RunChange, Status: store.RunSucceeded}
	st.messages = append(st.messages, store.WebMessage{Role: "user", Content: "换成 A 卡", RunID: &runID})
	agent := &fakeAgent{store: st, contextAvailable: false}
	svc, _ := NewService(context.Background(), st, agent, newFakeSink())
	result, err := svc.StartMessage(context.Background(), "owner-1", "session-1", requestID, "换成 A 卡")
	if err != nil || !result.Duplicate || result.Run.ID != runID {
		t.Fatalf("幂等重试应先于 context 检查:result=%+v err=%v", result, err)
	}
}

func TestTitleFromText(t *testing.T) {
	if got := TitleFromText("  8000 元\n 2K 玩黑神话  "); got != "8000 元 2K 玩黑神话" {
		t.Fatalf("TitleFromText=%q", got)
	}
	long := "一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二三"
	if got := TitleFromText(long); len([]rune(got)) != 32 {
		t.Fatalf("长标题 rune=%d", len([]rune(got)))
	}
}

// blockingAgent 模拟阻塞中的 A2A 调用:直到 ctx 取消才返回错误。
type blockingAgent struct{}

func (blockingAgent) Screen(ctx context.Context, _, _ string, _ ScreenInput) (ScreenResult, error) {
	<-ctx.Done()
	return ScreenResult{}, ctx.Err()
}
func (blockingAgent) Remote(ctx context.Context, _, _ string, _ json.RawMessage) (RemoteResult, error) {
	<-ctx.Done()
	return RemoteResult{}, ctx.Err()
}
func (blockingAgent) ContextAvailable(context.Context, string, string) (bool, error) {
	return true, nil
}

func TestServiceCancelInterruptsRun(t *testing.T) {
	st := newFakeProductStore()
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, blockingAgent{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Shutdown(context.Background()) }()
	started, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000011", "8000 元 2K 玩黑神话")
	if err != nil {
		t.Fatal(err)
	}
	if _, requested, err := svc.RequestCancel(context.Background(), "owner-1", "session-1", started.Run.ID); err != nil || !requested {
		t.Fatalf("RequestCancel requested=%v err=%v", requested, err)
	}
	sink.wait(t)
	st.mu.Lock()
	run := st.runs[started.Run.ID]
	lastError := st.session.LastError
	st.mu.Unlock()
	if run.Status != store.RunInterrupted {
		t.Fatalf("取消后 run 状态=%s, want interrupted", run.Status)
	}
	var problem struct{ Code string }
	if err := json.Unmarshal(lastError, &problem); err != nil || problem.Code != "run_cancelled" {
		t.Fatalf("取消 problem=%s err=%v", lastError, err)
	}
	names := sink.names()
	if !strings.Contains(strings.Join(names, ","), "run.cancelled") {
		t.Fatalf("缺少 run.cancelled 事件:%v", names)
	}
	// 取消后的会话可以立即再发消息(锁已释放由真实 store 唯一索引保证)。
	next, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000012", "再试一次")
	if err != nil || next.Run.Status != store.RunRunning {
		t.Fatalf("取消后新消息应可启动:%+v err=%v", next, err)
	}
}
