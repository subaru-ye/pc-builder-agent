package product

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"

	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type proposalStore interface {
	LatestProposal(context.Context, string) (json.RawMessage, error)
	CompletePlanningRun(context.Context, store.CompletePlanningParams) (store.PlanningCompletion, error)
	BuildByVersion(context.Context, string, int) (store.BuildVersion, error)
}

func (s *Service) planningContext(ctx context.Context, sessionID string, payload json.RawMessage) (json.RawMessage, error) {
	var input schemas.PlanningInput
	if json.Unmarshal(payload, &input) != nil {
		return nil, fmt.Errorf("invalid planning input")
	}
	if input.SchemaVersion != 2 {
		input = schemas.PlanningInput{SchemaVersion: 2, State: schemas.LegacyPlanningState(payload)}
	}
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
	}
	if len(previous) > 0 {
		_ = json.Unmarshal(previous, &saved)
		input.PreviousProposal = saved.Result
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

func (s *Service) completePlanning(ctx context.Context, r store.AgentRun, payload json.RawMessage, result planning.Result, before int) error {
	st, ok := s.store.(proposalStore)
	if !ok {
		return fmt.Errorf("proposal persistence unavailable")
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
		build = &store.SaveBuildVersionParams{SessionID: r.SessionID, ParentID: parent, RequirementSpec: payload, Draft: result.Draft, Validation: validation, Quote: quote, CandidateSnapshot: snapshot}
		phase = store.PhaseReady
	} else if result.Outcome == "collect" || result.Outcome == "clarify" {
		phase = store.PhaseCollecting
	}
	// A proposal is a successful conversation outcome, not a generation outage.
	out, e := st.CompletePlanningRun(context.WithoutCancel(ctx), store.CompletePlanningParams{
		Completion:  store.CompleteRunParams{RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(), AssistantContent: result.Reply, Status: store.RunSucceeded, Phase: phase},
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
