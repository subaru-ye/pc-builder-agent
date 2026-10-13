package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

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
