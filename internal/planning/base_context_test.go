package planning

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestBasePartsReceiveCurrentFactsOutsideInitialSample(t *testing.T) {
	for _, mode := range []string{"priced", "missing price", "missing model"} {
		t.Run(mode, func(t *testing.T) {
			catalog, draft := fixture(t)
			base, err := schemas.DecodeBuildDraft(draft)
			if err != nil {
				t.Fatal(err)
			}
			id := *base.Selection.GPU
			var expected *string
			for i, c := range catalog.Candidates {
				if c.SKU != id {
					continue
				}
				if mode == "missing model" {
					catalog.Candidates = append(catalog.Candidates[:i], catalog.Candidates[i+1:]...)
				} else {
					if mode == "missing price" {
						catalog.Candidates[i].PriceCNY = nil
					}
					expected = catalog.Candidates[i].PriceCNY
				}
				break
			}
			// The actual base GPU is beyond the two-item illustrative sample.
			catalog.Candidates = append([]store.Candidate{{SKU: "sample-gpu-a", Category: schemas.CategoryGPU}, {SKU: "sample-gpu-b", Category: schemas.CategoryGPU}}, catalog.Candidates...)
			m := &scriptedModel{respond: func(_ int, req *model.LLMRequest) *genai.Content {
				var input struct {
					Base    []Candidate `json:"base_candidates"`
					Initial []Candidate `json:"initial_candidates"`
					Missing []string    `json:"unresolved_base_ids"`
				}
				if err := json.Unmarshal([]byte(req.Contents[0].Parts[0].Text), &input); err != nil {
					t.Fatal(err)
				}
				for _, c := range input.Initial {
					if c.ID == id {
						t.Fatal("test did not exclude base from initial sample")
					}
				}
				found, missing := false, false
				for _, c := range input.Base {
					if c.ID == id {
						found = true
						if (c.Price == nil) != (expected == nil) || c.Price != nil && *c.Price != *expected {
							t.Fatal("base price was invented or lost")
						}
					}
				}
				for _, absent := range input.Missing {
					missing = missing || absent == id
				}
				if found != (mode != "missing model") || missing != (mode == "missing model") {
					t.Fatalf("base presence=%v missing=%v", found, missing)
				}
				return genai.NewContentFromText(`{"outcome":"collect","reply":"已收到当前配件资料","issues":[]}`, genai.RoleModel)
			}}
			_, err = (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState(), BaseDraft: draft})
			if err != nil || m.calls != 1 {
				t.Fatalf("%v calls=%d", err, m.calls)
			}
		})
	}
}

func TestSearchByCandidateIDFindsExactBasePart(t *testing.T) {
	x := execution{candidates: []Candidate{{ID: "gpu-a", Category: schemas.CategoryGPU}, {ID: "gpu-sapphire-7700xt-pulse", Category: schemas.CategoryGPU, Model: "PULSE Radeon RX 7700 XT 12GB"}}, seen: map[string]bool{}}
	response := x.call(context.Background(), map[string]any{"action": "search_local", "payload": `{"query":"gpu-sapphire-7700xt-pulse","category":"gpu","limit":1}`})
	got := response["candidates"].([]Candidate)
	if len(got) != 1 || got[0].ID != "gpu-sapphire-7700xt-pulse" {
		t.Fatalf("exact SKU search failed: %+v", got)
	}
}
