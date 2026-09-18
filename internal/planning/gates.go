package planning

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// deliveryGateCounters bounds how often the server loops the model back on a
// final delivery decision; every gate fires only while repair turns remain.
type deliveryGateCounters struct {
	budget, unknown, hardreq, total int
}

// budgetCeiling 返回 must 预算硬上限（含用户明确表达的弹性）。未表达 must 预算时为 false；
// 会计只使用陈述值，不引入默认预算或弹性。
func (x *execution) budgetCeiling() (*big.Rat, bool) {
	field, ok := x.input.State.Fields["budget_cny"]
	if !ok || field.Status != "active" || field.Strength != "must" {
		return nil, false
	}
	spec := x.accountingSpec()
	if spec.BudgetCNY <= 0 {
		return nil, false
	}
	upper := new(big.Rat).SetInt64(int64(spec.BudgetCNY))
	flex, _ := new(big.Rat).SetString(fmt.Sprint(spec.BudgetFlex))
	if flex != nil && flex.Sign() > 0 {
		upper.Mul(upper, new(big.Rat).Add(big.NewRat(1, 1), flex))
	}
	return upper, true
}

// budgetGateFeedback 在模型把"存在严格更便宜同品类候选"的超预算交付为 clarify/proposal 时
// 返回回环反馈；没有更便宜候选时返回空串——那才是合法的用户取舍。
func (x *execution) budgetGateFeedback(outcome string) string {
	if outcome != "clarify" && outcome != "proposal" {
		return ""
	}
	if x.result.Validation == nil || x.result.Quote == nil {
		return ""
	}
	draft, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil {
		return ""
	}
	upper, ok := x.budgetCeiling()
	if !ok {
		return ""
	}
	quote := *x.result.Quote
	spec := x.accountingSpec()
	if spec.BudgetBasis == "new_purchase" && quote.PurchaseTotalCNY != nil {
		quote.TotalCNY, quote.MissingCount = *quote.PurchaseTotalCNY, quote.PurchaseMissingCount
	}
	total, ok := new(big.Rat).SetString(quote.TotalCNY)
	if !ok || total.Cmp(upper) <= 0 {
		return ""
	}
	alts := x.budgetAlternatives(quote, draft)
	if alts == nil {
		return ""
	}
	selected := map[schemas.Category]*big.Rat{}
	for _, line := range quote.Lines {
		if line.UnitPriceCNY == nil {
			continue
		}
		p, ok := new(big.Rat).SetString(*line.UnitPriceCNY)
		if !ok {
			continue
		}
		if cur, seen := selected[line.Category]; !seen || p.Cmp(cur) < 0 {
			selected[line.Category] = p
		}
	}
	listed, _ := alts["candidates"].(map[string]any)
	filtered := map[string]any{}
	for cat, rows := range listed {
		sel, ok := selected[schemas.Category(cat)]
		if !ok {
			continue
		}
		for _, c := range rows.([]Candidate) {
			if p, ok := new(big.Rat).SetString(*c.Price); ok && p.Cmp(sel) < 0 {
				filtered[cat] = rows
				break
			}
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{
		"ceiling_cny": upper.FloatString(2), "total_cny": total.FloatString(2),
		"budget_alternatives": map[string]any{"candidates": filtered},
	})
	return "预算自纠反馈：" + string(payload) + "\n当前总价超出 must 预算硬上限，且上述品类存在严格更便宜的有报价候选；这属于你可以自行决定的调整，不得停在 clarify 等待用户取舍，也不得带着未处理的超预算直接交付 proposal。请本轮直接替换选定候选（同品类、容量或性能档位不降）并重新输出 draft 触发 evaluate；只有每个品类都比较过更便宜候选后仍无法压回预算时，才允许 clarify 并说明必须牺牲的具体用户硬性要求。"
}

// unknownAlternatives 对校验 unknown 的缺失字段列出同品类字段完整且有报价的未选中候选
// （每字段 ≤3，按价格升序）；没有任何替代时返回 nil。
func (x *execution) unknownAlternatives(report *schemas.ValidationReport, draft schemas.BuildDraft) map[string]any {
	if report == nil {
		return nil
	}
	selected := map[string]bool{}
	for _, sku := range draft.Selection.SKUs() {
		selected[sku] = true
	}
	valid := map[schemas.Category]bool{}
	for _, c := range schemas.AllCategories {
		valid[c] = true
	}
	out := map[string]any{}
	count := 0
scan:
	for _, check := range report.Checks {
		if check.Outcome != schemas.OutcomeUnknown {
			continue
		}
		for _, missing := range check.MissingFields {
			dot := strings.Index(missing, ".")
			if dot <= 0 || count >= 4 {
				break scan
			}
			category, key := schemas.Category(missing[:dot]), missing[dot+1:]
			if !valid[category] {
				continue
			}
			if _, seen := out[missing]; seen {
				continue
			}
			rows := []Candidate{}
			for _, c := range x.candidates {
				if c.Category != category || c.Price == nil || selected[c.ID] {
					continue
				}
				if p, ok := new(big.Rat).SetString(*c.Price); !ok || p.Sign() <= 0 {
					continue
				}
				specs := map[string]json.RawMessage{}
				if json.Unmarshal(c.Specs, &specs) != nil {
					continue
				}
				if v := specs[key]; len(v) == 0 || string(v) == "null" {
					continue
				}
				rows = append(rows, c)
			}
			if len(rows) == 0 {
				continue
			}
			sort.SliceStable(rows, func(i, j int) bool {
				a, _ := new(big.Rat).SetString(*rows[i].Price)
				b, _ := new(big.Rat).SetString(*rows[j].Price)
				return a.Cmp(b) < 0
			})
			if len(rows) > 3 {
				rows = rows[:3]
			}
			out[missing] = rows
			count++
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// unknownGateFeedback 当模型仍交付含 unknown 的最终结果且目录存在字段完整替代时返回反馈。
func (x *execution) unknownGateFeedback(outcome string, clarifiesEvaluatedDraft bool) string {
	if outcome != "proposal" && !clarifiesEvaluatedDraft {
		return ""
	}
	if x.result.Validation == nil || x.result.Quote == nil {
		return ""
	}
	draft, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil {
		return ""
	}
	alts := x.unknownAlternatives(x.result.Validation, draft)
	if alts == nil {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{"fields": alts})
	return "unknown替代反馈：" + string(payload) + "\n这些校验未知项在目录中存在字段完整且有报价的同品类候选，不能当作市场无解：请改选字段完整的候选重新输出 draft（会自动再次 evaluate），不得以 unknown proposal 收尾或停在 clarify 等待用户；确实全部候选都缺该字段时才如实说明并交付。"
}

// hardRequirementGateFeedback 当 clarify 的自身 assessments 中存在未满足的 must 字段时返回反馈；
// 平台联动等可检索替代必须先核验，不得直接作为用户取舍交付。
func (x *execution) hardRequirementGateFeedback() string {
	if x.result.Outcome != "clarify" || x.result.Validation == nil {
		return ""
	}
	assessed := map[string]Assessment{}
	for _, a := range x.result.Assessments {
		assessed[a.Field] = a
	}
	unresolved := []map[string]string{}
	for field, v := range x.input.State.Fields {
		if v.Status != "active" || v.Strength != "must" || v.Kind == "fact" || v.Kind == "context" {
			continue
		}
		if field == "budget_cny" || field == "budget_basis" || field == "budget_flex" {
			continue
		}
		a := assessed[field]
		if a.Status == "met" {
			continue
		}
		unresolved = append(unresolved, map[string]string{
			"field": field, "label": schemas.RequirementFieldLabel(field),
			"status": a.Status, "explanation": a.Explanation,
		})
	}
	if len(unresolved) == 0 {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{"must_unresolved": unresolved})
	return "must交付核验：" + string(payload) + "\n上述 must 未满足且尚未证明目录无法满足；先检索可行替代（含平台联动：连带换CPU/主板等），能交付标注偏差与联动原因的 proposal 就交付；只有确认无候选可满足，或调整必须牺牲用户明确表达的硬性条件时才允许 clarify 并说明具体取舍。若检索额度已用完，直接交付标注偏差的 proposal。"
}

// deliveryGate 按预算→unknown→must 顺序检测终局交付决策，命中时返回回环反馈。
func (x *execution) deliveryGate(outcome string, clarifiesEvaluatedDraft bool, gates *deliveryGateCounters, turn, turns int) string {
	if turn >= turns-2 || gates.total >= 3 {
		return ""
	}
	if gates.budget < 2 {
		if fb := x.budgetGateFeedback(outcome); fb != "" {
			gates.budget++
			gates.total++
			return fb
		}
	}
	if gates.unknown < 1 && (outcome == "proposal" || clarifiesEvaluatedDraft) {
		if fb := x.unknownGateFeedback(outcome, clarifiesEvaluatedDraft); fb != "" {
			gates.unknown++
			gates.total++
			return fb
		}
	}
	if gates.hardreq < 1 && outcome == "clarify" {
		if fb := x.hardRequirementGateFeedback(); fb != "" {
			gates.hardreq++
			gates.total++
			return fb
		}
	}
	return ""
}
