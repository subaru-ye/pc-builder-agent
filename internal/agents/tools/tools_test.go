package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// fakeSearcher 记录入参并返回预置结果,无 DB。
type fakeSearcher struct {
	gotQuery store.CandidateQuery
	result   store.CandidateResult
	err      error
}

func (f *fakeSearcher) Candidates(_ context.Context, q store.CandidateQuery) (store.CandidateResult, error) {
	f.gotQuery = q
	return f.result, f.err
}

func TestNewSearchPartsConstructs(t *testing.T) {
	if _, err := NewSearchParts(&fakeSearcher{}); err != nil {
		t.Fatalf("NewSearchParts 构造失败(schema 推断?): %v", err)
	}
}

func TestRunSearchPartsMapsQueryAndResult(t *testing.T) {
	price := "1299.00"
	snap := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	f := &fakeSearcher{result: store.CandidateResult{
		Candidates: []store.Candidate{{
			SKU:      "amd-ryzen5-7600",
			Brand:    "AMD",
			Model:    "Ryzen 5 7600",
			Category: schemas.CategoryCPU,
			Specs:    []byte(`{"socket":"AM5","tdp_w":65}`),
			PriceCNY: &price,
		}},
		Truncated:    3,
		SnapshotDate: &snap,
	}}
	maxP := 1500
	got, err := runSearchParts(context.Background(), f, SearchPartsArgs{
		Category:    "cpu",
		Socket:      "AM5",
		PriceMaxCNY: &maxP,
		TopN:        5,
	})
	if err != nil {
		t.Fatalf("意外报错: %v", err)
	}
	// 入参保真映射
	if f.gotQuery.Category != schemas.CategoryCPU || f.gotQuery.Socket != "AM5" ||
		f.gotQuery.PriceMaxCNY == nil || *f.gotQuery.PriceMaxCNY != 1500 || f.gotQuery.TopN != 5 {
		t.Errorf("入参映射错误: %+v", f.gotQuery)
	}
	// 出参映射
	if got.Truncated != 3 || got.SnapshotDate != "2026-07-27" || len(got.Candidates) != 1 {
		t.Errorf("出参头部错误: %+v", got)
	}
	c := got.Candidates[0]
	if c.SKU != "amd-ryzen5-7600" || c.Specs["socket"] != "AM5" || c.PriceCNY == nil || *c.PriceCNY != "1299.00" {
		t.Errorf("候选映射错误: %+v", c)
	}
}

func TestRunSearchPartsErrorPropagates(t *testing.T) {
	f := &fakeSearcher{err: store.ErrInvalidQuery}
	if _, err := runSearchParts(context.Background(), f, SearchPartsArgs{Category: "cpu", TopN: 1}); !errors.Is(err, store.ErrInvalidQuery) {
		t.Errorf("store 错误应透传, 得到 %v", err)
	}
}

// fakeEvaluator 记录收到的 selection 并返回预置校验结果。
type fakeEvaluator struct {
	gotSel schemas.BuildSelection
	result validate.Result
	err    error
}

func (f *fakeEvaluator) Evaluate(_ context.Context, sel schemas.BuildSelection) (validate.Result, error) {
	f.gotSel = sel
	return f.result, f.err
}

const draftJSON = `{
  "schema_version": 1,
  "requirement_ref": "req_001",
  "build_ref": "build_001",
  "selection": {
    "cpu": "amd-ryzen5-7500f",
    "motherboard": "msi-b650m-mortar-wifi",
    "memory": "kingston-fury-beast-ddr5-6000-16gx2",
    "ssd": [{"sku": "samsung-990pro-1tb", "quantity": 1}],
    "gpu": "asus-dual-rtx4070s",
    "psu": "seasonic-focus-gx-750",
    "case": "fractal-north",
    "cooler": "thermalright-pa120-se"
  }
}`

func TestNewValidateBuildConstructs(t *testing.T) {
	if _, err := NewValidateBuild(&fakeEvaluator{}); err != nil {
		t.Fatalf("NewValidateBuild 构造失败(schema 推断?): %v", err)
	}
}

func TestRunValidateBuildDecodesAndDelegates(t *testing.T) {
	f := &fakeEvaluator{result: validate.Result{
		Report: schemas.ValidationReport{BuildRef: "build_001", OverallStatus: schemas.OverallPass},
		Quote:  validate.Quote{TotalCNY: "6100.00"},
	}}
	got, err := runValidateBuild(context.Background(), f, ValidateBuildArgs{BuildDraftJSON: draftJSON})
	if err != nil {
		t.Fatalf("意外报错: %v", err)
	}
	if f.gotSel.CPU != "amd-ryzen5-7500f" || f.gotSel.BuildRef != "build_001" {
		t.Errorf("selection 未正确传入校验核心: %+v", f.gotSel)
	}
	if got.Report.OverallStatus != schemas.OverallPass || got.Quote.TotalCNY != "6100.00" {
		t.Errorf("出参映射错误: %+v", got)
	}
}

func TestRunValidateBuildRejectsBadDraft(t *testing.T) {
	f := &fakeEvaluator{}
	_, err := runValidateBuild(context.Background(), f, ValidateBuildArgs{BuildDraftJSON: `{"schema_version": 2}`})
	if err == nil || !strings.Contains(err.Error(), "BuildDraft 不合法") {
		t.Errorf("非法 draft 应报 schema 错误, 得到 %v", err)
	}
}

func TestRunValidateBuildEvaluatorErrorPropagates(t *testing.T) {
	sentinel := errors.New("store: 未知 SKU")
	f := &fakeEvaluator{err: sentinel}
	if _, err := runValidateBuild(context.Background(), f, ValidateBuildArgs{BuildDraftJSON: draftJSON}); !errors.Is(err, sentinel) {
		t.Errorf("校验核心错误应透传, 得到 %v", err)
	}
}
