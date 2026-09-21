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
	budget, unknown, hardreq, nobudget, stalled, total int
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
//
// 约束口径（2026-09-19 修订）：服务端不得静默改写 draft。但预算门检测到超预算
// 且回环耗尽时，服务端可执行确定性替换（budget_solver.go）：候选只能来自本地
// 目录（有报价、有规格），只动未锁定品类，替换后容量/性能档位不降，必须重新
// 通过全部校验规则；每次替换在 Issues 中显式标注（换成什么、为什么、依据）。
// 除此之外的任何 draft 字段仍不可由服务端修改；不引入目录外候选，不碰用户
// must/锁定件/用途相关容量，不改 rationale 语义（替换理由以独立 issue 追加）。
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
		// 无严格更便宜候选时，只有本轮已用 price_asc 核验过目录低价才允许
		// 超预算终局；否则"已遍历"是不可采信的自述，要求先核验。
		// ponytail: 只校验"出现过 price_asc 查询"，不逐品类核对——按品类核对需
		// 兼容性知识，先以有界回环压制未核验声明，升级路径是按超支品类核对。
		if len(x.priceAsc) > 0 {
			return ""
		}
		payload, _ := json.Marshal(map[string]any{
			"ceiling_cny": upper.FloatString(2), "total_cny": total.FloatString(2),
		})
		return "预算自纠反馈：" + string(payload) + "\n当前总价超出 must 预算硬上限，而本轮尚未用 order_by=price_asc 检索过目录低价；未核验的\"已遍历所有品类更便宜候选\"声明不可作为交付依据。请先用 search_local（order_by=price_asc）核对超支品类及可下调品类的最低价，能压回预算就替换并重新 evaluate；确认每个品类都无更低价后才交付标注偏差的 proposal（issues 列明牺牲项），不得停在 clarify。"
	}
	payload, _ := json.Marshal(map[string]any{
		"ceiling_cny": upper.FloatString(2), "total_cny": total.FloatString(2),
		"budget_alternatives": map[string]any{"candidates": filtered},
	})
	return "预算自纠反馈：" + string(payload) + "\n当前总价超出 must 预算硬上限，且上述品类存在严格更便宜的有报价候选；这属于你可以自行决定的调整，不得停在 clarify 等待用户取舍，也不得带着未处理的超预算直接交付 proposal。请本轮直接替换选定候选（同品类、容量或性能档位不降）并重新输出 draft 触发 evaluate；只有每个品类都比较过更便宜候选后仍无法压回预算时，才交付标注偏差的 proposal（issues 列明必须牺牲的具体用户硬性要求），不得停在 clarify 等待用户取舍。"
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

// sizePrefViolated 服务端核验硬性 ITX 板型：已选主板不是 ITX 时 must 板型未满足，
// 不采信模型自评。临时放宽（scope=temporary）与未知板型不触发。
func (x *execution) sizePrefViolated(v schemas.RequirementField) bool {
	if v.Scope == "temporary" {
		return false
	}
	var want string
	if json.Unmarshal(v.Value, &want) != nil {
		return false
	}
	if want != "itx" {
		return false
	}
	draft, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil || draft.Selection.Motherboard == "" {
		return false
	}
	for _, c := range x.candidates {
		if c.Category != schemas.CategoryMotherboard || c.ID != draft.Selection.Motherboard {
			continue
		}
		specs := map[string]json.RawMessage{}
		if json.Unmarshal(c.Specs, &specs) != nil {
			return false
		}
		var got string
		if json.Unmarshal(specs["form_factor"], &got) != nil {
			return false
		}
		return got != "" && got != "itx"
	}
	return false
}

// noBudgetGateFeedback 权威状态中没有用户表达的预算金额时，交付配置是把关键
// 取舍（花多少钱）替用户做掉；此时应检索比较后以 collect 收口列出待确认项。
func (x *execution) noBudgetGateFeedback(outcome string) string {
	if outcome != "ready" && outcome != "proposal" {
		return ""
	}
	if x.result.Validation == nil {
		return ""
	}
	if f, ok := x.input.State.Fields["budget_cny"]; ok && f.Status == "active" {
		return ""
	}
	return "预算未定收口反馈：当前权威状态没有用户表达的预算金额，不能交付配置或候选方案，也不得自行假设预算。请完成必要的检索比较后，以 outcome=collect 收口，reply 列出为开始选配仍需用户确认的信息（首先是整机预算上限）。"
}

// stalledProposalGateFeedback 当模型交付的 proposal 已通过校验、全部硬性要求满足、
// 总价在 must 预算内且不存在未匹配已有件时，其议题属于可自行决定的调整，不是用户
// 取舍；此时应直接交付 ready。已有件缺匹配（notes 非空）是真实的用户决策，不适用。
func (x *execution) stalledProposalGateFeedback() string {
	if x.result.Outcome != "proposal" || x.result.Validation == nil || x.result.Quote == nil {
		return ""
	}
	// 校验仍有 unknown 时，unknown 门负责判断是否存在字段完整替代；真无解的
	// unknown 必须如实保留，不得被本门强制 ready。
	if x.result.Validation.OverallStatus != schemas.OverallPass {
		return ""
	}
	if len(x.result.Issues) == 0 {
		return ""
	}
	if _, notes := x.deliveryIssues(); len(notes) > 0 {
		return ""
	}
	assessed := map[string]Assessment{}
	for _, a := range x.result.Assessments {
		assessed[a.Field] = a
	}
	for field, v := range x.input.State.Fields {
		if v.Status != "active" || v.Strength != "must" || v.Kind == "fact" || v.Kind == "context" {
			continue
		}
		if field == "budget_cny" || field == "budget_basis" || field == "budget_flex" {
			continue
		}
		if assessed[field].Status != "met" {
			return ""
		}
	}
	budgetNote := ""
	if upper, ok := x.budgetCeiling(); ok {
		quote := *x.result.Quote
		spec := x.accountingSpec()
		if spec.BudgetBasis == "new_purchase" && quote.PurchaseTotalCNY != nil {
			quote.TotalCNY, quote.MissingCount = *quote.PurchaseTotalCNY, quote.PurchaseMissingCount
		}
		total, valid := new(big.Rat).SetString(quote.TotalCNY)
		if !valid || total.Cmp(upper) > 0 {
			return ""
		}
		budgetNote = "、总价在预算内"
	}
	return "stalled交付核验：当前草稿已通过校验、全部硬性要求满足" + budgetNote + "，不存在必须由用户取舍的问题；你列出的议题属于可自行决定的调整。若个别候选仍缺字段，先改选字段完整的候选并重新 evaluate，然后直接输出 outcome=ready 交付当前 draft；不得以自创取舍的 proposal 收尾。"
}

// deliveryGate 按预算→unknown→must→自纠门顺序检测终局交付决策，命中时返回回环反馈。
// 各门回环耗尽后模型仍交付超预算 draft 时，由 budget_solver.go 的确定性压价接手
// （见 Run 循环中 deliveryGate 之后的 budgetFixDue 分支）。
func (x *execution) deliveryGate(outcome string, clarifiesEvaluatedDraft bool, gates *deliveryGateCounters, turn, turns int) string {
	if turn >= turns-2 || gates.total >= 3 {
		return ""
	}
	if gates.nobudget < 1 {
		if fb := x.noBudgetGateFeedback(outcome); fb != "" {
			gates.nobudget++
			gates.total++
			return fb
		}
	}
	if gates.budget < 2 {
		if fb := x.budgetGateFeedback(outcome); fb != "" {
			gates.budget++
			gates.total++
			return fb
		}
	}
	if gates.unknown < 2 && (outcome == "proposal" || clarifiesEvaluatedDraft) {
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
	if gates.stalled < 1 && outcome == "proposal" {
		if fb := x.stalledProposalGateFeedback(); fb != "" {
			gates.stalled++
			gates.total++
			return fb
		}
	}
	return ""
}
