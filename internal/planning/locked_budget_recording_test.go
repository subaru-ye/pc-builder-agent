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

func TestRecordedBudgetClarificationGetsOneReview(t *testing.T) {
	for _, mode := range []string{"keep_question", "repair", "no_remaining_turns", "live_repair"} {
		t.Run(mode, func(t *testing.T) {
			file, recordedCalls := "testdata/budget_clarification_recording_20260915.json", 4
			if mode == "live_repair" {
				file, recordedCalls = "testdata/budget_clarification_success_recording_20260915.json", 7
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
			if len(f.Responses) != recordedCalls {
				t.Fatal("original recording changed")
			}
			catalogRaw, err := os.ReadFile(f.CatalogFile)
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(catalogRaw)) != f.CatalogSHA {
				t.Fatal("catalog changed", err)
			}
			var suite struct {
				Catalog struct{ Candidates []Candidate }
			}
			if err = json.Unmarshal(catalogRaw, &suite); err != nil {
				t.Fatal(err)
			}
			catalog := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)}}}
			for _, c := range suite.Catalog.Candidates {
				catalog.Candidates = append(catalog.Candidates, store.Candidate{SKU: c.ID, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: c.Specs, PriceCNY: c.Price})
			}
			witnessRaw, err := os.ReadFile("testdata/locked_budget_witness_20260915.json")
			if err != nil {
				t.Fatal(err)
			}
			var witness struct{ Draft json.RawMessage }
			if err = json.Unmarshal(witnessRaw, &witness); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(f.Input)
			m := &scriptedModel{respond: func(n int, req *model.LLMRequest) *genai.Content {
				if mode == "live_repair" && n == 6 {
					feedback := req.Contents[len(req.Contents)-1].Parts[0].Text
					if !strings.Contains(feedback, "交付核验反馈") || len(req.Config.Tools) == 0 {
						t.Fatal("successful real continuation missed review")
					}
				}
				if n <= recordedCalls {
					return f.Responses[n-1]
				}
				if n == 5 {
					feedback := req.Contents[len(req.Contents)-1].Parts[0].Text
					if !strings.Contains(feedback, "交付核验反馈") || !strings.Contains(feedback, "软偏好不等于额外授权门槛") || !strings.Contains(feedback, "9428.70") || len(req.Config.Tools) == 0 {
						t.Fatalf("missing review or remaining tools: %s", feedback)
					}
				}
				if mode == "keep_question" {
					return f.Responses[3]
				}
				// Authored continuation: proves tools can resume, not that the real
				// model found the witness. The first four responses stay untouched.
				if n == 5 {
					return function("search_local_batch", `{"queries":[{"category":"cpu","order_by":"price_asc"},{"category":"motherboard","order_by":"price_asc"},{"category":"memory","order_by":"price_asc"}]}`)
				}
				if n == 6 {
					return function("evaluate", `{"draft":`+string(witness.Draft)+`}`)
				}
				return genai.NewContentFromText(`{"outcome":"ready","draft":`+string(witness.Draft)+`,"reply":"已核对预算内替代，显卡保持不变，其他部件存在取舍。","issues":[],"assessments":[{"field":"free.locked_parts","status":"met","evidence":["local:gpu-sapphire-7700xt-pulse"]}]}`, genai.RoleModel)
			}}
			maxTurns, wantCalls := 8, 5
			if mode == "no_remaining_turns" {
				maxTurns, wantCalls = 4, 4
			}
			if mode == "repair" || mode == "live_repair" {
				wantCalls = 7
			}
			got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: maxTurns}).Run(context.Background(), f.Input)
			if err != nil || got.ModelCalls != wantCalls {
				t.Fatalf("calls=%d outcome=%s err=%v", got.ModelCalls, got.Outcome, err)
			}
			after, _ := json.Marshal(f.Input)
			if string(before) != string(after) {
				t.Fatal("user requirements or base changed")
			}
			if mode != "repair" && mode != "live_repair" {
				if got.Outcome != "clarify" || len(got.Issues) == 0 || got.Quote.TotalCNY != "9428.70" {
					t.Fatalf("question lost: outcome=%s issues=%v", got.Outcome, got.Issues)
				}
				return
			}
			wantTotal, wantTools := "7114.00", 13
			if mode == "live_repair" {
				wantTotal, wantTools = "6754.00", 10
			}
			if got.Outcome != "ready" || got.Quote.TotalCNY != wantTotal || got.ToolCalls != wantTools || got.Validation.OverallStatus != schemas.OverallPass {
				t.Fatalf("repair failed: outcome=%s quote=%+v tools=%d issues=%v", got.Outcome, got.Quote, got.ToolCalls, got.Issues)
			}
			base, _ := schemas.DecodeBuildDraft(f.Input.BaseDraft)
			chosen, _ := schemas.DecodeBuildDraft(got.Draft)
			if base.Selection.GPU == nil || chosen.Selection.GPU == nil || *base.Selection.GPU != *chosen.Selection.GPU {
				t.Fatal("locked GPU changed")
			}
		})
	}
}

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
