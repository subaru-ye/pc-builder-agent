package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

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
