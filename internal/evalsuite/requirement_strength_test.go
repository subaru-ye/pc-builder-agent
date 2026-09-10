package evalsuite

import (
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestBrandAssertionDistinguishesExplicitPreferenceFromMandatoryConstraint(t *testing.T) {
	spec := testCase().Requirement
	selection := passingResult("8800.00").Draft.Selection
	gpu := "gpu-amd"
	selection.GPU = &gpu
	spec.BrandPref.GPU = schemas.GPUBrandNvidia
	spec.ConstraintStrengths = map[string]string{"brand_pref.gpu": "prefer"}
	if detail := assertBrandPref(spec, selection, testSnapshot()); detail != "" {
		t.Fatalf("allowed soft brand tradeoff was graded as a hard failure: %s", detail)
	}
	spec.ConstraintStrengths["brand_pref.gpu"] = "must"
	if detail := assertBrandPref(spec, selection, testSnapshot()); detail == "" {
		t.Fatal("mandatory brand mismatch passed")
	}
}
