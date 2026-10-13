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
		payload, confirmationID, err = confirmPayloadForRetry(ctx, tx, p, reviewHash)
		if err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, err
		}
	} else {
		payload, err = confirmPayloadFresh(ctx, tx, p, state, reviewSpec, requirementState, reviewHash, readiness)
		if err != nil {
			return AgentRun{}, StartConfirmRunResult{}, false, err
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

// confirmPayloadForRetry 构造失败重试的 Builder 载荷:目标必须属于本会话、
// kind=build 且已失败,其快照仍与当前核定预览一致;复用快照与冻结选型
// 上下文,继承载荷原样保留,只替换本轮 run 标识(PlanningInput 中唯一随
// run 变化的字段),先前 run 的载荷/hash 不修改。
func confirmPayloadForRetry(ctx context.Context, tx pgx.Tx, p StartConfirmRunParams, reviewHash string) (json.RawMessage, string, error) {
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
		return nil, "", ErrRunNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("store: 读取重试目标失败: %w", err)
	}
	if kind != RunBuild || status != RunFailed || targetConfirmation == "" || len(inheritedPayload) == 0 {
		return nil, "", ErrRetryTargetInvalid
	}
	if targetReviewHash != reviewHash {
		return nil, "", ErrRequirementReviewConflict
	}
	var inherited schemas.PlanningInput
	if err := json.Unmarshal(inheritedPayload, &inherited); err != nil {
		return nil, "", fmt.Errorf("store: 解析重试目标载荷失败: %w", err)
	}
	inherited.RunID = p.RunID
	payload, err := json.Marshal(inherited)
	if err != nil {
		return nil, "", err
	}
	return payload, targetConfirmation, nil
}

// confirmPayloadFresh 组装新确认的 Builder 载荷:上一提案、基线草稿与冻结
// 约束一起进入不可变载荷,并插入本轮的 requirement_confirmations 快照。
func confirmPayloadFresh(ctx context.Context, tx pgx.Tx, p StartConfirmRunParams, state schemas.RequirementState, reviewSpec, requirementState json.RawMessage, reviewHash string, readiness schemas.RequirementReadiness) (json.RawMessage, error) {
	var previous struct {
		Result json.RawMessage `json:"result"`
		RunID  string          `json:"run_id"`
	}
	var previousProposal json.RawMessage
	var previousRunID string
	err := tx.QueryRow(ctx, `SELECT result, run_id::text FROM session_proposals
		WHERE session_id = $1 ORDER BY id DESC LIMIT 1`, p.SessionID).Scan(&previous.Result, &previous.RunID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("store: 读取上一提案失败: %w", err)
	default:
		previousProposal, previousRunID = previous.Result, previous.RunID
	}
	var baseDraft json.RawMessage
	var version *int
	if err := tx.QueryRow(ctx, `SELECT max(version) FROM builds WHERE session_id = $1`, p.SessionID).Scan(&version); err != nil {
		return nil, fmt.Errorf("store: 查询最新配置版本失败: %w", err)
	}
	if version != nil {
		if err := tx.QueryRow(ctx, `SELECT draft FROM builds WHERE session_id = $1 AND version = $2`, p.SessionID, *version).Scan(&baseDraft); err != nil {
			return nil, fmt.Errorf("store: 读取基线草稿失败: %w", err)
		}
	}
	payload, err := schemas.PlanningBuilderInput(p.RunID, state, &schemas.EffectiveConstraints{
		Spec: reviewSpec, Defaults: readiness.EffectiveDefaults,
	}, baseDraft, previousProposal, previousRunID)
	if err != nil {
		return nil, err
	}
	var specObject map[string]json.RawMessage
	if err := json.Unmarshal(reviewSpec, &specObject); err != nil {
		return nil, err
	}
	scope := specObject["configuration_scope"]
	if scope == nil {
		scope = json.RawMessage(`[]`)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO requirement_confirmations
		(id, session_id, requirement_spec, requirement_state, review_hash, revision, configuration_scope, schema_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, p.ConfirmationID, p.SessionID, reviewSpec, requirementState,
		reviewHash, state.Revision, scope, schemas.RequirementStateSchemaVersion); err != nil {
		return nil, fmt.Errorf("store: 冻结确认快照失败: %w", err)
	}
	return payload, nil
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
