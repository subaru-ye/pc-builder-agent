package evalsuite

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestAdaptiveBudgetKeepsDeliveryVetoAndRequiresProof(t *testing.T) {
	c := testCase()
	c.Expect = Expect{Outcome: "budget_adaptive"}
	if !AssertCase(c, passingResult("8000.00"), testSnapshot()).Passed {
		t.Fatal("valid delivery rejected")
	}
	if f := hasFailure(t, AssertCase(c, passingResult("9000.00"), testSnapshot()), "A4"); !f.Veto {
		t.Fatal("over-budget delivery lost veto")
	}
	r := passingResult("8000.00")
	r.Succeeded = false
	r.Decision = &buildharness.Decision{Kind: "search_exhausted", Reason: "no_verified_solution"}
	if AssertCase(c, r, testSnapshot()).Passed {
		t.Fatal("model search failure became soft pass")
	}
	cat := store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)}}
	for _, category := range schemas.AllCategories {
		price := "100.00"
		cat.Candidates = append(cat.Candidates, store.Candidate{SKU: string(category), Category: category, PriceCNY: &price})
	}
	c.Requirement.BudgetCNY = 100
	d := buildharness.AssessCatalog(buildharness.BuildInput{Requirement: c.Requirement}, cat)
	if d == nil {
		t.Fatal("fixture must prove budget infeasibility")
	}
	r = buildharness.BuildResult{Decision: d, Message: d.Message}
	snap := NewSnapshotView(cat)
	if !AssertCase(c, r, snap).Passed {
		t.Fatal("valid budget proof rejected")
	}
	for _, mutate := range []func(*buildharness.BuildResult){
		func(r *buildharness.BuildResult) { r.Decision.LowerBoundCNY = "99999.00" },
		func(r *buildharness.BuildResult) { r.Decision.Scope = "entire_market" },
		func(r *buildharness.BuildResult) {
			r.Decision.Message = "加到下限就保证能装好"
			r.Message = r.Decision.Message
		},
		func(r *buildharness.BuildResult) { r.Attempts = 3 },
	} {
		copyDecision := *d
		bad := buildharness.BuildResult{Decision: &copyDecision, Message: d.Message}
		mutate(&bad)
		if AssertCase(c, bad, snap).Passed {
			t.Fatalf("forged refusal passed: %+v", bad)
		}
	}
	snap.Catalog = nil
	if AssertCase(c, r, snap).Passed {
		t.Fatal("missing evidence passed")
	}
	// 正确处理数包含两种构建结果,但报告必须分开展示。
	records := []CaseRecord{
		{CaseID: "delivered", Stage: StageBuild, Seed: 1, Result: ptrResult(passingResult("8000.00")), Verdict: Verdict{Passed: true}},
		{CaseID: "refused", Stage: StageBuild, Seed: 1, Result: &r, Verdict: Verdict{Passed: true}},
		{CaseID: "screened", Stage: StageScreening, Seed: 1, Verdict: Verdict{Passed: true}},
		{CaseID: "failed", Stage: StageBuild, Seed: 1, Result: &r, Verdict: Verdict{}},
	}
	summary := Summarize(ReportMeta{}, records)
	if summary.Passed != 3 || summary.Delivered != 1 || summary.NonDelivered != 1 || summary.ScreeningCorrect != 1 || summary.Failed != 1 {
		t.Fatalf("conflated delivery with refusal: %+v", summary)
	}
	dir := t.TempDir()
	if err := summary.WriteReport(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil || !strings.Contains(string(data), "成功交付 1 / 合理非交付 1 / 初筛正确 1") {
		t.Fatalf("missing breakdown: %v", err)
	}
}

func ptrResult(r buildharness.BuildResult) *buildharness.BuildResult { return &r }

func TestV14BudgetMigrationOnlyChangesContract(t *testing.T) {
	_, old, err := LoadSuite("testdata/suites/v1.3.json", "testdata/cases")
	if err != nil {
		t.Fatal(err)
	}
	_, next, err := LoadSuite("testdata/suites/v1.4.json", "testdata/cases")
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 40 || len(next) != 40 {
		t.Fatal("unexpected version size")
	}
	byID := map[string]Case{}
	for _, c := range next {
		byID[c.ID] = c
	}
	for _, a := range old {
		b := byID[a.ID]
		if a.ID == "L1-004" {
			b = byID["L4-215"]
			if b.ID != "L4-215" || a.Expect.Outcome != "pass" || b.Expect.Outcome != "budget_adaptive" || !reflect.DeepEqual(a.Requirement, b.Requirement) {
				t.Fatal("unreviewed budget migration")
			}
		} else if !reflect.DeepEqual(a, b) {
			t.Fatalf("unrelated case changed: %s", a.ID)
		}
	}
}
