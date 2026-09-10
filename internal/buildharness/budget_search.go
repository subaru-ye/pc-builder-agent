package buildharness

import (
	"context"
	"math"
	"sort"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// budgetRepair 先保留已有的定向修复,无完整可行解时扩大到未锁定品类组合。
// 使用真实报价预筛,最多改变三类、额外验证 512 组;调预算改单仍遵守相对基线最多两类。
// 这是有界搜索,找不到方案不代表完整目录无解,也不会放宽预算或兼容性。
func (h *runner) budgetRepair(ctx context.Context, input BuildInput, current schemas.BuildSelection,
	quote validate.Quote, bundle CandidateBundle, initial RepairPlan) (RepairPlan, int, error) {
	preferred, checked, err := h.feasibleBudgetPreference(ctx, input.Requirement, current, bundle, initial.Mutable)
	if err != nil {
		return initial, checked, err
	}
	if len(preferred) > 0 {
		selection := current
		for category, sku := range preferred {
			setSelectionSKU(&selection, category, sku)
		}
		if validateChangeConstraints(input, selection) == nil {
			result, err := h.eval.Evaluate(ctx, selection)
			if err != nil {
				return initial, checked, err
			}
			checked++
			if result.Report.OverallStatus == schemas.OverallPass && inBudgetWindow(input.Requirement, result.Quote) {
				initial.PreferredSelection = preferred
				return initial, checked, nil
			}
		}
	}
	budgetQuote := validate.BudgetQuote(input.Requirement, quote)
	total, ok := parsePriceFen(&budgetQuote.TotalCNY)
	if !ok || budgetQuote.MissingCount > 0 {
		return initial, checked, nil
	}
	currentPrices := quoteByCategory(quote)
	locked := categorySet(input.Locked)
	type option struct {
		category schemas.Category
		sku      string
		quantity int
		price    int64
	}
	var options [][]option
	for _, group := range bundle.Groups {
		if locked[group.Category] {
			continue
		}
		var choices []option
		for _, candidate := range group.Candidates {
			price, ok := parsePriceFen(candidate.PriceCNY)
			if !ok {
				continue
			}
			quantities := []int{1}
			if group.Category == schemas.CategorySSD {
				quantities = append(quantities, 2)
			}
			for _, quantity := range quantities {
				selection := current
				setSelectionSKU(&selection, group.Category, candidate.SKU)
				if group.Category == schemas.CategorySSD {
					selection.SSDs[0].Quantity = quantity
				}
				if sameCategorySelection(current, selection, group.Category) {
					continue
				}
				choices = append(choices, option{group.Category, candidate.SKU, quantity, price * int64(quantity)})
			}
		}
		if group.Category == schemas.CategoryGPU && input.Requirement.UseCase.Type != schemas.UseCaseGaming && !mandatoryGPU(input.Requirement) && current.GPU != nil {
			choices = append(choices, option{category: group.Category})
		}
		if len(choices) > 0 {
			options = append(options, choices)
		}
	}
	target := int64(input.Requirement.BudgetCNY) * 100
	flex := int64(math.Round(float64(target) * input.Requirement.BudgetFlex))
	type proposal struct {
		selection  schemas.BuildSelection
		categories []schemas.Category
		distance   int64
		key        string
	}
	var proposals []proposal
	var visit func(int, schemas.BuildSelection, []schemas.Category, int64) error
	visit = func(start int, selection schemas.BuildSelection, changed []schemas.Category, price int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(changed) > 0 && price >= target-flex && price <= target+flex && validateChangeConstraints(input, selection) == nil {
			distance := price - target
			if distance < 0 {
				distance = -distance
			}
			proposals = append(proposals, proposal{selection, append([]schemas.Category(nil), changed...), distance, canonicalSelection(selection)})
		}
		if len(changed) == 3 {
			return nil
		}
		for i := start; i < len(options); i++ {
			for _, choice := range options[i] {
				next := selection
				if choice.category == schemas.CategoryGPU && choice.sku == "" {
					next.GPU = nil
				} else {
					setSelectionSKU(&next, choice.category, choice.sku)
				}
				if choice.category == schemas.CategorySSD {
					next.SSDs[0].Quantity = choice.quantity
				}
				if err := visit(i+1, next, append(changed, choice.category), price-currentPrices[choice.category]+choice.price); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(0, current, nil, total); err != nil {
		return initial, checked, err
	}
	sort.Slice(proposals, func(i, j int) bool {
		a, b := proposals[i], proposals[j]
		if len(a.categories) != len(b.categories) {
			return len(a.categories) < len(b.categories)
		}
		if a.distance != b.distance {
			return a.distance < b.distance
		}
		return a.key < b.key
	})
	for i, p := range proposals {
		if i >= 512 {
			break
		}
		if err := ctx.Err(); err != nil {
			return initial, checked, err
		}
		checked++
		result, err := h.eval.Evaluate(ctx, p.selection)
		if err != nil {
			return initial, checked, err
		}
		if result.Report.OverallStatus != schemas.OverallPass || result.Quote.MissingCount > 0 || !inBudgetWindow(input.Requirement, result.Quote) {
			continue
		}
		plan := RepairPlan{Mutable: p.categories, Reason: initial.Reason + "+bounded_search", PreferredSelection: map[schemas.Category]string{}}
		for _, category := range p.categories {
			if category == schemas.CategorySSD {
				plan.PreferredSSDs = p.selection.SSDs
				continue
			}
			if category == schemas.CategoryGPU && p.selection.GPU == nil {
				plan.DropGPU = true
				continue
			}
			plan.PreferredSelection[category] = selectionSKUs(p.selection, category)[0]
		}
		return plan, checked, nil
	}
	return initial, checked, nil
}
