package product

import (
	"bytes"
	"slices"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

type fakeProductStore struct {
	mu           sync.Mutex
	session      store.WebSession
	runs         map[string]store.AgentRun
	buildRuns       map[string]store.BuildRun
	builderPayloads map[string]json.RawMessage
	confirmation    *store.RequirementConfirmation
	messages     []store.WebMessage
	latest       int
	fingerprints map[string]string
	edits        map[string]string // request_id → fingerprint(草稿编辑幂等)
	onMessageRun func(messageID string) // planningFakeStore 挂接建议消费等扩展语义
	onComplete   func(store.CompleteRunParams)
}

func newFakeProductStore() *fakeProductStore {
	return &fakeProductStore{
		session:      store.WebSession{ID: "session-1", OwnerID: "owner-1", Phase: store.PhaseCollecting},
		runs:            make(map[string]store.AgentRun),
		buildRuns:       make(map[string]store.BuildRun),
		builderPayloads: make(map[string]json.RawMessage),
		fingerprints:    make(map[string]string),
		edits:           make(map[string]string),
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
func (f *fakeProductStore) StartMessageRun(ctx context.Context, p store.StartMessageRunParams) (store.AgentRun, bool, error) {
	if hook := f.onMessageRun; hook != nil {
		defer hook(p.MessageID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r := store.AgentRun{ID: p.RunID, SessionID: p.SessionID, ClientRequestID: p.RequestID,
		Kind: store.RunScreening, Status: store.RunRunning, StartedAt: time.Now()}
	f.runs[r.ID] = r
	f.fingerprints[r.ID] = p.RequestFingerprint
	f.messages = append(f.messages, store.WebMessage{
		ID: p.MessageID, SessionID: p.SessionID, Role: "user", Content: p.Text, RunID: &r.ID, CreatedAt: time.Now(),
	})
	f.session.Phase = store.PhaseCollecting
	return r, false, nil
}

// startConfirmRunForTest 是生产 StartConfirmRun 的假实现:同一 readiness/hash
// 门控,但不做 SQL 级并发控制。
func (f *fakeProductStore) StartConfirmRun(_ context.Context, p store.StartConfirmRunParams) (store.AgentRun, store.StartConfirmRunResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, run := range f.runs {
		if run.Kind == store.RunBuild && run.ClientRequestID == p.RequestID {
			if f.fingerprints[run.ID] != p.RequestFingerprint {
				return store.AgentRun{}, store.StartConfirmRunResult{}, false, store.ErrIdempotencyConflict
			}
			return run, store.StartConfirmRunResult{}, true, nil
		}
	}
	if len(f.session.RequirementState) == 0 {
		return store.AgentRun{}, store.StartConfirmRunResult{}, false, schemas.ErrRequirementStateUnsupported
	}
	state, err := schemas.DecodeRequirementState(f.session.RequirementState)
	if err != nil {
		return store.AgentRun{}, store.StartConfirmRunResult{}, false, err
	}
	if state.Revision != p.ExpectedRevision {
		return store.AgentRun{}, store.StartConfirmRunResult{}, false, store.ErrRequirementRevision
	}
	spec, readiness, err := schemas.RequirementStateSpec(state)
	if err != nil {
		return store.AgentRun{}, store.StartConfirmRunResult{}, false, err
	}
	if !readiness.ConfirmationEligible {
		return store.AgentRun{}, store.StartConfirmRunResult{}, false, store.ErrRequirementNotReady
	}
	reviewHash, err := schemas.CanonicalHash(spec)
	if err != nil {
		return store.AgentRun{}, store.StartConfirmRunResult{}, false, err
	}
	if reviewHash != p.ExpectedReviewHash {
		return store.AgentRun{}, store.StartConfirmRunResult{}, false, store.ErrRequirementReviewConflict
	}
	// 与生产语义一致:新确认从当前状态组装;失败重试继承目标 run 的冻结
	// 载荷,只替换 run 标识。
	var payload json.RawMessage
	confirmationID := p.ConfirmationID
	if p.RetryOfRunID != "" {
		target, ok := f.buildRuns[p.RetryOfRunID]
		inheritedPayload, hasPayload := f.builderPayloads[p.RetryOfRunID]
		if !ok || target.SessionID != p.SessionID {
			return store.AgentRun{}, store.StartConfirmRunResult{}, false, store.ErrRunNotFound
		}
		if target.Status != store.RunFailed || target.ConfirmationID == "" || !hasPayload {
			return store.AgentRun{}, store.StartConfirmRunResult{}, false, store.ErrRetryTargetInvalid
		}
		confirmationID = target.ConfirmationID
		var inherited schemas.PlanningInput
		if err := json.Unmarshal(inheritedPayload, &inherited); err != nil {
			return store.AgentRun{}, store.StartConfirmRunResult{}, false, err
		}
		inherited.RunID = p.RunID
		payload, err = json.Marshal(inherited)
		if err != nil {
			return store.AgentRun{}, store.StartConfirmRunResult{}, false, err
		}
	} else {
		payload, err = schemas.PlanningBuilderInput(p.RunID, state, nil, nil, nil, "")
		if err != nil {
			return store.AgentRun{}, store.StartConfirmRunResult{}, false, err
		}
		f.confirmation = &store.RequirementConfirmation{ID: p.ConfirmationID, SessionID: p.SessionID,
			RequirementSpec: spec, RequirementState: f.session.RequirementState, ReviewHash: reviewHash,
			Revision: state.Revision, SchemaVersion: schemas.RequirementStateSchemaVersion, CreatedAt: time.Now()}
	}
	builderHash, err := schemas.CanonicalHash(payload)
	if err != nil {
		return store.AgentRun{}, store.StartConfirmRunResult{}, false, err
	}
	r := store.AgentRun{ID: p.RunID, SessionID: p.SessionID, ClientRequestID: p.RequestID,
		Kind: store.RunBuild, Status: store.RunRunning, StartedAt: time.Now()}
	f.runs[r.ID] = r
	f.fingerprints[r.ID] = p.RequestFingerprint
	f.builderPayloads[r.ID] = append(json.RawMessage(nil), payload...)
	f.buildRuns[r.ID] = store.BuildRun{AgentRun: r, ConfirmationID: confirmationID, ReviewHash: reviewHash, BuilderInputHash: builderHash}
	f.session.Phase = store.PhaseBuilding
	f.session.ConfirmationID = confirmationID
	return r, store.StartConfirmRunResult{ConfirmationID: confirmationID, ReviewHash: reviewHash,
		BuilderInputPayload: payload, BuilderInputHash: builderHash}, false, nil
}

func (f *fakeProductStore) ConfirmationByID(_ context.Context, sessionID, id string) (store.RequirementConfirmation, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "" || f.confirmation == nil || f.confirmation.SessionID != sessionID || f.confirmation.ID != id {
		return store.RequirementConfirmation{}, false, nil
	}
	return *f.confirmation, true, nil
}

func (f *fakeProductStore) BuildRuns(_ context.Context, sessionID string) ([]store.BuildRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.BuildRun, 0, len(f.buildRuns))
	for _, run := range f.buildRuns {
		if run.SessionID == sessionID {
			run.Version = 0
			out = append(out, run)
		}
	}
	return out, nil
}

func (f *fakeProductStore) RequirementEditFingerprint(_ context.Context, sessionID, requestID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sessionID != f.session.ID {
		return "", false, nil
	}
	fp, ok := f.edits[requestID]
	return fp, ok, nil
}

func (f *fakeProductStore) EditRequirementDraft(_ context.Context, p store.EditRequirementDraftParams) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if old, ok := f.edits[p.RequestID]; ok {
		if old != p.Fingerprint {
			return false, store.ErrIdempotencyConflict
		}
		return false, nil
	}
	state, err := schemas.DecodeRequirementState(f.session.RequirementState)
	if err != nil {
		return false, err
	}
	if state.Revision != p.ExpectedRevision {
		return false, store.ErrRequirementRevision
	}
	f.edits[p.RequestID] = p.Fingerprint
	f.session.RequirementState = p.Next
	f.session.PendingRequirement = p.Pending
	return true, nil
}
func (f *fakeProductStore) CompleteRun(ctx context.Context, p store.CompleteRunParams) (*store.WebMessage, error) {
	if hook := f.onComplete; hook != nil {
		defer hook(p)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[p.RunID]
	r.Status = p.Status
	f.runs[p.RunID] = r
	if br, ok := f.buildRuns[p.RunID]; ok {
		br.Status = p.Status
		if p.BuildVersion > 0 {
			br.Version = p.BuildVersion
		}
		f.buildRuns[p.RunID] = br
	}
	f.session.Phase = p.Phase
	f.session.RecoveryPhase = p.RecoveryPhase
	f.session.LastError = p.Error
	if p.SetPending {
		f.session.PendingRequirement = p.PendingRequirement
	}
	if p.SetRequirementState {
		f.session.RequirementState = p.RequirementState
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
type planningFakeStore struct {
	*fakeProductStore
	proposals []fakeRequirementProposal
}

func newPlanningFakeStore() *planningFakeStore {
	planner := &planningFakeStore{fakeProductStore: newFakeProductStore()}
	planner.onMessageRun = planner.consumeProposals
	planner.onComplete = planner.saveProposals
	return planner
}

type fakeRequirementProposal struct {
	sessionID, assistantMessageID, field string
	value                                json.RawMessage
	text, consumedBy                     string
	resolved                             bool
}

// ActiveRequirementProposals 与生产语义一致:只返回绑定本轮用户消息、
// 未解析的建议;裸"可以"没有可验证对象时返回空。
func (f *planningFakeStore) ActiveRequirementProposals(_ context.Context, sessionID, userMessageID string) ([]store.RequirementProposalRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.RequirementProposalRecord{}
	for _, proposal := range f.proposals {
		if proposal.sessionID == sessionID && proposal.consumedBy == userMessageID && !proposal.resolved {
			out = append(out, store.RequirementProposalRecord{Field: proposal.field, Value: proposal.value, Text: proposal.text})
		}
	}
	return out, nil
}

// consumeProposals 模拟 StartMessageRun 事务内的紧邻消费:只有"最后一条
// assistant 消息"绑定的未消费建议对本轮有效。
func (f *planningFakeStore) consumeProposals(userMessageID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lastAssistant := ""
	for _, message := range f.messages {
		if message.Role == "assistant" {
			lastAssistant = message.ID
		}
	}
	for i := range f.proposals {
		if f.proposals[i].assistantMessageID == lastAssistant && f.proposals[i].consumedBy == "" && !f.proposals[i].resolved {
			f.proposals[i].consumedBy = userMessageID
		}
	}
}

// saveProposals 与生产同事务语义:文本未展示的建议丢弃。
func (f *planningFakeStore) saveProposals(p store.CompleteRunParams) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, proposal := range p.SaveProposals {
		if !strings.Contains(p.AssistantContent, proposal.Text) {
			continue
		}
		f.proposals = append(f.proposals, fakeRequirementProposal{
			sessionID: p.SessionID, assistantMessageID: p.AssistantMessageID,
			field: proposal.Field, value: proposal.Value, text: proposal.Text,
		})
	}
	for i := range f.proposals {
		if f.proposals[i].sessionID != p.SessionID || f.proposals[i].consumedBy != p.ConsumeMessageID || f.proposals[i].resolved {
			continue
		}
		for _, accepted := range p.ResolveProposals {
			if accepted.Field == f.proposals[i].field && sameRequirementJSON(accepted.Value, f.proposals[i].value) {
				f.proposals[i].resolved = true
			}
		}
	}
}

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

// turnScreenResult 把净化后的更新批包装为 v2 一轮结果供服务层消费。
func turnScreenResult(update *schemas.RequirementUpdate, answer string) ScreenResult {
	turn := pipeline.RequirementTurnResult{Operations: update.Operations, Observations: update.Observations, Answer: answer}
	return ScreenResult{Turn: &turn, RequirementUpdate: update}
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
	if f.screen.Turn == nil && f.screen.RequirementUpdate != nil {
		f.screen.Turn = &pipeline.RequirementTurnResult{
			Operations:   f.screen.RequirementUpdate.Operations,
			Observations: f.screen.RequirementUpdate.Observations,
		}
	}
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
	st := newPlanningFakeStore()
	update := &schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage("8000"), Kind: "constraint", Strength: "must", Scope: "session", Evidence: "stated", Quote: "预算8000"},
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "游戏"},
		{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"2K"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "2K"},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage("[]"), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "全部新买"},
	}}
	agent := &fakeAgent{store: st.fakeProductStore, screen: turnScreenResult(update, "需求已经整理好，请确认。"), contextAvailable: true}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000001", "预算8000，2K 玩游戏，全部新买")
	if err != nil || started.Run.Kind != store.RunScreening {
		t.Fatalf("StartMessage=%+v err=%v", started, err)
	}
	sink.wait(t)
	ws, _ := st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseRequirementReady || len(ws.PendingRequirement) == 0 {
		t.Fatalf("screening 后状态不正确:%+v", ws)
	}
	// 确认请求绑定用户看到的核定预览:revision 与 review_hash 来自服务端。
	detail, err := svc.GetSession(context.Background(), "owner-1", "session-1")
	if err != nil || detail.Axes.ReviewHash == "" || detail.Axes.Readiness == nil || !detail.Axes.Readiness.ConfirmationEligible {
		t.Fatalf("核定预览不可用: %+v err=%v", detail.Axes, err)
	}
	var state schemas.RequirementState
	if err := json.Unmarshal(detail.Session.RequirementState, &state); err != nil {
		t.Fatal(err)
	}
	if detail.Axes.Confirmation.Status != ConfirmationUnconfirmed || detail.Axes.Build.Status != BuildNone {
		t.Fatalf("确认前三轴应为 unconfirmed/none: %+v %+v", detail.Axes.Confirmation, detail.Axes.Build)
	}
	confirmed, err := svc.StartConfirm(context.Background(), "owner-1", "session-1",
		"00000000-0000-4000-8000-000000000002", ConfirmRequest{
			ExpectedRevision: state.Revision, ExpectedReviewHash: detail.Axes.ReviewHash})
	if err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	ws, _ = st.WebSessionByOwner(context.Background(), "owner-1", "session-1")
	if ws.Phase != store.PhaseReady || st.latest != 1 {
		t.Fatalf("confirm 后状态不正确:session=%+v latest=%d", ws, st.latest)
	}
	// Builder 收到的就是 run 冻结的完整载荷:发送 hash 与冻结 hash 一致(V5)。
	var input schemas.PlanningInput
	if err := json.Unmarshal(agent.remotePayload, &input); err != nil || input.SchemaVersion != 2 || input.RunID != confirmed.Run.ID {
		t.Fatalf("Remote 应收到冻结 PlanningInput:%s err=%v", agent.remotePayload, err)
	}
	if got := input.State.Fields["budget_cny"].Value; string(got) != "8000" {
		t.Fatalf("冻结载荷的预算不正确: %s", got)
	}
	st.mu.Lock()
	buildRun := st.buildRuns[confirmed.Run.ID]
	st.mu.Unlock()
	if sent, _ := schemas.CanonicalHash(agent.remotePayload); sent != buildRun.BuilderInputHash {
		t.Fatalf("发送载荷 hash %s 与冻结 hash %s 不一致", sent, buildRun.BuilderInputHash)
	}
	if buildRun.ConfirmationID == "" || buildRun.ReviewHash != detail.Axes.ReviewHash || buildRun.ConfirmationID != ws.ConfirmationID {
		t.Fatalf("run 未绑定确认快照: %+v ws=%+v", buildRun, ws)
	}
	wantOrder := []string{"run.started", "run.progress", "requirement.updated", "assistant.completed", "requirement.ready", "run.completed",
		"run.started", "requirement.confirmed", "run.progress", "run.progress", "assistant.completed", "build.saved", "run.completed"}
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
	st := newPlanningFakeStore()
	update := &schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_cny", Value: json.RawMessage("8000"), Kind: "constraint", Strength: "must", Scope: "session", Evidence: "stated", Quote: "预算8000"},
			{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "游戏"},
		},
	}
	agent := &fakeAgent{store: st.fakeProductStore, screen: turnScreenResult(update, "需求已经整理好，请确认。"), contextAvailable: true}
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
	// v2 每轮最多一个问题组:按领域优先级先问已有配件,分辨率留到下一轮。
	if last.Role != "assistant" || !strings.Contains(last.Content, "已有配件") {
		t.Fatalf("追问应指向领域问题计划选中的首个缺失项: %s", last.Content)
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
	st := newPlanningFakeStore()
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
	st := newPlanningFakeStore()
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
	st := newPlanningFakeStore()
	update := &schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_cny", Value: json.RawMessage("8000"), Kind: "constraint", Strength: "must", Scope: "session", Evidence: "stated", Quote: "预算8000"},
			{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "游戏"},
			{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "1080p"},
			{Op: "set", Field: "existing_parts", Value: json.RawMessage("[]"), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "全部新买"},
		},
	}
	result := turnScreenResult(update, "开始为本轮选配。")
	// 用户"直接开始配"是本轮请求事实:多标签 signal 不是授权,仍需核定面板。
	result.Turn.Signals.RequestsBuild = true
	agent := &fakeAgent{store: st.fakeProductStore, screen: result, contextAvailable: true}
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
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || !strings.Contains(messages[len(messages)-1].Content, "开始为本轮选配。") ||
		!strings.Contains(messages[len(messages)-1].Content, "现在可以核定需求") {
		t.Fatalf("最终回复应先展示模型回答,再提示核定而不启动 Builder: %+v", messages)
	}
	if !slices.Contains(sink.names(), "presentation.action") {
		t.Fatalf("ready+requests_build 应产生 open_requirement_review: %v", sink.names())
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

// 旧确定性改单/RunChange 直达 Builder 的路径已关闭:ready 阶段的聊天(包括
// "降 500""换成 A 卡""开始吧")只能是 screening 轮,不得调用 Remote,也不得
// 产生 ChangeRequest。
func TestChatInReadyPhaseCannotStartBuilder(t *testing.T) {
	for _, text := range []string{"降 500", "换成 A 卡", "就这样，开始吧"} {
		t.Run(text, func(t *testing.T) {
			st := newPlanningFakeStore()
			st.session.Phase = store.PhaseReady
			st.latest = 1
			agent := &fakeAgent{store: st.fakeProductStore, screen: turnScreenResult(&schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}}, "好的"), contextAvailable: true}
			sink := newFakeSink()
			svc, err := NewService(context.Background(), st, agent, sink)
			if err != nil {
				t.Fatal(err)
			}
			started, err := svc.StartMessage(context.Background(), "owner-1", "session-1",
				"00000000-0000-4000-8000-000000000006", text)
			if err != nil {
				t.Fatal(err)
			}
			if started.Run.Kind != store.RunScreening {
				t.Fatalf("ready 阶段聊天应产生 screening 运行,得到 %s", started.Run.Kind)
			}
			sink.wait(t)
			if agent.remotePayload != nil {
				t.Fatalf("聊天不得直达 Builder: %s", agent.remotePayload)
			}
			if agent.screenCalls == 0 {
				t.Fatal("ready 阶段聊天应进入 screening 轮")
			}
			if st.latest != 1 {
				t.Fatalf("聊天不得新增配置版本: latest=%d", st.latest)
			}
		})
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
	if err != nil {
		t.Fatal(err)
	}
	_ = changeInput // RunChange 运行已不再产生;上下文过滤按 kind 一致处理。
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
		Kind: store.RunScreening, Status: store.RunSucceeded}
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
