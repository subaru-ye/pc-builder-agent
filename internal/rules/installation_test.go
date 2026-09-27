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
		{"fits", func(*schemas.ResolvedBuild) {}, schemas.OutcomePass, "均在机箱安装范围内"},
		{"form-factor-mismatch", func(b *schemas.ResolvedBuild) { b.PSU.FormFactor = psuffp(schemas.PSUFormFactorATX) }, schemas.OutcomeFail, "所选电源为 atx"},
		{"length-over", func(b *schemas.ResolvedBuild) { b.PSU.LengthMM = ip(140) }, schemas.OutcomeFail, "超过机箱电源限长"},
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

// 相邻 ITX 漏口（静态审阅发现，红-first）：通吃机箱（matx,itx）声明了
// supported_psu_form_factors=[sfx,sfx_l] 时，电源仓是数据可验的物理限界，
// 兼容规则必须核对实际选中的电源——ATX 电源塞 SFX 仓位不得放行
// （修复前：非纯 ITX 机箱直接 pass，ATX 电源随校验通过）。
func TestDeclaredPSUBayEnforcedOnNonITXOnlyCase(t *testing.T) {
	b := validBuild()
	b.Motherboard.FormFactor = ffp(schemas.FormFactorITX)
	b.Case.SupportedFormFactors = []schemas.FormFactor{schemas.FormFactorMATX, schemas.FormFactorITX}
	b.Case.SupportedPSUFormFactors = []schemas.PSUFormFactor{schemas.PSUFormFactorSFX, schemas.PSUFormFactorSFXL}
	b.Case.PSULengthMaxMM = ip(130)
	b.PSU.FormFactor = psuffp(schemas.PSUFormFactorATX)
	b.PSU.LengthMM = ip(140)
	got := formFactorSupportRule{}.Check(b)
	if got.Outcome != schemas.OutcomeFail {
		t.Fatalf("声明 SFX 电源仓的通吃机箱选 ATX 电源必须 fail: %+v", got)
	}
}

// 相邻正例：同一通吃机箱选 SFX 电源（仓内限长内）——合法组合必须保持 pass。
func TestDeclaredPSUBayPassesWithSFXOnNonITXOnlyCase(t *testing.T) {
	b := validBuild()
	b.Motherboard.FormFactor = ffp(schemas.FormFactorITX)
	b.Case.SupportedFormFactors = []schemas.FormFactor{schemas.FormFactorMATX, schemas.FormFactorITX}
	b.Case.SupportedPSUFormFactors = []schemas.PSUFormFactor{schemas.PSUFormFactorSFX, schemas.PSUFormFactorSFXL}
	b.Case.PSULengthMaxMM = ip(130)
	b.PSU.FormFactor = psuffp(schemas.PSUFormFactorSFX)
	b.PSU.LengthMM = ip(125)
	got := formFactorSupportRule{}.Check(b)
	if got.Outcome != schemas.OutcomePass {
		t.Fatalf("合法 SFX 组合必须 pass: %+v", got)
	}
}
