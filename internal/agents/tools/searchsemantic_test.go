package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// fakeEmbedder 记录查询文本并返回预置向量,无端点。
type fakeEmbedder struct {
	gotText string
	vec     []float32
	err     error
}

func (f *fakeEmbedder) EmbedOne(_ context.Context, text string) ([]float32, error) {
	f.gotText = text
	return f.vec, f.err
}

// fakeSemanticSearcher 记录入参并返回预置结果,无 DB。
type fakeSemanticSearcher struct {
	gotQuery store.SemanticQuery
	result   store.SemanticResult
	err      error
}

func (f *fakeSemanticSearcher) SemanticCandidates(_ context.Context, q store.SemanticQuery) (store.SemanticResult, error) {
	f.gotQuery = q
	return f.result, f.err
}

func TestNewSearchPartsSemanticConstructs(t *testing.T) {
	if _, err := NewSearchPartsSemantic(&fakeEmbedder{}, &fakeSemanticSearcher{}); err != nil {
		t.Fatalf("NewSearchPartsSemantic 构造失败(schema 推断?): %v", err)
	}
}

func TestRunSearchPartsSemanticMapsQueryAndResult(t *testing.T) {
	price := "4599.00"
	snap := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	e := &fakeEmbedder{vec: []float32{0.1, 0.2}}
	s := &fakeSemanticSearcher{result: store.SemanticResult{
		Candidates: []store.SemanticCandidate{{
			Candidate: store.Candidate{
				SKU:      "gpu-asus-4070tis-tuf",
				Brand:    "Asus",
				Model:    "TUF 4070 Ti SUPER",
				Category: schemas.CategoryGPU,
				Specs:    []byte(`{"tdp_w":285}`),
				PriceCNY: &price,
			},
			MatchText:  "显卡: Asus TUF。噪音表现:安静低噪。",
			Similarity: 0.66,
		}},
		Truncated:    15,
		SnapshotDate: &snap,
	}}
	got, err := runSearchPartsSemantic(context.Background(), e, s, SearchPartsSemanticArgs{
		Query:    "要安静的显卡",
		Category: "gpu",
		TopN:     5,
	})
	if err != nil {
		t.Fatalf("意外报错: %v", err)
	}
	// 查询文本原样进 embedder,向量与品类保真进 store
	if e.gotText != "要安静的显卡" {
		t.Errorf("查询文本未保真: %q", e.gotText)
	}
	if s.gotQuery.Category != schemas.CategoryGPU || s.gotQuery.TopN != 5 ||
		len(s.gotQuery.QueryEmbedding) != 2 || s.gotQuery.QueryEmbedding[1] != 0.2 {
		t.Errorf("入参映射错误: %+v", s.gotQuery)
	}
	// 出参映射
	if got.Truncated != 15 || got.SnapshotDate != "2026-07-28" || len(got.Candidates) != 1 {
		t.Errorf("出参头部错误: %+v", got)
	}
	c := got.Candidates[0]
	if c.SKU != "gpu-asus-4070tis-tuf" || c.Category != "gpu" || c.Similarity != 0.66 ||
		!strings.Contains(c.MatchText, "安静低噪") || c.Specs["tdp_w"] != float64(285) ||
		c.PriceCNY == nil || *c.PriceCNY != "4599.00" {
		t.Errorf("候选映射错误: %+v", c)
	}
}

func TestRunSearchPartsSemanticRejectsEmptyQuery(t *testing.T) {
	e := &fakeEmbedder{}
	_, err := runSearchPartsSemantic(context.Background(), e, &fakeSemanticSearcher{},
		SearchPartsSemanticArgs{Query: "  ", TopN: 5})
	if err == nil || !strings.Contains(err.Error(), "query 不得为空") {
		t.Errorf("空 query 应报错, 得到 %v", err)
	}
	if e.gotText != "" {
		t.Error("空 query 不应触发向量化")
	}
}

func TestRunSearchPartsSemanticEmbedderErrorPropagates(t *testing.T) {
	sentinel := errors.New("端点返回 429")
	_, err := runSearchPartsSemantic(context.Background(), &fakeEmbedder{err: sentinel},
		&fakeSemanticSearcher{}, SearchPartsSemanticArgs{Query: "白色海景房", TopN: 5})
	if !errors.Is(err, sentinel) {
		t.Errorf("embedder 错误应透传, 得到 %v", err)
	}
}

func TestRunSearchPartsSemanticStoreErrorPropagates(t *testing.T) {
	_, err := runSearchPartsSemantic(context.Background(), &fakeEmbedder{vec: []float32{1}},
		&fakeSemanticSearcher{err: store.ErrInvalidQuery},
		SearchPartsSemanticArgs{Query: "白色海景房", TopN: 0})
	if !errors.Is(err, store.ErrInvalidQuery) {
		t.Errorf("store 错误应透传, 得到 %v", err)
	}
}
