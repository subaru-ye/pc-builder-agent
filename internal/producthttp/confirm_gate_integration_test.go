package producthttp

// 确认/Builder gate 的端到端集成验证(requirement-confirmation-builder-gate-v2):
// 通过可控成功/失败 Builder 产物验证 V5(发送载荷 hash = run 冻结 hash)、
// 三轴落位(current/outdated/failed)、运行中编辑不创建第二个 AgentRun、
// 草稿已修改时不得以旧快照重试。
import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// gateGateway 是可控 Builder 替身:Remote 记录实际收到的载荷,可挂起等待外部
// 释放;failAfter 用尽后返回显式业务失败,否则经 SaveBuildVersion 落版本并成功。
type gateGateway struct {
	st        *store.Store
	mu        chan struct{} // hold 时 Remote 阻塞直到关闭
	payload   json.RawMessage
	hold      bool
	failAfter int
}

func (g *gateGateway) Screen(context.Context, string, string, product.ScreenInput) (product.ScreenResult, error) {
	turn := pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{}}
	return product.ScreenResult{Turn: &turn}, nil
}

func (g *gateGateway) Remote(ctx context.Context, _, sessionID string, payload json.RawMessage) (product.RemoteResult, error) {
	g.payload = append(json.RawMessage(nil), payload...)
	if g.hold && g.mu != nil {
		<-g.mu
	}
	if g.failAfter <= 0 {
		return product.RemoteResult{Text: "数据暂时不可用", Decision: &buildharness.Decision{Kind: "data_unavailable", Reason: "requirement_evidence_missing"}}, nil
	}
	g.failAfter--
	var input schemas.PlanningInput
	if err := json.Unmarshal(payload, &input); err != nil {
		return product.RemoteResult{}, err
	}
	if _, err := g.st.SaveBuildVersion(ctx, store.SaveBuildVersionParams{
		SessionID: sessionID, RequirementSpec: payload,
		Draft:      json.RawMessage(`{"schema_version":1}`),
		Validation: json.RawMessage(`{"overall_status":"pass"}`),
		Quote:      json.RawMessage(`{"total_cny":100}`),
		RunID:      input.RunID,
	}); err != nil {
		return product.RemoteResult{}, err
	}
	return product.RemoteResult{Text: "配置已生成。"}, nil
}

func (g *gateGateway) ContextAvailable(context.Context, string, string) (bool, error) { return true, nil }

func waitRunTerminal(t *testing.T, svc *product.Service, owner, runID string) store.AgentRun {
	t.Helper()
	for i := 0; i < 400; i++ {
		run, err := svc.GetRun(context.Background(), owner, runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != store.RunRunning {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run 未在限时内结束")
	return store.AgentRun{}
}

func TestConfirmGateEndToEndAndAxes(t *testing.T) {
	_, _, st := requirementIntegrationAPI(t)
	ctx := context.Background()
	owner := "gate-e2e-owner"
	gateway := &gateGateway{st: st, failAfter: 1}
	svc, err := product.NewService(ctx, st, gateway, runevents.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(ctx) })

	ws, err := svc.CreateSession(ctx, owner, "90000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	// 侧栏确定性编辑把草稿补齐:不经过聊天或模型,也不创建任何 AgentRun。
	// 每次使用不同幂等键:同键不同请求是稳定冲突。
	editWithKey := func(key string, ops ...schemas.RequirementOperation) product.SessionDetail {
		t.Helper()
		detail, err := svc.GetSession(ctx, owner, ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		var state schemas.RequirementState
		if err := json.Unmarshal(detail.Session.RequirementState, &state); err != nil {
			t.Fatal(err)
		}
		detail, err = svc.EditRequirement(ctx, owner, ws.ID, key, product.RequirementEdit{ExpectedRevision: state.Revision, Operations: ops})
		if err != nil {
			t.Fatal(err)
		}
		return detail
	}
	detail := editWithKey("90000000-0000-4000-8000-000000000021",
		schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: json.RawMessage(`8000`), Strength: "must"},
		schemas.RequirementOperation{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must"},
		schemas.RequirementOperation{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"2K"`), Kind: "fact", Strength: "must"},
		schemas.RequirementOperation{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Kind: "fact", Strength: "must"},
	)
	if detail.Axes.Readiness == nil || detail.Axes.Readiness.Status != "ready" ||
		detail.Axes.Confirmation.Status != product.ConfirmationUnconfirmed || detail.Axes.Build.Status != product.BuildNone {
		t.Fatalf("确认前三轴不正确: %+v", detail.Axes)
	}
	var state schemas.RequirementState
	if err := json.Unmarshal(detail.Session.RequirementState, &state); err != nil {
		t.Fatal(err)
	}
	first := product.ConfirmRequest{ExpectedRevision: state.Revision, ExpectedReviewHash: detail.Axes.ReviewHash}
	started, err := svc.StartConfirm(ctx, owner, ws.ID, "90000000-0000-4000-8000-000000000011", first)
	if err != nil {
		t.Fatal(err)
	}
	if run := waitRunTerminal(t, svc, owner, started.Run.ID); run.Status != store.RunSucceeded {
		t.Fatalf("首次确认生成失败: %s", run.Status)
	}
	detail, err = svc.GetSession(ctx, owner, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Axes.Confirmation.Status != product.ConfirmationConfirmed || detail.Axes.Build.Status != product.BuildCurrent ||
		detail.Axes.Build.Version == nil || *detail.Axes.Build.Version != 1 {
		t.Fatalf("生成后三轴应 confirmed/current/v1: %+v", detail.Axes)
	}
	// V5:实际发送载荷的规范化 hash 与 run 冻结 hash 一致,载荷绑定该 run。
	runs, err := st.BuildRuns(ctx, ws.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("build run 读取失败:%+v err=%v", runs, err)
	}
	var input schemas.PlanningInput
	if err := json.Unmarshal(gateway.payload, &input); err != nil || input.RunID != started.Run.ID {
		t.Fatalf("Builder 未收到冻结载荷:%s err=%v", gateway.payload, err)
	}
	sentHash, err := schemas.CanonicalHash(gateway.payload)
	if err != nil || sentHash != runs[0].BuilderInputHash {
		t.Fatalf("发送 hash=%s 冻结 hash=%s err=%v", sentHash, runs[0].BuilderInputHash, err)
	}
	if runs[0].ConfirmationID != detail.Session.ConfirmationID {
		t.Fatal("run 未绑定会话确认快照")
	}

	// 草稿修改 → modified/outdated;旧核定预览的确认被拒绝且不启动 Builder。
	detail = editWithKey("90000000-0000-4000-8000-000000000022",
		schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`), Strength: "must"})
	if detail.Axes.Confirmation.Status != product.ConfirmationModified || detail.Axes.Build.Status != product.BuildOutdated {
		t.Fatalf("修改后三轴应 modified/outdated: %+v", detail.Axes)
	}
	_, staleErr := svc.StartConfirm(ctx, owner, ws.ID, "90000000-0000-4000-8000-000000000012", first)
	if !errors.Is(staleErr, store.ErrRequirementRevision) && !errors.Is(staleErr, store.ErrRequirementReviewConflict) {
		t.Fatalf("旧核定预览应被拒绝(revision/hash),得到 %v", staleErr)
	}
	if runsAfterStale, err := st.BuildRuns(ctx, ws.ID); err != nil || len(runsAfterStale) != 1 {
		t.Fatalf("被拒绝的确认不得启动 Builder:%+v err=%v", runsAfterStale, err)
	}

	// 新预览确认,Remote 挂起期间侧栏编辑成功:无第二个 build run,phase 仍是
	// building;释放后 Builder 显式失败 → failed 与 modified 并存。
	gateway.failAfter = 0
	fresh := confirmRequest(t, svc, owner, ws.ID)
	gateway.hold = true
	gateway.mu = make(chan struct{})
	second, err := svc.StartConfirm(ctx, owner, ws.ID, "90000000-0000-4000-8000-000000000013", fresh)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // 等 Remote 进入挂起(已记录载荷)
	detail = editWithKey("90000000-0000-4000-8000-000000000023",
		schemas.RequirementOperation{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`), Kind: "fact", Strength: "must"})
	if detail.Axes.Confirmation.Status != product.ConfirmationModified {
		t.Fatalf("运行中编辑应立即使 confirmation=modified: %+v", detail.Axes.Confirmation)
	}
	duringRun, err := st.BuildRuns(ctx, ws.ID)
	if err != nil || len(duringRun) != 2 {
		t.Fatalf("运行中不得出现第二个 build run:%+v err=%v", duringRun, err)
	}
	wsRow, err := st.WebSessionByOwner(ctx, owner, ws.ID)
	if err != nil || wsRow.Phase != store.PhaseBuilding {
		t.Fatalf("运行中编辑不得改变 phase:%+v err=%v", wsRow.Phase, err)
	}
	close(gateway.mu)
	if run := waitRunTerminal(t, svc, owner, second.Run.ID); run.Status != store.RunFailed {
		t.Fatalf("第二次生成应失败: %s", run.Status)
	}
	detail, err = svc.GetSession(ctx, owner, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Axes.Confirmation.Status != product.ConfirmationModified || detail.Axes.Build.Status != product.BuildFailed {
		t.Fatalf("失败后三轴应 modified/failed: %+v", detail.Axes)
	}
	if detail.Axes.Build.Version == nil || *detail.Axes.Build.Version != 1 {
		t.Fatalf("历史成功配置应保留可见: %+v", detail.Axes.Build)
	}

	// 草稿已在运行中修改:以失败 run 的旧预览请求 retry 必须被拒绝
	// (不得以旧快照重试),须先重新核定。
	_, retryStaleErr := svc.StartConfirm(ctx, owner, ws.ID, "90000000-0000-4000-8000-000000000014", product.ConfirmRequest{
		ExpectedRevision: fresh.ExpectedRevision, ExpectedReviewHash: fresh.ExpectedReviewHash, RetryOfRunID: second.Run.ID})
	if !errors.Is(retryStaleErr, store.ErrRequirementRevision) && !errors.Is(retryStaleErr, store.ErrRequirementReviewConflict) {
		t.Fatalf("草稿已修改不得以旧快照重试,得到 %v", retryStaleErr)
	}
	if runsAfterStaleRetry, err := st.BuildRuns(ctx, ws.ID); err != nil || len(runsAfterStaleRetry) != 2 {
		t.Fatalf("被拒绝的重试不得创建 run:%+v err=%v", runsAfterStaleRetry, err)
	}
	// 重新核定后以新快照确认(替代旧目标的完整重新生成)。
	gateway.failAfter = 1
	third, err := svc.StartConfirm(ctx, owner, ws.ID, "90000000-0000-4000-8000-000000000015", confirmRequest(t, svc, owner, ws.ID))
	if err != nil {
		t.Fatal(err)
	}
	if run := waitRunTerminal(t, svc, owner, third.Run.ID); run.Status != store.RunSucceeded {
		t.Fatalf("重新核定后的生成失败: %s", run.Status)
	}
	detail, err = svc.GetSession(ctx, owner, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Axes.Confirmation.Status != product.ConfirmationConfirmed || detail.Axes.Build.Status != product.BuildCurrent ||
		detail.Axes.Build.Version == nil || *detail.Axes.Build.Version != 2 {
		t.Fatalf("重新核定生成后三轴应 confirmed/current/v2: %+v", detail.Axes)
	}
	finalRuns, err := st.BuildRuns(ctx, ws.ID)
	if err != nil || len(finalRuns) != 3 {
		t.Fatalf("共三个 build run:%+v err=%v", finalRuns, err)
	}
	if finalRuns[2].BuilderInputHash == finalRuns[1].BuilderInputHash {
		t.Fatal("不同 run 的冻结载荷 hash 必须可区分")
	}
}
