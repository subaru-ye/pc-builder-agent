package validate

import "github.com/subaru-ye/pc-builder-agent/internal/schemas"

// WithOwnership 派生采购合计；原始整机报价保留。调用方先锁定已核验型号。
func WithOwnership(q Quote, spec schemas.RequirementSpec) Quote {
	if len(spec.OwnedParts) == 0 {
		return q
	}
	owned := map[schemas.Category]bool{}
	for _, part := range spec.OwnedParts {
		owned[part.Category] = true
	}
	q.Lines = append([]QuoteLine(nil), q.Lines...)
	var total int64
	missing := map[string]bool{}
	for i := range q.Lines {
		line := &q.Lines[i]
		line.Owned = owned[line.Category]
		if line.Owned {
			continue
		}
		if line.SubtotalCNY == nil {
			missing[line.SKU] = true
			continue
		}
		value, err := parseCents(*line.SubtotalCNY)
		if err != nil {
			missing[line.SKU] = true
			continue
		}
		total += value
	}
	s := formatCents(total)
	q.PurchaseTotalCNY = &s
	q.PurchaseMissingCount = len(missing)
	return q
}

// BudgetQuote 投影预算口径；不改动对外展示的完整报价。
func BudgetQuote(spec schemas.RequirementSpec, q Quote) Quote {
	q = WithOwnership(q, spec)
	if spec.BudgetBasis == "new_purchase" && q.PurchaseTotalCNY != nil {
		q.TotalCNY = *q.PurchaseTotalCNY
		q.MissingCount = q.PurchaseMissingCount
	}
	return q
}
