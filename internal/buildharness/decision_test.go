package buildharness

import (
	"context"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"strings"
	"testing"
)

func TestCatalogDecisionProofAndUnknown(t *testing.T) {
	cat := fixtureCatalog()
	spec := fixtureRequirement()
	spec.BudgetCNY = 100
	d := AssessCatalog(BuildInput{Requirement: spec}, cat)
	if d == nil || d.Kind != "catalog_infeasible" || d.LowerBoundCNY != "6200.00" {
		t.Fatalf("independent minima: %+v", d)
	}
	// 一个可能更便宜但未知价格的必需件，足以让正下界证明失效。
	cat.Candidates[0].PriceCNY = nil
	if d := AssessCatalog(BuildInput{Requirement: spec}, cat); d != nil {
		t.Fatalf("unknown price must not prove infeasibility: %+v", d)
	}
	cat = fixtureCatalog()
	var kept = cat.Candidates[:0]
	for _, p := range cat.Candidates {
		if p.Category != schemas.CategoryPSU {
			kept = append(kept, p)
		}
	}
	cat.Candidates = kept
	if d := AssessCatalog(BuildInput{Requirement: spec}, cat); d == nil || d.Reason != "catalog_category_missing" {
		t.Fatalf("missing category: %+v", d)
	}
}

func TestOwnedBindingAndLockConflict(t *testing.T) {
	cat := fixtureCatalog()
	cat.Candidates[0].Model = "Exact CPU A"
	spec := fixtureRequirement()
	spec.OwnedParts = []schemas.OwnedPart{{Category: schemas.CategoryCPU, Model: "AMD Exact CPU A", Quantity: 1}}
	spec.BudgetBasis = "new_purchase"
	input, d := bindOwned(BuildInput{Requirement: spec}, cat.Candidates)
	if d != nil || input.BaseSelection.CPU != "cpu-amd-a" || !categorySet(input.Locked)[schemas.CategoryCPU] {
		t.Fatalf("binding: %+v %+v", input, d)
	}
	if validateChangeConstraints(input, schemas.BuildSelection{CPU: "cpu-intel-a"}) == nil {
		t.Fatal("owned CPU replaced")
	}
	spec.OwnedParts[0].Model = "AMD Exact CPU" // 近似型号不能通过。
	if _, d := bindOwned(BuildInput{Requirement: spec}, cat.Candidates); d == nil || d.Reason != "owned_model_unresolved" {
		t.Fatal("accepted approximate model")
	}
	spec.OwnedParts[0].Model = "AMD Exact CPU A"
	if _, d := bindOwned(BuildInput{Requirement: spec, BaseSelection: &schemas.BuildSelection{CPU: "cpu-intel-a"}, Locked: []schemas.Category{schemas.CategoryCPU}}, cat.Candidates); d == nil || d.Reason != "owned_lock_conflict" {
		t.Fatal("silently changed locked base")
	}
}

func TestClarificationDoesNotCallModel(t *testing.T) {
	m := &fakeModel{}
	h := newTestHarness(t, m, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
		t.Fatal("must not validate unresolved owned parts")
		return validate.Result{}, nil
	})
	spec := fixtureRequirement()
	spec.ExistingParts = []schemas.Category{schemas.CategoryCPU}
	r, err := h.Run(context.Background(), BuildInput{Requirement: spec})
	if err != nil || r.Decision == nil || r.Decision.Kind != "clarify" || r.Succeeded || len(m.requests) != 0 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
}

func TestOwnedPurchaseAndCompatibilityStillValidated(t *testing.T) {
	for _, compatible := range []bool{true, false} {
		t.Run(map[bool]string{true: "compatible", false: "incompatible"}[compatible], func(t *testing.T) {
			spec := fixtureRequirement()
			spec.BudgetCNY = 6000
			spec.BudgetFlex = 0
			spec.BudgetBasis = "new_purchase"
			spec.OwnedParts = []schemas.OwnedPart{{Category: schemas.CategoryCPU, Model: "CPU A", Quantity: 1}}
			input := BuildInput{Requirement: spec, BaseSelection: &schemas.BuildSelection{CPU: "cpu-a"}, Locked: []schemas.Category{schemas.CategoryCPU}}
			bundle := testBundle()
			bundle.OwnedInput = &input
			m := &fakeModel{outputs: []string{draftJSON("psu-a", "b1"), draftJSON("psu-a", "b2"), draftJSON("psu-a", "b3")}}
			seen := 0
			eval := evalFunc(func(_ context.Context, sel schemas.BuildSelection) (validate.Result, error) {
				seen++
				if sel.CPU != "cpu-a" {
					t.Fatal("owned selection changed")
				}
				r := passingResult("8000.00")
				cpu := "2000.00"
				rest := "6000.00"
				r.Quote.Lines = []validate.QuoteLine{{Category: schemas.CategoryCPU, SKU: sel.CPU, Quantity: 1, SubtotalCNY: &cpu}, {Category: schemas.CategoryPSU, SKU: sel.PSU, Quantity: 1, SubtotalCNY: &rest}}
				if !compatible {
					r.Report.OverallStatus = schemas.OverallFail
					r.Report.Checks = []schemas.CheckResult{{RuleID: schemas.RuleMemoryGeneration, Outcome: schemas.OutcomeFail, Severity: schemas.SeverityError}}
				}
				return r, nil
			})
			h, err := New(Config{Model: m, Planner: fixedPlanner{bundle: bundle}, Eval: eval, Repairer: NewRepairPlanner()})
			if err != nil {
				t.Fatal(err)
			}
			r, err := h.Run(context.Background(), BuildInput{Requirement: spec})
			if err != nil || seen == 0 || r.Succeeded != compatible {
				t.Fatalf("result=%+v err=%v", r, err)
			}
			if compatible && (r.Result.Quote.TotalCNY != "8000.00" || r.Result.Quote.PurchaseTotalCNY == nil || *r.Result.Quote.PurchaseTotalCNY != "6000.00") {
				t.Fatal("wrong purchase quote")
			}
			if !compatible && (r.Decision == nil || r.Decision.Kind != "search_exhausted" || !strings.Contains(r.Message, "不能据此认定")) {
				t.Fatalf("unjustified rejection: %+v", r)
			}
		})
	}
}
