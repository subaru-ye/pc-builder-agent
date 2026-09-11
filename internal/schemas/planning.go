package schemas

import (
	"encoding/json"
	"strings"
)

// PlanningInput is the versioned execution snapshot. Unknown requirements remain
// unknown; the legacy RequirementSpec projection is not an admission gate.
type PlanningInput struct {
	SchemaVersion    int              `json:"schema_version"`
	State            RequirementState `json:"requirement_state"`
	BaseDraft        json.RawMessage  `json:"base_draft,omitempty"`
	PreviousProposal json.RawMessage  `json:"previous_proposal,omitempty"`
}

func PlanningRequirement(state RequirementState) (json.RawMessage, error) {
	// Routing copy is not part of requirement equality or the confirmed snapshot.
	state.Reply, state.NextAction = "", ""
	state.Changes, state.History = []RequirementChange{}, []RequirementChange{}
	return json.Marshal(PlanningInput{SchemaVersion: 2, State: state})
}

// FreeField retains independently editable, sourced requirements without adding
// product-specific enumerations. IDs are stable across subsequent updates.
func FreeField(key string) bool {
	return strings.HasPrefix(key, "free.") && len(key) > 5 && len(key) <= 100
}

// LegacyPlanningState adapts a saved requirement without inventing message
// provenance or treating old execution defaults as stated preferences.
func LegacyPlanningState(raw json.RawMessage) RequirementState {
	state := NewRequirementState()
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return state
	}
	for _, key := range RequirementFieldKeys {
		parts := strings.SplitN(key, ".", 2)
		v := values[key]
		if len(parts) == 2 {
			var nested map[string]json.RawMessage
			_ = json.Unmarshal(values[parts[0]], &nested)
			v = nested[parts[1]]
		}
		if len(v) == 0 || string(v) == `"any"` || string(v) == `""` || string(v) == "null" || key == "budget_flex" {
			continue
		}
		kind := "constraint"
		if strings.HasPrefix(key, "use_case.") || key == "recipient" || key == "owned_parts" || key == "existing_parts" {
			kind = "fact"
		}
		var strengths map[string]string
		_ = json.Unmarshal(values["constraint_strengths"], &strengths)
		state.Fields[key] = RequirementField{Value: v, Status: "active", Kind: kind, Strength: strengths[key], Source: &RequirementSource{Kind: "confirmed", Quote: "来自历史已确认需求；原消息来源未记录"}}
	}
	return state
}
