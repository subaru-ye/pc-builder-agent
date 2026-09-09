package buildharness

import (
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestOwnedClarificationAsksOnlyMissingFacts(t *testing.T) {
	for _, basis := range []string{"new_purchase", "full_build", ""} {
		spec := fixtureRequirement()
		spec.ExistingParts = []schemas.Category{schemas.CategoryCPU}
		spec.BudgetBasis = basis
		d := clarification(spec)
		if d == nil || !strings.Contains(d.Message, "CPU的完整型号") || strings.Contains(d.Message, "数量") || strings.Contains(d.Message, "owned_parts") {
			t.Fatalf("unexpected request: %+v", d)
		}
		if strings.Contains(d.Message, "确认预算") != (basis == "") {
			t.Fatalf("known budget basis re-asked: %s: %+v", basis, d)
		}
	}
	spec := fixtureRequirement()
	spec.ExistingParts = []schemas.Category{schemas.CategoryCPU}
	spec.OwnedParts = []schemas.OwnedPart{{Category: schemas.CategoryCPU, Model: "AMD Ryzen 5 7600", Quantity: 1}}
	spec.BudgetBasis = ""
	d := clarification(spec)
	if d == nil || strings.Contains(d.Message, "型号") || !strings.Contains(d.Message, "确认预算") {
		t.Fatalf("known model re-asked: %+v", d)
	}
	spec.BudgetBasis = "new_purchase"
	if d := clarification(spec); d != nil {
		t.Fatalf("complete owned information should not ask: %+v", d)
	}
	spec.ExistingParts = append(spec.ExistingParts, schemas.CategoryMemory)
	if d := clarification(spec); d == nil || d.Message != "请补充已有内存的完整型号。" {
		t.Fatalf("unexpected memory request: %+v", d)
	}
}
