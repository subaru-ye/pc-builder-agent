package schemas

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func ownedRequirementState(t *testing.T) RequirementState {
	t.Helper()
	return updateState(t, completeRequirementState(t), "已有 AMD Ryzen 5 7600 和两块 SN580 1TB", `[
		{"op":"set","field":"owned_parts","kind":"fact","value":[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1},{"category":"ssd","model":"SN580 1TB","quantity":2}],"quote":"已有 AMD Ryzen 5 7600 和两块 SN580 1TB"}
	]`)
}

func TestOwnedModelEstablishesCategoryWithoutGuessingOtherFacts(t *testing.T) {
	state := ownedRequirementState(t)
	category := state.Fields["existing_parts"]
	if string(category.Value) != `["cpu","ssd"]` || category.Kind != "fact" || category.DerivedFrom != "owned_parts" || category.Source.MessageID != state.Fields["owned_parts"].Source.MessageID {
		t.Fatalf("missing linked fact: %+v", category)
	}
	if state.Fields["brand_pref.cpu"].Status != "unknown" || state.Fields["budget_basis"].Status != "unknown" || len(state.Changes) != 2 {
		t.Fatal("model identity inferred unrelated preferences or hid its history")
	}
}

func TestOwnedCategoryEditsRemoveModelsInChatAndPanel(t *testing.T) {
	for _, kind := range []string{"chat", "edit"} {
		for _, categories := range []string{`[]`, `["ssd"]`} {
			t.Run(kind+categories, func(t *testing.T) {
				base := ownedRequirementState(t)
				base = updateState(t, base, "预算只算新增购买", `[{"op":"set","field":"budget_basis","value":"new_purchase","quote":"预算只算新增购买"}]`)
				frozen, _ := json.Marshal(base)
				next, err := ApplyRequirementUpdate(base, RequirementUpdate{Operations: []RequirementOperation{{Op: "set", Field: "existing_parts", Value: json.RawMessage(categories), Quote: "不再复用CPU", Kind: "fact"}}}, RequirementSource{Kind: kind, MessageID: "cancel", Quote: "不再复用CPU"})
				if err != nil {
					t.Fatal(err)
				}
				var owned []OwnedPart
				if err := json.Unmarshal(next.Fields["owned_parts"].Value, &owned); err != nil {
					t.Fatal(err)
				}
				if categories == `[]` && len(owned) != 0 {
					t.Fatal("cleared model still active")
				}
				if categories == `["ssd"]` && (len(owned) != 1 || owned[0].Category != CategorySSD || owned[0].Quantity != 2) {
					t.Fatalf("other owned part lost: %+v", owned)
				}
				if next.Fields["owned_parts"].Source.MessageID != "cancel" || len(next.Changes) != 2 {
					t.Fatal("linked change lacks provenance")
				}
				next = updateState(t, next, "预算改9000", `[{"op":"set","field":"budget_cny","value":9000,"quote":"预算改9000"}]`)
				stored, _ := json.Marshal(next)
				var reloaded RequirementState
				if err := json.Unmarshal(stored, &reloaded); err != nil {
					t.Fatal(err)
				}
				raw, _, err := RequirementStateSpec(reloaded)
				if err != nil {
					t.Fatal(err)
				}
				spec, err := DecodeRequirementSpec(raw)
				if err != nil {
					t.Fatal(err)
				}
				for _, part := range spec.OwnedParts {
					if part.Category == CategoryCPU {
						t.Fatal("revoked CPU revived in execution projection")
					}
				}
				if spec.BudgetCNY != 9000 || spec.NoisePref != NoisePrefSilent || spec.ConstraintStrengths["noise_pref"] != "prefer" {
					t.Fatal("unrelated preference changed")
				}
				after, _ := json.Marshal(base)
				if !bytes.Equal(frozen, after) {
					t.Fatal("confirmed/input snapshot modified")
				}
			})
		}
	}
}

func TestOwnedModelRemovalAndAlternativeDoNotRemoveOwnership(t *testing.T) {
	base := ownedRequirementState(t)
	next := updateState(t, base, "如果不复用CPU", `[{"op":"alternative","field":"existing_parts","value":["ssd"],"quote":"如果不复用CPU"}]`)
	if !reflect.DeepEqual(base.Fields, next.Fields) {
		t.Fatal("alternative changed ownership")
	}
	next = updateState(t, next, "型号记不清了", `[{"op":"remove","field":"owned_parts","quote":"型号记不清了"}]`)
	if next.Fields["owned_parts"].Status != "removed" || !reflect.DeepEqual(next.Fields["existing_parts"], base.Fields["existing_parts"]) {
		t.Fatal("forgetting model removed known ownership")
	}
}

func TestOwnedTemporaryExceptionRestoresLinkedFactsAfterRefresh(t *testing.T) {
	base := ownedRequirementState(t)
	next := updateState(t, base, "这次先只复用SSD", `[{"op":"set","field":"existing_parts","value":["ssd"],"scope":"temporary","quote":"这次先只复用SSD"}]`)
	if next.Fields["owned_parts"].Previous == nil || next.Fields["owned_parts"].Scope != "temporary" {
		t.Fatal("lost model backup")
	}
	raw, _ := json.Marshal(next)
	if err := json.Unmarshal(raw, &next); err != nil {
		t.Fatal(err)
	}
	next = updateState(t, next, "预算9000", `[{"op":"set","field":"budget_cny","value":9000,"quote":"预算9000"}]`)
	next = updateState(t, next, "恢复原来已有配件", `[{"op":"restore","field":"existing_parts","quote":"恢复原来已有配件"}]`)
	if !bytes.Equal(next.Fields["owned_parts"].Value, base.Fields["owned_parts"].Value) || next.Fields["owned_parts"].Previous != nil {
		t.Fatal("temporary restore lost original models")
	}
	if next.Fields["owned_parts"].Source.MessageID != "恢复原来已有配件" || len(next.Changes) != 2 {
		t.Fatal("restore lacks linked history")
	}
}

func TestTemporaryOwnedModelRestoresCategoryButNotLaterDirectEdit(t *testing.T) {
	next := updateState(t, NewRequirementState(), "这次借一颗7600", `[{"op":"set","field":"owned_parts","value":[{"category":"cpu","model":"7600"}],"scope":"temporary","quote":"这次借一颗7600"}]`)
	next = updateState(t, next, "恢复原来已有件", `[{"op":"restore","field":"owned_parts","quote":"恢复原来已有件"}]`)
	if next.Fields["existing_parts"].Status != "unknown" || next.Fields["owned_parts"].Status != "unknown" {
		t.Fatal("temporary loan survived restore")
	}

	next = updateState(t, ownedRequirementState(t), "这次只复用CPU", `[{"op":"set","field":"existing_parts","value":["cpu"],"scope":"temporary","quote":"这次只复用CPU"}]`)
	next = updateState(t, next, "CPU实际是12400F", `[{"op":"set","field":"owned_parts","value":[{"category":"cpu","model":"Intel Core i5-12400F"}],"scope":"session","quote":"CPU实际是12400F"}]`)
	next = updateState(t, next, "恢复原来的复用类别", `[{"op":"restore","field":"existing_parts","quote":"恢复原来的复用类别"}]`)
	if !bytes.Contains(next.Fields["owned_parts"].Value, []byte("12400F")) || bytes.Contains(next.Fields["owned_parts"].Value, []byte("7600")) {
		t.Fatal("restoring categories overwrote corrected model")
	}
}

func TestOwnedSynchronizationRollsBackWithInvalidLaterOperation(t *testing.T) {
	base := ownedRequirementState(t)
	update := RequirementUpdate{Operations: []RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`)},
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`-1`)},
	}}
	next, err := ApplyRequirementUpdate(base, update, RequirementSource{Kind: "edit"})
	if err == nil || !reflect.DeepEqual(base, next) {
		t.Fatal("invalid update partially committed linked fields")
	}
}
