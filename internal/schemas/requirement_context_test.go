package schemas

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestBackgroundCannotBecomeFixedRequirement(t *testing.T) {
	for _, sourceKind := range []string{"chat", "edit"} {
		for _, field := range []string{"budget_cny", "budget_basis", "noise_pref", "owned_parts"} {
			t.Run(sourceKind+"/"+field, func(t *testing.T) {
				state := NewRequirementState()
				source := RequirementSource{Kind: sourceKind, MessageID: "message", Quote: "参考信息"}
				update := RequirementUpdate{Operations: []RequirementOperation{
					{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Quote: source.Quote},
					{Op: "set", Field: field, Value: json.RawMessage(`3000`), Kind: "context", Quote: source.Quote},
				}}
				next, err := ApplyRequirementUpdate(state, update, source)
				if !errors.Is(err, ErrRequirementContextField) || !reflect.DeepEqual(next, state) {
					t.Fatalf("invalid metadata partially applied: %v", err)
				}
			})
		}
	}
	for _, field := range []string{"notes", "free.quote_reference"} {
		next := updateState(t, NewRequirementState(), "显卡报价3000，仅供参考", `[{"op":"set","field":"`+field+`","kind":"context","value":"显卡报价3000，仅供参考","quote":"显卡报价3000，仅供参考"}]`)
		if next.Fields[field].Status != "active" || next.Fields["budget_cny"].Status != "unknown" {
			t.Fatal("valid background rejected or promoted to a budget")
		}
	}
}

func TestLegacyContextFieldCanBeRemovedOrExplicitlyCorrected(t *testing.T) {
	state := NewRequirementState()
	state.Fields["budget_cny"] = RequirementField{Status: "active", Kind: "context", Value: json.RawMessage(`3000`), Strength: "must"}
	for _, op := range []string{
		`{"op":"remove","field":"budget_cny","quote":"纠正预算"}`,
		`{"op":"set","field":"budget_cny","kind":"constraint","value":3000,"quote":"纠正预算"}`,
	} {
		updateState(t, state, "纠正预算", "["+op+"]")
	}
	state.Fields["budget_cny"] = RequirementField{Status: "active", Previous: &RequirementField{Status: "active", Kind: "context", Value: json.RawMessage(`3000`)}}
	_, err := ApplyRequirementUpdate(state, RequirementUpdate{Operations: []RequirementOperation{{Op: "restore", Field: "budget_cny", Quote: "恢复"}}}, RequirementSource{Kind: "chat", Quote: "恢复"})
	if !errors.Is(err, ErrRequirementContextField) {
		t.Fatalf("restored an invalid background field: %v", err)
	}
}
