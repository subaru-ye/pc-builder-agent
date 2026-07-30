package schemas

import (
	"encoding/json"
	"fmt"
)

// ChangeRequestSchemaVersion 当前唯一支持的 ChangeRequest schema 版本。
const ChangeRequestSchemaVersion = 1

// ChangeIntent 改单意图,三类之一;单次请求只承载一类(设计方案 §四)。
type ChangeIntent string

const (
	IntentSwapPart         ChangeIntent = "swap_part"
	IntentAdjustBudget     ChangeIntent = "adjust_budget"
	IntentChangeConstraint ChangeIntent = "change_constraint"
)

func (i *ChangeIntent) UnmarshalJSON(b []byte) error {
	s, err := unmarshalString(b, "intent")
	if err != nil {
		return err
	}
	switch ChangeIntent(s) {
	case IntentSwapPart, IntentAdjustBudget, IntentChangeConstraint:
		*i = ChangeIntent(s)
		return nil
	}
	return fmt.Errorf("非法改单意图枚举 %q(仅 swap_part|adjust_budget|change_constraint)", s)
}

// SwapSpec swap_part 意图的目标:换哪个品类,以及自然语言换件提示。
type SwapSpec struct {
	Category   Category
	TargetHint string
}

// ChangeRequest 初筛 Agent → 生成 Agent 的改单请求(设计方案 §四,2026-07-26 冻结)。
// 唯一权威出处为设计方案 §四,字段变更先改文档再改本包(CLAUDE.md 工程纪律)。
// BaseBuildRef 是模型自报的基版本引用,仅作展示;实际基版本以代码维护的
// 改单状态块(build_state)为真值(工程实践指引 §四.1)。
type ChangeRequest struct {
	SchemaVersion    int
	BaseBuildRef     string
	Intent           ChangeIntent
	Swap             *SwapSpec       // intent=swap_part 时非 nil
	BudgetDeltaCNY   *int            // intent=adjust_budget 时非 nil(正加负减)
	ConstraintPatch  json.RawMessage // intent=change_constraint 时非空:RequirementSpec 字段局部覆盖
	LockedCategories []Category      // 显式加锁;锁定全集语义见 HardLocked
	Notes            string
}

// changeRequestWire 线上格式:指针区分「键缺失」与「显式给值」。
type changeRequestWire struct {
	SchemaVersion    *int            `json:"schema_version"`
	BaseBuildRef     *string         `json:"base_build_ref"`
	Intent           *ChangeIntent   `json:"intent"`
	Swap             *swapSpecWire   `json:"swap"`
	BudgetDeltaCNY   *int            `json:"budget_delta_cny"`
	ConstraintPatch  json.RawMessage `json:"constraint_patch"`
	LockedCategories []Category      `json:"locked_categories"`
	Notes            *string         `json:"notes"`
}

type swapSpecWire struct {
	Category   *Category `json:"category"`
	TargetHint *string   `json:"target_hint"`
}

// DecodeChangeRequest 严格解码改单请求 JSON;违反契约返回 error(schema error)。
// intent 与对应必填字段联动校验:swap_part↔swap、adjust_budget↔budget_delta_cny、
// change_constraint↔constraint_patch,且不得携带其他意图的载荷字段。
func DecodeChangeRequest(data []byte) (ChangeRequest, error) {
	var w changeRequestWire
	if err := decodeStrict(data, &w); err != nil {
		return ChangeRequest{}, fmt.Errorf("change request: %w", err)
	}

	if w.SchemaVersion == nil {
		return ChangeRequest{}, fmt.Errorf("change request: 缺少 schema_version")
	}
	if *w.SchemaVersion != ChangeRequestSchemaVersion {
		return ChangeRequest{}, fmt.Errorf("change request: 不支持的 schema_version %d(当前仅 %d)", *w.SchemaVersion, ChangeRequestSchemaVersion)
	}
	if w.BaseBuildRef == nil || *w.BaseBuildRef == "" {
		return ChangeRequest{}, fmt.Errorf("change request: base_build_ref 缺失或为空")
	}
	if w.Intent == nil {
		return ChangeRequest{}, fmt.Errorf("change request: 缺少 intent")
	}

	out := ChangeRequest{
		SchemaVersion: *w.SchemaVersion,
		BaseBuildRef:  *w.BaseBuildRef,
		Intent:        *w.Intent,
	}

	switch *w.Intent {
	case IntentSwapPart:
		if w.Swap == nil {
			return ChangeRequest{}, fmt.Errorf("change request: intent=swap_part 必须提供 swap")
		}
		if w.BudgetDeltaCNY != nil || len(w.ConstraintPatch) > 0 {
			return ChangeRequest{}, fmt.Errorf("change request: intent=swap_part 不得携带 budget_delta_cny / constraint_patch")
		}
		if w.Swap.Category == nil || !isValidCategory(*w.Swap.Category) {
			return ChangeRequest{}, fmt.Errorf("change request: swap.category 缺失或非法(须为八大类之一)")
		}
		out.Swap = &SwapSpec{Category: *w.Swap.Category}
		if w.Swap.TargetHint != nil {
			out.Swap.TargetHint = *w.Swap.TargetHint
		}
	case IntentAdjustBudget:
		if w.BudgetDeltaCNY == nil {
			return ChangeRequest{}, fmt.Errorf("change request: intent=adjust_budget 必须提供 budget_delta_cny")
		}
		if *w.BudgetDeltaCNY == 0 {
			return ChangeRequest{}, fmt.Errorf("change request: budget_delta_cny 不得为 0")
		}
		if w.Swap != nil || len(w.ConstraintPatch) > 0 {
			return ChangeRequest{}, fmt.Errorf("change request: intent=adjust_budget 不得携带 swap / constraint_patch")
		}
		out.BudgetDeltaCNY = w.BudgetDeltaCNY
	case IntentChangeConstraint:
		if len(w.ConstraintPatch) == 0 {
			return ChangeRequest{}, fmt.Errorf("change request: intent=change_constraint 必须提供 constraint_patch")
		}
		if w.Swap != nil || w.BudgetDeltaCNY != nil {
			return ChangeRequest{}, fmt.Errorf("change request: intent=change_constraint 不得携带 swap / budget_delta_cny")
		}
		patch, err := validateConstraintPatch(w.ConstraintPatch)
		if err != nil {
			return ChangeRequest{}, err
		}
		out.ConstraintPatch = patch
	}

	if err := validateCategories("locked_categories", w.LockedCategories); err != nil {
		return ChangeRequest{}, fmt.Errorf("change request: %w", err)
	}
	out.LockedCategories = w.LockedCategories

	if w.Notes != nil {
		out.Notes = *w.Notes
	}
	return out, nil
}

// validateConstraintPatch 校验 constraint_patch 为非空 JSON 对象,且不夹带
// schema_version / budget_cny(预算调整必须走 adjust_budget 意图)。
// 键名与取值的合法性在派生需求单重新严格解码时兜底,这里只挡明确越界项。
func validateConstraintPatch(raw json.RawMessage) (json.RawMessage, error) {
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, fmt.Errorf("change request: constraint_patch 必须是 JSON 对象: %w", err)
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("change request: constraint_patch 不得为空对象")
	}
	for _, banned := range []string{"schema_version", "budget_cny"} {
		if _, ok := patch[banned]; ok {
			return nil, fmt.Errorf("change request: constraint_patch 不得覆盖 %s(预算调整走 adjust_budget)", banned)
		}
	}
	return raw, nil
}

// HardLocked 硬锁定品类全集(P4 设计 §4 冻结语义):
// swap_part 解锁集仅 {swap.category},其余七类全部硬锁定;
// adjust_budget / change_constraint 仅显式 locked_categories 硬锁定。
// 锁定由校验节点确定性比对 selection 强制,不靠提示词(FR-402)。
func (cr ChangeRequest) HardLocked() []Category {
	if cr.Intent != IntentSwapPart {
		return cr.LockedCategories
	}
	var out []Category
	for _, c := range AllCategories {
		if cr.Swap != nil && c == cr.Swap.Category {
			continue
		}
		out = append(out, c)
	}
	return out
}
