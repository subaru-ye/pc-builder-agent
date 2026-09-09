package evalsuite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestClarificationReplaysExactLegacyAndCurrentTemplates(t *testing.T) {
	c := testCase()
	c.Requirement.ExistingParts = []schemas.Category{schemas.CategoryCPU}
	c.Requirement.BudgetBasis = "new_purchase"
	c.Expect = Expect{Outcome: "clarify", Reason: "missing_owned_information"}
	cat := store.CatalogSnapshot{}
	d := buildharness.AssessCatalog(buildharness.BuildInput{Requirement: c.Requirement}, cat)
	for _, version := range []int{0, 1} {
		copyDecision := *d
		copyDecision.ExplanationVersion = version
		if version == 0 {
			copyDecision.Message = "请补充已有配件的完整型号和数量，并确认预算是仅用于新增购买，还是包含已有件的整机参考总价？缺失项：" + strings.Join(d.Fields, "、")
		}
		r := buildharness.BuildResult{Decision: &copyDecision, Message: copyDecision.Message}
		if v := AssertCase(c, r, NewSnapshotView(cat)); !v.Passed {
			t.Fatalf("version %d rejected: %+v", version, v)
		}
		copyDecision.Message = "资料齐全，可以直接交付"
		r.Message = copyDecision.Message
		if AssertCase(c, r, NewSnapshotView(cat)).Passed {
			t.Fatal("arbitrary explanation accepted")
		}
	}
	d.ExplanationVersion = 99
	if AssertCase(c, buildharness.BuildResult{Decision: d, Message: d.Message}, NewSnapshotView(cat)).Passed {
		t.Fatal("unknown explanation version accepted")
	}
}

func TestNonDeliveryRequiresReproducibleEvidence(t *testing.T) {
	cat := store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}}
	for _, category := range schemas.AllCategories {
		price := "100.00"
		cat.Candidates = append(cat.Candidates, store.Candidate{SKU: string(category), Category: category, PriceCNY: &price})
	}
	c := testCase()
	c.Requirement.BudgetCNY = 100
	c.Expect = Expect{Outcome: "catalog_infeasible", Reason: "budget_lower_bound"}
	d := buildharness.AssessCatalog(buildharness.BuildInput{Requirement: c.Requirement}, cat)
	if d == nil {
		t.Fatal("test catalog should prove infeasibility")
	}
	r := buildharness.BuildResult{Decision: d, Message: d.Message}
	snap := NewSnapshotView(cat)
	if v := AssertCase(c, r, snap); !v.Passed {
		t.Fatalf("valid proof:%+v", v)
	}
	copyDecision := *d
	copyDecision.LowerBoundCNY = "9999.00"
	r.Decision = &copyDecision
	if AssertCase(c, r, snap).Passed {
		t.Fatal("forged proof passed")
	}
	r.Decision = d
	r.Succeeded = true
	if AssertCase(c, r, snap).Passed {
		t.Fatal("delivered despite refusal passed")
	}
	r.Succeeded = false
	r.Message = "系统报错"
	if AssertCase(c, r, snap).Passed {
		t.Fatal("generic failure counted as correct refusal")
	}
	// 真实运行异常即使带“没有候选”字样，也不能算正确业务结果或剔除分母。
	records, err := RunCases(context.Background(), []Case{c}, Deps{Harness: &stubHarness{err: fmt.Errorf("网络接口没有候选: timeout")}, Snapshot: snap})
	if err != nil || records[0].Verdict.Passed || records[0].Verdict.DataError {
		t.Fatalf("runtime error hidden: %v %+v", err, records)
	}
}

func TestOwnedScreeningQuestions(t *testing.T) {
	c := Case{Stage: StageScreening, Expect: Expect{Kind: "clarify", ClarifyFields: []string{"owned_parts", "budget_basis"}}}
	if !AssertScreeningCase(c, "请提供已有 CPU 的完整型号？预算是新增购买费用还是整机总价？").Passed {
		t.Fatal("valid clarification rejected")
	}
	basisCase := Case{Stage: StageScreening, Expect: Expect{Kind: "clarify", ClarifyFields: []string{"budget_basis"}}}
	if !AssertScreeningCase(basisCase, "这 6000 元是仅指新增购买其他配件的费用，还是整台电脑的参考总价（包含这颗 CPU 的价值）？").Passed {
		t.Fatal("live reply with a comma and 整台电脑 was incorrectly rejected")
	}
	ownedCase := Case{Stage: StageScreening, Expect: Expect{Kind: "clarify", ClarifyFields: []string{"owned_parts"}}}
	if !AssertScreeningCase(ownedCase, "请分别提供 **具体的完整型号和数量**，例如 CPU 是 Intel i5-13400。").Passed {
		t.Fatal("live imperative request was incorrectly rejected")
	}
	if !AssertScreeningCase(ownedCase, "请分别告诉我 CPU 和内存的完整型号（例如 Intel i5-12400、DDR4 16GB x2 等）。").Passed {
		t.Fatal("live request with an intervening adverb was incorrectly rejected")
	}
	if !AssertScreeningCase(ownedCase, "请补充以下信息：\n\n1. **CPU 和内存的完整型号与数量**（例如：i5-13400 一颗、DDR5 16GB×2 两条），以便确认适配性。\n2. 预算口径确认。").Passed {
		t.Fatal("live request followed by a list was incorrectly rejected")
	}
	if !AssertScreeningCase(ownedCase, "**请提供以下信息：**\n* CPU 的完整型号\n- 内存的完整型号").Passed {
		t.Fatal("bulleted request list was incorrectly rejected")
	}
	for _, text := range []string{
		"已记录以下信息：\n1. CPU 型号\n还有问题吗？",
		"请补充以下信息：\n1. 预算。\nCPU 型号已记录。",
		"请补充以下信息：\n1. 预算。\n接下来是已有信息。\n2. CPU 型号。",
		"请补充以下信息：\n1. 无需提供 CPU 型号。",
	} {
		if AssertScreeningCase(ownedCase, text).Passed {
			t.Fatalf("request-list scope leaked: %s", text)
		}
	}
	for _, text := range []string{"无需请分别提供完整型号。还有问题吗？", "请分别提供预算。型号已经记下。"} {
		if AssertScreeningCase(ownedCase, text).Passed {
			t.Fatalf("negated or unrelated request passed: %s", text)
		}
	}
	for _, text := range []string{"型号和预算口径已记录。还有其他需求吗？", "请提供 CPU 完整型号？", "预算是新增购买费用还是整机总价？"} {
		if AssertScreeningCase(c, text).Passed {
			t.Fatalf("missing question passed: %s", text)
		}
	}
}

func TestSearchExhaustedCannotHideOwnedReplacement(t *testing.T) {
	c := testCase()
	c.Expect = Expect{Outcome: "search_exhausted", Reason: "no_verified_solution"}
	c.Requirement.OwnedParts = []schemas.OwnedPart{{Category: schemas.CategoryCPU, Model: "Exact CPU", Quantity: 1}}
	c.Requirement.BudgetBasis = "new_purchase"
	snap := testSnapshot()
	snap.Catalog = &store.CatalogSnapshot{Candidates: []store.Candidate{{Category: schemas.CategoryCPU, SKU: "cpu-a", Model: "Exact CPU"}}}
	r := passingResult("8000.00")
	r.Succeeded = false
	r.Result.Report.OverallStatus = schemas.OverallFail
	r.Result.Quote = validate.WithOwnership(r.Result.Quote, c.Requirement)
	r.Message = "当前搜索失败，不能据此认定整个目录无解。"
	r.Decision = &buildharness.Decision{Kind: "search_exhausted", Reason: "no_verified_solution", Scope: "current_run", Message: r.Message}
	if v := AssertCase(c, r, snap); !v.Passed {
		t.Fatalf("valid stopped search: %+v", v)
	}
	r.Draft.Selection.CPU = "replacement-cpu"
	if f := hasFailure(t, AssertCase(c, r, snap), "A9"); !f.Veto {
		t.Fatal("owned replacement must remain a veto even without delivery")
	}
	r.Draft.Selection.CPU = "cpu-a"
	base := passingSelection()
	c.BaseSelection, c.Locked = &base, []schemas.Category{schemas.CategoryMemory}
	r.Draft.Selection.Memory = "replacement-memory"
	if f := hasFailure(t, AssertCase(c, r, snap), "A8"); !f.Veto {
		t.Fatal("locked replacement must remain a veto even without delivery")
	}
}
