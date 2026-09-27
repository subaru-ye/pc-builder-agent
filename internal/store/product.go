package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

var (
	ErrWebSessionNotFound     = errors.New("产品会话不存在")
	ErrRunNotFound            = errors.New("运行不存在")
	ErrSessionBusy            = errors.New("会话已有活动运行")
	ErrInvalidSessionPhase    = errors.New("会话阶段不允许当前操作")
	ErrIdempotencyConflict    = errors.New("幂等键已用于不同请求")
	ErrRequirementRevision    = errors.New("需求已更新，请刷新后重试")
	ErrRequirementNotReady    = errors.New("需求尚未达到确认条件")
	ErrRequirementReviewConflict = errors.New("需求核定预览已变化，请重新核定")
	ErrRetryTargetInvalid     = errors.New("重试目标运行无效")
	ErrWebMessageNotFound     = errors.New("产品消息不存在")
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
	StatusLabel      string
	ID               string
	OwnerID          string
	CreateRequestID  string
	Title            string
	Archived         bool
	Phase            SessionPhase
	RecoveryPhase    *SessionPhase
	PendingRequirement json.RawMessage
	RequirementState json.RawMessage
	// ConfirmationID 指向当前确认快照(requirement_confirmations);空表示从未确认。
	ConfirmationID string
	LastError      json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
	VersionCount   int
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

// WebMessageByID 按会话内消息 ID 精确读取,供偏好保存等来源核验使用:
// sessionID 同时出现在条件里,跨会话引用一律不存在。
func (s *Store) WebMessageByID(ctx context.Context, sessionID, messageID string) (WebMessage, error) {
	var m WebMessage
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, session_id, client_message_id::text, role, content, run_id::text, created_at,
		       COALESCE(display_content,''), COALESCE(build_version, 0)
		FROM web_messages WHERE id = $1 AND session_id = $2`, messageID, sessionID).
		Scan(&m.ID, &m.SessionID, &m.ClientMessageID, &m.Role,
			&m.Content, &m.RunID, &m.CreatedAt, &m.DisplayContent, &m.BuildVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebMessage{}, ErrWebMessageNotFound
	}
	if err != nil {
		return WebMessage{}, fmt.Errorf("store: 查询产品消息失败: %w", err)
	}
	return m, nil
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
	// 紧邻轮次语义:上一条 assistant 消息绑定的未消费建议只对本轮有效。
	if err := consumeRequirementProposalsTx(ctx, tx, p.SessionID, p.MessageID); err != nil {
		return AgentRun{}, false, err
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

// messageTransition 是消息运行的 phase 门控。产品层一律 ForceScreening:
// ready 阶段的普通聊天也是 screening 轮,不存在 RunChange 旁路;building/
// changing 阶段拒绝新消息运行(草稿编辑走独立事务,不经过这里)。
func messageTransition(phase SessionPhase, recovery *string) (RunKind, SessionPhase, bool, error) {
	if phase == PhaseError {
		if recovery == nil {
			return "", "", false, &InvalidPhaseError{Phase: phase}
		}
		phase = SessionPhase(*recovery)
	}
	switch phase {
	case PhaseCollecting, PhaseRequirementReady, PhaseReady:
		return RunScreening, PhaseCollecting, false, nil
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
	// ConfirmationID 是新确认快照的主键(服务层生成);retry 复用目标 run 的
	// 快照时忽略该值。
	ConfirmationID string
	// ExpectedRevision/ExpectedReviewHash 绑定用户看到的核定预览:revision 或
	// review_hash 任一变化都拒绝(默认规则/配置范围在 revision 不变时也可能变化)。
	ExpectedRevision   int
	ExpectedReviewHash string
	// RetryOfRunID 仅失败重试时非空:复用目标 run 的确认快照,为新 run 另冻
	// 新载荷与新 hash;目标必须属于本会话、kind=build 且已失败。
	RetryOfRunID string
	// RequestFingerprint 是确认请求体的规范化指纹:同键同请求重放返回原 run,
	// 同键不同请求返回稳定冲突。
	RequestFingerprint string
}

type StartConfirmRunResult struct {
	ConfirmationID      string
	ReviewHash          string
	BuilderInputPayload json.RawMessage
	BuilderInputHash    string
}

// StartConfirmRun 是唯一的 Builder admission 入口:在同一事务里完成幂等、
// revision/review_hash/readiness 校验、确认快照冻结与 run 的完整执行载荷
// 冻结。提交前不产生任何部分状态;聊天文字与旧改单路径都不能到达这里。
func (s *Store) StartConfirmRun(ctx context.Context, p StartConfirmRunParams) (AgentRun, StartConfirmRunResult, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 开启确认运行事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var requirementState json.RawMessage
	var revision int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((requirement_state->>'revision')::int, 0), requirement_state FROM web_sessions
		WHERE id = $1 AND owner_id = $2 FOR UPDATE`, p.SessionID, p.OwnerID).Scan(&revision, &requirementState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentRun{}, StartConfirmRunResult{}, false, ErrWebSessionNotFound
		}
		return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 锁定确认会话失败: %w", err)
	}
	// 幂等重放先于一切校验:同键同请求返回原始 run,不重新读取当前草稿。
	if existing, found, err := runByRequestTx(ctx, tx, p.SessionID, p.RequestID); err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, err
	} else if found {
		var oldFingerprint string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(request_fingerprint, '') FROM agent_runs WHERE id = $1`, existing.ID).Scan(&oldFingerprint); err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 读取确认指纹失败: %w", err)
		}
		if existing.Kind != RunBuild || oldFingerprint != p.RequestFingerprint {
			return AgentRun{}, StartConfirmRunResult{}, false, ErrIdempotencyConflict
		}
		return existing, StartConfirmRunResult{}, true, nil
	}
	if len(requirementState) == 0 {
		// v1 一次性切换:没有增量状态的旧会话不能继续确认。
		return AgentRun{}, StartConfirmRunResult{}, false, schemas.ErrRequirementStateUnsupported
	}
	state, err := schemas.DecodeRequirementState(requirementState)
	if err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, err
	}
	if state.Revision != p.ExpectedRevision {
		return AgentRun{}, StartConfirmRunResult{}, false, ErrRequirementRevision
	}
	// 确认入口与 Planning/API 共用同一核定预览投影(展开有效默认)与
	// readiness 规则;不信任客户端提交的 eligible/missing/snapshot。
	reviewSpec, readiness, err := schemas.RequirementReviewSpec(state)
	if err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, err
	}
	if !readiness.ConfirmationEligible {
		return AgentRun{}, StartConfirmRunResult{}, false, ErrRequirementNotReady
	}
	reviewHash, err := schemas.CanonicalHash(reviewSpec)
	if err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, err
	}
	if reviewHash != p.ExpectedReviewHash {
		return AgentRun{}, StartConfirmRunResult{}, false, ErrRequirementReviewConflict
	}
	// Builder 上下文在确认事务内确定并冻结:run ID、上一提案与基线草稿是
	// 最终载荷的一部分,不得延迟到执行时再拼装。失败重试是唯一例外:选型
	// 上下文整体继承目标 run 的冻结载荷,只替换本轮 run 标识,不从当前
	// session 重新组装(见下)。
	var payload json.RawMessage
	var builderInputHash string
	confirmationID := p.ConfirmationID
	if p.RetryOfRunID != "" {
		// 失败重试:目标必须属于本会话、kind=build 且已失败,其快照仍与当前
		// 核定预览一致;复用快照与冻结选型上下文,仅为新 run 标识另冻载荷
		// 与新 hash。
		var kind RunKind
		var status RunStatus
		var targetConfirmation string
		var targetReviewHash string
		var inheritedPayload json.RawMessage
		err := tx.QueryRow(ctx, `SELECT r.kind, r.status, COALESCE(r.confirmation_id::text, ''), COALESCE(c.review_hash, ''),
			r.builder_input_payload
			FROM agent_runs r LEFT JOIN requirement_confirmations c ON c.id = r.confirmation_id
			WHERE r.id = $1 AND r.session_id = $2`, p.RetryOfRunID, p.SessionID).Scan(&kind, &status, &targetConfirmation, &targetReviewHash, &inheritedPayload)
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentRun{}, StartConfirmRunResult{}, false, ErrRunNotFound
		}
		if err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 读取重试目标失败: %w", err)
		}
		if kind != RunBuild || status != RunFailed || targetConfirmation == "" || len(inheritedPayload) == 0 {
			return AgentRun{}, StartConfirmRunResult{}, false, ErrRetryTargetInvalid
		}
		if targetReviewHash != reviewHash {
			return AgentRun{}, StartConfirmRunResult{}, false, ErrRequirementReviewConflict
		}
		confirmationID = targetConfirmation
		// 继承冻结载荷:state/base_draft/previous_proposal/previous_run_id
		// 原样保留,只替换本轮 run 标识(PlanningInput 中唯一随 run 变化的
		// 字段);先前 run 的载荷/hash 不修改。
		var inherited schemas.PlanningInput
		if err := json.Unmarshal(inheritedPayload, &inherited); err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 解析重试目标载荷失败: %w", err)
		}
		inherited.RunID = p.RunID
		payload, err = json.Marshal(inherited)
		if err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, err
		}
	} else {
		var previous struct {
			Result json.RawMessage `json:"result"`
			RunID  string          `json:"run_id"`
		}
		var previousProposal json.RawMessage
		var previousRunID string
		err = tx.QueryRow(ctx, `SELECT result, run_id::text FROM session_proposals
			WHERE session_id = $1 ORDER BY id DESC LIMIT 1`, p.SessionID).Scan(&previous.Result, &previous.RunID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 读取上一提案失败: %w", err)
		default:
			previousProposal, previousRunID = previous.Result, previous.RunID
		}
		var baseDraft json.RawMessage
		var version *int
		if err := tx.QueryRow(ctx, `SELECT max(version) FROM builds WHERE session_id = $1`, p.SessionID).Scan(&version); err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 查询最新配置版本失败: %w", err)
		}
		if version != nil {
			if err := tx.QueryRow(ctx, `SELECT draft FROM builds WHERE session_id = $1 AND version = $2`, p.SessionID, *version).Scan(&baseDraft); err != nil {
				return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 读取基线草稿失败: %w", err)
			}
		}
		payload, err = schemas.PlanningBuilderInput(p.RunID, state, &schemas.EffectiveConstraints{
			Spec: reviewSpec, Defaults: readiness.EffectiveDefaults,
		}, baseDraft, previousProposal, previousRunID)
		if err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, err
		}
		var specObject map[string]json.RawMessage
		if err := json.Unmarshal(reviewSpec, &specObject); err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, err
		}
		scope := specObject["configuration_scope"]
		if scope == nil {
			scope = json.RawMessage(`[]`)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO requirement_confirmations
			(id, session_id, requirement_spec, requirement_state, review_hash, revision, configuration_scope, schema_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, confirmationID, p.SessionID, reviewSpec, requirementState,
			reviewHash, state.Revision, scope, schemas.RequirementStateSchemaVersion); err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 冻结确认快照失败: %w", err)
		}
	}
	builderInputHash, err = schemas.CanonicalHash(payload)
	if err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, err
	}
	r, err := insertRunTx(ctx, tx, p.RunID, p.SessionID, p.RequestID, RunBuild)
	if isRunningConflict(err) {
		return AgentRun{}, StartConfirmRunResult{}, false, ErrSessionBusy
	}
	if err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, err
	}
	// run 永久绑定不可变快照与两个 hash:发送路径只读 builder_input_payload,
	// 发送前再校验实际载荷 hash 与冻结值一致(V5)。
	if _, err := tx.Exec(ctx, `UPDATE agent_runs SET request_fingerprint = NULLIF($2, ''),
		confirmation_id = $3, builder_input_payload = $4, builder_input_hash = $5,
		retry_of_run_id = NULLIF($6, '')::uuid WHERE id = $1`, r.ID, p.RequestFingerprint,
		confirmationID, payload, builderInputHash, p.RetryOfRunID); err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 冻结运行载荷失败: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE web_sessions SET phase = 'building', recovery_phase = NULL,
		last_error = NULL, confirmation_id = $2, updated_at = now() WHERE id = $1`, p.SessionID, confirmationID); err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 更新确认阶段失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentRun{}, StartConfirmRunResult{}, false, fmt.Errorf("store: 提交确认运行失败: %w", err)
	}
	return r, StartConfirmRunResult{ConfirmationID: confirmationID, ReviewHash: reviewHash,
		BuilderInputPayload: payload, BuilderInputHash: builderInputHash}, false, nil
}

// RequirementConfirmation 是不可变的确认快照:review_spec 是用户核定时看到的
// 规范化有效 RequirementSpec,review_hash 基于它;builder_input_hash 另存于 run。
type RequirementConfirmation struct {
	ID                 string
	SessionID          string
	RequirementSpec    json.RawMessage
	RequirementState   json.RawMessage
	ReviewHash         string
	Revision           int
	ConfigurationScope json.RawMessage
	SchemaVersion      int
	CreatedAt          time.Time
}

func (s *Store) ConfirmationByID(ctx context.Context, sessionID, id string) (RequirementConfirmation, bool, error) {
	if id == "" {
		return RequirementConfirmation{}, false, nil
	}
	var c RequirementConfirmation
	err := s.pool.QueryRow(ctx, `SELECT id::text, session_id, requirement_spec, requirement_state,
		review_hash, revision, configuration_scope, schema_version, created_at
		FROM requirement_confirmations WHERE id = $1 AND session_id = $2`, id, sessionID).
		Scan(&c.ID, &c.SessionID, &c.RequirementSpec, &c.RequirementState, &c.ReviewHash,
			&c.Revision, &c.ConfigurationScope, &c.SchemaVersion, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RequirementConfirmation{}, false, nil
	}
	if err != nil {
		return RequirementConfirmation{}, false, fmt.Errorf("store: 查询确认快照失败: %w", err)
	}
	return c, true, nil
}

// BuildRun 是 build 运行与其冻结快照关联的读取视图;Version 是该 run 产出的
// 配置版本(成功才有)。ReviewHash 来自其确认快照。
type BuildRun struct {
	AgentRun
	ConfirmationID   string
	ReviewHash       string
	BuilderInputHash string
	RetryOfRunID     string
	Version          int
}

// BuildRuns 返回会话全部 build 运行(新→旧);三轴 build_relation 的派生输入。
func (s *Store) BuildRuns(ctx context.Context, sessionID string) ([]BuildRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id::text, r.session_id, r.client_request_id::text, r.kind, r.status,
	       r.error, r.started_at, r.finished_at, r.cancel_requested_at, r.expires_at,
	       COALESCE(r.confirmation_id::text, ''), COALESCE(c.review_hash, ''), COALESCE(r.builder_input_hash, ''),
	       COALESCE(r.retry_of_run_id::text, ''), COALESCE(b.version, 0)
		FROM agent_runs r
		LEFT JOIN requirement_confirmations c ON c.id = r.confirmation_id
		LEFT JOIN builds b ON b.run_id = r.id
		WHERE r.session_id = $1 AND r.kind = 'build'
		ORDER BY r.started_at DESC, r.id DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: 查询 build 运行失败: %w", err)
	}
	defer rows.Close()
	out := make([]BuildRun, 0)
	for rows.Next() {
		var run BuildRun
		if err := rows.Scan(&run.ID, &run.SessionID, &run.ClientRequestID, &run.Kind, &run.Status,
			&run.Error, &run.StartedAt, &run.FinishedAt, &run.CancelRequestedAt, &run.ExpiresAt,
			&run.ConfirmationID, &run.ReviewHash, &run.BuilderInputHash, &run.RetryOfRunID, &run.Version); err != nil {
			return nil, fmt.Errorf("store: 读取 build 运行失败: %w", err)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历 build 运行失败: %w", err)
	}
	return out, nil
}

type EditRequirementDraftParams struct {
	OwnerID, SessionID, RequestID, Fingerprint string
	ExpectedRevision                           int
	// Next 是产品层已用共享 Reducer 应用后的完整草稿状态;Pending 是其投影
	// (不合格时为 nil)。事务只校验 revision 与幂等,不触碰 phase/run。
	Next, Pending json.RawMessage
}

// RequirementEditFingerprint 预检草稿编辑幂等:服务层在重放路径(可能包含
// 无法重复应用的操作,如 restore)应用 Reducer 之前先识别重复请求。
func (s *Store) RequirementEditFingerprint(ctx context.Context, sessionID, requestID string) (string, bool, error) {
	if requestID == "" {
		return "", false, nil
	}
	var fingerprint string
	err := s.pool.QueryRow(ctx, `SELECT fingerprint FROM requirement_edit_requests
		WHERE session_id = $1 AND request_id = $2`, sessionID, requestID).Scan(&fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: 查询草稿编辑指纹失败: %w", err)
	}
	return fingerprint, true, nil
}

// EditRequirementDraft 是运行中也可用的独立原子草稿编辑事务:不创建 AgentRun、
// 不改 phase,原 Builder run 与确认快照保持不变。同一会话同一请求键重复提交
// 同一指纹时按幂等成功处理(不重复应用)。
func (s *Store) EditRequirementDraft(ctx context.Context, p EditRequirementDraftParams) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("store: 开启草稿编辑事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revision int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((requirement_state->>'revision')::int, 0) FROM web_sessions
		WHERE id = $1 AND owner_id = $2 FOR UPDATE`, p.SessionID, p.OwnerID).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrWebSessionNotFound
		}
		return false, fmt.Errorf("store: 锁定草稿会话失败: %w", err)
	}
	var fingerprint string
	if err := tx.QueryRow(ctx, `INSERT INTO requirement_edit_requests (session_id, request_id, fingerprint)
		VALUES ($1, $2, $3) ON CONFLICT (session_id, request_id) DO NOTHING RETURNING fingerprint`,
		p.SessionID, p.RequestID, p.Fingerprint).Scan(&fingerprint); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("store: 记录草稿编辑失败: %w", err)
	}
	if fingerprint == "" {
		var oldFingerprint string
		if err := tx.QueryRow(ctx, `SELECT fingerprint FROM requirement_edit_requests
			WHERE session_id = $1 AND request_id = $2`, p.SessionID, p.RequestID).Scan(&oldFingerprint); err != nil {
			return false, fmt.Errorf("store: 读取草稿编辑指纹失败: %w", err)
		}
		if oldFingerprint != p.Fingerprint {
			return false, ErrIdempotencyConflict
		}
		return false, nil
	}
	if revision != p.ExpectedRevision {
		return false, ErrRequirementRevision
	}
	if _, err := tx.Exec(ctx, `UPDATE web_sessions SET requirement_state = $2,
		pending_requirement = $3, updated_at = now() WHERE id = $1`, p.SessionID, p.Next, p.Pending); err != nil {
		return false, fmt.Errorf("store: 保存草稿失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("store: 提交草稿编辑失败: %w", err)
	}
	return true, nil
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
	// SaveProposals 与 assistant 消息同事务保存的需求建议(仅文本已展示的)。
	SaveProposals []RequirementProposalSave
	// ConsumeMessageID 是本轮用户消息;ResolveProposals 按其定位被采纳建议。
	ConsumeMessageID  string
	ResolveProposals []RequirementProposalAccept
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
	if len(p.SaveProposals) > 0 && message != nil {
		if err := saveRequirementProposalsTx(ctx, tx, p.SessionID, message.ID, p.AssistantContent, p.SaveProposals); err != nil {
			return nil, err
		}
	}
	if len(p.ResolveProposals) > 0 && p.ConsumeMessageID != "" {
		if err := resolveAcceptedProposalsTx(ctx, tx, p.SessionID, p.ConsumeMessageID, p.ResolveProposals); err != nil {
			return nil, err
		}
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
