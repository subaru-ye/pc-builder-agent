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

// A chat command authorizes another planning pass after initial confirmation.
// Persist the updated state before invoking Builder, within the same run.
func (s *Service) planFromScreening(ctx context.Context, ownerID string, r store.AgentRun, state schemas.RequirementState, source schemas.RequirementSource) error {
	st, ok := s.store.(interface {
		ContinueScreeningRun(context.Context, string, string, string, schemas.RequirementState) (store.AgentRun, json.RawMessage, error)
	})
	if !ok {
		return fmt.Errorf("planning continuation unavailable")
	}
	next, pending, err := st.ContinueScreeningRun(ctx, ownerID, r.SessionID, r.ID, state)
	if err != nil {
		return err
	}
	var input schemas.PlanningInput
	if err = json.Unmarshal(pending, &input); err != nil {
		return err
	}
	input.Request = &source
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(state)
	s.publish(ctx, r.ID, "requirement.updated", json.RawMessage(raw))
	s.executeRemote(ctx, next, ownerID, payload)
	return nil
}

func (s *Service) planningContext(ctx context.Context, sessionID, runID string, payload json.RawMessage) (json.RawMessage, error) {
	var input schemas.PlanningInput
	if json.Unmarshal(payload, &input) != nil {
		return nil, fmt.Errorf("invalid planning input")
	}
	if input.SchemaVersion != 2 {
		input = schemas.PlanningInput{SchemaVersion: 2, State: schemas.LegacyPlanningState(payload)}
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
	if result.Outcome == "ready" {
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
	} else if result.Outcome == "collect" || result.Outcome == "clarify" {
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
