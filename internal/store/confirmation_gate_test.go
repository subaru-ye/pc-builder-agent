package store

// 确认快照与 Builder gate 的持久化集成测试(requirement-confirmation-builder-gate-v2):
// 快照/run 原子关系、冻结载荷 hash 一致(V5)、幂等重放、revision/review_hash
// 门控、失败重试复用快照、运行中草稿编辑不创建第二个 run。需要 PG_TEST_DSN。
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// confirmReadySession 建会话并把草稿写成 readiness 完整状态,返回状态与核定预览 hash。
func confirmReadySession(t *testing.T, s *Store, owner, sessionID, createKey string) (schemas.RequirementState, string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateWebSession(ctx, sessionID, owner, createKey); err != nil {
		t.Fatal(err)
	}
	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`8000`)},
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`)},
		{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"2K"`)},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`)},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算8000，2K 游戏，全部新买"})
	if err != nil {
		t.Fatal(err)
	}
	stateJSON, _ := json.Marshal(state)
	spec, readiness, err := schemas.RequirementReviewSpec(state)
	if err != nil || !readiness.ConfirmationEligible {
		t.Fatalf("夹具状态应可确认:readiness=%+v err=%v", readiness, err)
	}
	if _, _, err := s.StartMessageRun(ctx, StartMessageRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "50000000-0000-4000-8000-0000000000ac", RunID: "50000000-0000-4000-8000-0000000000aa",
		MessageID: "50000000-0000-4000-8000-0000000000ab", Text: "预算8000，2K 游戏，全部新买", Title: "确认门控夹具",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteRun(ctx, CompleteRunParams{
		RunID: "50000000-0000-4000-8000-0000000000aa", SessionID: sessionID, Status: RunSucceeded,
		Phase: PhaseRequirementReady, PendingRequirement: spec, SetPending: true,
		RequirementState: stateJSON, SetRequirementState: true,
	}); err != nil {
		t.Fatal(err)
	}
	reviewHash, err := schemas.CanonicalHash(spec)
	if err != nil {
		t.Fatal(err)
	}
	return state, reviewHash
}

// TestConfirmSnapshotAndPayloadFrozen 证明确认事务的原子冻结:快照行、run 的
// 两个 hash、完整 PlanningInput 载荷(含 RunID)与重放语义同时成立。
func TestConfirmSnapshotAndPayloadFrozen(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	const (
		owner      = "owner-gate"
		sessionID  = "session-gate"
		confirmKey = "60000000-0000-4000-8000-000000000001"
		runID      = "60000000-0000-4000-8000-000000000002"
		snapshotID = "60000000-0000-4000-8000-000000000003"
	)
	state, reviewHash := confirmReadySession(t, s, owner, sessionID, "60000000-0000-4000-8000-000000000000")

	run, out, duplicate, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: confirmKey, RunID: runID,
		ConfirmationID: snapshotID, ExpectedRevision: state.Revision,
		ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-1",
	})
	if err != nil || duplicate {
		t.Fatalf("确认失败:run=%+v duplicate=%v err=%v", run, duplicate, err)
	}
	if out.ConfirmationID != snapshotID || out.ReviewHash != reviewHash {
		t.Fatalf("确认结果不正确:%+v", out)
	}
	// 发送载荷 hash 与冻结 hash 一致(V5);载荷绑定本轮 run ID。
	payloadHash, err := schemas.CanonicalHash(out.BuilderInputPayload)
	if err != nil {
		t.Fatal(err)
	}
	if payloadHash != out.BuilderInputHash {
		t.Fatalf("builder_input_hash 与载荷不一致:%s != %s", payloadHash, out.BuilderInputHash)
	}
	confirmation, found, err := s.ConfirmationByID(ctx, sessionID, snapshotID)
	if err != nil || !found {
		t.Fatalf("确认快照读取失败: found=%v err=%v", found, err)
	}
	var input schemas.PlanningInput
	if err := json.Unmarshal(out.BuilderInputPayload, &input); err != nil || input.RunID != runID || input.SchemaVersion != 2 {
		t.Fatalf("载荷不是冻结的完整 PlanningInput:%s err=%v", out.BuilderInputPayload, err)
	}
	if got := input.State.Fields["budget_cny"].Value; string(got) != "8000" {
		t.Fatalf("载荷状态不正确: %s", got)
	}
	// 有效选型约束随载荷冻结:完整核定预览(gaming 未填写 performance_goal
	// 物化 balanced)与默认来源清单;原始 State 不含被展开的默认。
	if input.EffectiveConstraints == nil || len(input.EffectiveConstraints.Spec) == 0 {
		t.Fatal("载荷缺少冻结的有效选型约束")
	}
	// JSONB 不保留字节格式,按语义比较冻结约束与快照 review_spec。
	if !semanticJSONEqual(input.EffectiveConstraints.Spec, confirmation.RequirementSpec) {
		t.Fatal("冻结约束的 spec 与确认快照的 review_spec 不一致")
	}
	frozenSpec, err := schemas.DecodeRequirementSpec(input.EffectiveConstraints.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if frozenSpec.UseCase.PerformanceGoal != schemas.PerformanceGoalBalanced {
		t.Fatalf("gaming 未填写 performance_goal 冻结值=%q want balanced", frozenSpec.UseCase.PerformanceGoal)
	}
	if frozenSpec.BudgetFlex != schemas.DefaultBudgetFlex {
		t.Fatalf("冻结弹性=%v want 系统默认 %v", frozenSpec.BudgetFlex, schemas.DefaultBudgetFlex)
	}
	defaults := map[string]schemas.RequirementDefault{}
	for _, d := range input.EffectiveConstraints.Defaults {
		defaults[d.Field] = d
		if d.Origin != "system_default" {
			t.Fatalf("默认来源=%q want system_default: %+v", d.Origin, d)
		}
	}
	for _, field := range []string{"budget_flex", "size_pref", "noise_pref", "brand_pref.cpu", "brand_pref.gpu", "configuration_scope", "performance_goal"} {
		if _, ok := defaults[field]; !ok {
			t.Fatalf("默认清单缺少 %s: %v", field, input.EffectiveConstraints.Defaults)
		}
	}
	if input.State.Fields["use_case.performance_goal"].Status != "unknown" || input.State.Fields["budget_flex"].Status != "unknown" {
		t.Fatal("被展开的系统默认不得写回用户草稿字段")
	}
	// 快照行不可变且包含展开默认的 review_spec 与 revision。
	if confirmation.ReviewHash != reviewHash || confirmation.Revision != state.Revision {
		t.Fatalf("确认快照读取不正确:%+v", confirmation)
	}
	var specObject map[string]json.RawMessage
	if err := json.Unmarshal(confirmation.RequirementSpec, &specObject); err != nil {
		t.Fatal(err)
	}
	if _, ok := specObject["budget_flex"]; !ok {
		t.Fatal("review_spec 应展开系统默认(预算弹性)")
	}
	// run 行永久绑定快照与两个 hash;重放同键同请求返回原 run。
	ws, err := s.WebSessionByOwner(ctx, owner, sessionID)
	if err != nil || ws.ConfirmationID != snapshotID || ws.Phase != PhaseBuilding {
		t.Fatalf("会话确认指针不正确:%+v err=%v", ws, err)
	}
	buildRuns, err := s.BuildRuns(ctx, sessionID)
	if err != nil || len(buildRuns) != 1 || buildRuns[0].ConfirmationID != snapshotID ||
		buildRuns[0].BuilderInputHash != out.BuilderInputHash || buildRuns[0].RetryOfRunID != "" {
		t.Fatalf("build run 关联不正确:%+v err=%v", buildRuns, err)
	}
	replayed, _, duplicate, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: confirmKey, RunID: "60000000-0000-4000-8000-000000000099",
		ConfirmationID: "60000000-0000-4000-8000-000000000009", ExpectedRevision: state.Revision,
		ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-1",
	})
	if err != nil || !duplicate || replayed.ID != runID {
		t.Fatalf("同键同请求重放应返回原 run:%+v duplicate=%v err=%v", replayed, duplicate, err)
	}
	// 同键不同请求是稳定冲突。
	if _, _, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: confirmKey, RunID: "60000000-0000-4000-8000-000000000098",
		ConfirmationID: "60000000-0000-4000-8000-000000000098", ExpectedRevision: state.Revision,
		ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-2",
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("同键不同请求应冲突,得到 %v", err)
	}
}

// TestConfirmGates 覆盖 revision、review hash(含默认变化)与 readiness 门控。
func TestConfirmGates(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	const (
		owner     = "owner-gates"
		sessionID = "session-gates"
	)
	state, reviewHash := confirmReadySession(t, s, owner, sessionID, "61000000-0000-4000-8000-000000000000")
	const createKey = "61000000-0000-4000-8000-000000000000"
	params := func(requestID, runID, hash string, revision int) StartConfirmRunParams {
		return StartConfirmRunParams{
			OwnerID: owner, SessionID: sessionID, RequestID: requestID, RunID: runID,
			ConfirmationID: runID + "-c", ExpectedRevision: revision,
			ExpectedReviewHash: hash, RequestFingerprint: requestID,
		}
	}
	// revision 不匹配 → 拒绝,且不产生 run/快照。
	if _, _, _, err := s.StartConfirmRun(ctx, params(
		"61000000-0000-4000-8000-000000000001", "61000000-0000-4000-8000-000000000002", reviewHash, state.Revision+1)); !errors.Is(err, ErrRequirementRevision) {
		t.Fatalf("过期 revision 应拒绝,得到 %v", err)
	}
	// revision 相同但默认规则变化(hash 不同)→ 拒绝旧核定预览。
	if _, _, _, err := s.StartConfirmRun(ctx, params(
		"61000000-0000-4000-8000-000000000003", "61000000-0000-4000-8000-000000000004",
		fmt.Sprintf("%064d", 0), state.Revision)); !errors.Is(err, ErrRequirementReviewConflict) {
		t.Fatalf("过期 review hash 应拒绝,得到 %v", err)
	}
	// readiness 不完整 → 稳定拒绝。
	draft, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "remove", Field: "budget_cny"},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算先不定"})
	if err != nil {
		t.Fatal(err)
	}
	draftJSON, _ := json.Marshal(draft)
	if _, _, err := s.StartMessageRun(ctx, StartMessageRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "50000000-0000-4000-8000-0000000000ad", RunID: "50000000-0000-4000-8000-0000000000ae",
		MessageID: "50000000-0000-4000-8000-0000000000af", Text: "预算先不定", Title: "确认门控夹具",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteRun(ctx, CompleteRunParams{
		RunID: "50000000-0000-4000-8000-0000000000ae", SessionID: sessionID, Status: RunSucceeded,
		Phase: PhaseCollecting, SetPending: true, RequirementState: draftJSON, SetRequirementState: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.StartConfirmRun(ctx, params(
		"61000000-0000-4000-8000-000000000005", "61000000-0000-4000-8000-000000000006", reviewHash, draft.Revision)); !errors.Is(err, ErrRequirementNotReady) {
		t.Fatalf("不完整需求应拒绝确认,得到 %v", err)
	}
	runs, err := s.BuildRuns(ctx, sessionID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("被拒绝的确认不得留下 build run:%+v err=%v", runs, err)
	}
}

// TestConfirmConcurrentSingleRun 证明并发确认只产生一个 run 与一个快照。
func TestConfirmConcurrentSingleRun(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	const (
		owner     = "owner-concurrent"
		sessionID = "session-concurrent"
	)
	state, reviewHash := confirmReadySession(t, s, owner, sessionID, "62000000-0000-4000-8000-000000000000")
	const attempts = 6
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, _, errs[i] = s.StartConfirmRun(ctx, StartConfirmRunParams{
				OwnerID: owner, SessionID: sessionID,
				RequestID:  fmt.Sprintf("62000000-0000-4000-8000-00000000010%d", i),
				RunID:      fmt.Sprintf("62000000-0000-4000-8000-00000000020%d", i),
				ConfirmationID: fmt.Sprintf("62000000-0000-4000-8000-00000000030%d", i),
				ExpectedRevision: state.Revision, ExpectedReviewHash: reviewHash,
				RequestFingerprint: fmt.Sprintf("fp-%d", i),
			})
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for i, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		if !errors.Is(err, ErrSessionBusy) {
			t.Fatalf("并发确认[%d]应成功或 session_busy,得到 %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("并发确认成功数=%d, want 1", succeeded)
	}
	runs, err := s.BuildRuns(ctx, sessionID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("build run 数=%d err=%v", len(runs), err)
	}
}

// TestConfirmRetryReusesSnapshot 证明失败重试复用快照、为新 run 另冻载荷与
// hash;草稿已修改后不得以旧快照重试。
func TestConfirmRetryReusesSnapshot(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	const (
		owner       = "owner-retry"
		sessionID   = "session-retry"
		firstRun    = "63000000-0000-4000-8000-000000000001"
		firstSnap   = "63000000-0000-4000-8000-000000000002"
		retryRun    = "63000000-0000-4000-8000-000000000003"
		firstKey    = "63000000-0000-4000-8000-000000000004"
		retryKey    = "63000000-0000-4000-8000-000000000005"
	)
	state, reviewHash := confirmReadySession(t, s, owner, sessionID, "63000000-0000-4000-8000-000000000000")
	run, out, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: firstKey, RunID: firstRun,
		ConfirmationID: firstSnap, ExpectedRevision: state.Revision,
		ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-first",
	})
	if err != nil {
		t.Fatal(err)
	}
	problem := json.RawMessage(`{"code":"generation_failed"}`)
	recovery := PhaseRequirementReady
	if _, err := s.CompleteRun(ctx, CompleteRunParams{RunID: run.ID, SessionID: sessionID,
		Status: RunFailed, Phase: PhaseError, RecoveryPhase: &recovery, Error: problem}); err != nil {
		t.Fatal(err)
	}
	// 非失败 run / 不存在 run 不能作为重试目标。
	if _, _, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "63000000-0000-4000-8000-000000000006",
		RunID: "63000000-0000-4000-8000-000000000007", RetryOfRunID: "63000000-0000-4000-8000-0000000000ff",
		ExpectedRevision: state.Revision, ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-x",
	}); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("未知重试目标应 404,得到 %v", err)
	}
	// 失败重试:复用快照,新 run/载荷/hash。
	retry, retryOut, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: retryKey, RunID: retryRun,
		RetryOfRunID: firstRun, ExpectedRevision: state.Revision,
		ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-retry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if retryOut.ConfirmationID != firstSnap {
		t.Fatalf("重试应复用确认快照:%+v", retryOut)
	}
	if retryOut.BuilderInputHash == out.BuilderInputHash {
		t.Fatal("新 run 标识应产生新的冻结载荷与 hash")
	}
	retryPayloadHash, err := schemas.CanonicalHash(retryOut.BuilderInputPayload)
	if err != nil || retryPayloadHash != retryOut.BuilderInputHash {
		t.Fatalf("重试载荷 hash 不一致:%s err=%v", retryPayloadHash, err)
	}
	var input schemas.PlanningInput
	if err := json.Unmarshal(retryOut.BuilderInputPayload, &input); err != nil || input.RunID != retryRun {
		t.Fatalf("重试载荷应绑定新 run:%s err=%v", retryOut.BuilderInputPayload, err)
	}
	// 原 run 的载荷/hash 不被修改。
	buildRuns, err := s.BuildRuns(ctx, sessionID)
	if err != nil || len(buildRuns) != 2 {
		t.Fatalf("build run 数=%d err=%v", len(buildRuns), err)
	}
	if buildRuns[1].ID != firstRun || buildRuns[1].BuilderInputHash != out.BuilderInputHash {
		t.Fatalf("先前 run 的冻结载荷被修改:%+v", buildRuns[1])
	}
	if buildRuns[0].RetryOfRunID != firstRun {
		t.Fatalf("重试关联未记录:%+v", buildRuns[0])
	}
	// 草稿修改后不得以旧快照重试:先改草稿再重试另一失败 run。
	if _, err := s.CompleteRun(ctx, CompleteRunParams{RunID: retry.ID, SessionID: sessionID,
		Status: RunFailed, Phase: PhaseError, RecoveryPhase: &recovery, Error: problem}); err != nil {
		t.Fatal(err)
	}
	updated, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`)},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算改成9000"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EditRequirementDraft(ctx, EditRequirementDraftParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "63000000-0000-4000-8000-000000000007",
		Fingerprint: "fp-edit", ExpectedRevision: updated.Revision - 1,
		Next: mustJSON(t, updated), Pending: nil,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "63000000-0000-4000-8000-000000000008",
		RunID: "63000000-0000-4000-8000-000000000009", RetryOfRunID: retryRun,
		ExpectedRevision: updated.Revision, ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-y",
	}); !errors.Is(err, ErrRequirementReviewConflict) {
		t.Fatalf("草稿已修改不得以旧快照重试,得到 %v", err)
	}
}

// TestEditRequirementDraftWhileBuilding 证明运行中草稿编辑:不创建第二个
// AgentRun、不改 phase,运行载荷与快照不变;幂等与 revision 约束保持。
func TestEditRequirementDraftWhileBuilding(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	const (
		owner      = "owner-edit"
		sessionID  = "session-edit"
		runID      = "64000000-0000-4000-8000-000000000001"
		snapshotID = "64000000-0000-4000-8000-000000000002"
	)
	state, reviewHash := confirmReadySession(t, s, owner, sessionID, "64000000-0000-4000-8000-000000000000")
	if _, _, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "64000000-0000-4000-8000-000000000003",
		RunID: runID, ConfirmationID: snapshotID, ExpectedRevision: state.Revision,
		ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-confirm",
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`)},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算改成9000"})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := s.EditRequirementDraft(ctx, EditRequirementDraftParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "64000000-0000-4000-8000-000000000004",
		Fingerprint: "fp-edit-1", ExpectedRevision: state.Revision,
		Next: mustJSON(t, updated), Pending: nil,
	})
	if err != nil || !applied {
		t.Fatalf("运行中编辑失败:applied=%v err=%v", applied, err)
	}
	// 同键同指纹重放:幂等成功且不重复应用。
	replayed, err := s.EditRequirementDraft(ctx, EditRequirementDraftParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "64000000-0000-4000-8000-000000000004",
		Fingerprint: "fp-edit-1", ExpectedRevision: state.Revision,
		Next: mustJSON(t, updated), Pending: nil,
	})
	if err != nil || replayed {
		t.Fatalf("同键同指纹应为幂等 no-op:applied=%v err=%v", replayed, err)
	}
	if _, err := s.EditRequirementDraft(ctx, EditRequirementDraftParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "64000000-0000-4000-8000-000000000004",
		Fingerprint: "fp-edit-2", ExpectedRevision: state.Revision,
		Next: mustJSON(t, updated), Pending: nil,
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("同键不同请求应冲突,得到 %v", err)
	}
	// 会话草稿已更新;phase 仍是 building,且没有第二个 run,载荷/快照不变。
	ws, err := s.WebSessionByOwner(ctx, owner, sessionID)
	if err != nil || ws.Phase != PhaseBuilding {
		t.Fatalf("编辑后 phase 不正确:%+v err=%v", ws, err)
	}
	var draft schemas.RequirementState
	if err := json.Unmarshal(ws.RequirementState, &draft); err != nil || draft.Revision != state.Revision+1 {
		t.Fatalf("草稿未更新:%+v err=%v", draft, err)
	}
	runs, err := s.BuildRuns(ctx, sessionID)
	if err != nil || len(runs) != 1 || runs[0].ID != runID {
		t.Fatalf("编辑不得创建第二个 build run:%+v err=%v", runs, err)
	}
	messageRuns, err := s.pool.Query(ctx, `SELECT kind FROM agent_runs WHERE session_id=$1`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer messageRuns.Close()
	kindCount := map[string]int{}
	for messageRuns.Next() {
		var kind string
		if err := messageRuns.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		kindCount[kind]++
	}
	// 只有夹具的 1 条 screening 与确认的 1 条 build;编辑不得创建任何 AgentRun。
	if kindCount["screening"] != 1 || kindCount["build"] != 1 || len(kindCount) != 2 {
		t.Fatalf("编辑不得创建任何 AgentRun:%v", kindCount)
	}
	// 第二次确认即使绑定当前预览也被 active Builder 硬拒绝(服务端双保险)。
	updatedSpec, _, err := schemas.RequirementReviewSpec(updated)
	if err != nil {
		t.Fatal(err)
	}
	updatedHash, err := schemas.CanonicalHash(updatedSpec)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: "64000000-0000-4000-8000-000000000005",
		RunID: "64000000-0000-4000-8000-000000000006", ConfirmationID: "64000000-0000-4000-8000-000000000007",
		ExpectedRevision: updated.Revision,
		ExpectedReviewHash: updatedHash, RequestFingerprint: "fp-second",
	}); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("运行中第二次确认应被拒绝,得到 %v", err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestConfirmRetryInheritsFrozenSelectionContext 证明失败重试的选型输入来自
// 目标 run 的冻结载荷:在两次运行之间改变会话的最新配置与提案后重试,除
// run 标识外 state/base_draft/previous_proposal/previous_run_id 全部不变,
// 且不从当前 session 重新组装。
func TestConfirmRetryInheritsFrozenSelectionContext(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	const (
		owner      = "owner-inherit"
		sessionID  = "session-inherit"
		firstRun   = "71000000-0000-4000-8000-000000000001"
		firstSnap  = "71000000-0000-4000-8000-000000000002"
		firstKey   = "71000000-0000-4000-8000-000000000003"
		retryRun   = "71000000-0000-4000-8000-000000000004"
		retryKey   = "71000000-0000-4000-8000-000000000005"
	)
	state, reviewHash := confirmReadySession(t, s, owner, sessionID, "71000000-0000-4000-8000-000000000000")
	run, out, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: firstKey, RunID: firstRun,
		ConfirmationID: firstSnap, ExpectedRevision: state.Revision,
		ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-inherit-first",
	})
	if err != nil {
		t.Fatal(err)
	}
	var firstPayload schemas.PlanningInput
	if err := json.Unmarshal(out.BuilderInputPayload, &firstPayload); err != nil {
		t.Fatal(err)
	}
	problem := json.RawMessage(`{"code":"generation_failed"}`)
	recovery := PhaseRequirementReady
	if _, err := s.CompleteRun(ctx, CompleteRunParams{RunID: run.ID, SessionID: sessionID,
		Status: RunFailed, Phase: PhaseError, RecoveryPhase: &recovery, Error: problem}); err != nil {
		t.Fatal(err)
	}

	// 两次运行之间改变最新配置与提案:新增 v2 版本(不同 draft)与一条新提案,
	// 模拟会话在失败后继续演化的情形。
	if _, err := s.SaveBuildVersion(ctx, SaveBuildVersionParams{
		SessionID: sessionID, RequirementSpec: json.RawMessage(`{"schema_version":2,"changed":"after-failure"}`),
		Draft: json.RawMessage(`{"schema_version":1,"changed":"after-failure"}`),
		Validation: json.RawMessage(`{"overall_status":"pass"}`), Quote: json.RawMessage(`{"total_cny":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO session_proposals (session_id, run_id, requirement, result, parent_version)
		VALUES ($1, $2, $3, $4, 0)`, sessionID, "71000000-0000-4000-8000-0000000000f0",
		json.RawMessage(`{"requirement_state":{"revision":1}}`),
		json.RawMessage(`{"outcome":"proposal","reply":"失败后新提案","draft":{"changed":"after-failure"},"issues":[]}`)); err != nil {
		t.Fatal(err)
	}

	_, retryOut, _, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: retryKey, RunID: retryRun,
		RetryOfRunID: firstRun, ExpectedRevision: state.Revision,
		ExpectedReviewHash: reviewHash, RequestFingerprint: "fp-inherit-retry",
	})
	if err != nil {
		t.Fatal(err)
	}
	var retryPayload schemas.PlanningInput
	if err := json.Unmarshal(retryOut.BuilderInputPayload, &retryPayload); err != nil {
		t.Fatal(err)
	}
	// 只替换 run 标识;选型输入与目标 run 的冻结载荷逐字段一致。
	if retryPayload.RunID != retryRun {
		t.Fatalf("重试载荷应绑定新 run:%s", retryPayload.RunID)
	}
	if !reflect.DeepEqual(retryPayload.State, firstPayload.State) {
		t.Fatal("重试改变了冻结的需求状态")
	}
	if !reflect.DeepEqual(retryPayload.BaseDraft, firstPayload.BaseDraft) {
		t.Fatalf("重试不得改用最新 build 作为基线:%s want %s", retryPayload.BaseDraft, firstPayload.BaseDraft)
	}
	if !reflect.DeepEqual(retryPayload.PreviousProposal, firstPayload.PreviousProposal) {
		t.Fatalf("重试不得改用最新提案:%s want %s", retryPayload.PreviousProposal, firstPayload.PreviousProposal)
	}
	if retryPayload.PreviousRunID != firstPayload.PreviousRunID {
		t.Fatalf("重试改变了上一提案 run:%s want %s", retryPayload.PreviousRunID, firstPayload.PreviousRunID)
	}
	// 重试载荷经 DB JSONB 往返,按语义比较冻结约束(DeepEqual 会因字节格式失败)。
	if !semanticJSONEqual(retryPayload.EffectiveConstraints.Spec, firstPayload.EffectiveConstraints.Spec) ||
		!reflect.DeepEqual(retryPayload.EffectiveConstraints.Defaults, firstPayload.EffectiveConstraints.Defaults) {
		t.Fatal("重试改变了冻结的有效选型约束")
	}
	// 新载荷产生新 hash;快照与原 run 的冻结内容不变。
	retryPayload.RunID = firstRun
	realigned, err := json.Marshal(retryPayload)
	if err != nil {
		t.Fatal(err)
	}
	if realignedHash, err := schemas.CanonicalHash(realigned); err != nil || realignedHash != out.BuilderInputHash {
		t.Fatalf("替换回原 run 标识后载荷应与原冻结一致:%s vs %s err=%v", realignedHash, out.BuilderInputHash, err)
	}
	buildRuns, err := s.BuildRuns(ctx, sessionID)
	if err != nil || len(buildRuns) != 2 {
		t.Fatalf("build run 数=%d err=%v", len(buildRuns), err)
	}
	if buildRuns[1].BuilderInputHash != out.BuilderInputHash || buildRuns[1].ConfirmationID != firstSnap {
		t.Fatalf("原 run 的冻结载荷/快照被修改:%+v", buildRuns[1])
	}
	if buildRuns[0].ConfirmationID != firstSnap {
		t.Fatal("重试未复用同一确认快照")
	}
}


// semanticJSONEqual 按 JSON 语义(对象 key 无序)比较两个 RawMessage;
// JSONB 往返不保留字节格式。
func semanticJSONEqual(a, b json.RawMessage) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}
