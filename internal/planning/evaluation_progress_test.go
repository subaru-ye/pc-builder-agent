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

func thermalRecording(t *testing.T) (schemas.PlanningInput, json.RawMessage, []*genai.Content, recordedCatalog) {
	t.Helper()
	raw, err := os.ReadFile("testdata/thermal_repeated_evaluation_20260915.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Input       schemas.PlanningInput
		Draft       json.RawMessage
		Responses   []*genai.Content
		CatalogFile string `json:"catalog_file"`
		CatalogSHA  string `json:"catalog_sha256"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(f.CatalogFile)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != f.CatalogSHA {
		t.Fatal("catalog drift", err)
	}
	var suite struct {
		Catalog struct{ Candidates []Candidate }
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	catalog := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)}}}
	for _, c := range suite.Catalog.Candidates {
		catalog.Candidates = append(catalog.Candidates, store.Candidate{SKU: c.ID, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: c.Specs, PriceCNY: c.Price})
	}
	return f.Input, f.Draft, f.Responses, catalog
}

func TestRecordedThermalFailureGetsProgressFeedbackWithoutChangingVerdict(t *testing.T) {
	input, _, responses, catalog := thermalRecording(t)
	seen := false
	m := &scriptedModel{respond: func(n int, request *model.LLMRequest) *genai.Content {
		for _, content := range request.Contents {
			for _, part := range content.Parts {
				if part.FunctionResponse == nil {
					continue
				}
				raw, _ := json.Marshal(part.FunctionResponse.Response)
				seen = seen || strings.Contains(string(raw), "重复校验本身不能补齐缺项")
			}
		}
		if n > len(responses) {
			t.Fatal("extra model call")
		}
		return responses[n-1]
	}}
	got, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), input)
	if err != nil || !seen || got.ModelCalls != 8 || got.Outcome != "proposal" || got.Validation.OverallStatus != schemas.OverallReview || got.Quote.TotalCNY != "21680.08" {
		t.Fatalf("seen=%v result=%+v err=%v", seen, got, err)
	}
}

func TestEvaluationComparisonUsesSelectionAndFreshFacts(t *testing.T) {
	input, raw, _, catalog := thermalRecording(t)
	for _, change := range []string{"narrative", "cooler", "quantity", "specification", "price"} {
		t.Run(change, func(t *testing.T) {
			x := execution{input: input, date: "2026-09-15"}
			for _, c := range catalog.Candidates {
				x.candidates = append(x.candidates, Candidate{ID: c.SKU, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: c.Specs, Price: c.PriceCNY})
			}
			first := x.evaluate(context.Background(), raw)
			if first["comparison"] != nil || first["error"] != nil {
				t.Fatal(first)
			}
			draft, _ := schemas.DecodeBuildDraft(raw)
			var wire map[string]any
			_ = json.Unmarshal(raw, &wire)
			selection := wire["selection"].(map[string]any)
			switch change {
			case "narrative":
				wire["build_ref"] = "renamed"
				wire["rationale"] = map[string]string{"cooler": "Only the explanation changed"}
			case "cooler":
				selection["cooler"] = "cooler-deepcool-ak620"
			case "quantity":
				selection["ssd"].([]any)[0].(map[string]any)["quantity"] = 2
			case "specification", "price":
				for i := range x.candidates {
					c := &x.candidates[i]
					if c.ID != draft.Selection.Cooler {
						continue
					}
					if change == "price" {
						price := "600.00"
						c.Price = &price
						continue
					}
					// Synthetic evidence update verifies invalidation only, never
					// written into the frozen or published product catalog.
					var specs map[string]any
					_ = json.Unmarshal(c.Specs, &specs)
					specs["cooling_capacity_w"] = 250
					c.Specs, _ = json.Marshal(specs)
				}
			}
			changed, _ := json.Marshal(wire)
			feedback := x.evaluate(context.Background(), changed)
			comparison, ok := feedback["comparison"].(map[string]bool)
			if !ok {
				t.Fatal(feedback)
			}
			if comparison["same_selection"] != (change != "cooler" && change != "quantity") || comparison["same_quote"] != (change == "narrative" || change == "specification") || comparison["same_validation"] != (change == "narrative" || change == "price") {
				t.Fatal(comparison)
			}
			if change == "cooler" && (x.result.Validation.OverallStatus != schemas.OverallPass || x.result.Quote.TotalCNY != "21578.32" || x.result.Quote.MissingCount != 0) {
				t.Fatalf("recorded alternative does not pass existing checks and quote: %+v %+v", x.result.Validation, x.result.Quote)
			}
		})
	}
}
