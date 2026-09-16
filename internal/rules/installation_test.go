package rules

import (
	"slices"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func psuffp(v schemas.PSUFormFactor) *schemas.PSUFormFactor { return &v }

func itxBuild() schemas.ResolvedBuild {
	b := validBuild()
	b.Motherboard.FormFactor = ffp(schemas.FormFactorITX)
	b.Case.SupportedFormFactors = []schemas.FormFactor{schemas.FormFactorITX}
	b.Case.SupportedPSUFormFactors = []schemas.PSUFormFactor{schemas.PSUFormFactorSFX, schemas.PSUFormFactorSFXL}
	b.Case.PSULengthMaxMM = ip(130)
	b.PSU.FormFactor = psuffp(schemas.PSUFormFactorSFX)
	b.PSU.LengthMM = ip(125)
	return b
}

func TestITXPSUInstallation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*schemas.ResolvedBuild)
		want   schemas.Outcome
		text   string
	}{
		{"fits", func(*schemas.ResolvedBuild) {}, schemas.OutcomePass, "均在 ITX 机箱安装范围内"},
		{"form-factor-mismatch", func(b *schemas.ResolvedBuild) { b.PSU.FormFactor = psuffp(schemas.PSUFormFactorATX) }, schemas.OutcomeFail, "所选电源为 atx"},
		{"length-over", func(b *schemas.ResolvedBuild) { b.PSU.LengthMM = ip(140) }, schemas.OutcomeFail, "超过 ITX 机箱电源限长"},
		{"missing", func(b *schemas.ResolvedBuild) { b.PSU.FormFactor, b.PSU.LengthMM = nil, nil }, schemas.OutcomeUnknown, "电源安装规格缺失"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := itxBuild()
			tc.change(&b)
			got := formFactorSupportRule{}.Check(b)
			if got.Outcome != tc.want || !strings.Contains(got.Detail, tc.text) {
				t.Fatalf("got %+v", got)
			}
			if tc.name == "missing" && (!slices.Contains(got.MissingFields, "psu.form_factor") || !slices.Contains(got.MissingFields, "psu.length_mm")) {
				t.Fatalf("missing fields incomplete: %v", got.MissingFields)
			}
		})
	}
}

func TestLargerCaseDoesNotInferPSUClearance(t *testing.T) {
	b := validBuild()
	got := formFactorSupportRule{}.Check(b)
	if got.Outcome != schemas.OutcomePass {
		t.Fatalf("non-ITX-only case should retain existing board check: %+v", got)
	}
}
