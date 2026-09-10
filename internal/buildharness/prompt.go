package buildharness

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

const builderV2Instruction = `你是装机配置选择器。候选数据已经由程序准备完毕，你不得调用工具、不得编造 SKU，也不得输出解释文字。
只输出一个符合既有 BuildDraft schema_version=1 的 JSON 对象。selection 的每个非 null SKU 必须来自 candidate_bundle；fixed_selection 中的品类必须原样照抄。
整机总价必须进入 budget_window_cny 的闭区间并尽量避免 specs 缺失造成 unknown；修复轮次必须满足 validation.required_adjustment_cny。游戏需求必须选择独显；非游戏仅当所选 CPU 明确有核显时 GPU 才可为 null。
requirement.requirement_semantics 区分 fact（用途事实）、context（补充说明）与 constraint（配置条件）。fact/context 即使标为 must，也应作为选型背景和理由，不是逐 SKU 的商品属性断言；完整使用 notes、用途和 requirement_details 中的有效信息，不忽略剪辑素材、软件场景等原文。requirement_observations 保留尚未确认或无法结构化的本轮原文，只作待确认背景，不得擅自填成精确参数或用户已采用的硬条件。只有配置条件明确标为 must 时才要求证据支持其满足，不凭语义命中或生成理由宣称核验通过；constraint_strengths 对 brand_pref.gpu 标为 must 且明确了品牌时必须选择对应独显，不可用 gpu=null 绕过。prefer 只用于可行配置间优先选择，不能因此拒绝其他品牌或尺寸的合格候选。候选组已将明确匹配软偏好的候选排在前面；若因预算、兼容性或锁定件无法采用，rationale 应说明取舍。requirement_details 是当前会话的补充需求，不代表长期个人偏好。
budget_window_cny.flex_source=execution_default 表示生成程序采用的预算弹性，不代表用户明确同意加价；不得写成用户已授权或偏好。user_requirement 才表示当前状态有明确弹性要求，legacy_unspecified 表示旧记录未保存来源。预算为 prefer 时仍遵守本次程序预算窗口，不自行放宽执行边界。
修复轮次只能修改 repair_plan.mutable 中的品类，其余品类必须照抄 previous_draft；若 repair_plan.preferred_selection 非空，必须逐项精确照抄；preferred_ssd_selection 非空时完整照抄其中的 SKU 和 quantity，drop_gpu=true 时 gpu 必须为 null。否则 mutable 当前 SKU 已从 candidate_bundle 移除，必须选择其中的替代 SKU。`

type promptEnvelope struct {
	SchemaVersion   int                 `json:"schema_version"`
	Attempt         int                 `json:"attempt"`
	OutputContract  map[string]any      `json:"output_contract"`
	Requirement     json.RawMessage     `json:"requirement"`
	BudgetWindow    budgetWindow        `json:"budget_window_cny"`
	Change          any                 `json:"change,omitempty"`
	FixedSelection  map[string]any      `json:"fixed_selection,omitempty"`
	BaseSelection   map[string]any      `json:"base_selection,omitempty"`
	CandidateBundle CandidateBundle     `json:"candidate_bundle"`
	PreviousDraft   map[string]any      `json:"previous_draft,omitempty"`
	Validation      *validationFeedback `json:"validation,omitempty"`
	RepairPlan      *RepairPlan         `json:"repair_plan,omitempty"`
}

type validationFeedback struct {
	OverallStatus         schemas.OverallStatus `json:"overall_status,omitempty"`
	TotalCNY              string                `json:"total_cny,omitempty"`
	BudgetDirection       string                `json:"budget_direction,omitempty"`
	RequiredAdjustmentCNY string                `json:"required_adjustment_cny,omitempty"`
	Checks                []feedbackCheck       `json:"checks,omitempty"`
}

type budgetWindow struct {
	TargetCNY  string `json:"target"`
	LowerCNY   string `json:"lower"`
	UpperCNY   string `json:"upper"`
	FlexSource string `json:"flex_source"`
}

type feedbackCheck struct {
	RuleID        schemas.RuleID   `json:"rule_id"`
	Outcome       schemas.Outcome  `json:"outcome"`
	Severity      schemas.Severity `json:"severity"`
	MissingFields []string         `json:"missing_fields,omitempty"`
	Detail        string           `json:"detail,omitempty"`
}

func buildPrompt(input BuildInput, full CandidateBundle, previous *schemas.BuildDraft, plan *RepairPlan,
	result validate.Result, attempt int) (string, error) {
	requirement, err := schemas.EncodeRequirementSpec(input.Requirement)
	if err != nil {
		return "", err
	}
	envelope := promptEnvelope{
		SchemaVersion: 1, Attempt: attempt, OutputContract: outputContract(attempt), Requirement: requirement,
		BudgetWindow: requirementBudgetWindow(input.Requirement),
		Change:       changeView(input.Change), FixedSelection: fixedSelection(input), CandidateBundle: full,
	}
	if input.BaseSelection != nil {
		envelope.BaseSelection = selectionMap(*input.BaseSelection)
	}
	if previous != nil {
		envelope.PreviousDraft = draftMap(*previous)
	}
	if plan != nil {
		envelope.RepairPlan = plan
		if previous != nil {
			envelope.CandidateBundle = restrictBundle(full, *plan, previous.Selection)
			envelope.FixedSelection = fixedForRepair(*previous, plan.Mutable)
			envelope.Validation = feedback(result, input.Requirement)
		}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("buildharness: 序列化模型输入失败: %w", err)
	}
	return string(encoded), nil
}

// outputContract 把完整 wire shape 放进每轮请求，避免紧凑提示下模型省略
// requirement_ref/build_ref 等没有业务判定作用、但 schema 强制要求的字段。
func outputContract(attempt int) map[string]any {
	return map[string]any{
		"instruction": "只返回一个 JSON 对象，所有 required 字段都必须存在；不要返回 output_contract 外层。fixed_selection 必须逐字照抄。budget_basis=new_purchase 时，仅将未列在 owned_parts 的配件计入 budget_window_cny，已有件仍参与兼容性校验；full_build 或未指定时预算为整机参考价。",
		"required":    []string{"schema_version", "requirement_ref", "build_ref", "selection"},
		"template": map[string]any{
			"schema_version":  1,
			"requirement_ref": "req_harness_v2",
			"build_ref":       fmt.Sprintf("build_harness_v2_%d", attempt),
			"selection": map[string]any{
				"cpu": "<candidate sku>", "gpu": "<candidate sku or null>",
				"motherboard": "<candidate sku>", "memory": "<candidate sku>",
				"ssd": []map[string]any{{"sku": "<candidate sku>", "quantity": 1}},
				"psu": "<candidate sku>", "case": "<candidate sku>", "cooler": "<candidate sku>",
			},
		},
	}
}

func changeView(change *schemas.ChangeRequest) any {
	if change == nil {
		return nil
	}
	out := map[string]any{"intent": change.Intent, "base_build_ref": change.BaseBuildRef, "notes": change.Notes}
	if change.Swap != nil {
		out["swap"] = map[string]any{"category": change.Swap.Category, "target_hint": change.Swap.TargetHint}
	}
	if change.BudgetDeltaCNY != nil {
		out["budget_delta_cny"] = *change.BudgetDeltaCNY
	}
	if len(change.ConstraintPatch) > 0 {
		out["constraint_patch"] = change.ConstraintPatch
	}
	return out
}

func fixedSelection(input BuildInput) map[string]any {
	if input.BaseSelection == nil || len(input.Locked) == 0 {
		return nil
	}
	all := selectionMap(*input.BaseSelection)
	out := make(map[string]any, len(input.Locked))
	for _, category := range input.Locked {
		out[string(category)] = all[string(category)]
	}
	return out
}

func fixedForRepair(previous schemas.BuildDraft, mutable []schemas.Category) map[string]any {
	mutableSet := categorySet(mutable)
	all := selectionMap(previous.Selection)
	out := make(map[string]any, len(schemas.AllCategories)-len(mutable))
	for _, category := range schemas.AllCategories {
		if !mutableSet[category] {
			out[string(category)] = all[string(category)]
		}
	}
	return out
}

func restrictBundle(bundle CandidateBundle, plan RepairPlan, previous schemas.BuildSelection) CandidateBundle {
	set := categorySet(plan.Mutable)
	out := CandidateBundle{SchemaVersion: bundle.SchemaVersion, SnapshotDate: bundle.SnapshotDate, Trimmed: bundle.Trimmed}
	for _, group := range bundle.Groups {
		if set[group.Category] {
			if group.Category == schemas.CategoryGPU && plan.DropGPU {
				out.Groups = append(out.Groups, CandidateGroup{Category: group.Category})
				continue
			}
			preferred := plan.PreferredSelection[group.Category]
			current := map[string]bool{}
			for _, sku := range selectionSKUs(previous, group.Category) {
				current[sku] = true
			}
			filtered := CandidateGroup{Category: group.Category}
			for _, candidate := range group.Candidates {
				if group.Category == schemas.CategorySSD && len(plan.PreferredSSDs) > 0 {
					for _, disk := range plan.PreferredSSDs {
						if disk.SKU == candidate.SKU {
							filtered.Candidates = append(filtered.Candidates, candidate)
							break
						}
					}
					continue
				}
				if preferred != "" {
					if candidate.SKU == preferred {
						filtered.Candidates = append(filtered.Candidates, candidate)
					}
					continue
				}
				if !current[candidate.SKU] {
					filtered.Candidates = append(filtered.Candidates, candidate)
				}
			}
			out.Groups = append(out.Groups, filtered)
		}
	}
	return out
}

func feedback(result validate.Result, requirement schemas.RequirementSpec) *validationFeedback {
	out := &validationFeedback{OverallStatus: result.Report.OverallStatus, TotalCNY: validate.BudgetQuote(requirement, result.Quote).TotalCNY}
	if delta, direction := budgetDirection(requirement, result.Quote); direction != "" {
		out.BudgetDirection = direction
		out.RequiredAdjustmentCNY = formatPromptFen(delta)
	}
	for _, check := range result.Report.Checks {
		if check.Outcome == schemas.OutcomePass {
			continue
		}
		out.Checks = append(out.Checks, feedbackCheck{
			RuleID: check.RuleID, Outcome: check.Outcome, Severity: check.Severity,
			MissingFields: check.MissingFields, Detail: check.Detail,
		})
	}
	return out
}

func requirementBudgetWindow(spec schemas.RequirementSpec) budgetWindow {
	target := int64(spec.BudgetCNY) * 100
	flex := int64(math.Round(float64(target) * spec.BudgetFlex))
	source := "legacy_unspecified"
	if len(spec.ConstraintStrengths) > 0 || len(spec.RequirementSemantics) > 0 {
		source = "execution_default"
		if spec.ConstraintStrengths["budget_flex"] != "" {
			source = "user_requirement"
		}
	}
	return budgetWindow{
		TargetCNY:  formatPromptFen(target),
		LowerCNY:   formatPromptFen(target - flex),
		UpperCNY:   formatPromptFen(target + flex),
		FlexSource: source,
	}
}

func formatPromptFen(fen int64) string {
	return fmt.Sprintf("%d.%02d", fen/100, fen%100)
}

func draftMap(draft schemas.BuildDraft) map[string]any {
	out := map[string]any{
		"schema_version": draft.SchemaVersion, "requirement_ref": draft.RequirementRef,
		"build_ref": draft.BuildRef, "selection": selectionMap(draft.Selection),
	}
	if draft.Rationale != nil {
		out["rationale"] = draft.Rationale
	}
	if draft.BudgetAllocation != nil {
		out["budget_allocation"] = draft.BudgetAllocation
	}
	return out
}

func selectionMap(selection schemas.BuildSelection) map[string]any {
	var gpu any
	if selection.GPU != nil {
		gpu = *selection.GPU
	}
	return map[string]any{
		"cpu": selection.CPU, "gpu": gpu, "motherboard": selection.Motherboard,
		"memory": selection.Memory, "ssd": selection.SSDs, "psu": selection.PSU,
		"case": selection.Case, "cooler": selection.Cooler,
	}
}
