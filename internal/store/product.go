package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

var (
	ErrWebSessionNotFound  = errors.New("产品会话不存在")
	ErrRunNotFound         = errors.New("运行不存在")
	ErrSessionBusy         = errors.New("会话已有活动运行")
	ErrInvalidSessionPhase = errors.New("会话阶段不允许当前操作")
	ErrIdempotencyConflict = errors.New("幂等键已用于不同请求")
	ErrRequirementRevision = errors.New("需求已更新，请刷新后重试")
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
	StatusLabel               string
	ID                        string
	OwnerID                   string
	CreateRequestID           string
	Title                     string
	Archived                  bool
	Phase                     SessionPhase
	RecoveryPhase             *SessionPhase
	PendingRequirement        json.RawMessage
	RequirementState          json.RawMessage
	ConfirmedRequirementState json.RawMessage
	ConfirmedRequirement      json.RawMessage
	ConfirmedAt               *time.Time
	LastError                 json.RawMessage
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	VersionCount              int
}

type WebMessage struct {
	ID              string
	SessionID       string
	ClientMessageID *string
	Role            string
	Content         string
	DisplayContent  string
	BuildVersion    int
	RunID           *string
	CreatedAt       time.Time
}

// RunLifetime 是 run 的服务端寿命,产品侧 per-run ctx 超时(product.RunTimeout)与
// agent_runs.expires_at 共用该值;超时未终止的 running 行由 ReclaimStaleRuns 回收。
const RunLifetime = 10 * time.Minute

type AgentRun struct {
	ID                string
	SessionID         string
	ClientRequestID   string
	Kind              RunKind
	Status            RunStatus
	Error             json.RawMessage
	StartedAt         time.Time
	FinishedAt        *time.Time
	CancelRequestedAt *time.Time
	ExpiresAt         *time.Time
}

// InvalidPhaseError 保留服务端观察到的 phase，供 transport 返回当前状态。
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
       s.requirement_state, s.confirmed_requirement_state, s.confirmed_requirement, s.confirmed_at, s.archived,
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
		&s.RequirementState, &s.ConfirmedRequirementState, &s.ConfirmedRequirement, &s.ConfirmedAt, &s.Archived, &s.StatusLabel)
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
		          requirement_state, confirmed_requirement_state, confirmed_requirement, confirmed_at, archived, ''`, id, ownerID, requestID, state)
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

func scanAgentRun(row interface{ Scan(...any) error }) (AgentRun, error) {
	var r AgentRun
	err := row.Scan(&r.ID, &r.SessionID, &r.ClientRequestID, &r.Kind, &r.Status,
		&r.Error, &r.StartedAt, &r.FinishedAt, &r.CancelRequestedAt, &r.ExpiresAt)
	return r, err
}

const agentRunColumns = `id::text, session_id, client_request_id::text, kind, status,
       error, started_at, finished_at, cancel_requested_at, expires_at`

func (s *Store) RunByOwner(ctx context.Context, ownerID, runID string) (AgentRun, error) {
	r, err := scanAgentRun(s.pool.QueryRow(ctx, `
		SELECT r.id::text, r.session_id, r.client_request_id::text, r.kind, r.status,
		       r.error, r.started_at, r.finished_at, r.cancel_requested_at, r.expires_at
		FROM agent_runs r
		JOIN web_sessions s ON s.id = r.session_id
		WHERE r.id = $1 AND s.owner_id = $2`, runID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRun{}, ErrRunNotFound
	}
	if err != nil {
		return AgentRun{}, fmt.Errorf("store: 查询运行失败: %w", err)
	}
	return r, nil
}

// MessageRunByRequest 在执行上下文预检前识别消息重试；相同 key 不同文本仍返回冲突。
func (s *Store) MessageRunByRequest(ctx context.Context, ownerID, sessionID, requestID, text string, fingerprints ...string) (AgentRun, bool, error) {
	var r AgentRun
	var oldText, oldFingerprint string
	err := s.pool.QueryRow(ctx, `
		SELECT r.id::text, r.session_id, r.client_request_id::text, r.kind, r.status,
		       r.error, r.started_at, r.finished_at, r.cancel_requested_at, r.expires_at,
		       m.content, COALESCE(m.request_fingerprint, '')
		FROM agent_runs r
		JOIN web_sessions s ON s.id = r.session_id
		JOIN web_messages m ON m.session_id = r.session_id AND m.client_message_id = r.client_request_id
		WHERE r.session_id = $1 AND r.client_request_id = $2 AND s.owner_id = $3`,
		sessionID, requestID, ownerID).Scan(&r.ID, &r.SessionID, &r.ClientRequestID, &r.Kind,
		&r.Status, &r.Error, &r.StartedAt, &r.FinishedAt, &r.CancelRequestedAt, &r.ExpiresAt, &oldText, &oldFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRun{}, false, nil
	}
	if err != nil {
		return AgentRun{}, false, fmt.Errorf("store: 查询消息幂等运行失败: %w", err)
	}
	fingerprint := ""
	if len(fingerprints) > 0 {
		fingerprint = fingerprints[0]
	}
	if oldFingerprint != fingerprint || (fingerprint == "" && oldText != text) {
		return AgentRun{}, false, ErrIdempotencyConflict
	}
	return r, true, nil
}

func (s *Store) ActiveRun(ctx context.Context, sessionID string) (*AgentRun, error) {
	r, err := scanAgentRun(s.pool.QueryRow(ctx,
		`SELECT `+agentRunColumns+` FROM agent_runs WHERE session_id = $1 AND status = 'running'`, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: 查询活动运行失败: %w", err)
	}
	return &r, nil
}

func (s *Store) WebMessages(ctx context.Context, sessionID string) ([]WebMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id::text, m.session_id, m.client_message_id::text, m.role, m.content, m.run_id::text, m.created_at,
		       COALESCE(m.display_content,''), COALESCE(m.build_version, (e.payload->>'build_version')::int, 0)
		FROM web_messages m LEFT JOIN run_evidence e ON e.run_id=m.run_id AND e.slot='build_output'
		WHERE m.session_id = $1 AND m.role <> 'system' ORDER BY m.created_at, m.id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: 查询产品消息失败: %w", err)
	}
	defer rows.Close()
	out := make([]WebMessage, 0)
	for rows.Next() {
		var m WebMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.ClientMessageID, &m.Role,
			&m.Content, &m.RunID, &m.CreatedAt, &m.DisplayContent, &m.BuildVersion); err != nil {
			return nil, fmt.Errorf("store: 读取产品消息失败: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历产品消息失败: %w", err)
	}
	return out, nil
}

// ScreeningMessages 只返回同类初筛运行的稳定产品消息。它不会把 build 的
// assistant 交付文本或 ADK/A2A 内部事件带回下一次模型请求。
func (s *Store) ScreeningMessages(ctx context.Context, sessionID string, kind RunKind) ([]WebMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id::text, m.session_id, m.client_message_id::text, m.role, m.content, m.run_id::text, m.created_at
		FROM web_messages m
		JOIN agent_runs r ON r.id = m.run_id
		WHERE m.session_id = $1 AND r.kind = $2 AND m.role <> 'system'
		  AND (m.role = 'user' OR (r.status = 'succeeded' AND NOT EXISTS (
			SELECT 1 FROM builds b
			WHERE b.session_id = r.session_id AND b.created_at >= r.started_at
			  AND (r.finished_at IS NULL OR b.created_at <= r.finished_at)
		  )))
		ORDER BY m.created_at, m.id`, sessionID, kind)
	if err != nil {
		return nil, fmt.Errorf("store: 查询同类初筛消息失败: %w", err)
	}
	defer rows.Close()
	out := make([]WebMessage, 0)
	for rows.Next() {
		var m WebMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.ClientMessageID, &m.Role,
			&m.Content, &m.RunID, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: 读取同类初筛消息失败: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历同类初筛消息失败: %w", err)
	}
	return out, nil
}

type StartMessageRunParams struct {
	OwnerID, SessionID, RequestID, RunID, MessageID, Text, Title string
	ForceScreening                                               bool
	ExpectedRevision                                             *int
	RequestFingerprint                                           string
}

// StartMessageRun 在同一事务完成所有权/phase/幂等校验、user message 与 run 创建。
func (s *Store) StartMessageRun(ctx context.Context, p StartMessageRunParams) (AgentRun, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentRun{}, false, fmt.Errorf("store: 开启消息运行事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var phase SessionPhase
	var recovery *string
	var revision int
	if err := tx.QueryRow(ctx, `SELECT phase, recovery_phase, COALESCE((requirement_state->>'revision')::int, 0) FROM web_sessions
		WHERE id = $1 AND owner_id = $2 FOR UPDATE`, p.SessionID, p.OwnerID).Scan(&phase, &recovery, &revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentRun{}, false, ErrWebSessionNotFound
		}
		return AgentRun{}, false, fmt.Errorf("store: 锁定产品会话失败: %w", err)
	}

	if existing, found, err := runByRequestTx(ctx, tx, p.SessionID, p.RequestID); err != nil {
		return AgentRun{}, false, err
	} else if found {
		var oldText, oldFingerprint string
		err := tx.QueryRow(ctx, `SELECT content, COALESCE(request_fingerprint, '') FROM web_messages
			WHERE session_id = $1 AND client_message_id = $2`, p.SessionID, p.RequestID).Scan(&oldText, &oldFingerprint)
		if err != nil || oldFingerprint != p.RequestFingerprint || (p.RequestFingerprint == "" && oldText != p.Text) {
			return AgentRun{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}

	kind, next, clearPending, err := messageTransition(phase, recovery)
	if err != nil {
		return AgentRun{}, false, err
	}
	if p.ExpectedRevision != nil && *p.ExpectedRevision != revision {
		return AgentRun{}, false, ErrRequirementRevision
	}
	if p.ForceScreening {
		kind, next, clearPending = RunScreening, PhaseCollecting, false
	}
	r, err := insertRunTx(ctx, tx, p.RunID, p.SessionID, p.RequestID, kind)
	if isRunningConflict(err) {
		return AgentRun{}, false, ErrSessionBusy
	}
	if err != nil {
		return AgentRun{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO web_messages
		(id, session_id, client_message_id, role, content, run_id, request_fingerprint)
		VALUES ($1, $2, $3, 'user', $4, $5, NULLIF($6, ''))`, p.MessageID, p.SessionID, p.RequestID, p.Text, p.RunID, p.RequestFingerprint); err != nil {
		return AgentRun{}, false, fmt.Errorf("store: 写入用户消息失败: %w", err)
	}
	pendingExpr := "pending_requirement"
	if clearPending {
		pendingExpr = "NULL"
	}
	if _, err := tx.Exec(ctx, `UPDATE web_sessions SET phase = $2, recovery_phase = NULL,
		last_error = NULL, pending_requirement = `+pendingExpr+`,
		title = CASE WHEN title = '新会话' AND NOT title_custom THEN $3 ELSE title END, updated_at = now()
		WHERE id = $1`, p.SessionID, next, p.Title); err != nil {
		return AgentRun{}, false, fmt.Errorf("store: 更新消息运行阶段失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentRun{}, false, fmt.Errorf("store: 提交消息运行失败: %w", err)
	}
	return r, false, nil
}

func messageTransition(phase SessionPhase, recovery *string) (RunKind, SessionPhase, bool, error) {
	if phase == PhaseError {
		if recovery == nil {
			return "", "", false, &InvalidPhaseError{Phase: phase}
		}
		phase = SessionPhase(*recovery)
	}
	switch phase {
	case PhaseCollecting:
		return RunScreening, PhaseCollecting, false, nil
	case PhaseRequirementReady:
		return RunScreening, PhaseCollecting, true, nil
	case PhaseReady:
		return RunChange, PhaseChanging, false, nil
	default:
		return "", "", false, &InvalidPhaseError{Phase: phase}
	}
}

func runByRequestTx(ctx context.Context, tx pgx.Tx, sessionID, requestID string) (AgentRun, bool, error) {
	r, err := scanAgentRun(tx.QueryRow(ctx,
		`SELECT `+agentRunColumns+` FROM agent_runs WHERE session_id = $1 AND client_request_id = $2`,
		sessionID, requestID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRun{}, false, nil
	}
	if err != nil {
		return AgentRun{}, false, fmt.Errorf("store: 查询幂等运行失败: %w", err)
	}
	return r, true, nil
}

func insertRunTx(ctx context.Context, tx pgx.Tx, id, sessionID, requestID string, kind RunKind) (AgentRun, error) {
	r, err := scanAgentRun(tx.QueryRow(ctx, `INSERT INTO agent_runs
		(id, session_id, client_request_id, kind, status, expires_at)
		VALUES ($1, $2, $3, $4, 'running', now() + $5::interval) RETURNING `+agentRunColumns,
		id, sessionID, requestID, kind, fmt.Sprintf("%d seconds", int(RunLifetime.Seconds()))))
	if err != nil {
		return AgentRun{}, fmt.Errorf("store: 创建运行失败: %w", err)
	}
	return r, nil
}

func isRunningConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "agent_runs_one_running_per_session_idx"
}

type StartConfirmRunParams struct {
	OwnerID, SessionID, RequestID, RunID string
	ExpectedRevision                     *int
}

func (s *Store) StartConfirmRun(ctx context.Context, p StartConfirmRunParams) (AgentRun, json.RawMessage, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentRun{}, nil, false, fmt.Errorf("store: 开启确认运行事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var phase SessionPhase
	var recovery *string
	var pending json.RawMessage
	var requirementState json.RawMessage
	var revision int
	if err := tx.QueryRow(ctx, `SELECT phase, recovery_phase, pending_requirement, COALESCE((requirement_state->>'revision')::int, 0), requirement_state FROM web_sessions
		WHERE id = $1 AND owner_id = $2 FOR UPDATE`, p.SessionID, p.OwnerID).
		Scan(&phase, &recovery, &pending, &revision, &requirementState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentRun{}, nil, false, ErrWebSessionNotFound
		}
		return AgentRun{}, nil, false, fmt.Errorf("store: 锁定确认会话失败: %w", err)
	}
	if existing, found, err := runByRequestTx(ctx, tx, p.SessionID, p.RequestID); err != nil {
		return AgentRun{}, nil, false, err
	} else if found {
		if existing.Kind != RunBuild {
			return AgentRun{}, nil, false, ErrIdempotencyConflict
		}
		return existing, pending, true, nil
	}
	if p.ExpectedRevision != nil && *p.ExpectedRevision != revision {
		return AgentRun{}, nil, false, ErrRequirementRevision
	}
	allowed := phase == PhaseRequirementReady || (phase == PhaseError && recovery != nil && SessionPhase(*recovery) == PhaseRequirementReady)
	if !allowed || len(pending) == 0 {
		return AgentRun{}, nil, false, &InvalidPhaseError{Phase: phase}
	}
	// 锁内核验投影，先处理已完成请求重试，再校验本次要确认的新草稿。
	if len(requirementState) > 0 {
		var state schemas.RequirementState
		if err := json.Unmarshal(requirementState, &state); err != nil {
			return AgentRun{}, nil, false, ErrRequirementRevision
		}
		spec, missing, err := planningProjection(state)
		var header struct {
			SchemaVersion int `json:"schema_version"`
		}
		_ = json.Unmarshal(pending, &header)
		if header.SchemaVersion == 1 && err == nil {
			// Explicit confirmation upgrades this draft only. Historical builds
			// retain their original requirement and candidate snapshots.
			pending = spec
			if _, err := tx.Exec(ctx, `UPDATE web_sessions SET pending_requirement = $2 WHERE id = $1`, p.SessionID, pending); err != nil {
				return AgentRun{}, nil, false, err
			}
		}
		var actual, proposed any
		if err != nil || len(missing) > 0 || json.Unmarshal(spec, &proposed) != nil || json.Unmarshal(pending, &actual) != nil || !reflect.DeepEqual(actual, proposed) {
			return AgentRun{}, nil, false, ErrRequirementRevision
		}
	}
	r, err := insertRunTx(ctx, tx, p.RunID, p.SessionID, p.RequestID, RunBuild)
	if isRunningConflict(err) {
		return AgentRun{}, nil, false, ErrSessionBusy
	}
	if err != nil {
		return AgentRun{}, nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE web_sessions SET phase = 'building', recovery_phase = NULL,
		confirmed_requirement = pending_requirement, confirmed_requirement_state = requirement_state,
		confirmed_at = now(), last_error = NULL, updated_at = now() WHERE id = $1`, p.SessionID); err != nil {
		return AgentRun{}, nil, false, fmt.Errorf("store: 更新确认阶段失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentRun{}, nil, false, fmt.Errorf("store: 提交确认运行失败: %w", err)
	}
	return r, pending, false, nil
}

func (s *Store) ReplacePendingRequirement(ctx context.Context, ownerID, sessionID string, spec json.RawMessage) error {
	cmd, err := s.pool.Exec(ctx, `UPDATE web_sessions SET pending_requirement = $3,
		phase = 'requirement_ready', recovery_phase = NULL, last_error = NULL, updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND
		(phase = 'requirement_ready' OR (phase = 'error' AND recovery_phase = 'requirement_ready'))`,
		sessionID, ownerID, spec)
	if err != nil {
		return fmt.Errorf("store: 更新待确认需求失败: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		if _, err := s.WebSessionByOwner(ctx, ownerID, sessionID); err != nil {
			return err
		}
		return ErrInvalidSessionPhase
	}
	return nil
}

type CompleteRunParams struct {
	RunID, SessionID, AssistantMessageID, AssistantContent string
	DisplayContent                                         string
	BuildVersion                                           int
	Status                                                 RunStatus
	Phase                                                  SessionPhase
	RecoveryPhase                                          *SessionPhase
	PendingRequirement                                     json.RawMessage
	SetPending                                             bool
	RequirementState                                       json.RawMessage
	SetRequirementState                                    bool
	Error                                                  json.RawMessage
	// F3 可观测性:模型身份与计量沿用 planning.Result 口径;0/空写 NULL(未知 ≠ 零)。
	ScreeningModel, BuilderModel string
	ModelCalls                   int
	ToolCalls                    int
	SearchCalls                  int
	PageCalls                    int
	Tokens                       int
	DurationMS                   int64
	CatalogSnapshotID            int64
	RetryCount                   int
}

// CompleteRun 原子完成 run、可选 assistant 消息与产品会话最终状态。
func (s *Store) CompleteRun(ctx context.Context, p CompleteRunParams) (*WebMessage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: 开启完成运行事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return s.completeRunStandalone(ctx, tx, p)
}

func (s *Store) completeRunStandalone(ctx context.Context, tx pgx.Tx, p CompleteRunParams) (*WebMessage, error) {
	message, err := completeRunTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: 提交运行结果失败: %w", err)
	}
	return message, nil
}

func completeRunTx(ctx context.Context, tx pgx.Tx, p CompleteRunParams) (*WebMessage, error) {
	cmd, err := tx.Exec(ctx, `UPDATE agent_runs SET status = $2, error = $3, finished_at = now(),
		screening_model = NULLIF($5,''), builder_model = NULLIF($6,''),
		model_calls = NULLIF($7,0), tool_calls = NULLIF($8,0), search_calls = NULLIF($9,0),
		page_calls = NULLIF($10,0), tokens = NULLIF($11,0), duration_ms = NULLIF($12,0),
		catalog_snapshot_id = NULLIF($13,0), retry_count = NULLIF($14,0)
		WHERE id = $1 AND session_id = $4 AND status = 'running'`,
		p.RunID, p.Status, nullableJSON(p.Error), p.SessionID,
		p.ScreeningModel, p.BuilderModel, p.ModelCalls, p.ToolCalls, p.SearchCalls,
		p.PageCalls, p.Tokens, p.DurationMS, p.CatalogSnapshotID, p.RetryCount)
	if err != nil {
		return nil, fmt.Errorf("store: 完成运行失败: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return nil, ErrRunNotFound
	}
	var message *WebMessage
	if p.AssistantContent != "" {
		var m WebMessage
		err := tx.QueryRow(ctx, `INSERT INTO web_messages (id, session_id, role, content, run_id, display_content, build_version)
			VALUES ($1, $2, 'assistant', $3, $4, NULLIF($5,''), NULLIF($6,0))
			RETURNING id::text, session_id, client_message_id::text, role, content, run_id::text, created_at`,
			p.AssistantMessageID, p.SessionID, p.AssistantContent, p.RunID, p.DisplayContent, p.BuildVersion).
			Scan(&m.ID, &m.SessionID, &m.ClientMessageID, &m.Role, &m.Content, &m.RunID, &m.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("store: 写入 assistant 消息失败: %w", err)
		}
		message = &m
		message.DisplayContent, message.BuildVersion = p.DisplayContent, p.BuildVersion
	}
	_, err = tx.Exec(ctx, `UPDATE web_sessions SET phase = $2, recovery_phase = $3,
		last_error = $4, pending_requirement = CASE WHEN $5 THEN $6 ELSE pending_requirement END,
		requirement_state = CASE WHEN $7 THEN $8 ELSE requirement_state END,
		updated_at = now() WHERE id = $1`, p.SessionID, p.Phase, p.RecoveryPhase,
		nullableJSON(p.Error), p.SetPending, nullableJSON(p.PendingRequirement),
		p.SetRequirementState, nullableJSON(p.RequirementState))
	if err != nil {
		return nil, fmt.Errorf("store: 更新运行最终阶段失败: %w", err)
	}
	return message, nil
}

type InterruptedRun struct {
	AgentRun
	RecoveryPhase SessionPhase
}

func (s *Store) InterruptRunning(ctx context.Context, problem json.RawMessage) ([]InterruptedRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: 开启中断恢复事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT `+agentRunColumns+` FROM agent_runs WHERE status = 'running' FOR UPDATE`)
	if err != nil {
		return nil, fmt.Errorf("store: 查询遗留运行失败: %w", err)
	}
	var out []InterruptedRun
	for rows.Next() {
		r, err := scanAgentRun(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: 读取遗留运行失败: %w", err)
		}
		recovery := recoveryPhaseForKind(r.Kind)
		out = append(out, InterruptedRun{AgentRun: r, RecoveryPhase: recovery})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历遗留运行失败: %w", err)
	}
	for _, item := range out {
		if _, err := tx.Exec(ctx, `UPDATE agent_runs SET status = 'interrupted', error = $2,
			finished_at = now() WHERE id = $1`, item.ID, problem); err != nil {
			return nil, fmt.Errorf("store: 标记运行中断失败: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE web_sessions SET phase = 'error', recovery_phase = $2,
			last_error = $3, updated_at = now() WHERE id = $1`, item.SessionID, item.RecoveryPhase, problem); err != nil {
			return nil, fmt.Errorf("store: 标记会话中断失败: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: 提交中断恢复失败: %w", err)
	}
	return out, nil
}

// RequestRunCancel 置位用户取消标志;仅 running 行可置位,重复请求保持幂等。
// 返回的 requested=false 表示 run 已处于终态(调用方可原样返回当前状态)。
func (s *Store) RequestRunCancel(ctx context.Context, ownerID, sessionID, runID string) (AgentRun, bool, error) {
	r, err := scanAgentRun(s.pool.QueryRow(ctx, `
		UPDATE agent_runs r SET cancel_requested_at = now()
		FROM web_sessions s
		WHERE r.id = $1 AND r.session_id = $2 AND s.id = r.session_id AND s.owner_id = $3
		  AND r.status = 'running' AND r.cancel_requested_at IS NULL
		RETURNING r.id::text, r.session_id, r.client_request_id::text, r.kind, r.status,
		       r.error, r.started_at, r.finished_at, r.cancel_requested_at, r.expires_at`, runID, sessionID, ownerID))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return AgentRun{}, false, fmt.Errorf("store: 请求取消运行失败: %w", err)
		}
		current, e := s.runByOwnerSession(ctx, ownerID, sessionID, runID)
		if e != nil {
			return AgentRun{}, false, e
		}
		return current, current.Status == RunRunning && current.CancelRequestedAt != nil, nil
	}
	return r, true, nil
}

func (s *Store) runByOwnerSession(ctx context.Context, ownerID, sessionID, runID string) (AgentRun, error) {
	r, err := scanAgentRun(s.pool.QueryRow(ctx, `
		SELECT r.id::text, r.session_id, r.client_request_id::text, r.kind, r.status,
		       r.error, r.started_at, r.finished_at, r.cancel_requested_at, r.expires_at
		FROM agent_runs r
		JOIN web_sessions s ON s.id = r.session_id
		WHERE r.id = $1 AND r.session_id = $2 AND s.owner_id = $3`, runID, sessionID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRun{}, ErrRunNotFound
	}
	if err != nil {
		return AgentRun{}, fmt.Errorf("store: 查询运行失败: %w", err)
	}
	return r, nil
}

// ReclaimStaleRuns 把已请求取消或超过 expires_at 的 running 行落成 interrupted,
// 并把对应会话置回可恢复的 error 阶段。sessionID 为空时扫描全部会话。
// 这是进程内取消路径失效(进程被杀)后的兜底,正常取消由服务端本地 cancel 完成。
func (s *Store) ReclaimStaleRuns(ctx context.Context, sessionID string) ([]InterruptedRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: 开启过期运行回收事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := `SELECT ` + agentRunColumns + ` FROM agent_runs WHERE status = 'running'
		AND (cancel_requested_at IS NOT NULL OR expires_at < now())`
	args := []any{}
	if sessionID != "" {
		query += ` AND session_id = $1`
		args = append(args, sessionID)
	}
	rows, err := tx.Query(ctx, query+` FOR UPDATE`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: 查询过期运行失败: %w", err)
	}
	var out []InterruptedRun
	for rows.Next() {
		r, err := scanAgentRun(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: 读取过期运行失败: %w", err)
		}
		recovery := recoveryPhaseForKind(r.Kind)
		out = append(out, InterruptedRun{AgentRun: r, RecoveryPhase: recovery})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历过期运行失败: %w", err)
	}
	for _, item := range out {
		problem := staleRunProblem(item.AgentRun)
		if _, err := tx.Exec(ctx, `UPDATE agent_runs SET status = 'interrupted', error = $2,
			finished_at = now() WHERE id = $1`, item.ID, problem); err != nil {
			return nil, fmt.Errorf("store: 标记过期运行中断失败: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE web_sessions SET phase = 'error', recovery_phase = $2,
			last_error = $3, updated_at = now() WHERE id = $1`, item.SessionID, item.RecoveryPhase, problem); err != nil {
			return nil, fmt.Errorf("store: 标记会话中断失败: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: 提交过期运行回收失败: %w", err)
	}
	return out, nil
}

func recoveryPhaseForKind(kind RunKind) SessionPhase {
	switch kind {
	case RunBuild:
		return PhaseRequirementReady
	case RunChange:
		return PhaseReady
	}
	return PhaseCollecting
}

func staleRunProblem(r AgentRun) json.RawMessage {
	if r.CancelRequestedAt != nil {
		return staleCancelProblem
	}
	return staleExpiredProblem
}

var (
	staleCancelProblem  = json.RawMessage(`{"type":"/problems/run_cancelled","title":"已停止本次生成","status":409,"code":"run_cancelled","detail":"本次生成已取消，此前的对话与数据保持不变，可重新发起请求。"}`)
	staleExpiredProblem = json.RawMessage(`{"type":"/problems/run_interrupted","title":"运行已中断","status":409,"code":"run_interrupted","detail":"运行超过时限已回收，此前的对话与数据保持不变，请使用新请求重试。"}`)
)

func (s *Store) LatestBuildVersion(ctx context.Context, sessionID string) (int, bool, error) {
	var v *int
	if err := s.pool.QueryRow(ctx, `SELECT max(version) FROM builds WHERE session_id = $1`, sessionID).Scan(&v); err != nil {
		return 0, false, fmt.Errorf("store: 查询最新配置版本失败: %w", err)
	}
	if v == nil {
		return 0, false, nil
	}
	return *v, true, nil
}

func planningProjection(state schemas.RequirementState) (json.RawMessage, []string, error) {
	raw, err := schemas.PlanningRequirement(state)
	return raw, []string{}, err
}
