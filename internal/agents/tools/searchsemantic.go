package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// QueryEmbedder 查询文本向量化的最小依赖面(internal/embedding.Client 实现之;
// 接口注入便于单测 fake,P3-语义选件设计.md §6)。
type QueryEmbedder interface {
	EmbedOne(ctx context.Context, text string) ([]float32, error)
}

// SemanticSearcher 语义检索 tool 对数据层的最小依赖面(*store.Store 实现之)。
type SemanticSearcher interface {
	SemanticCandidates(context.Context, store.SemanticQuery) (store.SemanticResult, error)
}

// SearchPartsSemanticArgs 语义检索入参(§6):query 必填,category 可选。
type SearchPartsSemanticArgs struct {
	Query    string `json:"query"`
	Category string `json:"category,omitempty"`
	TopN     int    `json:"top_n"`
}

// SemanticPartCandidate 语义命中候选:结构化元信息 + 命中解释素材。
type SemanticPartCandidate struct {
	SKU        string         `json:"sku"`
	Brand      string         `json:"brand"`
	Model      string         `json:"model"`
	Category   string         `json:"category"`
	Specs      map[string]any `json:"specs"`
	MatchText  string         `json:"match_text"` // embedding 原文,解释"因哪些词命中"
	Similarity float64        `json:"similarity"` // 余弦相似度,越大越相关
	PriceCNY   *string        `json:"price_cny"`
}

// SearchPartsSemanticResult 语义检索出参,截断与快照口径与 search_parts 一致。
type SearchPartsSemanticResult struct {
	Candidates   []SemanticPartCandidate `json:"candidates"`
	Truncated    int                     `json:"truncated"`
	SnapshotDate string                  `json:"snapshot_date"` // 空 = 库内无价格快照
}

// searchPartsSemanticDescription 与 search_parts 的分工边界(工程实践指引 §四.2)。
const searchPartsSemanticDescription = `按自然语言软偏好(安静/颜值/颜色/风格等)从零件库做语义检索,按相似度降序返回候选。

边界与分工:
- 软偏好(要安静、白色海景房、低调无光、客厅摆得出手)用本工具;预算/插槽/板型/内存代数等硬约束用 search_parts,本工具不做数值过滤。
- query 必填,写用户的软偏好原话或其浓缩;category 可选(cpu|gpu|motherboard|memory|ssd|psu|case|cooler),不传则跨品类;top_n 通常为 5,必须在 1..8。
- 返回的 match_text 是该零件的语义描述原文,选件理由要引用其中命中的词(如"噪音表现:安静低噪");similarity 只用于排序参考,不要向用户展示裸数值。
- 两路工具返回的 sku 同等有效;truncated > 0 表示还有命中未返回。

示例:
- 要安静的显卡:{"query":"要安静的显卡","category":"gpu","top_n":5}
- 白色海景房机箱:{"query":"白色海景房","category":"case","top_n":5}

协作:候选仍须满足预算并通过确定性 validator 校验;语义命中不豁免任何硬约束。`

// NewSearchPartsSemantic 构造语义检索 tool(query 向量化 → pgvector 余弦近邻)。
func NewSearchPartsSemantic(e QueryEmbedder, s SemanticSearcher) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "search_parts_semantic",
		Description: searchPartsSemanticDescription,
	}, func(ctx agent.Context, args SearchPartsSemanticArgs) (SearchPartsSemanticResult, error) {
		return runSearchPartsSemantic(ctx, e, s, args)
	})
}

// runSearchPartsSemantic query 向量化 → store.SemanticCandidates → 出参映射;
// 错误原样上抛(保真),入参维度/top_n/品类校验由 store 层统一把关。
func runSearchPartsSemantic(ctx context.Context, e QueryEmbedder, s SemanticSearcher,
	args SearchPartsSemanticArgs) (SearchPartsSemanticResult, error) {
	if strings.TrimSpace(args.Query) == "" {
		return SearchPartsSemanticResult{}, fmt.Errorf("tools: query 不得为空(软偏好原话或其浓缩)")
	}
	if args.TopN < 1 || args.TopN > 8 {
		return SearchPartsSemanticResult{}, fmt.Errorf("tools: top_n 必须在 1..8,常规使用 5")
	}
	vec, err := e.EmbedOne(ctx, args.Query)
	if err != nil {
		return SearchPartsSemanticResult{}, fmt.Errorf("tools: 查询向量化失败: %w", err)
	}
	res, err := s.SemanticCandidates(ctx, store.SemanticQuery{
		Category:       schemas.Category(args.Category),
		QueryEmbedding: vec,
		TopN:           args.TopN,
	})
	if err != nil {
		return SearchPartsSemanticResult{}, err
	}

	out := SearchPartsSemanticResult{
		Candidates: make([]SemanticPartCandidate, 0, len(res.Candidates)),
		Truncated:  res.Truncated,
	}
	if res.SnapshotDate != nil {
		out.SnapshotDate = res.SnapshotDate.Format("2006-01-02")
	}
	for _, c := range res.Candidates {
		var specs map[string]any
		if err := json.Unmarshal(c.Specs, &specs); err != nil {
			return SearchPartsSemanticResult{}, fmt.Errorf("tools: SKU %q specs 非法 JSON: %w", c.SKU, err)
		}
		out.Candidates = append(out.Candidates, SemanticPartCandidate{
			SKU:        c.SKU,
			Brand:      c.Brand,
			Model:      c.Model,
			Category:   string(c.Category),
			Specs:      specs,
			MatchText:  c.MatchText,
			Similarity: c.Similarity,
			PriceCNY:   c.PriceCNY,
		})
	}
	return out, nil
}
