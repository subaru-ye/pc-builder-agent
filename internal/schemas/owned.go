package schemas

import (
	"fmt"
	"strings"
)

// OwnedPart 保存用户提供的准确型号，不接受模型生成的内部 SKU。
type OwnedPart struct {
	Category Category `json:"category"`
	Model    string   `json:"model"`
	Quantity int      `json:"quantity,omitempty"`
}

func validateOwned(spec *RequirementSpec) error {
	if spec.BudgetBasis != "" && spec.BudgetBasis != "new_purchase" && spec.BudgetBasis != "full_build" {
		return fmt.Errorf("budget_basis 仅允许 new_purchase 或 full_build")
	}
	seen := map[Category]bool{}
	for i := range spec.OwnedParts {
		part := &spec.OwnedParts[i]
		if !isValidCategory(part.Category) || seen[part.Category] || strings.TrimSpace(part.Model) == "" {
			return fmt.Errorf("owned_parts 必须为不重复品类和非空准确型号")
		}
		seen[part.Category] = true
		if part.Quantity == 0 {
			part.Quantity = 1
		}
		if part.Quantity < 1 || part.Quantity > 8 || (part.Category != CategorySSD && part.Quantity != 1) {
			return fmt.Errorf("owned_parts 数量必须为 1，仅 SSD 支持 1–8 件同款")
		}
	}
	return nil
}

// MissingOwnedFields 不改变旧 JSON 的可解码性，在执行前明确需要补充的信息。
func MissingOwnedFields(spec RequirementSpec) []string {
	var fields []string
	seen := map[Category]bool{}
	for _, p := range spec.OwnedParts {
		if strings.TrimSpace(p.Model) != "" {
			seen[p.Category] = true
		}
	}
	for _, c := range spec.ExistingParts {
		if !seen[c] {
			fields = append(fields, "owned_parts."+string(c)+".model")
			seen[c] = true
		}
	}
	if (len(spec.ExistingParts) > 0 || len(spec.OwnedParts) > 0) && spec.BudgetBasis == "" {
		fields = append(fields, "budget_basis")
	}
	return fields
}
