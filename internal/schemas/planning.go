package schemas

import (
	"encoding/json"
	"strings"
)

// EffectiveConstraints 是确认事务冻结的有效选型约束:完整核定预览(展开系统
// 默认后的规范化 RequirementSpec v2 线上格式)与被展开默认的来源清单。Builder
// 的模型输入与确定性门槛只读取这份冻结值;系统默认规则此后变化不改变已冻结
// run 的执行语义。原始 RequirementState 仅保留用户事实与溯源。
type EffectiveConstraints struct {
	Spec     json.RawMessage       `json:"spec"`     // review_spec 线上格式(RequirementSpec v2)
	Defaults []RequirementDefault  `json:"defaults"` // 实际展开的系统默认(origin=system_default)
}

// PlanningInput is the versioned execution snapshot. Unknown requirements remain
// unknown; the legacy RequirementSpec projection is not an admission gate.
// 确认事务冻结完整载荷(含 RunID/BaseDraft/PreviousProposal/有效选型约束);
// 发送路径只读冻结载荷,不得在执行时重新组装。EffectiveConstraints 为 nil 的
// 旧载荷(评估回放/历史归档)按原语义从 State 推导。
type PlanningInput struct {
	SchemaVersion    int                   `json:"schema_version"`
	State            RequirementState      `json:"requirement_state"`
	EffectiveConstraints *EffectiveConstraints `json:"effective_constraints,omitempty"`
	BaseDraft        json.RawMessage       `json:"base_draft,omitempty"`
	PreviousProposal json.RawMessage       `json:"previous_proposal,omitempty"`
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

// PlanningBuilderInput 组装确认事务冻结的完整 Builder 执行载荷;state 不被
// 修改(Routing copy 的 Changes/History 不参与需求等价)。RunID 影响载荷内容,
// 因此失败重试必须为新 run 另冻一份载荷与新 hash。constraints 是确认事务
// 从核定预览冻结的有效选型约束(review_spec + 默认来源),随载荷一起冻结。
func PlanningBuilderInput(runID string, state RequirementState, constraints *EffectiveConstraints, baseDraft, previousProposal json.RawMessage, previousRunID string) (json.RawMessage, error) {
	// Routing copy is not part of requirement equality or the confirmed snapshot.
	state.Changes, state.History = []RequirementChange{}, []RequirementChange{}
	return json.Marshal(PlanningInput{SchemaVersion: 2, State: state, EffectiveConstraints: constraints, RunID: runID,
		BaseDraft: baseDraft, PreviousProposal: previousProposal, PreviousRunID: previousRunID})
}

// PlanningRequirement 返回不绑定 run 的最小 Builder 载荷包装,供快照比对等
// 场景使用;确认路径应使用 PlanningBuilderInput。
func PlanningRequirement(state RequirementState) (json.RawMessage, error) {
	return PlanningBuilderInput("", state, nil, nil, nil, "")
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
