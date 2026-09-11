package store

import (
	"context"
	"time"
)

// CandidateEvidence is read-only provenance from the published catalog. Planning
// copies it into its session snapshot; it never publishes external candidates.
type CandidateEvidence struct {
	ID, SKU, Field, URL, Text, Status string
	CapturedAt                        time.Time
}

func (s *Store) CandidateEvidence(ctx context.Context, skus []string) ([]CandidateEvidence, error) {
	rows, err := s.pool.Query(ctx, `SELECT evidence_id,sku,field_path,source_url,COALESCE(evidence_excerpt,''),evidence_status,captured_at FROM part_evidence WHERE sku=ANY($1) AND evidence_status IN ('verified','conflict') ORDER BY captured_at DESC LIMIT 128`, skus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CandidateEvidence{}
	for rows.Next() {
		var e CandidateEvidence
		if err := rows.Scan(&e.ID, &e.SKU, &e.Field, &e.URL, &e.Text, &e.Status, &e.CapturedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
