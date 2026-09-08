package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// 本文件是 P4 版本快照的读写层(版本快照与增量改单.md §2/§3)。
// store 包由此引入唯一的运行时写路径:requirements/builds 两张版本表;
// parts/prices 仍只读(导入走离线 cmd)。

// ErrBuildNotFound 指定会话/版本在 builds 表不存在,用 errors.Is 判别。
var ErrBuildNotFound = errors.New("配置版本不存在")

// SaveBuildVersionParams 落一个新版本所需的全部输入。
// RequirementID 与 RequirementSpec 二选一:前者复用既有需求单(swap_part 不改需求),
// 后者插入新 requirements 行(v1 首次落库、adjust_budget/change_constraint 派生需求)。
type SaveBuildVersionParams struct {
	SessionID       string
	ParentID        *int64          // nil = v1(树根)
	RequirementID   *int64          // 非 nil:复用既有需求单
	RequirementSpec json.RawMessage // RequirementID 为 nil 时必填:新需求单 JSON
	Change          json.RawMessage // 产生本版本的 ChangeRequest 原文;v1 为 nil
	Draft           json.RawMessage // BuildDraft 全文
	Validation      json.RawMessage // ValidationReport
	Quote           json.RawMessage // Quote(含 snapshot_date)
}

// SavedBuild 落库结果:版本号由 DB 侧派生(会话内最大版本 +1),是版本号唯一真值。
type SavedBuild struct {
	ID            int64
	Version       int
	RequirementID int64
}

// SaveBuildVersion 单事务落一个新版本:必要时插 requirements 行,再插 builds 行。
// 任一步失败整体回滚并显式报错(落库失败要响不静默,调用方须如实转达)。
func (s *Store) SaveBuildVersion(ctx context.Context, p SaveBuildVersionParams) (SavedBuild, error) {
	if p.SessionID == "" {
		return SavedBuild{}, fmt.Errorf("store: 落版本失败: session_id 为空")
	}
	if p.RequirementID == nil && len(p.RequirementSpec) == 0 {
		return SavedBuild{}, fmt.Errorf("store: 落版本失败: requirement_id 与 requirement_spec 至少给一个")
	}
	if len(p.Draft) == 0 || len(p.Validation) == 0 || len(p.Quote) == 0 {
		return SavedBuild{}, fmt.Errorf("store: 落版本失败: draft/validation/quote 均不得为空")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SavedBuild{}, fmt.Errorf("store: 开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 父版本必须属于同一会话,防止跨会话串树。
	if p.ParentID != nil {
		var parentSession string
		err := tx.QueryRow(ctx, `SELECT session_id FROM builds WHERE id = $1`, *p.ParentID).Scan(&parentSession)
		if err != nil {
			return SavedBuild{}, fmt.Errorf("store: 父版本 %d 不存在: %w", *p.ParentID, ErrBuildNotFound)
		}
		if parentSession != p.SessionID {
			return SavedBuild{}, fmt.Errorf("store: 父版本 %d 属于会话 %q,与当前会话 %q 不符", *p.ParentID, parentSession, p.SessionID)
		}
	}

	var reqID int64
	if p.RequirementID != nil {
		reqID = *p.RequirementID
	} else {
		err := tx.QueryRow(ctx,
			`INSERT INTO requirements (session_id, spec) VALUES ($1, $2) RETURNING id`,
			p.SessionID, p.RequirementSpec).Scan(&reqID)
		if err != nil {
			return SavedBuild{}, fmt.Errorf("store: 插入需求单失败: %w", err)
		}
	}

	// 版本号 = 会话内最大版本 +1(UNIQUE(session_id, version) 兜底并发冲突)。
	var out SavedBuild
	err = tx.QueryRow(ctx,
		`INSERT INTO builds (session_id, version, parent_id, requirement_id, change, draft, validation, quote)
		 SELECT $1, COALESCE(MAX(version), 0) + 1, $2, $3, $4, $5, $6, $7
		   FROM builds WHERE session_id = $1
		 RETURNING id, version`,
		p.SessionID, p.ParentID, reqID, nullableJSON(p.Change), p.Draft, p.Validation, p.Quote).
		Scan(&out.ID, &out.Version)
	if err != nil {
		return SavedBuild{}, fmt.Errorf("store: 插入版本失败: %w", err)
	}
	out.RequirementID = reqID

	if err := tx.Commit(ctx); err != nil {
		return SavedBuild{}, fmt.Errorf("store: 提交事务失败: %w", err)
	}
	return out, nil
}

// nullableJSON 空切片按 SQL NULL 写入(JSONB 列不接受空字节串)。
func nullableJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// BuildVersion builds 表单行(版本树节点),JSONB 原文透传由调用方解码。
type BuildVersion struct {
	ID            int64
	SessionID     string
	Version       int
	ParentID      *int64
	RequirementID int64
	Change        json.RawMessage // v1 为 nil
	Draft         json.RawMessage
	Validation    json.RawMessage
	Quote         json.RawMessage
	CreatedAt     time.Time
}

const buildVersionColumns = `id, session_id, version, parent_id, requirement_id, change, draft, validation, quote, created_at`

// scanBuildVersion 按 buildVersionColumns 列序扫一行。
func scanBuildVersion(row interface{ Scan(...any) error }) (BuildVersion, error) {
	var b BuildVersion
	err := row.Scan(&b.ID, &b.SessionID, &b.Version, &b.ParentID, &b.RequirementID,
		&b.Change, &b.Draft, &b.Validation, &b.Quote, &b.CreatedAt)
	return b, err
}

// BuildsBySession 取一个会话的全部版本,按 version 升序(版本树回放)。
func (s *Store) BuildsBySession(ctx context.Context, sessionID string) ([]BuildVersion, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+buildVersionColumns+` FROM builds WHERE session_id = $1 ORDER BY version`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: 查询版本树失败: %w", err)
	}
	defer rows.Close()

	var out []BuildVersion
	for rows.Next() {
		b, err := scanBuildVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("store: 读取版本行失败: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历版本树失败: %w", err)
	}
	return out, nil
}

// BuildByVersion 取会话内指定版本;不存在返回 ErrBuildNotFound。
func (s *Store) BuildByVersion(ctx context.Context, sessionID string, version int) (BuildVersion, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+buildVersionColumns+` FROM builds WHERE session_id = $1 AND version = $2`,
		sessionID, version)
	b, err := scanBuildVersion(row)
	if err != nil {
		return BuildVersion{}, fmt.Errorf("store: 会话 %q 无版本 v%d: %w", sessionID, version, ErrBuildNotFound)
	}
	return b, nil
}

// RequirementSpecByID 取需求单 JSON 原文;不存在显式报错。
func (s *Store) RequirementSpecByID(ctx context.Context, id int64) (json.RawMessage, error) {
	var spec json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT spec FROM requirements WHERE id = $1`, id).Scan(&spec)
	if err != nil {
		return nil, fmt.Errorf("store: 需求单 %d 不存在: %w", id, err)
	}
	return spec, nil
}

// SessionSummary 版本会话摘要(CLI list 无参时用于发现会话)。
type SessionSummary struct {
	SessionID     string
	VersionCount  int
	LatestVersion int
	LatestAt      time.Time
}

// Sessions 列出所有落过版本的会话,按最近落库时间倒序。
func (s *Store) Sessions(ctx context.Context) ([]SessionSummary, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT session_id, count(*), max(version), max(created_at)
		   FROM builds GROUP BY session_id ORDER BY max(created_at) DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: 查询会话列表失败: %w", err)
	}
	defer rows.Close()

	var out []SessionSummary
	for rows.Next() {
		var ss SessionSummary
		if err := rows.Scan(&ss.SessionID, &ss.VersionCount, &ss.LatestVersion, &ss.LatestAt); err != nil {
			return nil, fmt.Errorf("store: 读取会话摘要失败: %w", err)
		}
		out = append(out, ss)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历会话列表失败: %w", err)
	}
	return out, nil
}

// PartNames 按 SKU 批量查「品牌 型号」展示名(cmd/builds export 配置表用);
// 未收录的 SKU 不在结果里,由调用方优雅降级(只展示 SKU)。
func (s *Store) PartNames(ctx context.Context, skus []string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT sku, brand || ' ' || model FROM parts WHERE sku = ANY($1)`, skus)
	if err != nil {
		return nil, fmt.Errorf("store: 查询零件名称失败: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string, len(skus))
	for rows.Next() {
		var sku, name string
		if err := rows.Scan(&sku, &name); err != nil {
			return nil, fmt.Errorf("store: 读取零件名称失败: %w", err)
		}
		out[sku] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历零件名称失败: %w", err)
	}
	return out, nil
}
