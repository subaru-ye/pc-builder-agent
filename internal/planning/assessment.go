package planning

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"unicode"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func matchesOwnedPart(c Candidate, p schemas.OwnedPart) bool {
	return p.Model != "" && c.Category == p.Category &&
		(strings.EqualFold(p.Model, c.Model) || strings.EqualFold(p.Model, c.Brand+" "+c.Model))
}

// Internal rule identifiers echoed by the model into issue text must not reach
// user-visible copy; the Chinese detail stays, the code prefix is dropped.
var internalIssueCode = regexp.MustCompile(`^[A-Z][A-Z_]{3,}[：:]`)

// budgetOverrunClaim 匹配模型自述的"超预算/超硬上限"断言；服务端预算上限
// 是冻结约束的确定性算术，与之矛盾的模型断言按无依据剔除。
var budgetOverrunClaim = regexp.MustCompile(`超出硬上限|超过硬上限|超出预算|超过预算|预算超出`)

// budgetAccountingVocab 是"纯预算误判"判定的封闭词表：仅当一个匹配断言
// 关键词的 issue 在剔除这些记账措辞与标点后不剩任何字母、数字或简写标记，
// 才被认定为纯预算误判。词表外的一切内容（配件、用户决策、未知措辞）一律
// 保守保留为待解决项——BV2-110 的混合行同时携带误判与 ①②③ 真实取舍，
// 整行剔除会让方案越过未解决项错误 ready（ponytail：封闭词表偏保守，误判
// 为"混合"只会多保留，不会多删除；词表按真实误判样例再扩）。
var budgetAccountingVocab = []string{
	"当前报价", "报价", "总价", "合计", "金额",
	"超出", "超过", "预算", "硬上限", "弹性", "范围内", "超支", "缺口", "不足", "超",
	"元", "约",
}

// tradeoffShorthandMarks 是取舍简写标记：箭头/升降号表示压价方向；圈号
// （①②③ 等 No 类，unicode.IsNumber 已覆盖）与数字构成选项/金额简写。
// 它们承载真实取舍内容（如压价选项 7434→6244、方案列表），不是可剔除的
// "空残渣"。
const tradeoffShorthandMarks = "→←↔↑↓⇒⇄⇀⇁"

// pureBudgetOverrunClaim 判定一条 issue 是否"可明确识别为纯预算误判"：
// 整行只含记账措辞，不含任何字母、数字或简写标记。含数字/箭头/圈号的行
// （"7000→6244"、"①7434②6244"、金额"434元"）按混合内容保留为待解决项，
// 方案停在 proposal。
func pureBudgetOverrunClaim(issue string) bool {
	if !budgetOverrunClaim.MatchString(issue) {
		return false
	}
	s := issue
	for _, w := range budgetAccountingVocab {
		s = strings.ReplaceAll(s, w, "")
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || strings.ContainsRune(tradeoffShorthandMarks, r) {
			return false
		}
	}
	return true
}

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
	serverOverBudget := false
	if upper, ok := x.budgetCeiling(); ok {
		if total, ok := new(big.Rat).SetString(quote.TotalCNY); ok && total.Cmp(upper) > 0 {
			issues = append(issues, "候选价格超过已表达的预算范围，需继续调整或讨论取舍")
			serverOverBudget = true
		}
	}
	// 预算上限是冻结合同的确定性算术（budget×(1+flex)）：完整合计已核验且在
	// 上限内时，模型自述的"超出预算/硬上限"与权威核算矛盾。剔除仅限"可明确
	// 识别为纯预算误判"的独立 issue（整行只含记账措辞与数字）；混合或无法
	// 判定的行一律保留为待解决项——方案因此停在 proposal，不得越过未解决项
	// 自动 ready（BV2-110 混合行携带 ①②③ 真实取舍，整行剔除曾把它们一并
	// 删掉）。合计缺价或不可解析时不剔除——服务端没有核验过算术，"可能超出"
	// 未被矛盾。note 只是剔除发生后的如实复述（辅助信息），不是正确性的
	// 兜底机制；保留行的无依据断言随行可见，属保守剔除的已知代价。
	// ponytail: 封闭词表偏保守——把真实混合行误判为"纯"需要词表吞掉取舍
	// 措辞，词表保持最小并只按真实样例扩充。
	if !serverOverBudget {
		if upper, ok := x.budgetCeiling(); ok {
			if total, parsed := new(big.Rat).SetString(quote.TotalCNY); parsed && quote.MissingCount == 0 {
				filtered := make([]string, 0, len(x.result.Issues))
				dropped := false
				for _, issue := range x.result.Issues {
					if pureBudgetOverrunClaim(issue) {
						dropped = true
						continue
					}
					filtered = append(filtered, issue)
				}
				x.result.Issues = filtered
				if amount := new(big.Rat).SetInt64(int64(spec.BudgetCNY)); dropped && total.Cmp(amount) > 0 {
					notes = append(notes, fmt.Sprintf("报价 %s 元高于预算金额 %d 元，在有效上限 %s 元（预算×(1+弹性)）内交付，不构成超支；是否接受或继续压价由用户决定。",
						total.FloatString(2), spec.BudgetCNY, upper.FloatString(0)))
				}
			}
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
		if !matched && !selected[p.Category] {
			// 仅"品类未被选中（真正沿用用户已有件）"的零匹配已有件出 note；
			// 被选中的品类由 finalize 的计价取舍 clarify 点名，此处不再宣称
			// "按已有件核账、不计入采购合计"。
			unmatched = append(unmatched, categoryLabel(p.Category)+"："+p.Model)
		}
	}
	if len(unmatched) > 0 {
		notes = append(notes, "已有配件未匹配到目录准确型号（"+strings.Join(unmatched, "、")+"）；相应品类按用户已有件使用，不计入采购合计。")
	}
	return issues, notes
}

// unmatchedOwnedParts 列出目录零精确匹配的已有件（全部，不区分品类是否被
// 选中）。用于 draft 不可解码、核账无从执行的兜底：此时任何"沿用"都未经
// 核账，取舍必须回到用户。
func (x *execution) unmatchedOwnedParts() []schemas.OwnedPart {
	var unmatched []schemas.OwnedPart
	for _, p := range x.accountingSpec().OwnedParts {
		matched := false
		for _, c := range x.candidates {
			if matchesOwnedPart(c, p) {
				matched = true
			}
		}
		if !matched {
			unmatched = append(unmatched, p)
		}
	}
	return unmatched
}

// ownershipTradeoffPending 检测"已有件保留 vs 改购"需要用户取舍的交付形态：
// 用户已有件在目录无精确型号匹配，draft 仍以其他候选占住该品类。此时选中
// SKU 与用户已有件的型号/数量未核实，不得仅凭同品类把选中 SKU 当作已有件
// 免计价；保留还是改购是计价取舍，必须 clarify 并说明取舍，等待用户确认。
// 该条件是结构化的：不依赖模型自述措辞，也适用于模型直接宣称的 ready。
// （取代 r11 起按"替身/沿用用户"窄措辞收敛的 placeholderDelivery——BV2-104
// 现场证明"继续沿用"等变体绕过措辞检测。）
// ownershipTradeoffPending 检测"已有件保留 vs 改购"需要用户取舍的交付形态：
// 品类被 draft 选中，但选中的 SKU 与用户已有件不对应（型号不匹配，或 SSD
// 数量不一致）。此时保留（换用/沿用已有件，该品类不计采购）还是改购（选中
// 件计入采购）是计价取舍，必须 clarify 并说明，等待用户确认——状态合同
// 无法表达"保留旧件同时改购新件"的授权，只能由用户确认或先行移除已有件。
func (x *execution) ownershipTradeoffPending(draft schemas.BuildDraft) []schemas.OwnedPart {
	selected := map[schemas.Category][]Candidate{}
	for _, id := range draft.Selection.SKUs() {
		for _, c := range x.candidates {
			if c.ID == id {
				selected[c.Category] = append(selected[c.Category], c)
			}
		}
	}
	owned := x.accountingSpec().OwnedParts
	var pending []schemas.OwnedPart
	// SSD 按品类整体判断（WithOwnership 按品类豁免，部分对应必须整品类
	// 计价——不能只豁免对应件让不对应件搭车免费）。
	if len(selected[schemas.CategorySSD]) > 0 && !x.ssdSelectionCorresponds(draft, owned) {
		for _, p := range owned {
			if p.Category == schemas.CategorySSD {
				pending = append(pending, p)
			}
		}
	}
	for _, p := range owned {
		if p.Category == schemas.CategorySSD {
			continue
		}
		chosen := selected[p.Category]
		if len(chosen) == 0 {
			// 品类被留空：仅当该已有件在目录有精确匹配（规格真值可核、槽位
			// 兼容性由规则引擎按旧件真值检验）时才是"真正沿用"。零匹配的
			// 留空意味着"沿用"未经任何核账——容量/代别是否被旧件满足未知，
			// 保留还是改购仍是用户计价取舍，不得静默交付（BV2-104 Pass³v3
			// r3：draft 留空 memory 以 proposal 自报，绕过取舍 clarify）。
			matched := false
			for _, c := range x.candidates {
				if matchesOwnedPart(c, p) {
					matched = true
				}
			}
			if !matched {
				pending = append(pending, p)
			}
			continue
		}
		corresponds := false
		for _, c := range chosen {
			if matchesOwnedPart(c, p) {
				corresponds = true
			}
		}
		if !corresponds {
			pending = append(pending, p)
		}
	}
	return pending
}

// ssdSelectionCorresponds 报告 SSD 选中（型号+各自数量）是否与该品类已有件
// 整体对应：每个选中 SKU 都对应某已有件型号，且各对应已有件的选中数量合计
// 等于其已有数量（多盘保护保留）。
func (x *execution) ssdSelectionCorresponds(draft schemas.BuildDraft, owned []schemas.OwnedPart) bool {
	byID := map[string]Candidate{}
	for _, c := range x.candidates {
		byID[c.ID] = c
	}
	used := map[int]int{}
	for _, s := range draft.Selection.SSDs {
		c, ok := byID[s.SKU]
		if !ok {
			return false
		}
		matched := -1
		for i, p := range owned {
			if p.Category == schemas.CategorySSD && matchesOwnedPart(c, p) {
				matched = i
				break
			}
		}
		if matched < 0 {
			return false
		}
		used[matched] += s.Quantity
	}
	for i, p := range owned {
		if p.Category != schemas.CategorySSD {
			continue
		}
		if used[i] != max(1, p.Quantity) {
			return false
		}
	}
	return true
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

// verifiedOwnership 已有件核账：豁免以"实际选中件与已有件对应"为唯一依据。
// 非 SSD 品类要求 draft 选中的候选与用户型号精确匹配（matchesOwnedPart 品牌
// 全名）；SSD 额外要求数量合计一致——型号不同即新购件（即使数量相同）、数量
// 不一致即有超额采购，都不得整品类免计价，这正是 BV2-104"账实不一致"与
// 其 SSD 变体的会计根因。不对应的品类走 finalize 的计价取舍 clarify。
func (x *execution) verifiedOwnership(draft schemas.BuildDraft) schemas.RequirementSpec {
	spec := x.accountingSpec()
	owned := spec.OwnedParts
	spec.OwnedParts = nil
	selected := map[schemas.Category][]Candidate{}
	for _, id := range draft.Selection.SKUs() {
		for _, c := range x.candidates {
			if c.ID == id {
				selected[c.Category] = append(selected[c.Category], c)
			}
		}
	}
	for _, p := range owned {
		if p.Category == schemas.CategorySSD {
			// WithOwnership 按品类豁免：只有 SSD 选中（型号+各自数量）与已有
			// 件整体对应时才可整品类豁免；部分对应（已有 A+B、选中 A+C）必须
			// 整品类计价，不得把对应件单独放入 OwnedParts 让其余搭车免费。
			if x.ssdSelectionCorresponds(draft, owned) {
				spec.OwnedParts = append(spec.OwnedParts, p)
			}
			continue
		}
		verified := false
		for _, c := range selected[p.Category] {
			if matchesOwnedPart(c, p) {
				verified = true
			}
		}
		if verified {
			spec.OwnedParts = append(spec.OwnedParts, p)
		}
	}
	return spec
}
