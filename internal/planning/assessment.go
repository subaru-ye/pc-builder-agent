package planning

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func matchesOwnedPart(c Candidate, p schemas.OwnedPart) bool {
	return p.Model != "" && c.Category == p.Category &&
		(strings.EqualFold(p.Model, c.Model) || strings.EqualFold(p.Model, c.Brand+" "+c.Model))
}

// Internal rule identifiers echoed by the model into issue text must not reach
// user-visible copy; the Chinese detail stays, the code prefix is dropped.
var internalIssueCode = regexp.MustCompile(`^[A-Z][A-Z_]{3,}[：:]`)

func stripInternalIssueCodes(issues []string) []string {
	for i, s := range issues {
		if loc := internalIssueCode.FindStringIndex(s); loc != nil {
			issues[i] = strings.TrimSpace(s[loc[1]:])
		}
	}
	return issues
}

// frozenSpec 严格解码确认事务冻结的有效选型约束;无冻结载荷返回 (nil, nil)
// (历史归档/评估回放走旧路径)。损坏的冻结约束返回错误——调用方不得回退到
// 原始 state 或关闭预算门槛后继续生成(Runner.Run 入口即校验并拒绝)。
func (x *execution) frozenSpec() (*schemas.RequirementSpec, error) {
	if x.input.EffectiveConstraints == nil {
		return nil, nil
	}
	spec, err := schemas.DecodeRequirementSpec(x.input.EffectiveConstraints.Spec)
	if err != nil {
		return nil, fmt.Errorf("planning: 冻结的有效选型约束无效: %w", err)
	}
	return &spec, nil
}

// accountingSpec 是预算/口径/已有件的确定性会计输入。确认/Builder gate v2 起
// 优先读取确认事务冻结的有效选型约束(已展开系统默认并标注来源):冻结后修改
// 系统默认规则不改变旧 run 的执行语义。冻结约束损坏时按空会计失败关闭——
// 绝不静默回退到可变推导;nil 的旧载荷按原语义从 State 读取。预算金额、口径
// 与支出下限永不引入默认,预算弹性未表达时按系统默认执行(与核定预览同一来源)。
func (x *execution) accountingSpec() schemas.RequirementSpec {
	frozen, err := x.frozenSpec()
	if err != nil {
		return schemas.RequirementSpec{}
	}
	if frozen != nil {
		return *frozen
	}
	var spec schemas.RequirementSpec
	read := func(field string, dst any) {
		if f := x.input.State.Fields[field]; f.Status == "active" {
			_ = json.Unmarshal(f.Value, dst)
		}
	}
	read("budget_cny", &spec.BudgetCNY)
	read("budget_flex", &spec.BudgetFlex)
	if x.input.State.Fields["budget_flex"].Status != "active" {
		spec.BudgetFlex = schemas.DefaultBudgetFlex
	}
	read("budget_basis", &spec.BudgetBasis)
	read("owned_parts", &spec.OwnedParts)
	return spec
}

// budgetMust 报告预算是否为 must 硬约束:冻结路径读冻结载荷的
// constraint_strengths,旧载荷读 State 字段强度。冻结约束损坏时失败关闭
// (false = 无预算门槛不得成为继续生成的理由;Run 入口已先行拒绝无效载荷)。
func (x *execution) budgetMust() bool {
	frozen, err := x.frozenSpec()
	if err != nil {
		return false
	}
	if frozen != nil {
		return frozen.ConstraintStrengths["budget_cny"] == "must"
	}
	field, ok := x.input.State.Fields["budget_cny"]
	return ok && field.Status == "active" && field.Strength == "must"
}

func (x *execution) deliveryIssues() (issues, notes []string) {
	issues = []string{}
	if x.result.Validation == nil {
		return []string{"候选尚未完成兼容性核验"}, nil
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
		return append(issues, "报价尚未核验"), nil
	}
	spec := x.accountingSpec()
	// Ownership has been verified at category level at evaluate; purchase
	// accounting already excludes verified categories.
	quote := *x.result.Quote
	if spec.BudgetBasis == "new_purchase" && quote.PurchaseTotalCNY != nil {
		quote.TotalCNY, quote.MissingCount = *quote.PurchaseTotalCNY, quote.PurchaseMissingCount
	}
	if quote.MissingCount > 0 {
		issues = append(issues, "部分配件价格未知，合计尚不完整")
	}
	if upper, ok := x.budgetCeiling(); ok {
		if total, ok := new(big.Rat).SetString(quote.TotalCNY); ok && total.Cmp(upper) > 0 {
			issues = append(issues, "候选价格超过已表达的预算范围，需继续调整或讨论取舍")
		}
	}
	// Extra attributes with missing evidence remain visible on the candidate.
	// Only actual compatibility/price gaps and unresolved user conditions affect
	// delivery; an unrelated unknown attribute must not become a new hard gate.
	unmatched := []string{}
	for _, p := range spec.OwnedParts {
		matched := false
		for _, c := range x.result.Candidates {
			if matchesOwnedPart(c, p) {
				matched = true
			}
		}
		if !matched {
			unmatched = append(unmatched, p.Model)
		}
	}
	if len(unmatched) > 0 {
		notes = append(notes, "已有配件未匹配到目录准确型号（"+strings.Join(unmatched, "、")+"）；相应品类已按用户已有件核账，不计入采购合计。")
	}
	return issues, notes
}

// placeholderDelivery 检测"已有件占位交付"：已有件在目录无精确型号匹配、
// draft 仍以其他候选占住该品类，且模型自述把替身说成用户已有件。保留已有件
// 还是改购新件是用户的计价取舍，模型必须 clarify 而非以占位配置交付。
// ponytail: "用户已表态沿用"无法程序判定，只能以模型自述中的替身措辞窄特征
// 收敛已知的占位交付形态；升级路径是模型结构化声明替身意图字段。
func (x *execution) placeholderDelivery() bool {
	if x.result.Validation == nil || len(x.result.Draft) == 0 {
		return false
	}
	draft, err := schemas.DecodeBuildDraft(x.result.Draft)
	if err != nil {
		return false
	}
	selected := map[schemas.Category]bool{}
	for _, id := range draft.Selection.SKUs() {
		for _, c := range x.candidates {
			if c.ID == id {
				selected[c.Category] = true
			}
		}
	}
	unmatched := false
	for _, p := range x.accountingSpec().OwnedParts {
		if p.Category == schemas.CategorySSD {
			continue
		}
		matched := false
		for _, c := range x.candidates {
			if matchesOwnedPart(c, p) {
				matched = true
			}
		}
		if !matched && selected[p.Category] {
			unmatched = true
		}
	}
	if !unmatched {
		return false
	}
	text := x.result.Reply + strings.Join(x.result.Issues, "")
	return strings.Contains(text, "替身") || strings.Contains(text, "沿用用户")
}

// verifiedOwnership 已有件按品类核账：用户明确断言的 owned_part 在 draft
// 对应品类恰好选了一件即视为核验，替身候选不计采购价；SSD 以品类内总数量
// 一致防多盘误豁免。目录精确匹配与否只影响 note，不影响核账。
func (x *execution) verifiedOwnership(draft schemas.BuildDraft) schemas.RequirementSpec {
	spec := x.accountingSpec()
	owned := spec.OwnedParts
	spec.OwnedParts = nil
	selected := map[schemas.Category]bool{}
	for _, id := range draft.Selection.SKUs() {
		for _, c := range x.candidates {
			if c.ID == id {
				selected[c.Category] = true
			}
		}
	}
	for _, p := range owned {
		if p.Category == schemas.CategorySSD {
			total := 0
			for _, s := range draft.Selection.SSDs {
				total += s.Quantity
			}
			if total == max(1, p.Quantity) {
				spec.OwnedParts = append(spec.OwnedParts, p)
			}
			continue
		}
		if selected[p.Category] {
			spec.OwnedParts = append(spec.OwnedParts, p)
		}
	}
	return spec
}
