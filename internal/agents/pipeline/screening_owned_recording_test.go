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
