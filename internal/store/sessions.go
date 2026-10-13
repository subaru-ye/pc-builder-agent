package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type SessionPhase string

const (
	PhaseCollecting       SessionPhase = "collecting"
	PhaseRequirementReady SessionPhase = "requirement_ready"
	PhaseBuilding         SessionPhase = "building"
	PhaseReady            SessionPhase = "ready"
	PhaseChanging         SessionPhase = "changing"
	PhaseError            SessionPhase = "error"
)

type RunKind string

const (
	RunScreening RunKind = "screening"
	RunBuild     RunKind = "build"
	RunChange    RunKind = "change"
)

type RunStatus string

const (
	RunRunning     RunStatus = "running"
	RunSucceeded   RunStatus = "succeeded"
	RunFailed      RunStatus = "failed"
	RunInterrupted RunStatus = "interrupted"
)

type WebSession struct {
	StatusLabel        string
	ID                 string
	OwnerID            string
	CreateRequestID    string
	Title              string
	Archived           bool
	Phase              SessionPhase
	RecoveryPhase      *SessionPhase
	PendingRequirement json.RawMessage
	RequirementState   json.RawMessage
	// ConfirmationID 指向当前确认快照(requirement_confirmations);空表示从未确认。
	ConfirmationID string
	LastError      json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
	VersionCount   int
}

type InvalidPhaseError struct{ Phase SessionPhase }

func (e *InvalidPhaseError) Error() string {
	return fmt.Sprintf("%v:当前为 %s", ErrInvalidSessionPhase, e.Phase)
}

func (e *InvalidPhaseError) Unwrap() error { return ErrInvalidSessionPhase }

func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

const webSessionColumns = `s.id, s.owner_id, s.create_request_id::text, s.title, s.phase,
       s.recovery_phase, s.pending_requirement, s.last_error, s.created_at, s.updated_at,
       (SELECT count(*) FROM builds b WHERE b.session_id = s.id),
       s.requirement_state, COALESCE(s.confirmation_id::text, ''), s.archived,
       CASE WHEN s.phase='requirement_ready' AND EXISTS (
           SELECT 1 FROM session_proposals p WHERE p.id=(SELECT max(p2.id) FROM session_proposals p2 WHERE p2.session_id=s.id)
           AND p.result->>'outcome'='proposal'
           AND p.requirement->'requirement_state'->>'revision'=s.requirement_state->>'revision'
       ) THEN '方案待完善' ELSE '' END`

func scanWebSession(row interface{ Scan(...any) error }) (WebSession, error) {
	var (
		s        WebSession
		recovery *string
	)
	err := row.Scan(&s.ID, &s.OwnerID, &s.CreateRequestID, &s.Title, &s.Phase,
		&recovery, &s.PendingRequirement, &s.LastError, &s.CreatedAt, &s.UpdatedAt, &s.VersionCount,
		&s.RequirementState, &s.ConfirmationID, &s.Archived, &s.StatusLabel)
	if recovery != nil {
		p := SessionPhase(*recovery)
		s.RecoveryPhase = &p
	}
	return s, err
}

func (s *Store) CreateWebSession(ctx context.Context, id, ownerID, requestID string) (WebSession, error) {
	state, err := json.Marshal(schemas.NewRequirementState())
	if err != nil {
		return WebSession{}, fmt.Errorf("store: 初始化需求状态失败: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO web_sessions (id, owner_id, create_request_id, phase, requirement_state)
		VALUES ($1, $2, $3, 'collecting', $4)
		ON CONFLICT (owner_id, create_request_id)
		DO UPDATE SET create_request_id = EXCLUDED.create_request_id
		RETURNING id, owner_id, create_request_id::text, title, phase, recovery_phase,
		          pending_requirement, last_error, created_at, updated_at, 0,
		          requirement_state, COALESCE(confirmation_id::text, ''), archived, ''`, id, ownerID, requestID, state)
	ws, err := scanWebSession(row)
	if err != nil {
		return WebSession{}, fmt.Errorf("store: 创建产品会话失败: %w", err)
	}
	return ws, nil
}

func (s *Store) WebSessionByOwner(ctx context.Context, ownerID, sessionID string) (WebSession, error) {
	ws, err := scanWebSession(s.pool.QueryRow(ctx,
		`SELECT `+webSessionColumns+` FROM web_sessions s WHERE s.id = $1 AND s.owner_id = $2`,
		sessionID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return WebSession{}, ErrWebSessionNotFound
	}
	if err != nil {
		return WebSession{}, fmt.Errorf("store: 查询产品会话失败: %w", err)
	}
	return ws, nil
}

func (s *Store) WebSessionsByOwner(ctx context.Context, ownerID string) ([]WebSession, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+webSessionColumns+` FROM web_sessions s WHERE s.owner_id = $1 ORDER BY s.updated_at DESC, s.id`,
		ownerID)
	if err != nil {
		return nil, fmt.Errorf("store: 列出产品会话失败: %w", err)
	}
	defer rows.Close()
	out := make([]WebSession, 0)
	for rows.Next() {
		ws, err := scanWebSession(rows)
		if err != nil {
			return nil, fmt.Errorf("store: 读取产品会话失败: %w", err)
		}
		out = append(out, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历产品会话失败: %w", err)
	}
	return out, nil
}
