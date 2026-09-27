package store

// 跨会话偏好记忆的持久化(第一阶段独立基座,未接入产品调用链)。
// 归属沿用 owner_id TEXT;所有读写都强制 owner 匹配,subject 由调用方显式传入,
// 本人(self)与代配对象在查询层隔离。设计见 docs/tech/偏好记忆设计草案.md。
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

var (
	// ErrPreferenceMemoryNotFound 覆盖"不存在"与"不属于该 owner"两种情况:
	// 跨 owner 访问不泄露存在性。
	ErrPreferenceMemoryNotFound = errors.New("偏好记忆不存在")
	// ErrPreferenceMemoryNotActive 表示目标记忆已是 superseded/retracted,
	// 冲突更新与撤销只对 active 记录生效。
	ErrPreferenceMemoryNotActive = errors.New("偏好记忆已失效,不能再次变更")
	// ErrPreferenceUnchanged 表示重申了相同偏好值:不构成改主意,不产生 supersede 链。
	ErrPreferenceUnchanged = errors.New("偏好值未变化")
)

// preferenceRecallLimit 是召回的保守上限;注入预算(条数/字符)待接入合同定型后再定。
const preferenceRecallLimit = 100

const preferenceMemoryColumns = `id::text, owner_id, subject, field, value, strength, evidence,
	volatile, observed_at, status, supersedes::text, source, created_at, updated_at`

func scanPreferenceMemory(row pgx.Row) (schemas.PreferenceMemory, error) {
	var (
		m          schemas.PreferenceMemory
		value      []byte
		source     []byte
		observedAt *time.Time
		supersedes *string
	)
	err := row.Scan(&m.ID, &m.OwnerID, &m.Subject, &m.Field, &value, &m.Strength, &m.Evidence,
		&m.Volatile, &observedAt, &m.Status, &supersedes, &source, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return m, err
	}
	m.Value = value
	if observedAt != nil {
		m.ObservedAt = observedAt.Format("2006-01-02")
	}
	if supersedes != nil {
		m.Supersedes = *supersedes
	}
	m.Source, err = schemas.DecodePreferenceSource(source)
	if err != nil {
		return m, err
	}
	return m, nil
}

func preferenceObservedAt(m schemas.PreferenceMemory) (*time.Time, error) {
	parsed, err := schemas.ParsePreferenceDate(m.ObservedAt)
	if err != nil {
		return nil, err
	}
	if parsed.IsZero() {
		return nil, nil
	}
	return &parsed, nil
}

func preferenceSourceJSON(source schemas.PreferenceSource) ([]byte, error) {
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("store: 序列化偏好来源: %w", err)
	}
	return raw, nil
}

// CreatePreferenceMemory 写入一条新偏好。调用方必须先取得用户明确表达
// (stated)或明确接受(accepted_proposal)的证据;会话内 temporary/uncertain
// 内容在 schemas 校验层即被拒绝,不升级为长期记忆。
func (s *Store) CreatePreferenceMemory(ctx context.Context, m schemas.PreferenceMemory) (schemas.PreferenceMemory, error) {
	if err := schemas.ValidatePreferenceMemory(m); err != nil {
		return schemas.PreferenceMemory{}, err
	}
	observedAt, err := preferenceObservedAt(m)
	if err != nil {
		return schemas.PreferenceMemory{}, err
	}
	source, err := preferenceSourceJSON(m.Source)
	if err != nil {
		return schemas.PreferenceMemory{}, err
	}
	created, err := scanPreferenceMemory(s.pool.QueryRow(ctx, `
		INSERT INTO owner_preference_memories
			(owner_id, subject, field, value, strength, evidence, volatile, observed_at, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+preferenceMemoryColumns,
		m.OwnerID, m.Subject, m.Field, []byte(m.Value), m.Strength, m.Evidence, m.Volatile, observedAt, source))
	if err != nil {
		return schemas.PreferenceMemory{}, fmt.Errorf("store: 写入偏好记忆: %w", err)
	}
	return created, nil
}

// SupersedePreferenceMemory 在单事务中完成"改主意":旧 active 记录转 superseded
// (墓碑保留,不再召回),插入新 active 并回指 supersedes。库内部分唯一索引保证
// 同一 (owner, subject, field) 至多一条 active,并发双写会在提交时失败。
// 重申相同值返回 ErrPreferenceUnchanged,不产生新链。
func (s *Store) SupersedePreferenceMemory(ctx context.Context, ownerID, prevID string, next schemas.PreferenceMemory) (schemas.PreferenceMemory, error) {
	if next.OwnerID == "" {
		next.OwnerID = ownerID
	}
	if err := schemas.ValidatePreferenceMemory(next); err != nil {
		return schemas.PreferenceMemory{}, err
	}
	observedAt, err := preferenceObservedAt(next)
	if err != nil {
		return schemas.PreferenceMemory{}, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return schemas.PreferenceMemory{}, fmt.Errorf("store: 开始偏好变更事务: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		prevSubject, prevField, prevStatus string
		prevValue                          []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT subject, field, value, status FROM owner_preference_memories
		WHERE id = $1 AND owner_id = $2 FOR UPDATE`, prevID, ownerID).
		Scan(&prevSubject, &prevField, &prevValue, &prevStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return schemas.PreferenceMemory{}, ErrPreferenceMemoryNotFound
	}
	if err != nil {
		return schemas.PreferenceMemory{}, fmt.Errorf("store: 查询原偏好: %w", err)
	}
	if prevStatus != schemas.PreferenceStatusActive {
		return schemas.PreferenceMemory{}, ErrPreferenceMemoryNotActive
	}
	if prevSubject != next.Subject || prevField != next.Field {
		return schemas.PreferenceMemory{}, fmt.Errorf("%w: 冲突更新必须针对同一 (subject, field)", schemas.ErrPreferenceMemoryInvalid)
	}
	if schemas.EqualPreferenceValue(prevValue, next.Value) {
		return schemas.PreferenceMemory{}, ErrPreferenceUnchanged
	}

	if _, err := tx.Exec(ctx, `
		UPDATE owner_preference_memories
		SET status = $2, updated_at = now()
		WHERE id = $1`, prevID, schemas.PreferenceStatusSupersede); err != nil {
		return schemas.PreferenceMemory{}, fmt.Errorf("store: 墓碑化原偏好: %w", err)
	}
	next.Supersedes = prevID
	source, err := preferenceSourceJSON(next.Source)
	if err != nil {
		return schemas.PreferenceMemory{}, err
	}
	created, err := scanPreferenceMemory(tx.QueryRow(ctx, `
		INSERT INTO owner_preference_memories
			(owner_id, subject, field, value, strength, evidence, volatile, observed_at, supersedes, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+preferenceMemoryColumns,
		next.OwnerID, next.Subject, next.Field, []byte(next.Value), next.Strength, next.Evidence,
		next.Volatile, observedAt, prevID, source))
	if err != nil {
		return schemas.PreferenceMemory{}, fmt.Errorf("store: 写入新偏好: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return schemas.PreferenceMemory{}, fmt.Errorf("store: 提交偏好变更事务: %w", err)
	}
	return created, nil
}

// RetractPreferenceMemory 将 active 记忆转为 retracted 墓碑:语义是"别再按这个来",
// 审计痕迹保留;物理删除用 DeletePreferenceMemory。
func (s *Store) RetractPreferenceMemory(ctx context.Context, ownerID, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE owner_preference_memories
		SET status = $3, updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND status = $4`,
		id, ownerID, schemas.PreferenceStatusRetract, schemas.PreferenceStatusActive)
	if err != nil {
		return fmt.Errorf("store: 撤销偏好记忆: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.preferenceMemoryMissing(ctx, ownerID, id)
	}
	return nil
}

// DeletePreferenceMemory 物理删除记忆(含墓碑):用户要求遗忘即真删除,不留审计副本。
func (s *Store) DeletePreferenceMemory(ctx context.Context, ownerID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM owner_preference_memories WHERE id = $1 AND owner_id = $2`, id, ownerID)
	if err != nil {
		return fmt.Errorf("store: 删除偏好记忆: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.preferenceMemoryMissing(ctx, ownerID, id)
	}
	return nil
}

// ActivePreferenceMemories 召回指定 owner 与 subject 的全部 active 偏好。
// 返回值是待确认建议:易失事实的时效过滤由 schemas.FilterPreferenceSuggestions 完成,
// 当前会话需求永远优先于这里的任何记录。
func (s *Store) ActivePreferenceMemories(ctx context.Context, ownerID, subject string) ([]schemas.PreferenceMemory, error) {
	if ownerID == "" || subject == "" {
		return nil, fmt.Errorf("%w: 召回必须显式指定 owner 与 subject", schemas.ErrPreferenceMemoryInvalid)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+preferenceMemoryColumns+` FROM owner_preference_memories
		WHERE owner_id = $1 AND subject = $2 AND status = $3
		ORDER BY updated_at DESC, id
		LIMIT $4`, ownerID, subject, schemas.PreferenceStatusActive, preferenceRecallLimit)
	if err != nil {
		return nil, fmt.Errorf("store: 召回偏好记忆: %w", err)
	}
	defer rows.Close()
	var memories []schemas.PreferenceMemory
	for rows.Next() {
		m, err := scanPreferenceMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("store: 读取偏好记忆: %w", err)
		}
		memories = append(memories, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历偏好记忆: %w", err)
	}
	return memories, nil
}

// preferenceMemoryMissing 区分"不存在"与"存在但已失效",供撤销/删除给出准确错误。
func (s *Store) preferenceMemoryMissing(ctx context.Context, ownerID, id string) error {
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT status FROM owner_preference_memories WHERE id = $1 AND owner_id = $2`, id, ownerID).
		Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPreferenceMemoryNotFound
	}
	if err != nil {
		return fmt.Errorf("store: 查询偏好记忆状态: %w", err)
	}
	return ErrPreferenceMemoryNotActive
}
