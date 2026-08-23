// Package store P1 PostgreSQL 读取层:按 SKU 从 parts 展开 ResolvedBuild、
// 读取指定价格快照。parts/prices 只读不写(导入走离线 cmd);P4 起版本表
// (requirements/builds)有运行时写路径(versions.go)。规则包(internal/rules)
// 不得依赖本包(depguard 强制),依赖方向恒为 cli → store → schemas。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// ErrUnknownSKU 选择中的 SKU 在 parts 表不存在(或已停用),用 errors.Is 判别。
var ErrUnknownSKU = errors.New("未知 SKU")

// ErrSnapshotNotFound 指定日期没有价格快照批次。
var ErrSnapshotNotFound = errors.New("价格快照不存在")

// ErrInvalidQuery 结构化候选检索的入参非法(品类/过滤条件/top_n),用 errors.Is 判别。
var ErrInvalidQuery = errors.New("候选检索入参非法")

// Store 基于 pgxpool 的只读访问器。
type Store struct {
	pool *pgxpool.Pool
}

// New 建立连接池并 ping;dsn 为 PG_DSN(postgres://…)。
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: 创建连接池失败: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: 数据库不可达: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close 释放连接池。
func (s *Store) Close() { s.pool.Close() }

// partRow parts 表单行的解码中间态。
type partRow struct {
	Category schemas.Category
	Specs    []byte
}

// ResolveBuild 按 BuildSelection 批量查 parts 并展开为规则引擎输入。
// 任一 SKU 不存在/已停用/类目不符/specs 非法均返回错误(schema error 类,不产生 unknown)。
func (s *Store) ResolveBuild(ctx context.Context, sel schemas.BuildSelection) (schemas.ResolvedBuild, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT sku, category, active, catalog_state, specs FROM parts WHERE sku = ANY($1)`, sel.SKUs())
	if err != nil {
		return schemas.ResolvedBuild{}, fmt.Errorf("store: 查询 parts 失败: %w", err)
	}
	defer rows.Close()

	found := make(map[string]partRow)
	for rows.Next() {
		var (
			sku          string
			row          partRow
			active       bool
			catalogState string
		)
		if err := rows.Scan(&sku, &row.Category, &active, &catalogState, &row.Specs); err != nil {
			return schemas.ResolvedBuild{}, fmt.Errorf("store: 读取 parts 行失败: %w", err)
		}
		if !active || catalogState != "active_core" {
			return schemas.ResolvedBuild{}, fmt.Errorf("store: SKU %q 不属于 active_core: %w", sku, ErrUnknownSKU)
		}
		found[sku] = row
	}
	if err := rows.Err(); err != nil {
		return schemas.ResolvedBuild{}, fmt.Errorf("store: 遍历 parts 失败: %w", err)
	}

	out := schemas.ResolvedBuild{BuildRef: sel.BuildRef}

	// 七类必选 + 可选 GPU:逐一取行、校验类目、解码 canonical specs。
	if err := resolveInto(found, sel.CPU, schemas.CategoryCPU, schemas.DecodeCPUSpec, &out.CPU); err != nil {
		return schemas.ResolvedBuild{}, err
	}
	if err := resolveInto(found, sel.Motherboard, schemas.CategoryMotherboard, schemas.DecodeMotherboardSpec, &out.Motherboard); err != nil {
		return schemas.ResolvedBuild{}, err
	}
	if err := resolveInto(found, sel.Memory, schemas.CategoryMemory, schemas.DecodeMemorySpec, &out.Memory); err != nil {
		return schemas.ResolvedBuild{}, err
	}
	if err := resolveInto(found, sel.PSU, schemas.CategoryPSU, schemas.DecodePSUSpec, &out.PSU); err != nil {
		return schemas.ResolvedBuild{}, err
	}
	if err := resolveInto(found, sel.Case, schemas.CategoryCase, schemas.DecodeCaseSpec, &out.Case); err != nil {
		return schemas.ResolvedBuild{}, err
	}
	if err := resolveInto(found, sel.Cooler, schemas.CategoryCooler, schemas.DecodeCoolerSpec, &out.Cooler); err != nil {
		return schemas.ResolvedBuild{}, err
	}
	if sel.GPU != nil {
		out.GPU = new(schemas.GPUSpec)
		if err := resolveInto(found, *sel.GPU, schemas.CategoryGPU, schemas.DecodeGPUSpec, out.GPU); err != nil {
			return schemas.ResolvedBuild{}, err
		}
	}
	for i, sc := range sel.SSDs {
		var spec schemas.SSDSpec
		if err := resolveInto(found, sc.SKU, schemas.CategorySSD, schemas.DecodeSSDSpec, &spec); err != nil {
			return schemas.ResolvedBuild{}, fmt.Errorf("ssd[%d]: %w", i, err)
		}
		out.SSDs = append(out.SSDs, schemas.ResolvedSSD{Spec: spec, Quantity: sc.Quantity})
	}
	return out, nil
}

// resolveInto 从查询结果取一个 SKU:存在性、类目一致性、specs 解码三重校验。
func resolveInto[T any](found map[string]partRow, sku string, want schemas.Category,
	decode func([]byte) (T, error), dst *T) error {
	row, ok := found[sku]
	if !ok {
		return fmt.Errorf("store: SKU %q 不存在: %w", sku, ErrUnknownSKU)
	}
	if row.Category != want {
		return fmt.Errorf("store: SKU %q 类目为 %s,选择位置要求 %s", sku, row.Category, want)
	}
	spec, err := decode(row.Specs)
	if err != nil {
		return fmt.Errorf("store: SKU %q specs 非法: %w", sku, err)
	}
	*dst = spec
	return nil
}

// Snapshot 一个价格快照批次。
type Snapshot struct {
	ID           int64
	SnapshotDate time.Time
	FileSHA256   string
	ImportedAt   time.Time
}

// Price 快照内单 SKU 价格;PriceCNY 为精确十进制文本(NUMERIC(10,2) 原样),
// 数值运算方式待 P2 报价组装时裁定,读取层不做浮点转换。
type Price struct {
	SKU      string
	PriceCNY string
	Source   string
}

// SnapshotByDate 按快照日期(YYYY-MM-DD,仅日期部分参与匹配)取批次。
func (s *Store) SnapshotByDate(ctx context.Context, date time.Time) (Snapshot, error) {
	var snap Snapshot
	err := s.pool.QueryRow(ctx,
		`SELECT id, snapshot_date, file_sha256, imported_at
		   FROM price_snapshots WHERE snapshot_date = $1`, date).
		Scan(&snap.ID, &snap.SnapshotDate, &snap.FileSHA256, &snap.ImportedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, fmt.Errorf("store: 日期 %s 无快照: %w",
			date.Format("2006-01-02"), ErrSnapshotNotFound)
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("store: 查询快照失败: %w", err)
	}
	return snap, nil
}

// LatestSnapshot 取快照日期最新的批次;库内无任何批次时返回 ErrSnapshotNotFound。
func (s *Store) LatestSnapshot(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	err := s.pool.QueryRow(ctx,
		`SELECT id, snapshot_date, file_sha256, imported_at
		   FROM price_snapshots ORDER BY snapshot_date DESC LIMIT 1`).
		Scan(&snap.ID, &snap.SnapshotDate, &snap.FileSHA256, &snap.ImportedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, fmt.Errorf("store: 库内无价格快照: %w", ErrSnapshotNotFound)
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("store: 查询快照失败: %w", err)
	}
	return snap, nil
}

// PricesBySnapshot 取指定批次的全部价格,按 SKU 升序。
func (s *Store) PricesBySnapshot(ctx context.Context, snapshotID int64) ([]Price, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT sku, price_cny::text, source FROM prices
		  WHERE snapshot_id = $1 ORDER BY sku`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("store: 查询价格失败: %w", err)
	}
	defer rows.Close()

	var out []Price
	for rows.Next() {
		var p Price
		if err := rows.Scan(&p.SKU, &p.PriceCNY, &p.Source); err != nil {
			return nil, fmt.Errorf("store: 读取价格行失败: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历价格失败: %w", err)
	}
	return out, nil
}

// CandidateQuery 结构化候选检索的硬约束条件(设计方案 P2 流水线设计 §4.1)。
// 只接受结构化条件,不接自然语言;软偏好(安静/颜值)留 P3 语义路径。
// 零值字段 = 不约束该维度;可选条件的适用品类见各字段注释,跨品类设置属非法查询。
type CandidateQuery struct {
	Category         schemas.Category // 必填,八大类枚举
	Brand            string           // 可选,品牌不区分大小写精确匹配
	Socket           string           // 可选,仅 cpu/motherboard(specs->>'socket')
	FormFactor       string           // 可选,仅 motherboard(板型)/case(支持板型数组包含)
	MemoryGeneration string           // 可选,仅 memory(generation)/motherboard(memory_generation)
	PriceMinCNY      *int             // 可选,最新快照价 >= 此值(含);nil = 不约束
	PriceMaxCNY      *int             // 可选,最新快照价 <= 此值(含);nil = 不约束
	TopN             int              // 必填,返回上限(>0)
}

// Candidate 单个候选件:销售/型号元信息 + canonical specs 原文 + 最新快照价。
type Candidate struct {
	SKU      string
	Brand    string
	Model    string
	Category schemas.Category
	Specs    json.RawMessage // canonical 规格原文,供生成 Agent 读关键参数
	PriceCNY *string         // nil = 最新快照无此 SKU 价(精确十进制文本,读取层不转浮点)
}

// CandidateResult 候选检索结果:已截断列表 + 被截断条数 + 报价所用快照日期。
type CandidateResult struct {
	Candidates   []Candidate
	Truncated    int        // 超出 top_n 被截断的条数(严禁静默截断,如实告知)
	SnapshotDate *time.Time // 报价所用快照日期;nil = 库内无任何快照
}

// Candidates 按硬约束从 parts 表结构化过滤候选件,关联最新快照价,按价升序返回前 top_n 条。
// 过滤全走 SQL(零 LLM);超出 top_n 的部分在 Truncated 如实回报,绝不静默丢弃。
// 库内无快照时价格均为 nil;若同时设了价格约束则返回 ErrSnapshotNotFound。
func (s *Store) Candidates(ctx context.Context, q CandidateQuery) (CandidateResult, error) {
	if err := q.validate(); err != nil {
		return CandidateResult{}, err
	}

	// 报价基准:最新快照。库内无快照时价格列全 nil;但若带价格约束则无法判定,如实报错。
	var (
		snapID   int64
		snapDate *time.Time
	)
	snap, err := s.LatestSnapshot(ctx)
	switch {
	case err == nil:
		snapID = snap.ID
		d := snap.SnapshotDate
		snapDate = &d
	case errors.Is(err, ErrSnapshotNotFound):
		if q.PriceMinCNY != nil || q.PriceMaxCNY != nil {
			return CandidateResult{}, fmt.Errorf("store: 库内无价格快照,无法按价格过滤: %w", err)
		}
	default:
		return CandidateResult{}, err
	}

	// 共享 WHERE:$1 固定为快照 id(供 JOIN 使用),其余条件从 $2 起。
	args := []any{snapID}
	ph := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	conds := []string{"p.active", "p.catalog_state = 'active_core'", "p.category = " + ph(string(q.Category))}
	if q.Brand != "" {
		conds = append(conds, "p.brand ILIKE "+ph(q.Brand))
	}
	if q.Socket != "" {
		conds = append(conds, "p.specs->>'socket' = "+ph(q.Socket))
	}
	if q.FormFactor != "" {
		if q.Category == schemas.CategoryCase {
			// 机箱 supported_form_factors 为 JSONB 数组,用 ? 判定包含。
			conds = append(conds, "p.specs->'supported_form_factors' ? "+ph(q.FormFactor))
		} else {
			conds = append(conds, "p.specs->>'form_factor' = "+ph(q.FormFactor))
		}
	}
	if q.MemoryGeneration != "" {
		if q.Category == schemas.CategoryMotherboard {
			conds = append(conds, "p.specs->>'memory_generation' = "+ph(q.MemoryGeneration))
		} else {
			conds = append(conds, "p.specs->>'generation' = "+ph(q.MemoryGeneration))
		}
	}
	if q.PriceMinCNY != nil {
		conds = append(conds, "pr.price_cny >= "+ph(*q.PriceMinCNY))
	}
	if q.PriceMaxCNY != nil {
		conds = append(conds, "pr.price_cny <= "+ph(*q.PriceMaxCNY))
	}

	where := strings.Join(conds, " AND ")
	from := "FROM parts p LEFT JOIN prices pr ON pr.sku = p.sku AND pr.snapshot_id = $1 WHERE " + where

	// 先 COUNT 全量命中(与 SELECT 同 WHERE),再取 top_n 行,Truncated = total - 返回数。
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) "+from, args...).Scan(&total); err != nil {
		return CandidateResult{}, fmt.Errorf("store: 统计候选总数失败: %w", err)
	}

	selectSQL := "SELECT p.sku, p.brand, p.model, p.category, p.specs, pr.price_cny::text " +
		from + " ORDER BY pr.price_cny ASC NULLS LAST, p.sku LIMIT " + ph(q.TopN)
	rows, err := s.pool.Query(ctx, selectSQL, args...)
	if err != nil {
		return CandidateResult{}, fmt.Errorf("store: 查询候选失败: %w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.SKU, &c.Brand, &c.Model, &c.Category, &c.Specs, &c.PriceCNY); err != nil {
			return CandidateResult{}, fmt.Errorf("store: 读取候选行失败: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return CandidateResult{}, fmt.Errorf("store: 遍历候选失败: %w", err)
	}

	return CandidateResult{Candidates: out, Truncated: total - len(out), SnapshotDate: snapDate}, nil
}

// validate 校验候选检索入参:品类合法、top_n 为正、价格区间一致、可选条件适用于对应品类。
func (q CandidateQuery) validate() error {
	if !validCategory(q.Category) {
		return fmt.Errorf("%w: 非法品类 %q", ErrInvalidQuery, q.Category)
	}
	if q.TopN <= 0 {
		return fmt.Errorf("%w: top_n 必须为正整数,得到 %d", ErrInvalidQuery, q.TopN)
	}
	if q.PriceMinCNY != nil && *q.PriceMinCNY < 0 {
		return fmt.Errorf("%w: price_min 不得为负,得到 %d", ErrInvalidQuery, *q.PriceMinCNY)
	}
	if q.PriceMaxCNY != nil && *q.PriceMaxCNY <= 0 {
		return fmt.Errorf("%w: price_max 必须为正,得到 %d", ErrInvalidQuery, *q.PriceMaxCNY)
	}
	if q.PriceMinCNY != nil && q.PriceMaxCNY != nil && *q.PriceMinCNY > *q.PriceMaxCNY {
		return fmt.Errorf("%w: price_min %d > price_max %d", ErrInvalidQuery, *q.PriceMinCNY, *q.PriceMaxCNY)
	}
	if q.Socket != "" && q.Category != schemas.CategoryCPU && q.Category != schemas.CategoryMotherboard {
		return fmt.Errorf("%w: socket 过滤仅适用于 cpu/motherboard,得到 %q", ErrInvalidQuery, q.Category)
	}
	if q.FormFactor != "" && q.Category != schemas.CategoryMotherboard && q.Category != schemas.CategoryCase {
		return fmt.Errorf("%w: form_factor 过滤仅适用于 motherboard/case,得到 %q", ErrInvalidQuery, q.Category)
	}
	if q.MemoryGeneration != "" && q.Category != schemas.CategoryMemory && q.Category != schemas.CategoryMotherboard {
		return fmt.Errorf("%w: memory_generation 过滤仅适用于 memory/motherboard,得到 %q", ErrInvalidQuery, q.Category)
	}
	return nil
}

// validCategory 判定是否为八大类之一。
func validCategory(c schemas.Category) bool {
	for _, known := range schemas.AllCategories {
		if c == known {
			return true
		}
	}
	return false
}
