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
	ConsumeMessageID string
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
