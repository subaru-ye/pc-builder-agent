package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type CompletePlanningParams struct {
	Completion                      CompleteRunParams
	Requirement, Result             json.RawMessage
	ExpectedRevision, ParentVersion int
	Build                           *SaveBuildVersionParams
}

type PlanningCompletion struct {
	Result    json.RawMessage
	Message   *WebMessage
	Version   int
	Duplicate bool
}

// CompletePlanningRun commits the proposal, optional immutable version, message
// and run status together. The run row serializes repeat deliveries; the session
// row guards against editing the requirement while this result is being saved.
func (s *Store) CompletePlanningRun(ctx context.Context, p CompletePlanningParams) (PlanningCompletion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlanningCompletion{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current json.RawMessage
	var phase SessionPhase
	if err = tx.QueryRow(ctx, `SELECT requirement_state,phase FROM web_sessions WHERE id=$1 FOR UPDATE`, p.Completion.SessionID).Scan(&current, &phase); err != nil {
		return PlanningCompletion{}, err
	}
	var status RunStatus
	if err = tx.QueryRow(ctx, `SELECT status FROM agent_runs WHERE id=$1 AND session_id=$2 FOR UPDATE`, p.Completion.RunID, p.Completion.SessionID).Scan(&status); err != nil {
		return PlanningCompletion{}, err
	}
	if status != RunRunning {
		var out PlanningCompletion
		err = tx.QueryRow(ctx, `SELECT result,COALESCE((result->>'build_version')::int,0) FROM session_proposals WHERE run_id=$1`, p.Completion.RunID).Scan(&out.Result, &out.Version)
		out.Duplicate = true
		return out, err
	}
	var state schemas.RequirementState
	if len(current) > 0 && json.Unmarshal(current, &state) != nil {
		return PlanningCompletion{}, fmt.Errorf("invalid current requirement state")
	}
	var result map[string]json.RawMessage
	if err = json.Unmarshal(p.Result, &result); err != nil {
		return PlanningCompletion{}, err
	}
	set := func(key string, value any) { result[key], _ = json.Marshal(value) }
	if state.Revision != p.ExpectedRevision {
		p.Build = nil
		var issues []string
		_ = json.Unmarshal(result["issues"], &issues)
		issues = append(issues, "需求已更新，本方案基于先前需求；请按当前需求继续选配")
		set("outcome", "proposal")
		set("issues", issues)
		set("delivery", map[string]any{"status": "stale", "issues": issues})
		p.Completion.AssistantContent = "本轮方案已保存，但需求已有更新，未替换正式配置。请核对当前需求后继续选配。"
		set("reply", p.Completion.AssistantContent)
		p.Completion.Phase = phase
		if phase == PhaseBuilding || phase == PhaseChanging {
			p.Completion.Phase = PhaseRequirementReady
		}
	}
	var buildID *int64
	version := 0
	if p.Build != nil {
		if p.Build.SessionID != p.Completion.SessionID {
			return PlanningCompletion{}, fmt.Errorf("build session mismatch")
		}
		out, e := saveBuildVersionTx(ctx, tx, *p.Build)
		if e != nil {
			return PlanningCompletion{}, e
		}
		version, buildID = out.Version, &out.ID
		p.Completion.BuildVersion = version
		p.Completion.Phase = PhaseReady
		set("build_version", version)
		set("delivery", map[string]any{"status": "delivered", "issues": []string{}})
		p.Completion.AssistantContent = fmt.Sprintf("配置已保存为 v%d。\n\n%s", version, p.Completion.AssistantContent)
		set("reply", p.Completion.AssistantContent)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return PlanningCompletion{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO session_proposals(session_id,run_id,requirement,result,parent_version,build_id) VALUES($1,$2,$3,$4,$5,$6)`, p.Completion.SessionID, p.Completion.RunID, p.Requirement, raw, p.ParentVersion, buildID); err != nil {
		return PlanningCompletion{}, err
	}
	// The finalized reply is already user-facing; legacy read-time formatting must
	// not replace it with a different summary after refresh.
	p.Completion.DisplayContent = p.Completion.AssistantContent
	message, err := completeRunTx(ctx, tx, p.Completion)
	if err != nil {
		return PlanningCompletion{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlanningCompletion{}, err
	}
	return PlanningCompletion{Result: raw, Message: message, Version: version}, nil
}
