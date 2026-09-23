package product

import (
	"bytes"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

const screenedRequirementWithFlex = `{
  "schema_version": 2,
  "configuration_scope": ["tower"],
  "budget_cny": 8500,
  "budget_flex": 0,
  "use_case": {"type": "gaming", "resolution": "2K"},
  "noise_pref": "silent"
}`

func TestNormalizeUnstatedBudgetFlexUsesSchemaDefault(t *testing.T) {
	got, changed := normalizeUnstatedBudgetFlex([]byte(screenedRequirementWithFlex), "8500 元，2K 玩黑神话，尽量安静")
	if !changed {
		t.Fatal("用户未指定预算弹性时应移除模型推断值")
	}
	spec, err := schemas.DecodeRequirementSpec(got)
	if err != nil {
		t.Fatal(err)
	}
	if spec.BudgetFlex != 0.1 {
		t.Fatalf("BudgetFlex=%v, want schema default 0.1", spec.BudgetFlex)
	}
}

func TestNormalizeUnstatedBudgetFlexPreservesExplicitSemantics(t *testing.T) {
	for _, text := range []string{
		"预算上下浮动 10%",
		"最多 8500，预算不能超",
		"预算可以超一点",
	} {
		got, changed := normalizeUnstatedBudgetFlex([]byte(screenedRequirementWithFlex), text)
		if changed {
			t.Fatalf("明确预算语义不应被修改: %s", text)
		}
		if !bytes.Equal(got, []byte(screenedRequirementWithFlex)) {
			t.Fatalf("payload 被意外改写: %s", text)
		}
	}
}

func TestNormalizeUnstatedBudgetFlexLeavesChangeRequestUntouched(t *testing.T) {
	payload := []byte(`{"schema_version":1,"base_build_ref":"v1","intent":"change_constraint","constraint_patch":{"budget_flex":0}}`)
	got, changed := normalizeUnstatedBudgetFlex(payload, "预算改严格一点")
	if changed || !bytes.Equal(got, payload) {
		t.Fatal("ChangeRequest 不应由需求初筛纠偏修改")
	}
}
