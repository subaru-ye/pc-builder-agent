package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// EmbeddingDims parts.embedding 向量维度(迁移 00003 的 vector(1024),
// 端点 embedding 模型实测输出);换模型即换维度,须新迁移重算全量。
const EmbeddingDims = 1024

// SemanticQuery 语义候选检索入参(自主规划流程.md §5)。
// 只管软偏好召回;预算/插槽/板型等硬约束走 Candidates 的 SQL 路径。
type SemanticQuery struct {
	Category       schemas.Category // 可选,空 = 跨品类;非空须为八大类之一
	QueryEmbedding []float32        // 必填,EmbeddingDims 维查询向量
	TopN           int              // 必填,返回上限(>0)
}

// SemanticCandidate 语义命中的候选件:结构化元信息 + 命中解释素材。
type SemanticCandidate struct {
	Candidate
	MatchText  string  // embedding 原文,供生成 Agent 解释"因哪些词命中"
	Similarity float64 // 余弦相似度(1 - cosine 距离),越大越相关
}

// SemanticResult 语义检索结果,截断与快照口径与 CandidateResult 一致。
type SemanticResult struct {
	Candidates   []SemanticCandidate
	Truncated    int        // 超出 top_n 被截断的条数(严禁静默截断)
	SnapshotDate *time.Time // 报价所用快照日期;nil = 库内无任何快照
}

// SemanticCandidates 按查询向量在 parts.embedding 上做余弦近邻检索,
// 只召回 active 且已生成 embedding 的零件,关联最新快照价,按相似度降序取前 top_n。
// 160 SKU 量级顺序扫描即可,不依赖向量索引。
func (s *Store) SemanticCandidates(ctx context.Context, q SemanticQuery) (SemanticResult, error) {
	if err := q.validate(); err != nil {
		return SemanticResult{}, err
	}

	// 报价基准:最新快照;库内无快照时价格列全 nil(本路径无价格约束,不报错)。
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
		// 优雅降级:候选照常返回,价格为 nil。
	default:
		return SemanticResult{}, err
	}

	args := []any{snapID}
	conds := []string{"p.active", "p.catalog_state = 'active_core'", "p.embedding IS NOT NULL"}
	if q.Category != "" {
		args = append(args, string(q.Category))
		conds = append(conds, fmt.Sprintf("p.category = $%d", len(args)))
	}
	from := "FROM parts p LEFT JOIN prices pr ON pr.sku = p.sku AND pr.snapshot_id = $1 WHERE " +
		strings.Join(conds, " AND ")

	// 与 Candidates 同口径:先 COUNT 全量命中,再取 top_n,Truncated 如实回报。
	// COUNT 不引用向量参数(pgx 扩展协议不允许多余参数),向量只加给 SELECT。
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) "+from, args...).Scan(&total); err != nil {
		return SemanticResult{}, fmt.Errorf("store: 统计语义候选总数失败: %w", err)
	}

	args = append(args, VectorLiteral(q.QueryEmbedding))
	vecPh := len(args)
	args = append(args, q.TopN)
	selectSQL := fmt.Sprintf("SELECT p.sku, p.brand, p.model, p.category, p.specs, COALESCE(p.embedding_text, ''), "+
		"1 - (p.embedding <=> $%d::vector) AS similarity, pr.price_cny::text "+
		"%s ORDER BY p.embedding <=> $%d::vector, p.sku LIMIT $%d", vecPh, from, vecPh, len(args))
	rows, err := s.pool.Query(ctx, selectSQL, args...)
	if err != nil {
		return SemanticResult{}, fmt.Errorf("store: 语义检索失败: %w", err)
	}
	defer rows.Close()

	var out []SemanticCandidate
	for rows.Next() {
		var c SemanticCandidate
		if err := rows.Scan(&c.SKU, &c.Brand, &c.Model, &c.Category, &c.Specs,
			&c.MatchText, &c.Similarity, &c.PriceCNY); err != nil {
			return SemanticResult{}, fmt.Errorf("store: 读取语义候选行失败: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return SemanticResult{}, fmt.Errorf("store: 遍历语义候选失败: %w", err)
	}

	return SemanticResult{Candidates: out, Truncated: total - len(out), SnapshotDate: snapDate}, nil
}

// validate 校验语义检索入参:维度精确匹配、top_n 为正、品类空或合法。
func (q SemanticQuery) validate() error {
	if len(q.QueryEmbedding) != EmbeddingDims {
		return fmt.Errorf("%w: 查询向量须为 %d 维,得到 %d", ErrInvalidQuery, EmbeddingDims, len(q.QueryEmbedding))
	}
	if q.TopN <= 0 {
		return fmt.Errorf("%w: top_n 必须为正整数,得到 %d", ErrInvalidQuery, q.TopN)
	}
	if q.Category != "" && !validCategory(q.Category) {
		return fmt.Errorf("%w: 非法品类 %q", ErrInvalidQuery, q.Category)
	}
	return nil
}

// VectorLiteral 把向量编码为 pgvector 文本字面量("[0.1,0.2,…]"),经 ::vector 转换。
// 本包查询与 cmd/embedparts 写入共用同一编码;160 SKU 量级不引入 pgvector-go。
func VectorLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v)*10 + 2)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
