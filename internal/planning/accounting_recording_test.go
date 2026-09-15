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

func TestLiveBudgetAccountingDoesNotRequireHardwareCitations(t *testing.T) {
	for _, tc := range []struct{ name, budget, flex, outcome string }{
		{"original_live_final", "8000", "0", "ready"},
		{"strict_budget_still_enforced", "4000", "0", "proposal"},
		{"only_explicit_flex_applies", "4000", "1", "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/budget_accounting_recording.json")
			if err != nil {
				t.Fatal(err)
			}
			var f struct {
				Input      schemas.PlanningInput
				Result     json.RawMessage
				Candidates []Candidate
			}
			if err = json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{"budget_cny": tc.budget, "budget_flex": tc.flex} {
				field := f.Input.State.Fields[key]
				field.Value = json.RawMessage(value)
				f.Input.State.Fields[key] = field
			}
			catalog := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{ID: 8, SnapshotDate: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)}}}
			for _, c := range f.Candidates {
				catalog.Candidates = append(catalog.Candidates, store.Candidate{SKU: c.ID, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: c.Specs, PriceCNY: c.Price})
			}
			m := &scriptedModel{respond: func(int, *model.LLMRequest) *genai.Content {
				return genai.NewContentFromText(string(f.Result), genai.RoleModel)
			}}
			got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), f.Input)
			if err != nil || got.Outcome != tc.outcome || got.Quote == nil || got.Quote.TotalCNY != "6513.00" || got.Validation.OverallStatus != schemas.OverallPass {
				t.Fatalf("wrong accounting: outcome=%s quote=%+v issues=%v err=%v", got.Outcome, got.Quote, got.Issues, err)
			}
		})
	}
}
