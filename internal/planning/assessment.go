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
	// 零匹配已有件仅在 draft 未选该品类（真正沿用用户已有件）时才出 note；
	// 已被选中的品类走 finalize 的计价取舍 clarify，不再宣称"已核账"。
	selected := map[schemas.Category]bool{}
	if draft, err := schemas.DecodeBuildDraft(x.result.Draft); err == nil {
		for _, id := range draft.Selection.SKUs() {
			for _, c := range x.candidates {
				if c.ID == id {
					selected[c.Category] = true
				}
			}
		}
	}
	unmatched := []string{}
	for _, p := range spec.OwnedParts {
		matched := false
		for _, c := range x.candidates {
			if matchesOwnedPart(c, p) {
				matched = true
			}
		}
		if !matched {
			// 非 SSD 且品类被选中 → finalize 的计价取舍 clarify 处理，此处
			// 不再宣称"已按已有件核账"；SSD 数量豁免与未选中品类保留 note。
			if p.Category != schemas.CategorySSD && selected[p.Category] {
				continue
			}
			unmatched = append(unmatched, categoryLabel(p.Category)+"："+p.Model)
		}
	}
	if len(unmatched) > 0 {
		notes = append(notes, "已有配件未匹配到目录准确型号（"+strings.Join(unmatched, "、")+"）；相应品类按用户已有件使用，不计入采购合计。")
	}
	return issues, notes
}

// ownershipTradeoffPending 检测"已有件保留 vs 改购"需要用户取舍的交付形态：
// 用户已有件在目录无精确型号匹配，draft 仍以其他候选占住该品类。此时选中
// SKU 与用户已有件的型号/数量未核实，不得仅凭同品类把选中 SKU 当作已有件
// 免计价；保留还是改购是计价取舍，必须 clarify 并说明取舍，等待用户确认。
// 该条件是结构化的：不依赖模型自述措辞，也适用于模型直接宣称的 ready。
// （取代 r11 起按"替身/沿用用户"窄措辞收敛的 placeholderDelivery——BV2-104
// 现场证明"继续沿用"等变体绕过措辞检测。）
func (x *execution) ownershipTradeoffPending(draft schemas.BuildDraft) []schemas.OwnedPart {
	selected := map[schemas.Category]bool{}
	for _, id := range draft.Selection.SKUs() {
		for _, c := range x.candidates {
			if c.ID == id {
				selected[c.Category] = true
			}
		}
	}
	var pending []schemas.OwnedPart
	for _, p := range x.accountingSpec().OwnedParts {
		// SSD 按品类内数量核账（存储可互换、型号核验无意义），不在此列。
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
			pending = append(pending, p)
		}
	}
	return pending
}

// ownershipTradeoffIssue 生成计价取舍 clarify 的问题文本，逐件给出品类与
// 用户型号，说明两条路径的计价差异。
func ownershipTradeoffIssue(pending []schemas.OwnedPart) string {
	parts := make([]string, 0, len(pending))
	for _, p := range pending {
		parts = append(parts, categoryLabel(p.Category)+"："+p.Model)
	}
	return "已有配件在目录无精确型号匹配（" + strings.Join(parts, "、") +
		"）：保留已有件（该品类不计入采购合计）还是改购新件（计入采购合计），是计价取舍，需用户确认后继续。"
}

// verifiedOwnership 已有件核账：非 SSD 品类只有在 draft 选中的候选与用户
// 型号精确匹配（matchesOwnedPart 品牌全名）时才视为核验、该品类不计采购价；
// 仅同品类选中不再构成核验——型号/数量未核实时把选中 SKU 当已有件免计价，
// 正是 BV2-104"账实不一致"的会计根因。SSD 以品类内总数量一致防多盘误豁免
// （存储可互换，型号匹配无意义）。零匹配且品类被选中的已有件走 finalize 的
// 计价取舍 clarify，不由本函数静默豁免。
func (x *execution) verifiedOwnership(draft schemas.BuildDraft) schemas.RequirementSpec {
	spec := x.accountingSpec()
	owned := spec.OwnedParts
	spec.OwnedParts = nil
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
		verified := false
		for _, id := range draft.Selection.SKUs() {
			for _, c := range x.candidates {
				if c.ID == id && matchesOwnedPart(c, p) {
					verified = true
				}
			}
		}
		if verified {
			spec.OwnedParts = append(spec.OwnedParts, p)
		}
	}
	return spec
}
