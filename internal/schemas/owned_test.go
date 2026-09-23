package schemas

import (
	"strings"
	"testing"
)

func TestOwnedContractRoundTrip(t *testing.T) {
	raw := `{"schema_version": 2, "configuration_scope": ["tower"],"budget_cny":3000,"use_case":{"type":"general"},"existing_parts":["cpu"],"owned_parts":[{"category":"cpu","model":"AMD Ryzen 5 7600"}],"budget_basis":"new_purchase"}`
	spec, err := DecodeRequirementSpec([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(MissingOwnedFields(spec)) != 0 || spec.OwnedParts[0].Quantity != 1 {
		t.Fatal("defaults or missing fields wrong")
	}
	wire, err := EncodeRequirementSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DecodeRequirementSpec(wire)
	if err != nil || again.BudgetBasis != "new_purchase" || again.OwnedParts[0].Model != "AMD Ryzen 5 7600" {
		t.Fatalf("lost contract: %s %v", wire, err)
	}
	for _, bad := range []string{strings.Replace(raw, `"new_purchase"`, `"guess"`, 1), strings.Replace(raw, `"model":"AMD Ryzen 5 7600"`, `"model":"AMD Ryzen 5 7600","quantity":2`, 1), strings.Replace(raw, `"model":"AMD Ryzen 5 7600"`, `"model":"AMD Ryzen 5 7600","sku":"guessed"`, 1)} {
		if _, err := DecodeRequirementSpec([]byte(bad)); err == nil {
			t.Fatal("accepted invalid owned contract")
		}
	}
}
