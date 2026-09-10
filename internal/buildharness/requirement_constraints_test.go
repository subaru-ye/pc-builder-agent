package buildharness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestExplicitSoftBrandKeepsAlternativesAndRanksPreferredFirst(t *testing.T) {
	embedder := &countingEmbedder{}
	planner, _ := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, embedder)
	spec := fixtureRequirement()
	spec.BrandPref.CPU, spec.BrandPref.GPU = schemas.CPUBrandIntel, schemas.GPUBrandNvidia
	spec.ConstraintStrengths = map[string]string{"brand_pref.cpu": "prefer", "brand_pref.gpu": "prefer"}
	bundle, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec})
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range []schemas.Category{schemas.CategoryCPU, schemas.CategoryGPU} {
		group := bundleGroup(&bundle, category)
		if len(group.Candidates) < 2 || !softConstraintMatch(spec, category, group.Candidates[0]) {
			t.Fatalf("soft preference was not ranked first: %+v", group)
		}
		fallback := false
		for _, candidate := range group.Candidates {
			fallback = fallback || !softConstraintMatch(spec, category, candidate)
		}
		if !fallback {
			t.Fatalf("soft %s brand became a hard filter", category)
		}
	}
	if embedder.calls != 0 {
		t.Fatal("deterministic brand ranking added an embedding call")
	}
	// An explicit must restores the historical hard constraint on the same value.
	spec.ConstraintStrengths["brand_pref.gpu"] = "must"
	bundle, err = planner.Prepare(context.Background(), BuildInput{Requirement: spec})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range bundleGroup(&bundle, schemas.CategoryGPU).Candidates {
		if !strings.Contains(candidate.SKU, "nvidia") {
			t.Fatalf("must brand retained a conflicting candidate: %s", candidate.SKU)
		}
	}
}

func TestSoftSizeCanUseAvailableBoardButMustSizeCannot(t *testing.T) {
	planner, _ := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, &countingEmbedder{})
	spec := fixtureRequirement()
	spec.SizePref = schemas.SizePrefITX
	spec.ConstraintStrengths = map[string]string{"size_pref": "prefer"}
	if _, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec}); err != nil {
		t.Fatalf("soft ITX preference incorrectly rejected available MATX boards: %v", err)
	}
	spec.ConstraintStrengths["size_pref"] = "must"
	if _, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec}); err == nil {
		t.Fatal("must ITX accepted a catalog with only MATX boards")
	}
}

func TestMandatoryBrandConflictWithOwnedGPURequiresClarification(t *testing.T) {
	planner, _ := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, &countingEmbedder{})
	spec := fixtureRequirement()
	spec.BrandPref.GPU = schemas.GPUBrandAMD
	spec.OwnedParts = []schemas.OwnedPart{{Category: schemas.CategoryGPU, Model: "GeForce RTX 4060", Quantity: 1}}
	spec.BudgetBasis = "new_purchase"
	spec.ConstraintStrengths = map[string]string{"brand_pref.gpu": "must"}
	_, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec})
	decision, ok := err.(*Decision)
	if !ok || decision.Kind != "clarify" || decision.Reason != "mandatory_locked_conflict" {
		t.Fatalf("owned NVIDIA card silently defeated mandatory AMD condition: %v", err)
	}
	spec.ConstraintStrengths["brand_pref.gpu"] = "prefer"
	if _, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec}); err != nil {
		t.Fatalf("soft preference should allow retaining the owned card: %v", err)
	}
}

func TestMandatoryUnverifiablePreferenceDoesNotClaimSuccessOrCallBuilder(t *testing.T) {
	for _, key := range []string{"noise_pref", "appearance", "notes"} {
		t.Run(key, func(t *testing.T) {
			model := &fakeModel{}
			harness := newTestHarness(t, model, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
				t.Fatal("unverified mandatory requirement reached final compatibility evaluation")
				return validate.Result{}, nil
			})
			spec := fixtureRequirement()
			spec.NoisePref = schemas.NoisePrefSilent
			spec.Notes = "白色海景房"
			spec.RequirementDetails = map[string]json.RawMessage{"appearance": json.RawMessage(`"白色海景房"`)}
			spec.ConstraintStrengths = map[string]string{key: "must"}
			result, err := harness.Run(context.Background(), BuildInput{Requirement: spec})
			if err != nil || result.Succeeded || result.Decision == nil || result.Decision.Reason != "requirement_evidence_missing" || len(model.requests) != 0 {
				t.Fatalf("mandatory unsupported condition escaped: %+v err=%v calls=%d", result, err, len(model.requests))
			}
			if len(result.Decision.Fields) != 1 || result.Decision.Fields[0] != key || !strings.Contains(result.Message, "尚待核验") {
				t.Fatalf("unhelpful evidence request: %+v", result.Decision)
			}
			spec.ConstraintStrengths[key] = "prefer"
			if unverifiedMandatoryRequirements(spec) != nil {
				t.Fatal("soft preference incorrectly blocked generation")
			}
		})
	}
}
