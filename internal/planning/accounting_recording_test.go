package planning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

func TestLiveOwnedPurchaseDeliveryUsesTheVerifiedBudgetBasis(t *testing.T) {
	for _, mode := range []string{"original", "whole_machine", "lower_budget", "unowned_memory", "wrong_owned_model", "missing_new_price", "missing_basis_assessment", "missing_basis_lower_budget", "missing_basis_new_price", "missing_basis_whole_machine"} {
		t.Run(mode, func(t *testing.T) {
			file, purchase := "testdata/owned_purchase_delivery_recording_20260915.json", "5925.00"
			if strings.HasPrefix(mode, "missing_basis_") {
				file, purchase = "testdata/budget_basis_assessment_recording_20260915.json", "5166.00"
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var f struct {
				Input       schemas.PlanningInput
				Responses   []*genai.Content
				CatalogFile string `json:"catalog_file"`
				CatalogSHA  string `json:"catalog_sha256"`
			}
			if err = json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			catalogRaw, err := os.ReadFile(f.CatalogFile)
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(catalogRaw)) != f.CatalogSHA {
				t.Fatal("catalog recording changed", err)
			}
			var suite struct {
				Catalog struct{ Candidates []Candidate }
			}
			if err = json.Unmarshal(catalogRaw, &suite); err != nil {
				t.Fatal(err)
			}
			field := func(name, value string) {
				v := f.Input.State.Fields[name]
				v.Value = json.RawMessage(value)
				f.Input.State.Fields[name] = v
			}
			switch mode {
			case "whole_machine", "missing_basis_whole_machine":
				field("budget_basis", `"full_build"`)
			case "lower_budget", "missing_basis_lower_budget":
				field("budget_cny", `5000`)
			case "unowned_memory":
				field("owned_parts", `[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1}]`)
			case "wrong_owned_model":
				field("owned_parts", `[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1},{"category":"memory","model":"different memory","quantity":1}]`)
			}
			catalog := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)}}}
			for _, c := range suite.Catalog.Candidates {
				if (mode == "missing_new_price" || mode == "missing_basis_new_price") && c.ID == "gpu-gb-5060-windforce" {
					c.Price = nil
				}
				catalog.Candidates = append(catalog.Candidates, store.Candidate{SKU: c.ID, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: c.Specs, PriceCNY: c.Price})
			}
			m := &scriptedModel{respond: func(n int, _ *model.LLMRequest) *genai.Content { return f.Responses[n-1] }}
			// The first complete model proposal is response 4. Later identical
			// retries were caused by duplicate full-machine/assessment gates.
			got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 4}).Run(context.Background(), f.Input)
			if err != nil || got.Quote == nil || got.ModelCalls != 4 || got.Validation.OverallStatus != schemas.OverallPass {
				t.Fatalf("unexpected replay: %+v %v", got, err)
			}
			if mode == "original" || mode == "missing_basis_assessment" {
				if got.Outcome != "ready" || got.Delivery.Status != "eligible" || len(got.Issues) != 0 || got.Quote.MissingCount != 1 || got.Quote.PurchaseMissingCount != 0 || got.Quote.PurchaseTotalCNY == nil || *got.Quote.PurchaseTotalCNY != purchase {
					t.Fatalf("valid purchase quote rejected: outcome=%s quote=%+v issues=%v", got.Outcome, got.Quote, got.Issues)
				}
			} else if mode == "wrong_owned_model" {
				// 已有件按品类核账：用户型号不在目录时替身候选不计采购价，
				// 降级为非阻塞 note，不再把会计缺口当交付问题。
				if got.Outcome != "ready" || got.Quote.PurchaseTotalCNY == nil || *got.Quote.PurchaseTotalCNY != purchase || got.Quote.PurchaseMissingCount != 0 {
					t.Fatalf("category-level ownership accounting failed: outcome=%s quote=%+v", got.Outcome, got.Quote)
				}
				if len(got.Delivery.Notes) != 1 || !strings.Contains(got.Delivery.Notes[0], "different memory") {
					t.Fatalf("missing ownership note: %+v", got.Delivery)
				}
			} else if got.Outcome != "proposal" || len(got.Issues) == 0 {
				t.Fatalf("unverified price/ownership or overbudget accepted: %+v", got)
			}
		})
	}
}
