package schemas

import (
	"encoding/json"
	"strings"
)

// PlanningInput is the versioned execution snapshot. Unknown requirements remain
// unknown; the legacy RequirementSpec projection is not an admission gate.
type PlanningInput struct {
	SchemaVersion    int                `json:"schema_version"`
	State            RequirementState   `json:"requirement_state"`
	BaseDraft        json.RawMessage    `json:"base_draft,omitempty"`
	PreviousProposal json.RawMessage    `json:"previous_proposal,omitempty"`
	Request          *RequirementSource `json:"request,omitempty"` // 本轮执行原话，不是新增的用户偏好。
	// RunID 是本轮产品侧 run，生成服务据此把完整产物归档到 planning_artifacts。
	RunID string `json:"run_id,omitempty"`
	// PreviousRunID 是上一轮 proposal 的 run；上一轮传输副本正文已降级，
	// 生成服务据此从归档补全证据正文。
	PreviousRunID string `json:"previous_run_id,omitempty"`
}

// ScreeningConversation supplies execution facts and the last assistant turn for
// reference resolution. It is not a second requirement memory or user evidence.
type ScreeningConversation struct {
	CanPlan       bool            `json:"can_plan"`
	BuildVersion  int             `json:"build_version,omitempty"`
	BaseDraft     json.RawMessage `json:"base_draft,omitempty"`
	Quote         json.RawMessage `json:"quote,omitempty"`
	Parts         json.RawMessage `json:"parts,omitempty"`
	Proposal      json.RawMessage `json:"proposal,omitempty"`
	LastAssistant string          `json:"last_assistant,omitempty"`
}

func PlanningRequirement(state RequirementState) (json.RawMessage, error) {
	// Routing copy is not part of requirement equality or the confirmed snapshot.
	state.Changes, state.History = []RequirementChange{}, []RequirementChange{}
	return json.Marshal(PlanningInput{SchemaVersion: 2, State: state})
}

// FreeField retains independently editable, sourced requirements without adding
// product-specific enumerations. IDs are stable across subsequent updates.
func FreeField(key string) bool {
	return strings.HasPrefix(key, "free.") && len(key) > 5 && len(key) <= 100
}

// LegacyPlanningState 仅供建立在 v1 历史产物上的评估工具回放旧归档使用;
// 产品运行时不得调用(v2 一次性切换,旧会话以稳定错误拒绝,不静默重建)。
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
