package buildharness

import (
	"fmt"
	"math"
	"sort"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// DeterministicRepairPlanner 按固定规则顺序选择最小可变品类。
type DeterministicRepairPlanner struct{}

func NewRepairPlanner() *DeterministicRepairPlanner { return &DeterministicRepairPlanner{} }

var ruleRepairOrder = map[schemas.RuleID][]schemas.Category{
	schemas.RuleSocketMatch:           {schemas.CategoryMotherboard, schemas.CategoryCPU},
	schemas.RuleChipsetSupport:        {schemas.CategoryMotherboard, schemas.CategoryCPU},
	schemas.RuleMemoryGeneration:      {schemas.CategoryMemory, schemas.CategoryMotherboard},
	schemas.RuleMemorySpeed:           {schemas.CategoryMemory, schemas.CategoryMotherboard},
	schemas.RuleGPUClearance:          {schemas.CategoryCase, schemas.CategoryGPU},
	schemas.RuleCoolerClearance:       {schemas.CategoryCooler, schemas.CategoryCase},
	schemas.RulePSUHeadroom:           {schemas.CategoryPSU, schemas.CategoryGPU, schemas.CategoryCPU},
	schemas.RuleFormFactorSupport:     {schemas.CategoryCase, schemas.CategoryMotherboard},
	schemas.RuleM2SlotCapacity:        {schemas.CategoryMotherboard, schemas.CategorySSD},
	schemas.RuleGPUPowerConnectors:    {schemas.CategoryPSU, schemas.CategoryGPU},
	schemas.RuleDisplayOutput:         {schemas.CategoryGPU, schemas.CategoryCPU},
	schemas.RuleCoolerThermalCapacity: {schemas.CategoryCooler, schemas.CategoryCPU},
}

func (p *DeterministicRepairPlanner) Plan(result validate.Result, draft schemas.BuildDraft, constraints RepairConstraints) (RepairPlan, error) {
	locked := categorySet(constraints.Locked)
	mutable := make([]schemas.Category, 0, 4)
	seen := map[schemas.Category]bool{}
	for _, check := range result.Report.Checks {
		needsRepair := check.Outcome == schemas.OutcomeUnknown ||
			(check.Outcome == schemas.OutcomeFail && check.Severity == schemas.SeverityError)
		if !needsRepair {
			continue
		}
		chosen := schemas.Category("")
		for _, category := range ruleRepairOrder[check.RuleID] {
			if !locked[category] && bundleHasAlternative(constraints.Bundle, category, draft.Selection) {
				chosen = category
				break
			}
		}
		if chosen == "" {
			return RepairPlan{}, fmt.Errorf("规则 %s 没有未锁定且可替换的品类", check.RuleID)
		}
		if !seen[chosen] {
			seen[chosen] = true
			mutable = append(mutable, chosen)
		}
	}
	hadValidationRepair := len(mutable) > 0
	budget, direction := budgetDirection(constraints.Requirement, result.Quote)
	if direction == "" {
		if len(mutable) > 0 {
			return RepairPlan{Mutable: mutable, Reason: "validation_rules"}, nil
		}
		return RepairPlan{}, fmt.Errorf("校验结果不需要定向修复")
	}
	budgetCategories := rankBudgetCategories(result.Quote, constraints.Bundle, locked, direction)
	if len(budgetCategories) == 0 {
		return RepairPlan{}, fmt.Errorf("预算%s但候选包中没有可用的定向替代项(差额 %d 分)", direction, budget)
	}
	for _, category := range budgetCategories {
		if !seen[category] {
			seen[category] = true
			mutable = append(mutable, category)
		}
	}
	reason := "budget_" + direction
	if hadValidationRepair {
		reason = "validation_rules+" + reason
	}
	plan := RepairPlan{Mutable: mutable, Reason: reason}
	return plan, nil
}

// rankBudgetCategories 最多返回两个对预算最有效的未锁定品类。它与规则修复
// 同轮合并，避免先耗尽规则修复轮次、最后才发现总价仍超出窗口。
func rankBudgetCategories(quote validate.Quote, bundle CandidateBundle, locked map[schemas.Category]bool, direction string) []schemas.Category {
	type ranked struct {
		category schemas.Category
		score    int64
	}
	var ranks []ranked
	current := quoteByCategory(quote)
	for _, category := range schemas.AllCategories {
		if locked[category] {
			continue
		}
		minimum, maximum, ok := candidatePriceRange(bundle, category)
		if !ok {
			continue
		}
		value := current[category]
		if direction == "over" && minimum < value {
			ranks = append(ranks, ranked{category: category, score: value})
		}
		if direction == "under" && maximum > value {
			ranks = append(ranks, ranked{category: category, score: maximum - value})
		}
	}
	sort.SliceStable(ranks, func(i, j int) bool {
		if ranks[i].score == ranks[j].score {
			return categoryIndex(ranks[i].category) < categoryIndex(ranks[j].category)
		}
		return ranks[i].score > ranks[j].score
	})
	mutable := make([]schemas.Category, 0, 2)
	for i := 0; i < len(ranks) && i < 2; i++ {
		mutable = append(mutable, ranks[i].category)
	}
	return mutable
}

func budgetDirection(spec schemas.RequirementSpec, quote validate.Quote) (int64, string) {
	if quote.MissingCount > 0 {
		return 0, ""
	}
	total, ok := parsePriceFen(&quote.TotalCNY)
	if !ok {
		return 0, ""
	}
	budget := int64(spec.BudgetCNY) * 100
	flex := int64(math.Round(float64(budget) * spec.BudgetFlex))
	lower, upper := budget-flex, budget+flex
	if total > upper {
		return total - upper, "over"
	}
	if total < lower {
		return lower - total, "under"
	}
	return 0, ""
}

func quoteByCategory(quote validate.Quote) map[schemas.Category]int64 {
	out := make(map[schemas.Category]int64, len(schemas.AllCategories))
	for _, line := range quote.Lines {
		if value, ok := parsePriceFen(line.SubtotalCNY); ok {
			out[line.Category] += value
		}
	}
	return out
}

func candidatePriceRange(bundle CandidateBundle, category schemas.Category) (int64, int64, bool) {
	group := bundleGroup(&bundle, category)
	if group == nil {
		return 0, 0, false
	}
	var minValue, maxValue int64
	found := false
	for _, candidate := range group.Candidates {
		value, ok := parsePriceFen(candidate.PriceCNY)
		if !ok {
			continue
		}
		if !found || value < minValue {
			minValue = value
		}
		if !found || value > maxValue {
			maxValue = value
		}
		found = true
	}
	return minValue, maxValue, found
}

func bundleHasAlternative(bundle CandidateBundle, category schemas.Category, selection schemas.BuildSelection) bool {
	group := bundleGroup(&bundle, category)
	if group == nil {
		return false
	}
	current := map[string]bool{}
	for _, sku := range selectionSKUs(selection, category) {
		current[sku] = true
	}
	for _, candidate := range group.Candidates {
		if !current[candidate.SKU] {
			return true
		}
	}
	return false
}

func categoryIndex(category schemas.Category) int {
	for index, item := range schemas.AllCategories {
		if category == item {
			return index
		}
	}
	return len(schemas.AllCategories)
}
