package planning

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func matchesOwnedPart(c Candidate, p schemas.OwnedPart) bool {
	return p.Model != "" && c.Category == p.Category &&
		(strings.EqualFold(p.Model, c.Model) || strings.EqualFold(p.Model, c.Brand+" "+c.Model))
}

// Accounting uses only stated values, never a default budget or spend floor.
func (x *execution) accountingSpec() schemas.RequirementSpec {
	var spec schemas.RequirementSpec
	read := func(field string, dst any) {
		if f := x.input.State.Fields[field]; f.Status == "active" {
			_ = json.Unmarshal(f.Value, dst)
		}
	}
	read("budget_cny", &spec.BudgetCNY)
	read("budget_flex", &spec.BudgetFlex)
	read("budget_basis", &spec.BudgetBasis)
	read("owned_parts", &spec.OwnedParts)
	return spec
}

func (x *execution) deliveryIssues() []string {
	issues := []string{}
	if x.result.Validation == nil {
		return []string{"候选尚未完成兼容性核验"}
	}
	for _, c := range x.result.Validation.Checks {
		if c.Outcome != schemas.OutcomePass {
			detail := c.Detail
			if detail == "" {
				detail = "部分规格待核验"
			}
			issues = append(issues, detail)
		}
	}
	if x.result.Quote == nil {
		return append(issues, "报价尚未核验")
	}
	spec := x.accountingSpec()
	quote := validate.BudgetQuote(spec, *x.result.Quote)
	// Ownership has already been matched by exact model and quantity at evaluate.
	quote = *x.result.Quote
	if spec.BudgetBasis == "new_purchase" && quote.PurchaseTotalCNY != nil {
		quote.TotalCNY, quote.MissingCount = *quote.PurchaseTotalCNY, quote.PurchaseMissingCount
	}
	if quote.MissingCount > 0 {
		issues = append(issues, "部分配件价格未知，合计尚不完整")
	}
	if spec.BudgetCNY > 0 && x.input.State.Fields["budget_cny"].Strength == "must" {
		total, ok := new(big.Rat).SetString(quote.TotalCNY)
		upper := new(big.Rat).SetInt64(int64(spec.BudgetCNY))
		flex, _ := new(big.Rat).SetString(fmt.Sprint(spec.BudgetFlex))
		if flex != nil {
			upper.Mul(upper, new(big.Rat).Add(big.NewRat(1, 1), flex))
		}
		if ok && total.Cmp(upper) > 0 {
			issues = append(issues, "候选价格超过已表达的预算范围，需继续调整或讨论取舍")
		}
	}
	// Extra attributes with missing evidence remain visible on the candidate.
	// Only actual compatibility/price gaps and unresolved user conditions affect
	// delivery; an unrelated unknown attribute must not become a new hard gate.
	for _, p := range spec.OwnedParts {
		matched := false
		for _, c := range x.result.Candidates {
			if matchesOwnedPart(c, p) {
				matched = true
			}
		}
		if !matched {
			issues = append(issues, "已有配件尚未对应到候选中的准确型号："+p.Model)
		}
	}
	return issues
}

func (x *execution) verifiedOwnership(draft schemas.BuildDraft) schemas.RequirementSpec {
	spec := x.accountingSpec()
	owned := spec.OwnedParts
	spec.OwnedParts = nil
	for _, p := range owned {
		matched, total := false, 0
		for _, id := range draft.Selection.SKUs() {
			for _, c := range x.candidates {
				if c.ID != id || c.Category != p.Category {
					continue
				}
				total++
				matched = matchesOwnedPart(c, p)
			}
		}
		quantity := max(1, p.Quantity)
		if p.Category == schemas.CategorySSD {
			for _, s := range draft.Selection.SSDs {
				if s.Quantity != quantity {
					matched = false
				}
			}
		}
		if matched && total == 1 {
			spec.OwnedParts = append(spec.OwnedParts, p)
		}
	}
	return spec
}
