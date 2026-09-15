package planning

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// A different real run found this exact selection. Re-evaluate it under the
// failed modification's actual state to distinguish planning failure from a
// catalog that cannot satisfy the budget. This is not a live-model regrade.
func TestRecordedSelectionSatisfiesLockedGPU7500Budget(t *testing.T) {
	raw, err := os.ReadFile("testdata/locked_budget_witness_20260915.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Input      schemas.PlanningInput `json:"input"`
		Draft      json.RawMessage       `json:"draft"`
		Candidates []Candidate           `json:"candidates"`
		Date       string                `json:"snapshot_date"`
		Total      string                `json:"expected_total_cny"`
	}
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	date, err := time.Parse("2006-01-02", f.Date)
	if err != nil {
		t.Fatal(err)
	}
	catalog := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: date}}}
	for _, c := range f.Candidates {
		catalog.Candidates = append(catalog.Candidates, store.Candidate{SKU: c.ID, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: c.Specs, PriceCNY: c.Price})
	}
	m := &scriptedModel{respond: func(call int, _ *model.LLMRequest) *genai.Content {
		if call == 1 {
			return function("evaluate", `{"draft":`+string(f.Draft)+`}`)
		}
		return genai.NewContentFromText(`{"outcome":"ready","draft":`+string(f.Draft)+`,"reply":"预算内替代候选，GPU保持不变。","issues":[],"assessments":[{"field":"budget_cny","status":"met","explanation":"按本轮报价核算，低于7500元","evidence":[]},{"field":"free.locked_parts","status":"met","explanation":"沿用原配置RX 7700 XT","evidence":["local:gpu-sapphire-7700xt-pulse"]}],"assumptions":[]}`, genai.RoleModel)
	}}
	result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), f.Input)
	if err != nil || result.Outcome != "ready" || result.Quote == nil || result.Quote.TotalCNY != f.Total || result.Quote.MissingCount != 0 || result.Validation == nil || result.Validation.OverallStatus != schemas.OverallPass {
		t.Fatalf("witness did not satisfy original requirements: %+v %v", result, err)
	}
	base, _ := schemas.DecodeBuildDraft(f.Input.BaseDraft)
	chosen, _ := schemas.DecodeBuildDraft(result.Draft)
	if base.Selection.GPU == nil || chosen.Selection.GPU == nil || *base.Selection.GPU != *chosen.Selection.GPU {
		t.Fatal("locked GPU changed")
	}
	// The witness must not weaken the budget gate merely to demonstrate success.
	budget := f.Input.State.Fields["budget_cny"]
	budget.Value = json.RawMessage(`6500`)
	f.Input.State.Fields["budget_cny"] = budget
	m.calls = 0
	lower, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 2}).Run(context.Background(), f.Input)
	if err != nil || lower.Outcome == "ready" {
		t.Fatalf("over-budget witness accepted: %+v %v", lower, err)
	}
}
