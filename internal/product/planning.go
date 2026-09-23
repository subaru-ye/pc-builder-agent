package product

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"log"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type proposalStore interface {
	LatestProposal(context.Context, string) (json.RawMessage, error)
	CompletePlanningRun(context.Context, store.CompletePlanningParams) (store.PlanningCompletion, error)
	BuildByVersion(context.Context, string, int) (store.BuildVersion, error)
}

// isChangeRequestPayload 按顶层 intent 键识别改单载荷;改单合同不是需求
// 状态投影,不做 v1 兼容包装,原样交给生成服务按其合同处理。
func isChangeRequestPayload(payload json.RawMessage) bool {
	var root map[string]json.RawMessage
	return json.Unmarshal(payload, &root) == nil && root["intent"] != nil
}

func (s *Service) planningContext(ctx context.Context, sessionID, runID string, payload json.RawMessage) (json.RawMessage, error) {
	var input schemas.PlanningInput
	if json.Unmarshal(payload, &input) != nil || input.SchemaVersion != 2 {
		// v1 一次性切换:旧需求单不再静默包装重建。改单载荷(schema v1 的
		// ChangeRequest)原样透传,其余不合规载荷以稳定错误拒绝。
		if isChangeRequestPayload(payload) {
			return payload, nil
		}
		return nil, schemas.ErrRequirementStateUnsupported
	}
	input.RunID = runID
	st, ok := s.store.(proposalStore)
	if !ok {
		return payload, nil
	}
	previous, e := st.LatestProposal(ctx, sessionID)
	if e != nil {
		return nil, e
	}
	var saved struct {
		Result json.RawMessage `json:"result"`
		RunID  string          `json:"run_id"`
	}
	if len(previous) > 0 {
		_ = json.Unmarshal(previous, &saved)
		input.PreviousProposal = saved.Result
		input.PreviousRunID = saved.RunID
	}
	version, found, e := s.store.LatestBuildVersion(ctx, sessionID)
	if e != nil {
		return nil, e
	}
	if found {
		base, e := st.BuildByVersion(ctx, sessionID, version)
		if e != nil {
			return nil, e
		}
		input.BaseDraft = base.Draft
	}
	return json.Marshal(input)
}

// builderIdentity 把生成服务回传的身份压成单列口径;缺省(旧协议)返回空。
func builderIdentity(b *planning.BuilderIdentity) string {
	if b == nil {
		return ""
	}
	return b.Provider + "/" + b.Model
}

// mergeArchivedResult 用归档补全控制面副本的候选/证据/评估/假设。
// 入库副本经 TransportDegrade,证据正文与字段引文保持有界;
// 旧协议全量回传的结果已有这些字段,跳过合并。
func (s *Service) mergeArchivedResult(ctx context.Context, runID string, result *planning.Result) {
	if len(result.Candidates) > 0 || runID == "" {
		return
	}
	reader, ok := s.store.(planning.ArtifactReader)
	if !ok {
		return
	}
	payload, err := reader.PlanningArtifact(ctx, runID)
	if err != nil || len(payload) == 0 {
		log.Printf("[api] run %s 归档产物不可用:%v", runID, err)
		return
	}
	var full planning.Result
	if json.Unmarshal(payload, &full) != nil {
		return
	}
	degraded := planning.TransportDegrade(full)
	result.Candidates = degraded.Candidates
	result.Evidence = degraded.Evidence
	result.Assessments = degraded.Assessments
	result.Assumptions = degraded.Assumptions
}

// replayVerificationIssue 用产品自己的 store 重放 validate.Node(零 LLM),
// 比对快照 id、核验结论、总价与候选集合。返回空串表示复验通过;非空为降级 issue。
func (s *Service) replayVerificationIssue(ctx context.Context, result planning.Result) string {
	resolver, ok := s.store.(validate.Resolver)
	if !ok {
		return ""
	}
	if result.Validation == nil || result.Quote == nil {
		return "服务端复验：生成结果缺少核验报告或报价"
	}
	draft, err := schemas.DecodeBuildDraft(result.Draft)
	if err != nil {
		return "服务端复验：生成结果配置无法解析"
	}
	candidateIDs := make(map[string]bool, len(result.Candidates))
	for _, c := range result.Candidates {
		candidateIDs[c.ID] = true
	}
	for _, sku := range draft.Selection.SKUs() {
		if !candidateIDs[sku] {
			return "服务端复验：配置所选候选不在生成侧候选集合中"
		}
	}
	replay, err := validate.New(resolver).Evaluate(ctx, draft.Selection)
	if err != nil {
		// 目录在 run 期间变化(候选消失等)按复验不一致降级,不作为内部故障。
		log.Printf("[api] run 复验执行失败(按不一致降级):%v", err)
		return "服务端复验：目录数据与生成侧不一致，无法复现核验"
	}
	if replay.Quote.SnapshotID != result.CatalogSnapshotID {
		return "服务端复验：目录价格快照已更新，与生成侧使用的快照不一致"
	}
	if replay.Report.OverallStatus != result.Validation.OverallStatus {
		return "服务端复验：核验结论与生成侧不一致"
	}
	if replay.Quote.TotalCNY != result.Quote.TotalCNY {
		return "服务端复验：报价合计与生成侧不一致"
	}
	return ""
}

func (s *Service) completePlanning(ctx context.Context, r store.AgentRun, payload json.RawMessage, result planning.Result, before int) error {
	st, ok := s.store.(proposalStore)
	if !ok {
		return fmt.Errorf("proposal persistence unavailable")
	}
	// F2 第二步:A2A 只回控制面;重载荷按 run_id 从归档读回,入库副本经降级保持有界。
	s.mergeArchivedResult(ctx, r.ID, &result)
	// F5:交付真值前用产品 store 零 LLM 重放校验节点;与生成侧结论不一致时
	// 降级为 proposal 并附 issue,不新增版本。
	if result.Outcome == "ready" {
		if issue := s.replayVerificationIssue(ctx, result); issue != "" {
			result.Outcome = "proposal"
			result.Issues = append(result.Issues, issue)
			result.Delivery = &planning.Delivery{Status: "unresolved", Issues: []string{issue}}
			result.Reply = "服务端复验与生成侧结论不一致，本轮未新增正式版本；已有配置保持不变，可重试或补充资料后继续。"
		}
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return e
	}
	var input schemas.PlanningInput
	if e = json.Unmarshal(payload, &input); e != nil {
		return e
	}
	var build *store.SaveBuildVersionParams
	phase := store.PhaseRequirementReady
	switch result.Outcome {
	case "ready":
		var parent *int64
		if before > 0 {
			base, e := st.BuildByVersion(ctx, r.SessionID, before)
			if e != nil {
				return e
			}
			parent = &base.ID
		}
		validation, _ := json.Marshal(result.Validation)
		quote, _ := json.Marshal(result.Quote)
		snapshot, _ := json.Marshal(map[string]any{"candidates": result.Candidates, "evidence": result.Evidence, "assessments": result.Assessments, "assumptions": result.Assumptions, "reply": result.Reply})
		build = &store.SaveBuildVersionParams{SessionID: r.SessionID, ParentID: parent, RequirementSpec: payload, Draft: result.Draft, Validation: validation, Quote: quote, CandidateSnapshot: snapshot, RunID: r.ID, CatalogSnapshotID: result.CatalogSnapshotID}
		phase = store.PhaseReady
	case "collect", "clarify":
		phase = store.PhaseCollecting
	}
	// A proposal is a successful conversation outcome, not a generation outage.
	out, e := st.CompletePlanningRun(context.WithoutCancel(ctx), store.CompletePlanningParams{
		Completion: store.CompleteRunParams{RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(), AssistantContent: result.Reply, Status: store.RunSucceeded, Phase: phase,
			BuilderModel: builderIdentity(result.Builder),
			ModelCalls:   result.ModelCalls, ToolCalls: result.ToolCalls, SearchCalls: result.SearchCalls, PageCalls: result.PageCalls,
			Tokens: int(result.Tokens), DurationMS: result.DurationMS, CatalogSnapshotID: result.CatalogSnapshotID},
		Requirement: payload, Result: raw, ExpectedRevision: input.State.Revision, ParentVersion: before, Build: build,
	})
	if e != nil {
		return e
	}
	if out.Duplicate {
		return nil
	}
	s.captureEvidence(ctx, r.ID, "build_output", map[string]any{"planning_result": out.Result, "build_version": out.Version})
	if out.Message != nil {
		s.publish(ctx, r.ID, "assistant.completed", map[string]any{"message": messagePayload(*out.Message)})
	}
	if out.Version > 0 {
		s.publish(ctx, r.ID, "build.saved", map[string]any{"version": out.Version, "build_url": fmt.Sprintf("/api/v1/sessions/%s/builds/%d", r.SessionID, out.Version)})
	}
	s.publish(ctx, r.ID, "run.completed", map[string]any{"status": "succeeded"})
	return nil
}
