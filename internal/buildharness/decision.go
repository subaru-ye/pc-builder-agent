package buildharness

import (
	"fmt"
	"math"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// Decision 是未交付的业务结果。接口错误不转换为 Decision。
type Decision struct {
	Kind          string   `json:"kind"`
	Reason        string   `json:"reason"`
	Fields        []string `json:"fields,omitempty"`
	Scope         string   `json:"scope"`
	SnapshotDate  string   `json:"snapshot_date,omitempty"`
	LowerBoundCNY string   `json:"lower_bound_cny,omitempty"`
	Message       string   `json:"message"`
}

func (d *Decision) Error() string { return d.Message }

func clarification(spec schemas.RequirementSpec) *Decision {
	fields := schemas.MissingOwnedFields(spec)
	if len(fields) == 0 {
		return nil
	}
	return &Decision{Kind: "clarify", Reason: "missing_owned_information", Fields: fields, Scope: "requirement",
		Message: "请补充已有配件的完整型号和数量，并确认预算是仅用于新增购买，还是包含已有件的整机参考总价？缺失项：" + strings.Join(fields, "、")}
}

// bindOwned 只接受准确匹配；副本内增加锁定，不改写调用者的基版本。
func bindOwned(input BuildInput, candidates []store.Candidate) (BuildInput, *Decision) {
	base := schemas.BuildSelection{}
	if input.BaseSelection != nil {
		base = *input.BaseSelection
		base.SSDs = append([]schemas.SSDSelection(nil), base.SSDs...)
	}
	input.Locked = append([]schemas.Category(nil), input.Locked...)
	locked := categorySet(input.Locked)
	for _, part := range input.Requirement.OwnedParts {
		var matches []store.Candidate
		for _, c := range candidates {
			if c.Category == part.Category && (strings.EqualFold(strings.TrimSpace(part.Model), c.Model) || strings.EqualFold(strings.TrimSpace(part.Model), c.Brand+" "+c.Model)) {
				matches = append(matches, c)
			}
		}
		if len(matches) != 1 {
			return input, &Decision{Kind: "data_unavailable", Reason: "owned_model_unresolved", Fields: []string{"owned_parts." + string(part.Category) + ".model"}, Scope: "current_catalog", Message: "当前目录无法唯一核验已有配件型号：" + part.Model + "。请补充准确型号或规格资料；本轮不会换成相似零件。"}
		}
		next := base
		setSelectionSKU(&next, part.Category, matches[0].SKU)
		if part.Category == schemas.CategorySSD {
			next.SSDs[0].Quantity = part.Quantity
			if part.Quantity == 0 {
				next.SSDs[0].Quantity = 1
			}
		}
		if locked[part.Category] && !sameCategorySelection(base, next, part.Category) {
			return input, &Decision{Kind: "clarify", Reason: "owned_lock_conflict", Fields: []string{"owned_parts." + string(part.Category) + ".model"}, Scope: "requirement", Message: "已有配件型号与锁定配置不同，请确认需要保留哪一件？"}
		}
		base = next
		if !locked[part.Category] {
			input.Locked = append(input.Locked, part.Category)
			locked[part.Category] = true
		}
	}
	if len(input.Requirement.OwnedParts) > 0 {
		input.BaseSelection = &base
	}
	return input, nil
}

// AssessCatalog 在完整目录上检查输入和乐观下界，不使用裁剪后的模型候选。
// 任一必要品类存在未知价格时放弃预算证明，不据此断言整个市场无解。
func AssessCatalog(input BuildInput, catalog store.CatalogSnapshot) *Decision {
	if d := clarification(input.Requirement); d != nil {
		return d
	}
	boundInput, d := bindOwned(input, catalog.Candidates)
	if d != nil {
		d.SnapshotDate = catalog.Snapshot.SnapshotDate.Format("2006-01-02")
		return d
	}
	date := catalog.Snapshot.SnapshotDate.Format("2006-01-02")
	owned := map[schemas.Category]bool{}
	for _, p := range input.Requirement.OwnedParts {
		owned[p.Category] = true
	}
	var total int64
	unknown := false
	for _, category := range schemas.AllCategories {
		if category == schemas.CategoryGPU && input.Requirement.UseCase.Type != schemas.UseCaseGaming && !owned[category] {
			continue
		}
		count := 0
		minimum := int64(math.MaxInt64)
		for _, candidate := range catalog.Candidates {
			if candidate.Category != category {
				continue
			}
			count++
			if input.Requirement.BudgetBasis == "new_purchase" && owned[category] {
				minimum = 0
				continue
			}
			value, ok := parsePriceFen(candidate.PriceCNY)
			if !ok || value < 0 {
				unknown = true
				continue
			}
			if value < minimum {
				minimum = value
			}
		}
		if count == 0 {
			return &Decision{Kind: "data_unavailable", Reason: "catalog_category_missing", Fields: []string{string(category)}, Scope: "current_catalog", SnapshotDate: date, Message: fmt.Sprintf("当前目录缺少 %s 候选，无法核验完整配置；这不代表市场上没有该配件。", category)}
		}
		if minimum != math.MaxInt64 {
			total += minimum
		}
	}
	_ = boundInput // 匹配在预算证明之前完成，避免未知已有件被算成免费合格件。
	budget := int64(input.Requirement.BudgetCNY) * 100
	upper := budget + int64(math.Round(float64(budget)*input.Requirement.BudgetFlex))
	if !unknown && total > upper {
		return &Decision{Kind: "catalog_infeasible", Reason: "budget_lower_bound", Scope: "current_catalog", SnapshotDate: date, LowerBoundCNY: fmt.Sprintf("%d.%02d", total/100, total%100), Message: fmt.Sprintf("按当前目录及 %s 价格快照，各必需品类独立最低价合计至少 ¥%d.%02d，已超过预算上限 ¥%d.%02d。当前目录无法满足该预算；请调整预算或补充可核验的商品资料。这不是整个市场无解的结论。", date, total/100, total%100, upper/100, upper%100)}
	}
	return nil
}
