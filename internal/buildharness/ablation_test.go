package buildharness

import (
	"context"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestAttemptAblationPreservesDeliveryGates(t *testing.T) {
	for _, limit := range []int{1, 3} {
		m := &fakeModel{outputs: []string{draftJSON("psu-a", "first"), draftJSON("psu-b", "fixed")}}
		eval := evalFunc(func(_ context.Context, sel schemas.BuildSelection) (validate.Result, error) {
			result := passingResult("8000.00")
			if sel.PSU == "psu-a" {
				result.Report.OverallStatus = schemas.OverallFail
				result.Report.Checks = []schemas.CheckResult{{RuleID: schemas.RulePSUHeadroom, Outcome: schemas.OutcomeFail, Severity: schemas.SeverityError}}
			}
			return result, nil
		})
		h, err := New(Config{Model: m, Planner: fixedPlanner{bundle: testBundle()}, Repairer: NewRepairPlanner(), Eval: eval, AttemptLimit: limit})
		if err != nil {
			t.Fatal(err)
		}
		result, err := h.Run(context.Background(), BuildInput{Requirement: fixtureRequirement()})
		if err != nil {
			t.Fatal(err)
		}
		if limit == 1 && (result.Succeeded || len(m.requests) != 1 || result.Attempts != 1) {
			t.Fatalf("single attempt bypassed checks: %+v", result)
		}
		if limit == 1 && !strings.Contains(result.Message, "1 次选配后") {
			t.Fatalf("message contradicts execution count: %s", result.Message)
		}
		if limit == 3 && (!result.Succeeded || result.Attempts != 2) {
			t.Fatalf("repair stopped prematurely: %+v", result)
		}
	}
	for _, limit := range []int{-1, 4} {
		if _, err := New(Config{Model: &fakeModel{}, Planner: fixedPlanner{}, Repairer: NewRepairPlanner(), Eval: evalFunc(func(context.Context, schemas.BuildSelection) (validate.Result, error) { return validate.Result{}, nil }), AttemptLimit: limit}); err == nil {
			t.Fatal("invalid attempt limit accepted")
		}
	}
}

func TestSemanticAblationMakesNoEmbeddingCall(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		embedder := &countingEmbedder{}
		p, err := NewCandidatePlannerWithOptions(&fakeCatalogSource{catalog: fixtureCatalog()}, embedder, PlannerOptions{DisableSemantic: disabled})
		if err != nil {
			t.Fatal(err)
		}
		spec := fixtureRequirement()
		spec.NoisePref = schemas.NoisePrefSilent
		bundle, err := p.Prepare(context.Background(), BuildInput{Requirement: spec})
		if err != nil {
			t.Fatal(err)
		}
		if len(bundle.Groups) == 0 {
			t.Fatal("core candidates lost")
		}
		if disabled && embedder.calls != 0 {
			t.Fatal("disabled semantic still embeds")
		}
		if !disabled && embedder.calls != 1 {
			t.Fatal("control did not exercise semantic path")
		}
	}
}
