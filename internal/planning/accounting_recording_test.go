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
				// 该场景校验超预算被拒:显式陈述弹性 0 表示严格预算
				// (弹性未填写时按系统默认 0.1 执行,与核定预览一致)。
				flex := f.Input.State.Fields["budget_flex"]
				flex.Status, flex.Kind, flex.Strength, flex.Value = "active", "constraint", "must", json.RawMessage(`0`)
				f.Input.State.Fields["budget_flex"] = flex
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
				// BV2-104 修复后的 v2 合同：已有件零匹配且品类被选中时，选中
				// SKU 与用户已有件未核实，不得按品类豁免免计价后 ready；保留
				// 还是改购是计价取舍，必须 clarify（旧断言 ready+豁免+非阻塞
				// note 正是账实不一致的根因语义，已修订）。
				if got.Outcome != "clarify" || !strings.Contains(strings.Join(got.Issues, "; "), "计价取舍") {
					t.Fatalf("zero-match owned part with selected category must clarify: outcome=%s issues=%v", got.Outcome, got.Issues)
				}
			} else if got.Outcome != "proposal" || len(got.Issues) == 0 {
				t.Fatalf("unverified price/ownership or overbudget accepted: %+v", got)
			}
		})
	}
}
