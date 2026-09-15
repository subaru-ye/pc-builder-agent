package pipeline

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestRecordedUnknownInformationIsNotConflict(t *testing.T) {
	raw, err := os.ReadFile("testdata/unknown_live_recording_20260915.json")
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
				for name, field := range state.Fields {
					if field.Status == "conflict" {
						t.Fatalf("step %d: absent information became conflict: %s", i+1, name)
					}
				}
				if state.NextAction != "confirm" {
					t.Fatal("recorded next action changed")
				}
				if (c.ID == "L5-304" && i == 0) || (c.ID == "L5-309" && i == 1) {
					field := "owned_parts"
					if c.ID == "L5-309" {
						field = "use_case.resolution"
					}
					if state.Fields[field].Status != "unknown" || len(state.Observations) == 0 {
						t.Fatal("unknown value or original source lost")
					}
				}
			}
			if c.ID == "L5-304" {
				if string(state.Fields["budget_basis"].Value) != `"new_purchase"` || string(state.Fields["existing_parts"].Value) != `["cpu"]` || state.Fields["owned_parts"].Status != "active" {
					t.Fatal("later model or budget basis did not become active")
				}
			} else if string(state.Fields["use_case.resolution"].Value) != `"2K"` || string(state.Fields["budget_cny"].Value) != "8000" {
				t.Fatal("later resolution lost or unrelated budget changed")
			}
		})
	}
}

func TestUnknownValueDoesNotReviveRemovalOrHideCorrection(t *testing.T) {
	for _, value := range []string{"null", `"unknown"`} {
		for _, status := range []string{"unknown", "removed", "active", "conflict"} {
			t.Run(value+"/"+status, func(t *testing.T) {
				state := schemas.NewRequirementState()
				state.Fields["use_case.resolution"] = schemas.RequirementField{Status: status, Strength: "must"}
				if status == "active" {
					state.Fields["use_case.resolution"] = schemas.RequirementField{Status: status, Strength: "must", Value: json.RawMessage(`"2K"`)}
				}
				state = semanticTurn(t, state, "分辨率没确定", `{"operations":[{"op":"set","field":"use_case.resolution","value":`+value+`,"evidence":"uncertain","quote":"分辨率没确定"}]}`)
				expected := status
				if status == "active" {
					expected = "conflict"
				}
				if state.Fields["use_case.resolution"].Status != expected {
					t.Fatalf("%s became %s", status, state.Fields["use_case.resolution"].Status)
				}
			})
		}
	}
	for _, field := range []string{"notes", "appearance", "recipient", "free.reference"} {
		if missingRequirementValue(schemas.RequirementOperation{Field: field, Value: json.RawMessage(`"unknown"`)}) {
			t.Fatalf("free text was treated as a protocol sentinel: %s", field)
		}
	}
	state := semanticTurn(t, schemas.NewRequirementState(), "分辨率在2K和4K之间还没商量好", `{"operations":[{"op":"conflict","field":"use_case.resolution","evidence":"uncertain","quote":"分辨率在2K和4K之间还没商量好"}]}`)
	if state.Fields["use_case.resolution"].Status != "conflict" {
		t.Fatal("explicit conflict erased")
	}
}
