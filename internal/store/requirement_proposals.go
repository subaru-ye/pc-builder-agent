package store

// 助手需求建议的持久化语义:与 assistant 消息同事务保存、只对紧接着的
// 下一条用户消息有效、消费或解析后失效。并发接受由 StartMessageRun 的
// 会话行锁串行化:同一建议只会绑定一个 consumed_by_message_id。
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// RequirementProposalRecord 是服务器侧的建议记录;value 为规范化后的值。
type RequirementProposalRecord struct {
	ID       int64           `json:"id"`
	Field    string          `json:"field"`
	Value    json.RawMessage `json:"value"`
	Text     string          `json:"text"`
	Accepted bool            `json:"accepted"`
}

// RequirementProposalSave 由产品层在组合出最终 assistant 文本后提交;
// Text 必须逐字出现在该文本中,否则该建议被丢弃(未展示的建议不可接受)。
type RequirementProposalSave struct {
	Field string
	Value json.RawMessage
	Text  string
}

// RequirementProposalAccept 是本轮被服务器验证采纳的建议(field+规范化值)。
type RequirementProposalAccept struct {
	Field string
	Value json.RawMessage
}

// consumeRequirementProposalsTx 把"最后一条 assistant 消息"绑定的未消费
// 建议标记为本轮用户消息专用:它们只对这一轮有效,后续任何消息不得复用。
// 只在 StartMessageRun 事务内、用户消息插入之后调用:此时"最后一条
// assistant 消息"恰好是紧邻本轮的前一条助手回复,中间隔过其他用户消息的
// 旧建议已在各自的首个后续轮被消费,不会再次命中。
func consumeRequirementProposalsTx(ctx context.Context, tx pgx.Tx, sessionID, userMessageID string) error {
	_, err := tx.Exec(ctx, `UPDATE requirement_proposals SET consumed_by_message_id = $2
		WHERE session_id = $1 AND resolved_at IS NULL AND consumed_by_message_id IS NULL
		AND assistant_message_id = (
			SELECT id FROM web_messages WHERE session_id = $1 AND role = 'assistant'
			ORDER BY created_at DESC, id DESC LIMIT 1)`,
		sessionID, userMessageID)
	if err != nil {
		return fmt.Errorf("store: 消费需求建议失败: %w", err)
	}
	return nil
}

// ActiveRequirementProposals 返回只对本轮用户消息有效的未解析建议;
// proposal 不存在时返回空列表(裸"可以"没有可验证对象)。
func (s *Store) ActiveRequirementProposals(ctx context.Context, sessionID, userMessageID string) ([]RequirementProposalRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, field, value, text FROM requirement_proposals
		WHERE session_id = $1 AND consumed_by_message_id = $2 AND resolved_at IS NULL
		ORDER BY id`, sessionID, userMessageID)
	if err != nil {
		return nil, fmt.Errorf("store: 查询本轮建议失败: %w", err)
	}
	defer rows.Close()
	out := []RequirementProposalRecord{}
	for rows.Next() {
		var record RequirementProposalRecord
		if err := rows.Scan(&record.ID, &record.Field, &record.Value, &record.Text); err != nil {
			return nil, fmt.Errorf("store: 读取本轮建议失败: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// saveRequirementProposalsTx 与 assistant 消息同事务保存建议;文本未出现在
// 最终助手回复中的建议被静默丢弃(不可接受的半状态不落库)。
func saveRequirementProposalsTx(ctx context.Context, tx pgx.Tx, sessionID, assistantMessageID, assistantContent string, proposals []RequirementProposalSave) error {
	for _, proposal := range proposals {
		if !strings.Contains(assistantContent, proposal.Text) {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO requirement_proposals
			(session_id, assistant_message_id, field, value, text) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (assistant_message_id, field) DO NOTHING`,
			sessionID, assistantMessageID, proposal.Field, []byte(proposal.Value), proposal.Text); err != nil {
			return fmt.Errorf("store: 保存需求建议失败: %w", err)
		}
	}
	return nil
}

// resolveAcceptedProposalsTx 把本轮明确接受的建议标记 resolved;
// 拒绝、覆盖或未接受的同类建议靠 consumed_by_message_id 已自然失效。
func resolveAcceptedProposalsTx(ctx context.Context, tx pgx.Tx, sessionID, userMessageID string, accepted []RequirementProposalAccept) error {
	for _, match := range accepted {
		if _, err := tx.Exec(ctx, `UPDATE requirement_proposals SET resolved_at = $3
			WHERE session_id = $1 AND consumed_by_message_id = $2 AND field = $4 AND value = $5`,
			sessionID, userMessageID, time.Now(), match.Field, []byte(match.Value)); err != nil {
			return fmt.Errorf("store: 解析需求建议失败: %w", err)
		}
	}
	return nil
}
