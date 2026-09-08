package buildharness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type runner struct {
	model    model.LLM
	planner  CandidatePlanner
	repairer RepairPlanner
	eval     Evaluator
	trace    TraceSink
}

func New(cfg Config) (Harness, error) {
	if cfg.Model == nil || cfg.Planner == nil || cfg.Repairer == nil || cfg.Eval == nil {
		return nil, fmt.Errorf("buildharness: Model/Planner/Repairer/Eval 不能为空")
	}
	if cfg.Trace == nil {
		cfg.Trace = noopTrace{}
	}
	return &runner{model: cfg.Model, planner: cfg.Planner, repairer: cfg.Repairer, eval: cfg.Eval, trace: cfg.Trace}, nil
}

func (h *runner) Run(ctx context.Context, input BuildInput) (out BuildResult, runErr error) {
	// 每次运行使用独立 evaluator 包装，避免并发运行互相污染预算口径。
	local := *h
	local.eval = ownedEvaluator{base: h.eval, spec: input.Requirement}
	h = &local
	defer func() {
		if runErr != nil {
			var d *Decision
			if errors.As(runErr, &d) {
				out = BuildResult{Decision: d, Message: d.Message}
				runErr = nil
			}
		} else if !out.Succeeded && out.Decision == nil {
			out.Decision = &Decision{Kind: "invalid_output", Reason: "invalid_model_output", Scope: "current_run", Message: out.Message}
		}
	}()
	if err := ctx.Err(); err != nil {
		return BuildResult{}, err
	}
	if d := clarification(input.Requirement); d != nil {
		return BuildResult{Decision: d, Message: d.Message}, nil
	}

	started := time.Now()
	locked := make([]string, len(input.Locked))
	for index, category := range input.Locked {
		locked[index] = string(category)
	}
	h.record(input, "harness.started", map[string]any{"mode": ModeV2, "locked_categories": locked})
	bundle, err := h.planner.Prepare(ctx, input)
	if err != nil {
		h.record(input, "harness.failed", map[string]any{"stage": "candidate_prepare"})
		return BuildResult{}, err
	}
	if bundle.OwnedInput != nil {
		input = *bundle.OwnedInput
	}
	counts := map[string]int{}
	for _, group := range bundle.Groups {
		counts[string(group.Category)] = len(group.Candidates)
	}
	h.record(input, "candidate.bundle", map[string]any{
		"snapshot_date": bundle.SnapshotDate, "candidate_counts": counts, "trimmed": bundle.Trimmed,
	})

	constraints := RepairConstraints{
		Requirement: input.Requirement, Change: input.Change, Base: input.BaseSelection,
		Locked: input.Locked, Bundle: bundle,
	}
	var previous *schemas.BuildDraft
	var plan *RepairPlan
	lastSelection := ""
	var lastResult validate.Result
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		h.record(input, "decision.started", map[string]any{"attempt": attempt})
		prompt, err := buildPrompt(input, bundle, previous, plan, lastResult, attempt)
		if err != nil {
			return BuildResult{}, err
		}
		callStarted := time.Now()
		text, err := h.generate(ctx, prompt)
		h.record(input, "decision.completed", map[string]any{
			"attempt": attempt, "duration_ms": time.Since(callStarted).Milliseconds(), "succeeded": err == nil,
		})
		if err != nil {
			h.record(input, "harness.failed", map[string]any{"stage": "model", "attempt": attempt})
			return BuildResult{}, err
		}

		draft, decodeErr := ExtractBuildDraft(text)
		if decodeErr != nil {
			if attempt == MaxAttempts {
				message := fmt.Sprintf("第 %d/%d 次模型输出仍不符合 BuildDraft schema:%v", attempt, MaxAttempts, decodeErr)
				h.finish(input, started, attempt, false)
				return BuildResult{Attempts: attempt, Message: message}, nil
			}
			mutable := unlockedCategories(input.Locked)
			plan = &RepairPlan{Mutable: mutable, Reason: "schema_error"}
			previous = nil
			lastResult = validate.Result{}
			h.recordRepair(input, attempt, *plan)
			continue
		}

		selectionKey := canonicalSelection(draft.Selection)
		if plan != nil && previous != nil {
			if err := validateRepairSelection(*plan, previous.Selection, draft.Selection); err != nil {
				if attempt == MaxAttempts {
					h.finish(input, started, attempt, false)
					return BuildResult{Attempts: attempt, Draft: draft, Message: err.Error()}, nil
				}
				// 沿用已验证的修复计划和上一份草案,不把违规输出升级为新基线。
				h.recordRepair(input, attempt, *plan)
				continue
			}
		}
		if lastSelection != "" && selectionKey == lastSelection {
			message := "连续两次提交完全相同且仍未通过的 selection，Harness v2 已终止以避免死循环。"
			h.finish(input, started, attempt, false)
			return unresolvedResult(attempt, draft, lastResult, message), nil
		}
		membershipBundle := bundle
		membershipCategories := schemas.AllCategories
		if plan != nil && previous != nil {
			membershipBundle = restrictBundle(bundle, *plan, previous.Selection)
			membershipCategories = plan.Mutable
		}
		if categories, membershipErr := validateMembershipCategories(membershipBundle, draft.Selection, membershipCategories); membershipErr != nil {
			if attempt == MaxAttempts {
				h.finish(input, started, attempt, false)
				return BuildResult{Attempts: attempt, Draft: draft, Message: membershipErr.Error()}, nil
			}
			plan = &RepairPlan{Mutable: withoutLocked(categories, input.Locked), Reason: "candidate_membership"}
			if len(plan.Mutable) == 0 {
				h.finish(input, started, attempt, false)
				return BuildResult{Attempts: attempt, Draft: draft, Message: membershipErr.Error()}, nil
			}
			previous, lastSelection = &draft, selectionKey
			h.recordRepair(input, attempt, *plan)
			continue
		}
		if constraintErr := validateChangeConstraints(input, draft.Selection); constraintErr != nil {
			if attempt == MaxAttempts {
				h.finish(input, started, attempt, false)
				return BuildResult{Attempts: attempt, Draft: draft, Message: constraintErr.Error()}, nil
			}
			mutable := unlockedCategories(input.Locked)
			if input.Change != nil && input.Change.Intent == schemas.IntentAdjustBudget {
				mutable = changedCategories(input.BaseSelection, draft.Selection)
				if len(mutable) > 2 {
					mutable = mutable[:2]
				}
			}
			plan = &RepairPlan{Mutable: mutable, Reason: "change_constraints"}
			previous, lastSelection = &draft, selectionKey
			h.recordRepair(input, attempt, *plan)
			continue
		}

		result, evalErr := h.eval.Evaluate(ctx, draft.Selection)
		if evalErr != nil {
			if errors.Is(evalErr, store.ErrUnknownSKU) {
				return BuildResult{}, fmt.Errorf("buildharness: 候选成员校验后仍出现未知 SKU: %w", evalErr)
			}
			return BuildResult{}, evalErr
		}
		if validate.BudgetQuote(input.Requirement, result.Quote).MissingCount > 0 {
			d := &Decision{Kind: "data_unavailable", Reason: "selected_price_missing", Scope: "current_catalog", SnapshotDate: bundle.SnapshotDate, Message: "所选需计入预算的配件缺少可核验报价，本轮不交付；请补充报价资料。"}
			return BuildResult{Attempts: attempt, Draft: draft, Result: result, Decision: d, Message: d.Message}, nil
		}
		lastResult = result
		ruleIDs := problematicRuleIDs(result.Report)
		h.record(input, "validation.completed", map[string]any{
			"attempt": attempt, "overall_status": result.Report.OverallStatus, "problem_rule_ids": ruleIDs,
		})

		budgetOK := inBudgetWindow(input.Requirement, result.Quote)
		retryUnknown := hasUnknown(result.Report)
		if result.Report.OverallStatus != schemas.OverallFail && budgetOK && (!retryUnknown || attempt == MaxAttempts) {
			enrichCandidateRationale(&draft, bundle, input.Requirement)
			h.finish(input, started, attempt, true)
			return BuildResult{Succeeded: true, Attempts: attempt, Draft: draft, Result: result}, nil
		}
		if attempt == MaxAttempts {
			budgetResult := result
			budgetResult.Quote = validate.BudgetQuote(input.Requirement, result.Quote)
			message := finalFailureMessage(budgetResult, budgetOK)
			h.finish(input, started, attempt, false)
			return unresolvedResult(attempt, draft, result, message), nil
		}

		nextPlan, planErr := h.repairer.Plan(result, draft, constraints)
		if planErr != nil {
			if _, direction := budgetDirection(input.Requirement, result.Quote); direction != "" {
				// 单件价格排序无替代项时,数量调整或其他品类组合仍可能可行。
				nextPlan = RepairPlan{Reason: "budget_" + direction}
			} else {
				h.finish(input, started, attempt, false)
				return unresolvedResult(attempt, draft, result, planErr.Error()), nil
			}
		}
		if strings.Contains(nextPlan.Reason, "budget_") {
			budgetPlan, checked, preferenceErr := h.budgetRepair(ctx, input, draft.Selection, result.Quote, bundle, nextPlan)
			if preferenceErr != nil {
				return BuildResult{}, preferenceErr
			}
			nextPlan = budgetPlan
			h.record(input, "repair.budget_candidates", map[string]any{
				"attempt": attempt, "checked": checked, "feasible": len(nextPlan.PreferredSelection) > 0 || len(nextPlan.PreferredSSDs) > 0 || nextPlan.DropGPU,
			})
			if len(nextPlan.Mutable) == 0 {
				h.finish(input, started, attempt, false)
				return unresolvedResult(attempt, draft, result, "有界候选搜索未找到满足预算与兼容性的修复方案,本轮不保存版本。"), nil
			}
		}
		plan = &nextPlan
		previous, lastSelection = &draft, selectionKey
		h.recordRepair(input, attempt, *plan)
	}
	return BuildResult{}, fmt.Errorf("buildharness: 不可达的执行状态")
}

func validateRepairSelection(plan RepairPlan, previous, current schemas.BuildSelection) error {
	mutable := categorySet(plan.Mutable)
	for _, category := range schemas.AllCategories {
		if !mutable[category] && !sameCategorySelection(previous, current, category) {
			return fmt.Errorf("修复改变了未开放的品类:%s", category)
		}
	}
	for category, sku := range plan.PreferredSelection {
		selected := selectionSKUs(current, category)
		if len(selected) != 1 || selected[0] != sku {
			return fmt.Errorf("修复未采用已验证候选:%s", category)
		}
	}
	if len(plan.PreferredSSDs) > 0 && !reflect.DeepEqual(plan.PreferredSSDs, current.SSDs) {
		return fmt.Errorf("修复未采用已验证的 SSD SKU/数量")
	}
	if plan.DropGPU && current.GPU != nil {
		return fmt.Errorf("修复要求 GPU 为 null")
	}
	return nil
}

// enrichCandidateRationale 只补充模型省略的展示理由，不参与规则或价格真值。
// 语义证据来自本轮已通过硬过滤的 Candidate Bundle，不追加模型调用。
func enrichCandidateRationale(draft *schemas.BuildDraft, bundle CandidateBundle, requirement schemas.RequirementSpec) {
	if draft.Rationale == nil {
		draft.Rationale = map[string]string{}
	}
	for _, category := range schemas.AllCategories {
		if draft.Rationale[string(category)] != "" {
			continue
		}
		group := bundleGroup(&bundle, category)
		if group == nil {
			continue
		}
		selected := map[string]bool{}
		for _, sku := range selectionSKUs(draft.Selection, category) {
			selected[sku] = true
		}
		for _, candidate := range group.Candidates {
			if selected[candidate.SKU] && strings.TrimSpace(candidate.MatchText) != "" {
				draft.Rationale[string(category)] = "偏好匹配：" + trimRunes(strings.TrimSpace(candidate.MatchText), 120)
				break
			}
		}
	}
	// 软偏好缺少硬规格字段时，只展示“接近目标、仍需核对”，不把颜色或
	// 噪声表现伪装成已验证事实。
	notes := strings.ToLower(requirement.Notes)
	if draft.Rationale[string(schemas.CategoryCase)] == "" &&
		(strings.Contains(notes, "白色") || strings.Contains(notes, "white") || strings.Contains(notes, "海景房")) {
		draft.Rationale[string(schemas.CategoryCase)] = "接近海景房风格；候选缺少可靠颜色字段，白色版本需购买前核对。"
	}
	if draft.Rationale[string(schemas.CategoryGPU)] == "" &&
		(requirement.NoisePref == schemas.NoisePrefSilent || strings.Contains(notes, "安静") || strings.Contains(notes, "低噪")) {
		draft.Rationale[string(schemas.CategoryGPU)] = "按安静低噪偏好筛选；候选缺少统一噪声数值，需购买前核对实测。"
	}
	if len(draft.Rationale) == 0 {
		draft.Rationale = nil
	}
}

func trimRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// feasibleBudgetPreference 用唯一的确定性 validator 枚举至多三个品类的候选
// 组合。三个品类覆盖一个规则修复品类与最多两个预算品类，最坏为 8³=512
// 个纯本地组合；只向下一轮模型暴露预算内且兼容的最佳组合。
func (h *runner) feasibleBudgetPreference(ctx context.Context, requirement schemas.RequirementSpec,
	current schemas.BuildSelection, bundle CandidateBundle, categories []schemas.Category) (map[schemas.Category]string, int, error) {
	if len(categories) == 0 || len(categories) > 3 {
		return nil, 0, nil
	}
	groups := make([]*CandidateGroup, len(categories))
	for index, category := range categories {
		groups[index] = bundleGroup(&bundle, category)
		if groups[index] == nil || len(groups[index].Candidates) == 0 {
			return nil, 0, nil
		}
	}
	target := int64(requirement.BudgetCNY) * 100
	bestDistance := int64(math.MaxInt64)
	bestStatus := 2
	bestKey := ""
	checked := 0
	var best map[schemas.Category]string
	var visit func(int, schemas.BuildSelection, map[schemas.Category]string, []string) error
	visit = func(index int, selection schemas.BuildSelection, preferred map[schemas.Category]string, keyParts []string) error {
		if index < len(groups) {
			for _, candidate := range groups[index].Candidates {
				next := selection
				setSelectionSKU(&next, categories[index], candidate.SKU)
				preferred[categories[index]] = candidate.SKU
				if err := visit(index+1, next, preferred, append(keyParts, candidate.SKU)); err != nil {
					return err
				}
			}
			delete(preferred, categories[index])
			return nil
		}
		if canonicalSelection(selection) == canonicalSelection(current) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		checked++
		result, err := h.eval.Evaluate(ctx, selection)
		if err != nil {
			return err
		}
		if result.Report.OverallStatus == schemas.OverallFail || !inBudgetWindow(requirement, result.Quote) {
			return nil
		}
		budgetQuote := validate.BudgetQuote(requirement, result.Quote)
		total, ok := parsePriceFen(&budgetQuote.TotalCNY)
		if !ok {
			return nil
		}
		distance := total - target
		if distance < 0 {
			distance = -distance
		}
		status := 1
		if result.Report.OverallStatus == schemas.OverallPass {
			status = 0
		}
		key := strings.Join(keyParts, "\x00")
		if best == nil || status < bestStatus || (status == bestStatus && (distance < bestDistance || (distance == bestDistance && key < bestKey))) {
			best = make(map[schemas.Category]string, len(preferred))
			for category, sku := range preferred {
				best[category] = sku
			}
			bestStatus, bestDistance, bestKey = status, distance, key
		}
		return nil
	}
	if err := visit(0, current, map[schemas.Category]string{}, nil); err != nil {
		return nil, checked, err
	}
	return best, checked, nil
}

func setSelectionSKU(selection *schemas.BuildSelection, category schemas.Category, sku string) {
	switch category {
	case schemas.CategoryCPU:
		selection.CPU = sku
	case schemas.CategoryGPU:
		selection.GPU = &sku
	case schemas.CategoryMotherboard:
		selection.Motherboard = sku
	case schemas.CategoryMemory:
		selection.Memory = sku
	case schemas.CategorySSD:
		selection.SSDs = []schemas.SSDSelection{{SKU: sku, Quantity: 1}}
	case schemas.CategoryPSU:
		selection.PSU = sku
	case schemas.CategoryCase:
		selection.Case = sku
	case schemas.CategoryCooler:
		selection.Cooler = sku
	}
}

func (h *runner) generate(ctx context.Context, prompt string) (string, error) {
	temperature := float32(0)
	req := &model.LLMRequest{
		Model:    h.model.Name(),
		Contents: []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(builderV2Instruction, genai.RoleUser),
			Temperature:       &temperature,
			ResponseMIMEType:  "application/json",
		},
	}
	var output strings.Builder
	gotContent := false
	for response, err := range h.model.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", err
		}
		if response == nil || response.Content == nil {
			continue
		}
		for _, part := range response.Content.Parts {
			if part.FunctionCall != nil {
				return "", fmt.Errorf("buildharness: v2 模型返回了未注册的工具调用")
			}
			if part.Text != "" {
				gotContent = true
				output.WriteString(part.Text)
			}
		}
	}
	if !gotContent {
		return "", fmt.Errorf("buildharness: 模型未返回文本内容")
	}
	return output.String(), nil
}

// ExtractBuildDraft 容忍围栏或少量前置文字，但只接受能严格通过既有 schema 的 JSON 对象。
func ExtractBuildDraft(text string) (schemas.BuildDraft, error) {
	var firstErr error
	for index := 0; index < len(text); index++ {
		if text[index] != '{' {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(text[index:]))
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			continue
		}
		draft, err := schemas.DecodeBuildDraft(raw)
		if err == nil {
			return draft, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		index += int(decoder.InputOffset()) - 1
	}
	if firstErr != nil {
		return schemas.BuildDraft{}, firstErr
	}
	return schemas.BuildDraft{}, fmt.Errorf("输出中没有 JSON 对象")
}

func validateMembershipCategories(bundle CandidateBundle, selection schemas.BuildSelection, categories []schemas.Category) ([]schemas.Category, error) {
	allowed := make(map[schemas.Category]map[string]bool, len(bundle.Groups))
	for _, group := range bundle.Groups {
		allowed[group.Category] = map[string]bool{}
		for _, candidate := range group.Candidates {
			allowed[group.Category][candidate.SKU] = true
		}
	}
	var invalid []schemas.Category
	for _, category := range categories {
		for _, sku := range selectionSKUs(selection, category) {
			if !allowed[category][sku] {
				invalid = append(invalid, category)
				break
			}
		}
	}
	if len(invalid) > 0 {
		return invalid, fmt.Errorf("selection 包含 Candidate Bundle 之外的品类:%s", joinCategories(invalid))
	}
	return nil, nil
}

func validateChangeConstraints(input BuildInput, current schemas.BuildSelection) error {
	if input.BaseSelection == nil {
		return nil
	}
	for _, category := range input.Locked {
		if !sameCategorySelection(*input.BaseSelection, current, category) {
			return fmt.Errorf("改单违反硬锁定品类:%s", category)
		}
	}
	if input.Change != nil && input.Change.Intent == schemas.IntentAdjustBudget {
		changed := changedCategories(input.BaseSelection, current)
		if len(changed) > 2 {
			return fmt.Errorf("预算改单改动了 %d 个品类，超过最多两类限制", len(changed))
		}
	}
	return nil
}

func inBudgetWindow(spec schemas.RequirementSpec, quote validate.Quote) bool {
	quote = validate.BudgetQuote(spec, quote)
	if quote.MissingCount > 0 {
		return true
	}
	total, ok := parsePriceFen(&quote.TotalCNY)
	if !ok {
		return false
	}
	budget := int64(spec.BudgetCNY) * 100
	flex := int64(math.Round(float64(budget) * spec.BudgetFlex))
	return total >= budget-flex && total <= budget+flex
}

func hasUnknown(report schemas.ValidationReport) bool {
	for _, check := range report.Checks {
		if check.Outcome == schemas.OutcomeUnknown {
			return true
		}
	}
	return false
}

func problematicRuleIDs(report schemas.ValidationReport) []string {
	var out []string
	for _, check := range report.Checks {
		if check.Outcome == schemas.OutcomeUnknown || check.Outcome == schemas.OutcomeFail {
			out = append(out, string(check.RuleID))
		}
	}
	return out
}

func finalFailureMessage(result validate.Result, budgetOK bool) string {
	if !budgetOK {
		return fmt.Sprintf("三次选配后总价 ¥%s 仍未进入预算弹性区间，本轮不保存版本。", result.Quote.TotalCNY)
	}
	var rules []string
	for _, check := range result.Report.Checks {
		if check.Outcome == schemas.OutcomeFail && check.Severity == schemas.SeverityError {
			rules = append(rules, string(check.RuleID))
		}
	}
	return "三次选配后兼容性仍未通过，本轮不保存版本。失败规则:" + strings.Join(rules, ",")
}

func canonicalSelection(selection schemas.BuildSelection) string {
	return string(rawJSON(selectionMap(selection)))
}

func changedCategories(base *schemas.BuildSelection, current schemas.BuildSelection) []schemas.Category {
	if base == nil {
		return nil
	}
	var out []schemas.Category
	for _, category := range schemas.AllCategories {
		if !sameCategorySelection(*base, current, category) {
			out = append(out, category)
		}
	}
	return out
}

func sameCategorySelection(base, current schemas.BuildSelection, category schemas.Category) bool {
	left, right := selectionSKUs(base, category), selectionSKUs(current, category)
	sort.Strings(left)
	sort.Strings(right)
	if category == schemas.CategorySSD {
		leftSSD := append([]schemas.SSDSelection(nil), base.SSDs...)
		rightSSD := append([]schemas.SSDSelection(nil), current.SSDs...)
		sort.Slice(leftSSD, func(i, j int) bool { return leftSSD[i].SKU < leftSSD[j].SKU })
		sort.Slice(rightSSD, func(i, j int) bool { return rightSSD[i].SKU < rightSSD[j].SKU })
		return reflect.DeepEqual(leftSSD, rightSSD)
	}
	return reflect.DeepEqual(left, right)
}

func unlockedCategories(locked []schemas.Category) []schemas.Category {
	return withoutLocked(schemas.AllCategories, locked)
}

func withoutLocked(categories, locked []schemas.Category) []schemas.Category {
	set := categorySet(locked)
	out := make([]schemas.Category, 0, len(categories))
	seen := map[schemas.Category]bool{}
	for _, category := range categories {
		if !set[category] && !seen[category] {
			seen[category] = true
			out = append(out, category)
		}
	}
	return out
}

func joinCategories(categories []schemas.Category) string {
	parts := make([]string, len(categories))
	for i, category := range categories {
		parts[i] = string(category)
	}
	return strings.Join(parts, ",")
}

func (h *runner) record(input BuildInput, name string, fields map[string]any) {
	if input.TraceID != "" {
		fields["session_fingerprint"] = input.TraceID
	}
	h.trace.Record(HarnessEvent{Name: name, Fields: fields})
}

func (h *runner) recordRepair(input BuildInput, attempt int, plan RepairPlan) {
	mutable := make([]string, len(plan.Mutable))
	for index, category := range plan.Mutable {
		mutable[index] = string(category)
	}
	h.record(input, "repair.planned", map[string]any{"attempt": attempt, "reason": plan.Reason, "mutable": mutable})
}

func (h *runner) finish(input BuildInput, started time.Time, attempts int, succeeded bool) {
	name := "harness.failed"
	if succeeded {
		name = "harness.completed"
	}
	h.record(input, name, map[string]any{
		"attempts": attempts, "duration_ms": time.Since(started).Milliseconds(), "succeeded": succeeded,
	})
}
