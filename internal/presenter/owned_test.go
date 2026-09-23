package presenter

import (
	"context"
	"encoding/json"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"strings"
	"testing"
)

func TestOwnedPurchaseDisplayAndExport(t *testing.T) {
	b := fixture(t, 1, 700, "800.00", "gpu-amd")
	raw := json.RawMessage(`{"schema_version": 2, "configuration_scope": ["tower"],"budget_cny":700,"use_case":{"type":"general"},"existing_parts":["cpu"],"owned_parts":[{"category":"cpu","model":"CPU 1"}],"budget_basis":"new_purchase"}`)
	spec, err := schemas.DecodeRequirementSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	var q validate.Quote
	if err := json.Unmarshal(b.Quote, &q); err != nil {
		t.Fatal(err)
	}
	q = validate.WithOwnership(q, spec)
	b.Quote, _ = json.Marshal(q)
	s := New(fakeReader{builds: []store.BuildVersion{b}, specs: map[int64]json.RawMessage{1: raw}})
	view, err := s.Build(context.Background(), "s1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Quote.BudgetDeltaCNY != "0.00" || view.Quote.TotalCNY != "800.00" || *view.Quote.PurchaseTotalCNY != "700.00" || !view.Parts[0].Owned {
		t.Fatalf("view:%+v", view)
	}
	md, err := s.Markdown(context.Background(), "s1", 1)
	if err != nil || !strings.Contains(md, "新增购买合计:¥700.00") || !strings.Contains(md, "用户已有，无需购买") {
		t.Fatalf("export:%s %v", md, err)
	}
}
