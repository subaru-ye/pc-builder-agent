package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// unitVec 合成单位基向量:第 idx 维为 1,其余为 0(余弦相似度可精确预期)。
func unitVec(idx int) []float32 {
	v := make([]float32, EmbeddingDims)
	v[idx] = 1
	return v
}

// seedEmbeddings 给夹具中的部分 SKU 写入合成 embedding(直连 SQL,绕开只读 Store)。
func seedEmbeddings(t *testing.T, s *Store, byIdx map[string]int) {
	t.Helper()
	ctx := context.Background()
	for sku, idx := range byIdx {
		_, err := s.pool.Exec(ctx,
			`UPDATE parts SET embedding = $1::vector, embedding_text = $2 WHERE sku = $3`,
			VectorLiteral(unitVec(idx)), "合成文本 "+sku, sku)
		if err != nil {
			t.Fatalf("写入 %s embedding 失败: %v", sku, err)
		}
	}
}

func TestSemanticCandidates(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// gpu 与查询向量同向(相似度 1);case 正交(相似度 0);cooler 半相关。
	seedEmbeddings(t, s, map[string]int{
		"gpu-rtx4070":  0,
		"case-air":     1,
		"cooler-ak620": 2,
	})
	// 查询向量:0.8*e0 + 0.6*e2 → gpu 最近,cooler 次之,case 最远。
	query := make([]float32, EmbeddingDims)
	query[0] = 0.8
	query[2] = 0.6

	t.Run("按相似度降序返回且带解释素材", func(t *testing.T) {
		r, err := s.SemanticCandidates(ctx, SemanticQuery{QueryEmbedding: query, TopN: 10})
		if err != nil {
			t.Fatalf("SemanticCandidates 失败: %v", err)
		}
		if len(r.Candidates) != 3 || r.Truncated != 0 {
			t.Fatalf("命中 %d 条(截断 %d),want 3 条 0 截断", len(r.Candidates), r.Truncated)
		}
		wantOrder := []string{"gpu-rtx4070", "cooler-ak620", "case-air"}
		for i, want := range wantOrder {
			if r.Candidates[i].SKU != want {
				t.Errorf("第 %d 名 = %s, want %s", i+1, r.Candidates[i].SKU, want)
			}
		}
		if sim := r.Candidates[0].Similarity; math.Abs(sim-0.8) > 1e-6 {
			t.Errorf("gpu 相似度 = %v, want 0.8", sim)
		}
		if mt := r.Candidates[0].MatchText; !strings.Contains(mt, "gpu-rtx4070") {
			t.Errorf("MatchText = %q, 应含 embedding 原文", mt)
		}
		if r.Candidates[0].PriceCNY == nil || *r.Candidates[0].PriceCNY != "4599.00" {
			t.Errorf("gpu 快照价 = %v, want 4599.00", r.Candidates[0].PriceCNY)
		}
		if r.SnapshotDate == nil {
			t.Error("SnapshotDate 不应为 nil")
		}
	})

	t.Run("未生成 embedding 的零件不参与召回", func(t *testing.T) {
		r, err := s.SemanticCandidates(ctx, SemanticQuery{
			Category: schemas.CategoryCPU, QueryEmbedding: query, TopN: 5})
		if err != nil {
			t.Fatalf("SemanticCandidates 失败: %v", err)
		}
		if len(r.Candidates) != 0 {
			t.Errorf("cpu 未写 embedding,命中 %d 条,want 0", len(r.Candidates))
		}
	})

	t.Run("品类过滤", func(t *testing.T) {
		r, err := s.SemanticCandidates(ctx, SemanticQuery{
			Category: schemas.CategoryCase, QueryEmbedding: query, TopN: 5})
		if err != nil {
			t.Fatalf("SemanticCandidates 失败: %v", err)
		}
		if len(r.Candidates) != 1 || r.Candidates[0].SKU != "case-air" {
			t.Fatalf("命中 %+v, want 仅 case-air", r.Candidates)
		}
	})

	t.Run("截断如实回报", func(t *testing.T) {
		r, err := s.SemanticCandidates(ctx, SemanticQuery{QueryEmbedding: query, TopN: 2})
		if err != nil {
			t.Fatalf("SemanticCandidates 失败: %v", err)
		}
		if len(r.Candidates) != 2 || r.Truncated != 1 {
			t.Errorf("返回 %d 条截断 %d,want 2 条截断 1", len(r.Candidates), r.Truncated)
		}
	})

	t.Run("停用零件不召回", func(t *testing.T) {
		seedEmbeddings(t, s, map[string]int{"cpu-inactive": 3})
		r, err := s.SemanticCandidates(ctx, SemanticQuery{
			Category: schemas.CategoryCPU, QueryEmbedding: query, TopN: 5})
		if err != nil {
			t.Fatalf("SemanticCandidates 失败: %v", err)
		}
		if len(r.Candidates) != 0 {
			t.Errorf("停用 SKU 不应召回,命中 %d 条", len(r.Candidates))
		}
	})

	t.Run("入参校验", func(t *testing.T) {
		cases := []SemanticQuery{
			{QueryEmbedding: []float32{1, 2, 3}, TopN: 5},          // 维度错
			{QueryEmbedding: query, TopN: 0},                       // top_n 非正
			{Category: "keyboard", QueryEmbedding: query, TopN: 5}, // 品类非法
		}
		for i, q := range cases {
			if _, err := s.SemanticCandidates(ctx, q); !errors.Is(err, ErrInvalidQuery) {
				t.Errorf("case %d: err = %v, want ErrInvalidQuery", i, err)
			}
		}
	})
}

// TestSemanticCandidatesNoSnapshot 库内无快照时优雅降级:价格 nil、不报错。
func TestSemanticCandidatesNoSnapshot(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if _, err := s.pool.Exec(ctx, `DELETE FROM price_snapshots`); err != nil {
		t.Fatalf("清空快照失败: %v", err)
	}
	seedEmbeddings(t, s, map[string]int{"gpu-rtx4070": 0})

	query := unitVec(0)
	r, err := s.SemanticCandidates(ctx, SemanticQuery{QueryEmbedding: query, TopN: 5})
	if err != nil {
		t.Fatalf("无快照应优雅降级,得到 %v", err)
	}
	if len(r.Candidates) != 1 || r.Candidates[0].PriceCNY != nil || r.SnapshotDate != nil {
		t.Errorf("want 1 条候选且价格/快照日期为 nil,得到 %+v", r)
	}
}

// TestVectorLiteral 向量字面量编码与 PG 往返一致。
func TestVectorLiteral(t *testing.T) {
	got := VectorLiteral([]float32{0.5, -1, 0})
	if got != "[0.5,-1,0]" {
		t.Errorf("VectorLiteral = %q, want [0.5,-1,0]", got)
	}
}
