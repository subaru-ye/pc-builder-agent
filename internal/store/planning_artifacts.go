package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

type SavePlanningArtifactParams struct {
	RunID     string
	SessionID string
	Payload   json.RawMessage
}

// planning_artifacts 保存 planning 完整产物（含证据正文）；跨进程传输只走降级副本。
func (s *Store) SavePlanningArtifact(ctx context.Context, p SavePlanningArtifactParams) error {
	_, e := s.pool.Exec(ctx, `INSERT INTO planning_artifacts(run_id,session_id,payload,payload_bytes) VALUES($1,$2,$3,$4)
ON CONFLICT(run_id) DO UPDATE SET payload=EXCLUDED.payload,payload_bytes=EXCLUDED.payload_bytes`,
		p.RunID, p.SessionID, p.Payload, len(p.Payload))
	return e
}

// 无归档行返回 (nil, nil)。
func (s *Store) PlanningArtifact(ctx context.Context, runID string) (json.RawMessage, error) {
	var raw json.RawMessage
	e := s.pool.QueryRow(ctx, `SELECT payload FROM planning_artifacts WHERE run_id=$1`, runID).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	return raw, e
}
