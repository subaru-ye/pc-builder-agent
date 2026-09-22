// Package tools 把确定性能力注册为 ADK functiontool,供 P2 流水线的 LLM Agent 调用。
// 契约:参数保真、严禁静默转换/截断,
// 工具描述从 Agent 视角写(边界 + 示例 + 协作关系,docs/tech/开发约定.md)。
// 本包只做「结构化入参 ↔ 内核」的映射;检索/校验逻辑分别在 store 与 agents/validate。
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// PartSearcher 检索 tool 对数据层的最小依赖面(*store.Store 实现之)。
type PartSearcher interface {
	Candidates(context.Context, store.CandidateQuery) (store.CandidateResult, error)
}

// SearchPartsArgs 结构化检索入参(§4.1):category 必填,其余按品类可选。
type SearchPartsArgs struct {
	Category         string `json:"category"`
	Brand            string `json:"brand,omitempty"`
	Socket           string `json:"socket,omitempty"`
	FormFactor       string `json:"form_factor,omitempty"`
	MemoryGeneration string `json:"memory_generation,omitempty"`
	PriceMinCNY      *int   `json:"price_min_cny,omitempty"`
	PriceMaxCNY      *int   `json:"price_max_cny,omitempty"`
	TopN             int    `json:"top_n"`
}

// PartCandidate 单条候选:SKU + 品牌型号 + canonical specs + 快照价(null=缺价)。
type PartCandidate struct {
	SKU      string         `json:"sku"`
	Brand    string         `json:"brand"`
	Model    string         `json:"model"`
	Specs    map[string]any `json:"specs"`
	PriceCNY *string        `json:"price_cny"`
}

// SearchPartsResult 检索出参:候选列表 + 截断计数(如实,严禁静默截断)+ 报价快照日期。
type SearchPartsResult struct {
	Candidates   []PartCandidate `json:"candidates"`
	Truncated    int             `json:"truncated"`
	SnapshotDate string          `json:"snapshot_date"` // 空 = 库内无价格快照
}

// searchPartsDescription 从生成 Agent 视角描述边界与协作(docs/tech/开发约定.md)。
const searchPartsDescription = `按硬约束从零件库(PostgreSQL)结构化检索候选零件,按快照价升序返回(无价者排最后)。

边界:
- 只接受结构化条件,不接受自然语言;软偏好(安静/颜值)不在本工具能力内。
- category 必填,取值:cpu|gpu|motherboard|memory|ssd|psu|case|cooler;top_n 通常为 5,必须在 1..8。
- socket 仅适用 cpu/motherboard;form_factor 仅适用 motherboard/case;memory_generation 仅适用 memory/motherboard;违规组合会报错。
- truncated > 0 表示还有匹配未返回,需要更多就放宽条件或加大 top_n,不要臆造 SKU。

示例:
- 查 AM5 主板前 5:{"category":"motherboard","socket":"AM5","top_n":5}
- 查 2000 元内显卡:{"category":"gpu","price_max_cny":2000,"top_n":8}

协作:所有选件必须来自本工具返回的 sku;整机输出后由确定性 validator 校验兼容性。`

// NewSearchParts 构造结构化选件检索 tool(SQL 硬过滤,零 LLM)。
func NewSearchParts(s PartSearcher) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "search_parts",
		Description: searchPartsDescription,
	}, func(ctx agent.Context, args SearchPartsArgs) (SearchPartsResult, error) {
		return runSearchParts(ctx, s, args)
	})
}

// runSearchParts 入参映射 → store.Candidates → 出参映射;错误原样上抛(保真)。
func runSearchParts(ctx context.Context, s PartSearcher, args SearchPartsArgs) (SearchPartsResult, error) {
	if args.TopN < 1 || args.TopN > 8 {
		return SearchPartsResult{}, fmt.Errorf("tools: top_n 必须在 1..8,常规使用 5")
	}
	res, err := s.Candidates(ctx, store.CandidateQuery{
		Category:         schemas.Category(args.Category),
		Brand:            args.Brand,
		Socket:           args.Socket,
		FormFactor:       args.FormFactor,
		MemoryGeneration: args.MemoryGeneration,
		PriceMinCNY:      args.PriceMinCNY,
		PriceMaxCNY:      args.PriceMaxCNY,
		TopN:             args.TopN,
	})
	if err != nil {
		return SearchPartsResult{}, err
	}

	out := SearchPartsResult{Candidates: make([]PartCandidate, 0, len(res.Candidates)), Truncated: res.Truncated}
	if res.SnapshotDate != nil {
		out.SnapshotDate = res.SnapshotDate.Format("2006-01-02")
	}
	for _, c := range res.Candidates {
		var specs map[string]any
		if err := json.Unmarshal(c.Specs, &specs); err != nil {
			return SearchPartsResult{}, fmt.Errorf("tools: SKU %q specs 非法 JSON: %w", c.SKU, err)
		}
		out.Candidates = append(out.Candidates, PartCandidate{
			SKU:      c.SKU,
			Brand:    c.Brand,
			Model:    c.Model,
			Specs:    specs,
			PriceCNY: c.PriceCNY,
		})
	}
	return out, nil
}
