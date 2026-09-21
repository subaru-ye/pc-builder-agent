package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
)

func (s *Store) SaveProposal(ctx context.Context, sessionID, runID string, requirement, result json.RawMessage, parent int) error {
	_, e := s.pool.Exec(ctx, `INSERT INTO session_proposals(session_id,run_id,requirement,result,parent_version) VALUES($1,$2,$3,$4,$5) ON CONFLICT(run_id) DO NOTHING`, sessionID, runID, requirement, result, parent)
	return e
}
func (s *Store) LatestProposal(ctx context.Context, sessionID string) (json.RawMessage, error) {
	var raw json.RawMessage
	e := s.pool.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'run_id',run_id,'requirement',requirement,'parent_version',parent_version,'created_at',created_at,'result',result) FROM session_proposals WHERE session_id=$1 ORDER BY id DESC LIMIT 1`, sessionID).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	return raw, e
}

// Account usage includes the background collector; reservations account for
// concurrent requests before the provider's account endpoint catches up.
func (s *Store) ReserveSearch(ctx context.Context, usage, budget int) error {
	var reserved int
	e := s.pool.QueryRow(ctx, `INSERT INTO search_quota(month,reserved) SELECT to_char(now() AT TIME ZONE 'UTC','YYYY-MM'),$1+1 WHERE $1<$2
 ON CONFLICT(month) DO UPDATE SET reserved=GREATEST(search_quota.reserved,$1)+1,updated_at=now()
 WHERE GREATEST(search_quota.reserved,$1)<$2 RETURNING reserved`, usage, budget).Scan(&reserved)
	if errors.Is(e, pgx.ErrNoRows) {
		return fmt.Errorf("search quota exhausted")
	}
	return e
}
