package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrWebSessionNotFound  = errors.New("产品会话不存在")
	ErrRunNotFound         = errors.New("运行不存在")
	ErrSessionBusy         = errors.New("会话已有活动运行")
	ErrInvalidSessionPhase = errors.New("会话阶段不允许当前操作")
	ErrIdempotencyConflict = errors.New("幂等键已用于不同请求")
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
	ID                 string
	OwnerID            string
	CreateRequestID    string
	Title              string
	Phase              SessionPhase
	RecoveryPhase      *SessionPhase
	PendingRequirement json.RawMessage
	LastError          json.RawMessage
	CreatedAt          time.Time
	UpdatedAt          time.Time
	VersionCount       int
}

type WebMessage struct {
	ID              string
	SessionID       string
	ClientMessageID *string
	Role            string
	Content         string
	RunID           *string
	CreatedAt       time.Time
}

type AgentRun struct {
	ID              string
	SessionID       string
	ClientRequestID string
	Kind            RunKind
	Status          RunStatus
	Error           json.RawMessage
	StartedAt       time.Time
	FinishedAt      *time.Time
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
       (SELECT count(*) FROM builds b WHERE b.session_id = s.id)`

func scanWebSession(row interface{ Scan(...any) error }) (WebSession, error) {
	var (
		s        WebSession
		recovery *string
	)
	err := row.Scan(&s.ID, &s.OwnerID, &s.CreateRequestID, &s.Title, &s.Phase,
		&recovery, &s.PendingRequirement, &s.LastError, &s.CreatedAt, &s.UpdatedAt, &s.VersionCount)
	if recovery != nil {
		p := SessionPhase(*recovery)
		s.RecoveryPhase = &p
	}
	return s, err
}

func (s *Store) CreateWebSession(ctx context.Context, id, ownerID, requestID string) (WebSession, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO web_sessions (id, owner_id, create_request_id, phase)
		VALUES ($1, $2, $3, 'collecting')
		ON CONFLICT (owner_id, create_request_id)
		DO UPDATE SET create_request_id = EXCLUDED.create_request_id
		RETURNING id, owner_id, create_request_id::text, title, phase, recovery_phase,
		          pending_requirement, last_error, created_at, updated_at, 0`, id, ownerID, requestID)
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
		&r.Error, &r.StartedAt, &r.FinishedAt)
	return r, err
}

const agentRunColumns = `id::text, session_id, client_request_id::text, kind, status,
       error, started_at, finished_at`

func (s *Store) RunByOwner(ctx context.Context, ownerID, runID string) (AgentRun, error) {
	r, err := scanAgentRun(s.pool.QueryRow(ctx, `
		SELECT `+agentRunColumns+` FROM agent_runs r
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
		SELECT id::text, session_id, client_message_id::text, role, content, run_id::text, created_at
		FROM web_messages WHERE session_id = $1 AND role <> 'system' ORDER BY created_at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: 查询产品消息失败: %w", err)
	}
	defer rows.Close()
	out := make([]WebMessage, 0)
	for rows.Next() {
		var m WebMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.ClientMessageID, &m.Role,
			&m.Content, &m.RunID, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: 读取产品消息失败: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历产品消息失败: %w", err)
	}
	return out, nil
}

type StartMessageRunParams struct {
	OwnerID, SessionID, RequestID, RunID, MessageID, Text, Title string
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
	if err := tx.QueryRow(ctx, `SELECT phase, recovery_phase FROM web_sessions
		WHERE id = $1 AND owner_id = $2 FOR UPDATE`, p.SessionID, p.OwnerID).Scan(&phase, &recovery); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentRun{}, false, ErrWebSessionNotFound
		}
		return AgentRun{}, false, fmt.Errorf("store: 锁定产品会话失败: %w", err)
	}

	if existing, found, err := runByRequestTx(ctx, tx, p.SessionID, p.RequestID); err != nil {
		return AgentRun{}, false, err
	} else if found {
		var oldText string
		err := tx.QueryRow(ctx, `SELECT content FROM web_messages
			WHERE session_id = $1 AND client_message_id = $2`, p.SessionID, p.RequestID).Scan(&oldText)
		if err != nil || oldText != p.Text {
			return AgentRun{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}

	kind, next, clearPending, err := messageTransition(phase, recovery)
	if err != nil {
		return AgentRun{}, false, err
	}
	r, err := insertRunTx(ctx, tx, p.RunID, p.SessionID, p.RequestID, kind)
	if isRunningConflict(err) {
		return AgentRun{}, false, ErrSessionBusy
	}
	if err != nil {
		return AgentRun{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO web_messages
		(id, session_id, client_message_id, role, content, run_id)
		VALUES ($1, $2, $3, 'user', $4, $5)`, p.MessageID, p.SessionID, p.RequestID, p.Text, p.RunID); err != nil {
		return AgentRun{}, false, fmt.Errorf("store: 写入用户消息失败: %w", err)
	}
	pendingExpr := "pending_requirement"
	if clearPending {
		pendingExpr = "NULL"
	}
	if _, err := tx.Exec(ctx, `UPDATE web_sessions SET phase = $2, recovery_phase = NULL,
		last_error = NULL, pending_requirement = `+pendingExpr+`,
		title = CASE WHEN title = '新会话' THEN $3 ELSE title END, updated_at = now()
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
		(id, session_id, client_request_id, kind, status)
		VALUES ($1, $2, $3, $4, 'running') RETURNING `+agentRunColumns,
		id, sessionID, requestID, kind))
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
	if err := tx.QueryRow(ctx, `SELECT phase, recovery_phase, pending_requirement FROM web_sessions
		WHERE id = $1 AND owner_id = $2 FOR UPDATE`, p.SessionID, p.OwnerID).
		Scan(&phase, &recovery, &pending); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentRun{}, nil, false, ErrWebSessionNotFound
		}
		return AgentRun{}, nil, false, fmt.Errorf("store: 锁定确认会话失败: %w", err)
	}
	if existing, found, err := runByRequestTx(ctx, tx, p.SessionID, p.RequestID); err != nil {
		return AgentRun{}, nil, false, err
	} else if found {
		return existing, pending, true, nil
	}
	allowed := phase == PhaseRequirementReady || (phase == PhaseError && recovery != nil && SessionPhase(*recovery) == PhaseRequirementReady)
	if !allowed || len(pending) == 0 {
		return AgentRun{}, nil, false, &InvalidPhaseError{Phase: phase}
	}
	r, err := insertRunTx(ctx, tx, p.RunID, p.SessionID, p.RequestID, RunBuild)
	if isRunningConflict(err) {
		return AgentRun{}, nil, false, ErrSessionBusy
	}
	if err != nil {
		return AgentRun{}, nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE web_sessions SET phase = 'building', recovery_phase = NULL,
		last_error = NULL, updated_at = now() WHERE id = $1`, p.SessionID); err != nil {
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
	Status                                                 RunStatus
	Phase                                                  SessionPhase
	RecoveryPhase                                          *SessionPhase
	PendingRequirement                                     json.RawMessage
	SetPending                                             bool
	Error                                                  json.RawMessage
}

// CompleteRun 原子完成 run、可选 assistant 消息与产品会话最终状态。
func (s *Store) CompleteRun(ctx context.Context, p CompleteRunParams) (*WebMessage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: 开启完成运行事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cmd, err := tx.Exec(ctx, `UPDATE agent_runs SET status = $2, error = $3, finished_at = now()
		WHERE id = $1 AND session_id = $4 AND status = 'running'`, p.RunID, p.Status, nullableJSON(p.Error), p.SessionID)
	if err != nil {
		return nil, fmt.Errorf("store: 完成运行失败: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return nil, ErrRunNotFound
	}
	var message *WebMessage
	if p.AssistantContent != "" {
		var m WebMessage
		err := tx.QueryRow(ctx, `INSERT INTO web_messages (id, session_id, role, content, run_id)
			VALUES ($1, $2, 'assistant', $3, $4)
			RETURNING id::text, session_id, client_message_id::text, role, content, run_id::text, created_at`,
			p.AssistantMessageID, p.SessionID, p.AssistantContent, p.RunID).
			Scan(&m.ID, &m.SessionID, &m.ClientMessageID, &m.Role, &m.Content, &m.RunID, &m.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("store: 写入 assistant 消息失败: %w", err)
		}
		message = &m
	}
	_, err = tx.Exec(ctx, `UPDATE web_sessions SET phase = $2, recovery_phase = $3,
		last_error = $4, pending_requirement = CASE WHEN $5 THEN $6 ELSE pending_requirement END,
		updated_at = now() WHERE id = $1`, p.SessionID, p.Phase, p.RecoveryPhase,
		nullableJSON(p.Error), p.SetPending, nullableJSON(p.PendingRequirement))
	if err != nil {
		return nil, fmt.Errorf("store: 更新运行最终阶段失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: 提交运行结果失败: %w", err)
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
		recovery := PhaseCollecting
		if r.Kind == RunBuild {
			recovery = PhaseRequirementReady
		} else if r.Kind == RunChange {
			recovery = PhaseReady
		}
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
