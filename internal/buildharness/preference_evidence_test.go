package buildharness

import (
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestSeriesWhiteDescriptionCannotOverrideSelectedBlackSKU(t *testing.T) {
	for _, name := range []string{"Lian Li O11 Dynamic EVO (Black)", "某型号 黑色"} {
		bundle := testBundle()
		group := bundleGroup(&bundle, schemas.CategoryCase)
		group.Candidates[0].Model = name
		group.Candidates[0].MatchText = "海景房系列，另有白色版本。"
		draft := schemas.BuildDraft{Selection: schemas.BuildSelection{Case: group.Candidates[0].SKU}, Rationale: map[string]string{"case": "白色风格完全满足", "cpu": "保留的其他品类理由"}}
		requirement := fixtureRequirement()
		requirement.Notes = "想要白色海景房,颜值优先"
		enrichCandidateRationale(&draft, bundle, requirement)
		text := draft.Rationale["case"]
		if !strings.Contains(text, "未满足白色") || !strings.Contains(text, "SKU") || strings.Contains(text, "完全满足") || draft.Rationale["cpu"] != "保留的其他品类理由" {
			t.Fatalf("unsupported color claim escaped: %+v", draft.Rationale)
		}
	}
}

func TestColorUnknownAndNegativePreferenceAreNotInvented(t *testing.T) {
	for _, tc := range []struct {
		model, notes string
		warn         bool
	}{
		{"Unknown Case", "白色机箱", true},
		{"Blackwidow Case", "白色机箱", true},
		{"Series Black/White", "白色机箱", true},
		{"Case (White)", "白色机箱", false},
		{"Case (Black)", "不要白色机箱", false},
	} {
		bundle := testBundle()
		group := bundleGroup(&bundle, schemas.CategoryCase)
		group.Candidates[0].Model = tc.model
		draft := schemas.BuildDraft{Selection: schemas.BuildSelection{Case: group.Candidates[0].SKU}}
		r := fixtureRequirement()
		r.Notes = tc.notes
		enrichCandidateRationale(&draft, bundle, r)
		text := draft.Rationale["case"]
		if strings.Contains(text, "尚未核验") != tc.warn || strings.Contains(text, "未满足白色") {
			t.Fatalf("ambiguous/negative condition falsely classified: %s: %s", tc.notes, text)
		}
	}
}
