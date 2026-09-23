package schemas

import (
	"encoding/json"
	"slices"
)

func recordRequirementChange(state *RequirementState, field, op string, before, after RequirementField, source RequirementSource) {
	state.Fields[field] = after
	if op != "alternative" && op != "conflict" {
		for i := range state.Observations {
			// unsupported capability 观察只能由能力专属撤销
			// (remove unsupported.<name>)解除,同字段普通更新不算放弃。
			if _, coded := unsupportedCapabilityName(state.Observations[i].Reason); coded {
				continue
			}
			if state.Observations[i].Field == field {
				state.Observations[i].Resolved = true
			}
		}
	}
	change := RequirementChange{Revision: state.Revision, Op: op, Field: field, Before: &before, After: &after, Source: source}
	state.Changes = append(state.Changes, change)
	state.History = append(state.History, change)
}

// Category and exact-model editors describe the same ownership. Synchronize
// their structural consequences here, after validation, for both chat and UI.
// No model names, quantities or user preferences are inferred from text.
func syncOwnershipFields(state *RequirementState, op RequirementOperation, after RequirementField, source RequirementSource) {
	if op.Op == "alternative" || op.Op == "conflict" || (op.Field != "existing_parts" && op.Field != "owned_parts") {
		return
	}
	partner := "owned_parts"
	if op.Field == "owned_parts" {
		partner = "existing_parts"
	}
	before := state.Fields[partner]
	if op.Op == "restore" && before.DerivedFrom == op.Field && before.Previous != nil {
		restored := *before.Previous
		restored.Source = &source
		recordRequirementChange(state, partner, "restore", before, restored, source)
		return
	}
	if op.Field == "existing_parts" && op.Op == "remove" {
		recordRequirementChange(state, partner, "remove", before, RequirementField{Status: "removed", Source: &source, DerivedFrom: op.Field}, source)
		return
	}
	if after.Status != "active" || before.Status == "conflict" {
		return
	}
	var categories []Category
	var owned []OwnedPart
	if op.Field == "existing_parts" {
		if before.Status != "active" || json.Unmarshal(after.Value, &categories) != nil || json.Unmarshal(before.Value, &owned) != nil {
			return
		}
		kept := make([]OwnedPart, 0, len(owned))
		for _, part := range owned {
			if slices.Contains(categories, part.Category) {
				kept = append(kept, part)
			}
		}
		if len(kept) == len(owned) {
			return
		}
		owned = kept
	} else {
		if json.Unmarshal(after.Value, &owned) != nil {
			return
		}
		if before.Status == "active" {
			_ = json.Unmarshal(before.Value, &categories)
		}
		count := len(categories)
		for _, part := range owned {
			if !slices.Contains(categories, part.Category) {
				categories = append(categories, part.Category)
			}
		}
		if count == len(categories) {
			return
		}
	}
	value, _ := json.Marshal(owned)
	if partner == "existing_parts" {
		value, _ = json.Marshal(categories)
	}
	linked := RequirementField{Value: value, Status: "active", Kind: "fact", Strength: "must", Scope: after.Scope, Source: &source, Evidence: after.Evidence, DerivedFrom: op.Field}
	if after.Scope == "temporary" {
		linked.Previous = &before
		if before.Scope == "temporary" && before.Previous != nil {
			linked.Previous = before.Previous
		}
	}
	recordRequirementChange(state, partner, "set", before, linked, source)
}
