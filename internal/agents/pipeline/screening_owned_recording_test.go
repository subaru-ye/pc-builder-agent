package pipeline

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestRecordedOwnedModelsSynchronizeCategories(t *testing.T) {
	raw, err := os.ReadFile("testdata/owned_live_recording_20260915.json")
	if err != nil {
		t.Fatal(err)
	}
	var recording struct {
		Cases []struct {
			ID    string `json:"id"`
			Steps []struct {
				Message  string `json:"message"`
				Response string `json:"raw_response"`
				Model    string `json:"expected_model"`
			} `json:"steps"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &recording); err != nil {
		t.Fatal(err)
	}
	for _, c := range recording.Cases {
		t.Run(c.ID, func(t *testing.T) {
			state := schemas.NewRequirementState()
			for _, step := range c.Steps {
				update, err := schemas.DecodeRequirementUpdate([]byte(step.Response))
				if err != nil {
					t.Fatal(err)
				}
				source := schemas.RequirementSource{Kind: "chat", MessageID: step.Message, Quote: step.Message}
				update = prepareRequirementUpdate(state, update, source)
				state, err = schemas.ApplyRequirementUpdate(state, update, source)
				if err != nil {
					t.Fatal(err)
				}
				var categories []schemas.Category
				var owned []schemas.OwnedPart
				if err := json.Unmarshal(state.Fields["existing_parts"].Value, &categories); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(state.Fields["owned_parts"].Value, &owned); err != nil {
					t.Fatal(err)
				}
				if len(categories) != 1 || categories[0] != schemas.CategoryCPU || len(owned) != 1 || owned[0].Model != step.Model || state.Fields["owned_parts"].Status != "active" {
					t.Fatalf("recorded model/category update diverged: %+v %+v", categories, owned)
				}
				if state.Fields["owned_parts"].Source.MessageID != step.Message || state.Fields["brand_pref.cpu"].Status != "unknown" {
					t.Fatal("lost provenance or inferred brand preference")
				}
			}
		})
	}
}

// Saved provider answers exercise the reducer; this is not a fresh model score.
// The fixture also preserves the known missing shopping-reference operation.
func TestRecordedOwnershipAndBudgetScope(t *testing.T) {
	raw, err := os.ReadFile("testdata/ownership_scope_live_recording_20260915.json")
	if err != nil {
		t.Fatal(err)
	}
	var recording struct {
		Cases []struct {
			ID    string `json:"id"`
			Steps []struct {
				Message  string `json:"message"`
				Response string `json:"raw_response"`
			} `json:"steps"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &recording); err != nil {
		t.Fatal(err)
	}
	for _, c := range recording.Cases {
		t.Run(c.ID, func(t *testing.T) {
			state := schemas.NewRequirementState()
			for i, step := range c.Steps {
				state = semanticTurn(t, state, step.Message, step.Response)
				if c.ID != "L5-305" {
					for _, field := range []string{"owned_parts", "existing_parts", "budget_basis"} {
						if state.Fields[field].Status != "unknown" {
							t.Fatalf("step %d: invented %s", i+1, field)
						}
					}
					continue
				}
				basis := `"new_purchase"`
				if i == 1 {
					basis = `"full_build"`
				}
				if string(state.Fields["budget_basis"].Value) != basis || string(state.Fields["budget_cny"].Value) != "6000" || string(state.Fields["use_case.type"].Value) != `"general"` {
					t.Fatal("explicit purchase scope or later correction lost")
				}
				var owned []schemas.OwnedPart
				if err := json.Unmarshal(state.Fields["owned_parts"].Value, &owned); err != nil {
					t.Fatal(err)
				}
				if len(owned) != 1 || owned[0].Model != "AMD Ryzen 5 7600" || string(state.Fields["existing_parts"].Value) != `["cpu"]` {
					t.Fatal("budget correction lost the existing CPU")
				}
			}
		})
	}
}
