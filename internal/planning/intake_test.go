package planning

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// The identities, canonical fields and prices are the published manual batch.
// The scripted query tests tool exposure, not real model choice quality.
func TestPublishedIntakeReachesModelLocalSearch(t *testing.T) {
	checkPublishedIntake(t, "2026-09-14-amd", "cpu", "supported_chipsets", 8)
}

func TestPublishedSSDIntakeReachesModelLocalSearch(t *testing.T) {
	checkPublishedIntake(t, "2026-09-14-ssd", "ssd", "form_factor", 3)
}

func TestPublishedGPUIntakeReachesModelLocalSearch(t *testing.T) {
	checkPublishedIntake(t, "2026-09-15-gpu", "gpu", "power_connectors", 1)
}

func TestPublishedMemoryAndCaseReachModelLocalSearch(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		checkPublishedIntake(t, "2026-09-15-memory-case", "memory", "generation", 1)
	})
	t.Run("case", func(t *testing.T) {
		checkPublishedIntake(t, "2026-09-15-memory-case", "case", "gpu_length_max_mm", 1)
	})
}

func checkPublishedIntake(t *testing.T, folder, category, requiredField string, count int) {
	t.Helper()
	root := "../../scripts/data/reviewed-intake/" + folder + "/"
	raw, err := os.ReadFile(root + "parts.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	offers, err := os.ReadFile(root + "offers.json")
	if err != nil {
		t.Fatal(err)
	}
	var selected []struct {
		SKU   string `json:"sku"`
		Offer struct {
			Price json.Number `json:"price_cny"`
		} `json:"offer"`
	}
	if err = json.Unmarshal(offers, &selected); err != nil {
		t.Fatal(err)
	}
	prices := map[string]string{}
	for _, row := range selected {
		prices[row.SKU] = row.Offer.Price.String()
	}
	catalog := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{ID: 5, SnapshotDate: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)}}}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row struct {
			SKU, Brand, Model string
			Category          schemas.Category
			Specs             json.RawMessage
		}
		if err = json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		price := prices[row.SKU]
		catalog.Candidates = append(catalog.Candidates, store.Candidate{SKU: row.SKU, Category: row.Category, Brand: row.Brand, Model: row.Model, Specs: row.Specs, PriceCNY: &price})
	}
	m := &scriptedModel{respond: func(call int, req *model.LLMRequest) *genai.Content {
		if call == 1 {
			return function("search_local", `{"category":"`+category+`"}`)
		}
		if call != 2 {
			t.Fatalf("unexpected extra model call: %d", call)
		}
		response := req.Contents[len(req.Contents)-1].Parts[0].FunctionResponse.Response
		found := response["candidates"].([]Candidate)
		if len(found) != count {
			t.Fatalf("new products absent from tool result: %d", len(found))
		}
		for _, c := range found {
			if c.Price == nil || *c.Price != prices[c.ID] || !strings.Contains(string(c.Specs), requiredField) {
				t.Fatalf("published quote/specs lost: %+v", c)
			}
		}
		return genai.NewContentFromText(`{"outcome":"collect","reply":"已取得新型号的规格与参考价，可继续比较。","issues":[],"assumptions":[]}`, genai.RoleModel)
	}}
	result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if err != nil || result.ToolCalls != 1 || result.SearchCalls != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}
