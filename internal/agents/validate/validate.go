// Package validate 实现 P2 校验 Agent 的确定性核心(零 LLM、零模型 SDK)。
// 职责(设计方案 §三.3 / P2 流水线设计 §3.3、§4.2):把 store.ResolveBuild →
// rules 引擎 → ValidationReport 与最新快照报价合计包成一个确定性节点,
// 结论覆盖上游任何 Agent 自评。本包受 golangci-lint depguard 约束不得 import LLM/ADK 包
// (与 internal/rules 一致的零 LLM 边界)。
package validate

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/subaru-ye/pc-builder-agent/internal/rules"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// Resolver 校验节点对数据层的最小依赖面(便于用 fake 单测,*store.Store 实现之)。
type Resolver interface {
	ResolveBuild(context.Context, schemas.BuildSelection) (schemas.ResolvedBuild, error)
	LatestSnapshot(context.Context) (store.Snapshot, error)
	PricesBySnapshot(context.Context, int64) ([]store.Price, error)
}

// QuoteLine 报价单行:一件(或同款多件)的品类、SKU、数量、单价与小计。
// 缺价时 UnitPriceCNY/SubtotalCNY 为 nil(优雅降级,不阻断校验)。
type QuoteLine struct {
	Category     schemas.Category
	SKU          string
	Quantity     int
	UnitPriceCNY *string
	SubtotalCNY  *string
}

// Quote 报价块(P2 流水线设计 §4.2):快照日期 + 合计 + 缺价计数。
// 金额一律精确十进制文本(内部按「分」整数运算,不用浮点)。
type Quote struct {
	SnapshotDate string // YYYY-MM-DD;库内无快照时为空
	TotalCNY     string // 已知价部分的合计
	MissingCount int    // 缺价 SKU 数(去重)
	MissingSKUs  []string
	Lines        []QuoteLine
}

// Result 校验节点产出:P1 校验报告 + 报价块。
type Result struct {
	Report schemas.ValidationReport
	Quote  Quote
}

// Node 确定性校验节点。engine 装配全部 12 条规则,data 为只读数据源。
type Node struct {
	data   Resolver
	engine *rules.Engine
}

// New 用给定数据源构造校验节点(生产环境传 *store.Store)。
func New(data Resolver) *Node {
	return &Node{data: data, engine: rules.NewDefaultEngine()}
}

// Evaluate 对一份配置选择执行:解析真值 → 规则校验 → 报价合计。
// 未知 SKU / schema error / 数据库错误作为 error 显式返回(§4.2 保真,不静默吞);
// 报价缺价则在 Quote 里优雅降级标注,不视为错误。
func (n *Node) Evaluate(ctx context.Context, sel schemas.BuildSelection) (Result, error) {
	resolved, err := n.data.ResolveBuild(ctx, sel)
	if err != nil {
		return Result{}, fmt.Errorf("校验: 解析配置失败: %w", err)
	}
	report, err := n.engine.Validate(resolved)
	if err != nil {
		return Result{}, fmt.Errorf("校验: 规则执行失败: %w", err)
	}
	quote, err := n.quote(ctx, sel)
	if err != nil {
		return Result{}, err
	}
	return Result{Report: report, Quote: quote}, nil
}

// quote 取最新快照价即时合计;库内无快照时全部缺价、优雅降级(不落库,归 P4)。
func (n *Node) quote(ctx context.Context, sel schemas.BuildSelection) (Quote, error) {
	snap, err := n.data.LatestSnapshot(ctx)
	if errors.Is(err, store.ErrSnapshotNotFound) {
		return computeQuote(sel, "", nil), nil
	}
	if err != nil {
		return Quote{}, fmt.Errorf("校验: 取最新快照失败: %w", err)
	}
	prices, err := n.data.PricesBySnapshot(ctx, snap.ID)
	if err != nil {
		return Quote{}, fmt.Errorf("校验: 取快照价格失败: %w", err)
	}
	byS := make(map[string]string, len(prices))
	for _, p := range prices {
		byS[p.SKU] = p.PriceCNY
	}
	return computeQuote(sel, snap.SnapshotDate.Format("2006-01-02"), byS), nil
}

// computeQuote 按「分」整数合计已知价部分;缺价件进 MissingSKUs(去重、排序)不入合计。
// 价格文本非法只会静默跳过为缺价?否——价格来自受控 NUMERIC(10,2),解析失败视为缺价并计缺,
// 保证报价永不因单件坏数据崩溃(校验结论以规则引擎为准)。
func computeQuote(sel schemas.BuildSelection, snapshotDate string, priceBySKU map[string]string) Quote {
	lines := buildLines(sel)
	var totalCents int64
	missingSet := make(map[string]struct{})
	for i := range lines {
		text, ok := priceBySKU[lines[i].SKU]
		if ok {
			if cents, err := parseCents(text); err == nil {
				sub := cents * int64(lines[i].Quantity)
				u := formatCents(cents)
				s := formatCents(sub)
				lines[i].UnitPriceCNY = &u
				lines[i].SubtotalCNY = &s
				totalCents += sub
				continue
			}
		}
		missingSet[lines[i].SKU] = struct{}{}
	}
	missing := make([]string, 0, len(missingSet))
	for sku := range missingSet {
		missing = append(missing, sku)
	}
	sort.Strings(missing)
	return Quote{
		SnapshotDate: snapshotDate,
		TotalCNY:     formatCents(totalCents),
		MissingCount: len(missing),
		MissingSKUs:  missing,
		Lines:        lines,
	}
}

// buildLines 把配置展开为稳定顺序的报价行:cpu、gpu(有则)、主板、内存、各 ssd、电源、机箱、散热。
func buildLines(sel schemas.BuildSelection) []QuoteLine {
	lines := []QuoteLine{{Category: schemas.CategoryCPU, SKU: sel.CPU, Quantity: 1}}
	if sel.GPU != nil {
		lines = append(lines, QuoteLine{Category: schemas.CategoryGPU, SKU: *sel.GPU, Quantity: 1})
	}
	lines = append(lines,
		QuoteLine{Category: schemas.CategoryMotherboard, SKU: sel.Motherboard, Quantity: 1},
		QuoteLine{Category: schemas.CategoryMemory, SKU: sel.Memory, Quantity: 1},
	)
	for _, s := range sel.SSDs {
		lines = append(lines, QuoteLine{Category: schemas.CategorySSD, SKU: s.SKU, Quantity: s.Quantity})
	}
	lines = append(lines,
		QuoteLine{Category: schemas.CategoryPSU, SKU: sel.PSU, Quantity: 1},
		QuoteLine{Category: schemas.CategoryCase, SKU: sel.Case, Quantity: 1},
		QuoteLine{Category: schemas.CategoryCooler, SKU: sel.Cooler, Quantity: 1},
	)
	return lines
}
