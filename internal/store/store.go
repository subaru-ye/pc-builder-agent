// Package store P1 PostgreSQL 读取层:按 SKU 从 parts 展开 ResolvedBuild、
// 读取指定价格快照。只读不写(导入走 Python 数据管道);规则包(internal/rules)
// 不得依赖本包(depguard 强制),依赖方向恒为 cli → store → schemas。
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// ErrUnknownSKU 选择中的 SKU 在 parts 表不存在(或已停用),用 errors.Is 判别。
var ErrUnknownSKU = errors.New("未知 SKU")

// ErrSnapshotNotFound 指定日期没有价格快照批次。
var ErrSnapshotNotFound = errors.New("价格快照不存在")

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
		`SELECT sku, category, active, specs FROM parts WHERE sku = ANY($1)`, sel.SKUs())
	if err != nil {
		return schemas.ResolvedBuild{}, fmt.Errorf("store: 查询 parts 失败: %w", err)
	}
	defer rows.Close()

	found := make(map[string]partRow)
	for rows.Next() {
		var (
			sku    string
			row    partRow
			active bool
		)
		if err := rows.Scan(&sku, &row.Category, &active, &row.Specs); err != nil {
			return schemas.ResolvedBuild{}, fmt.Errorf("store: 读取 parts 行失败: %w", err)
		}
		if !active {
			return schemas.ResolvedBuild{}, fmt.Errorf("store: SKU %q 已停用(active=false): %w", sku, ErrUnknownSKU)
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
