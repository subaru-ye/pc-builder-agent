package store

import (
	"context"
	"encoding/json"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// ContinueScreeningRun moves an explicitly authorized chat request into planning
// without ending its run or opening a race for a second message/confirmation.
// Historical builds and their requirement snapshots remain immutable.
func (s *Store) ContinueScreeningRun(ctx context.Context, ownerID, sessionID, runID string, state schemas.RequirementState) (AgentRun, json.RawMessage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentRun{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revision int
	var confirmed, hasBuild bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE((requirement_state->>'revision')::int,0), confirmed_requirement IS NOT NULL,
		EXISTS(SELECT 1 FROM builds WHERE session_id=$1) FROM web_sessions WHERE id=$1 AND owner_id=$2 FOR UPDATE`, sessionID, ownerID).Scan(&revision, &confirmed, &hasBuild); err != nil {
		return AgentRun{}, nil, err
	}
	if !confirmed {
		return AgentRun{}, nil, ErrInvalidSessionPhase
	}
	if state.Revision != revision+1 {
		return AgentRun{}, nil, ErrRequirementRevision
	}
	kind, phase := RunBuild, PhaseBuilding
	if hasBuild {
		kind, phase = RunChange, PhaseChanging
	}
	r, err := scanAgentRun(tx.QueryRow(ctx, `UPDATE agent_runs SET kind=$3 WHERE id=$1 AND session_id=$2 AND kind='screening' AND status='running' RETURNING `+agentRunColumns, runID, sessionID, kind))
	if err != nil {
		return AgentRun{}, nil, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return AgentRun{}, nil, err
	}
	pending, err := schemas.PlanningRequirement(state)
	if err != nil {
		return AgentRun{}, nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE web_sessions SET requirement_state=$2,pending_requirement=$3,
		confirmed_requirement_state=$2,confirmed_requirement=$3,confirmed_at=now(),
		phase=$4,recovery_phase=NULL,last_error=NULL,updated_at=now() WHERE id=$1`, sessionID, raw, pending, phase)
	if err != nil {
		return AgentRun{}, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AgentRun{}, nil, err
	}
	return r, pending, nil
}
