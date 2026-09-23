package evalsuite

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// SnapshotView 保存钉死快照的索引及完整目录，供 SKU、已有件和非交付证据重判。
// 厂商口径复用 buildharness 的 CPUVendor/GPUFamily(与规划器过滤同一套推断),
// 断言只依赖这些纯数据,与数据库解耦,轨迹文件里随用例一起留存供 replay。
type SnapshotView struct {
	SnapshotDate   string                 `json:"snapshot_date"`
	Catalog        *store.CatalogSnapshot `json:"catalog,omitempty"`
	SKUs           map[string]bool        `json:"skus"`
	BrandBySKU     map[string]string      `json:"brand_by_sku"`
	CPUVendorBySKU map[string]string      `json:"cpu_vendor_by_sku"`
	GPUFamilyBySKU map[string]string      `json:"gpu_family_by_sku"`
}

// NewSnapshotView 从 store 的候选快照构建投影。
func NewSnapshotView(cat store.CatalogSnapshot) SnapshotView {
	view := SnapshotView{
		SnapshotDate:   cat.Snapshot.SnapshotDate.Format("2006-01-02"),
		Catalog:        &cat,
		SKUs:           make(map[string]bool, len(cat.Candidates)),
		BrandBySKU:     make(map[string]string, len(cat.Candidates)),
		CPUVendorBySKU: make(map[string]string),
		GPUFamilyBySKU: make(map[string]string),
	}
	for _, candidate := range cat.Candidates {
		view.SKUs[candidate.SKU] = true
		view.BrandBySKU[candidate.SKU] = candidate.Brand
		switch candidate.Category {
		case schemas.CategoryCPU:
			view.CPUVendorBySKU[candidate.SKU] = buildharness.CPUVendor(candidate)
		case schemas.CategoryGPU:
			view.GPUFamilyBySKU[candidate.SKU] = buildharness.GPUFamily(candidate)
		}
	}
	return view
}

// AssertionFailure 是一条断言的失败记录;ID 与 P13 设计 §3.3 的编号一致。
// Veto=true 表示一票否决类失败(编造/越界/越权),计入 Pass^k 门禁与报告计数。
type AssertionFailure struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Veto   bool   `json:"veto,omitempty"`
}

// Attribution 是失败断言的规则级归因(最早且能解释后续失败的失败为主因)。
// 归因码体系见 docs/eval/评估集建立记录.md §2,随归因案例迭代。
type Attribution struct {
	Code    string `json:"code"`
	Primary bool   `json:"primary"`
	Detail  string `json:"detail"`
}

// Verdict 是一条用例的判定结论。
type Verdict struct {
	Passed    bool               `json:"passed"`
	DataError bool               `json:"data_error,omitempty"`
	Failures  []AssertionFailure `json:"failures,omitempty"`
}

// AssertCase 对一条 build 用例的执行结果运行全部断言。Succeeded=false 时 A2 即
// 失败,其余断言依赖交付产物,标记跳过(不计失败)。
func AssertCase(c Case, result buildharness.BuildResult, snap SnapshotView) Verdict {
	if c.Expect.Outcome == "budget_adaptive" {
		if result.Succeeded {
			c.Expect = Expect{Outcome: "pass"}
		} else {
			d := result.Decision
			if d == nil || d.Kind != "catalog_infeasible" || (d.Reason != "budget_lower_bound" && d.Reason != "platform_budget_lower_bound") {
				return Verdict{Failures: []AssertionFailure{{ID: "N1", Name: "合理非交付", Detail: "预算自适应期望仅接受合格交付或可重建的目录预算不足证明;搜索耗尽、缺数据与普通错误不通过"}}}
			}
			c.Expect = Expect{Outcome: "catalog_infeasible", Reason: d.Reason}
		}
	}
	if c.Expect.Outcome != "pass" {
		return assertNonDelivery(c, result, snap)
	}
	var failures []AssertionFailure
	add := func(id, name, format string, args ...any) {
		failures = append(failures, AssertionFailure{ID: id, Name: name, Detail: fmt.Sprintf(format, args...)})
	}
	addVeto := func(id, name, format string, args ...any) {
		failures = append(failures, AssertionFailure{ID: id, Name: name, Detail: fmt.Sprintf(format, args...), Veto: true})
	}

	// A2 交付结果符合期望(目前仅 pass)。
	if c.Expect.Outcome == "pass" && !result.Succeeded {
		add("A2", "交付结果=pass", "Succeeded=false attempts=%d overall=%s message=%q",
			result.Attempts, overallStatus(result), result.Message)
	}

	// 未交付时交付产物不完整,A1/A3–A8 无从谈起。
	if !result.Succeeded {
		return Verdict{Passed: false, Failures: failures}
	}

	// A1 draft 结构完整性:必填品类 SKU 非空、SSD 数量为正、GPU nil 或非空。
	if detail := assertDraftShape(result.Draft); detail != "" {
		add("A1", "draft 结构完整", "%s", detail)
	}

	// A3 快照口径一致。
	if got := result.Result.Quote.SnapshotDate; got != snap.SnapshotDate {
		add("A3", "快照口径一致", "quote 快照=%s 钉死快照=%s", got, snap.SnapshotDate)
	}

	// A4 预算窗口(交叉校验,veto):缺价时预算不可判定,记 data-error(数据
	// 前提不满足,从分母剔除),与 harness 的宽待语义一致但结论更保守。
	if validate.BudgetQuote(c.Requirement, result.Result.Quote).MissingCount > 0 {
		return Verdict{Passed: false, DataError: true, Failures: append(failures, AssertionFailure{
			ID: "A4", Name: "预算窗口", Veto: true,
			Detail: fmt.Sprintf("缺价 %d 件 %v,预算不可判定", result.Result.Quote.MissingCount, result.Result.Quote.MissingSKUs),
		})}
	}
	if detail := assertBudgetWindow(c.Requirement, result.Result.Quote); detail != "" {
		addVeto("A4", "预算窗口", "%s", detail)
	}

	// A5 brand_pref 满足。
	if detail := assertBrandPref(c.Requirement, result.Draft.Selection, snap); detail != "" {
		add("A5", "brand_pref 满足", "%s", detail)
	}

	// A6 SKU 成员复验(veto;harness v2 已强制,纵深防御)。
	for _, sku := range selectionSKUs(result.Draft.Selection) {
		if !snap.SKUs[sku] {
			addVeto("A6", "SKU 成员复验", "SKU %q 不在钉死快照候选内", sku)
		}
	}

	// A7 报价块完整:分项小计之和 == 合计。
	if detail := assertQuoteIntegrity(result.Result.Quote); detail != "" {
		add("A7", "报价块完整", "%s", detail)
	}

	if detail := assertOwnership(c, result, snap); detail != "" {
		addVeto("A9", "已有件与采购报价", "%s", detail)
	}

	// A8 锁定品类复验(veto):交付与基线逐字节一致。
	if detail := assertLocked(c.Locked, c.BaseSelection, &result.Draft.Selection); detail != "" {
		addVeto("A8", "锁定品类复验", "%s", detail)
	}

	return Verdict{Passed: len(failures) == 0, Failures: failures}
}

// AssertScreeningCase 从最终回复重算 S1/S2/S3/S4;首跑与 replay 共用同一入口。
func AssertScreeningCase(c Case, text string) Verdict {
	payload := pipeline.ExtractPayload(text)
	var failures []AssertionFailure
	add := func(id, name, format string, args ...any) {
		failures = append(failures, AssertionFailure{ID: id, Name: name, Detail: fmt.Sprintf(format, args...)})
	}

	var spec *schemas.RequirementSpec
	if len(payload) > 0 {
		// replay 的 v1 录制输出经评估专用 legacy 解码;live v2 输出不受影响。
		decoded, err := schemas.DecodeLegacyRequirementSpec(payload)
		if err != nil {
			// 可能是 ChangeRequest(改单),初筛用例当前不期望它。
			add("S1", "输出形态", "提取到 JSON 但不是合法 RequirementSpec:%v", err)
		} else {
			spec = &decoded
			if fields := schemas.MissingOwnedFields(decoded); len(fields) > 0 {
				add("S3", "已有件信息不足", "缺失:%v", fields)
			}
		}
	}

	// S1 输出形态符合期望:spec = 应给出需求单 JSON;clarify = 应只追问,不输出 JSON。
	switch c.Expect.Kind {
	case "spec":
		if spec == nil && len(failures) == 0 {
			add("S1", "输出形态", "期望输出需求单 JSON,实际未提取到(payload=%q)", truncate(string(payload), 120))
		}
	case "clarify":
		if len(payload) > 0 || strings.ContainsAny(text, "{}") || strings.Contains(text, "```") {
			add("S1", "输出形态", "期望纯文本追问,实际包含 JSON 或代码块:%s", truncate(text, 120))
		} else if strings.TrimSpace(text) == "" {
			add("S1", "输出形态", "期望追问,实际为空回复")
		} else if len(c.Expect.ClarifyFields) == 0 {
			if !asksFor(text, "") {
				add("S3", "追问内容", "未检测到询问或请求补充信息")
			}
		} else {
			for _, field := range c.Expect.ClarifyFields {
				if !asksFor(text, field) {
					add("S3", "追问内容", "未追问缺失字段 %s", field)
				}
			}
		}
	}

	// S4 只对显式声明的新题生效，旧题重放不会悄悄增加负向断言。
	if c.Expect.Kind == "clarify" && len(payload) == 0 {
		for _, field := range c.Expect.ForbiddenClarifyFields {
			if clause := repeatedQuestion(text, field); clause != "" {
				add("S4", "不重复追问已知信息", "仍追问字段 %s:%s", field, truncate(clause, 200))
			}
		}
	}

	// S2 字段口径符合期望。
	if spec != nil {
		failures = append(failures, assertSpecFields(*spec, c.Expect.SpecFields)...)
		if c.Expect.BudgetCNY != nil && spec.BudgetCNY != *c.Expect.BudgetCNY {
			add("S2", "字段口径", "budget_cny=%d,期望 %d", spec.BudgetCNY, *c.Expect.BudgetCNY)
		}
		if c.Expect.Resolution != "" && string(spec.UseCase.Resolution) != c.Expect.Resolution {
			add("S2", "字段口径", "resolution=%q,期望 %q", spec.UseCase.Resolution, c.Expect.Resolution)
		}
		if c.Expect.CPUBrand != "" && string(spec.BrandPref.CPU) != c.Expect.CPUBrand {
			add("S2", "字段口径", "brand_pref.cpu=%q,期望 %q", spec.BrandPref.CPU, c.Expect.CPUBrand)
		}
		if c.Expect.GPUBrand != "" && string(spec.BrandPref.GPU) != c.Expect.GPUBrand {
			add("S2", "字段口径", "brand_pref.gpu=%q,期望 %q", spec.BrandPref.GPU, c.Expect.GPUBrand)
		}
	}

	return Verdict{Passed: len(failures) == 0, Failures: failures}
}

// asksFor 是中文回归用例的确定性近似规则,不承担开放域语义判卷。
// 主题词和请求词须在同一短句，或位于明确请求引导的紧邻列表内。
// 不将普通陈述搭配后续无关问题视为追问。
func asksFor(text, field string) bool {
	terms := map[string][]string{
		"budget_cny":   {"预算", "多少钱", "多少元", "多少块", "花多少"},
		"resolution":   {"分辨率", "1080p", "1440p", "2160p", "2k", "4k"},
		"owned_parts":  {"型号", "哪款", "哪一款", "具体配件"},
		"budget_basis": {"新增", "购买", "整机", "整台电脑", "整台主机", "预算口径", "包含已有", "包括已有"},
		"use_case":     {"用途", "主要用来", "主要做", "纯游戏"},
	}
	for _, clause := range strings.FieldsFunc(strings.ToLower(expandQuestionList(text)), func(r rune) bool {
		return strings.ContainsRune("。！!；;，,\n", r)
	}) {
		if containsAny(clause, []string{"不用", "不必", "无需", "不需要", "无法", "不能", "不知道", "不清楚", "不支持", "没法", "抱歉"}) {
			continue
		}
		if field != "" && !containsAny(clause, terms[field]) {
			continue
		}
		// “分别/逐一”不改变请求含义；仍在同一短句内匹配主题和请求词。
		request := strings.NewReplacer("请分别", "请", "请逐一", "请").Replace(clause)
		if containsAny(request, []string{"多少", "多大", "什么", "哪", "几", "吗", "呢", "还是", "？", "?", "请提供", "请告诉", "请补充", "请确认", "请说明", "请给出", "告知"}) {
			return true
		}
	}
	return false
}

var questionListItem = regexp.MustCompile(`^(?:[0-9]+[.)、]|[-*])\s+(.+)$`)

// 只向明确请求之后紧邻的列表项传播请求词；普通段落立即结束这个作用域。
func expandQuestionList(text string) string {
	lines := strings.Split(text, "\n")
	request := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		header := strings.TrimRight(strings.Trim(trimmed, "*"), "：:")
		switch header {
		case "请补充以下信息", "请提供以下信息", "请确认以下信息", "请告诉我以下信息", "请问":
			request = "请提供 "
			continue
		}
		if item := questionListItem.FindStringSubmatch(trimmed); request != "" && item != nil {
			lines[i] = request + item[1]
			continue
		}
		request = ""
	}
	return strings.Join(lines, "\n")
}

func containsAny(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

// Attribute 把失败断言映射为领域归因码(规则级);首个失败为主因。
func Attribute(failures []AssertionFailure) []Attribution {
	var out []Attribution
	for _, failure := range failures {
		code := ""
		switch failure.ID {
		case "A4":
			code = "B2"
		case "A6":
			code = "B1"
		case "A8":
			code = "B4"
		case "A5":
			code = "B8"
		case "A7":
			code = "C1"
		case "A10":
			code = "B6"
		case "E1":
			code = "E1"
		case "A2":
			if strings.Contains(failure.Detail, "schema") {
				code = "B7"
			} else {
				code = "B6"
			}
		case "RUN":
			code = "C2"
		}
		if code == "" {
			continue
		}
		out = append(out, Attribution{Code: code, Primary: len(out) == 0, Detail: failure.Detail})
	}
	return out
}

// assertLocked 校验锁定品类交付 SKU 与基线逐字节一致(SSD 按集合比较)。
func assertLocked(locked []schemas.Category, base, draft *schemas.BuildSelection) string {
	if base == nil || draft == nil || len(locked) == 0 {
		return ""
	}
	var violations []string
	for _, category := range locked {
		want := categorySKUs(*base, category)
		got := categorySKUs(*draft, category)
		if !sameStringSet(want, got) {
			violations = append(violations, fmt.Sprintf("%s 基线=%v 交付=%v", category, want, got))
		}
	}
	return strings.Join(violations, ";")
}

func categorySKUs(sel schemas.BuildSelection, category schemas.Category) []string {
	switch category {
	case schemas.CategoryCPU:
		return []string{sel.CPU}
	case schemas.CategoryGPU:
		if sel.GPU == nil {
			return nil
		}
		return []string{*sel.GPU}
	case schemas.CategoryMotherboard:
		return []string{sel.Motherboard}
	case schemas.CategoryMemory:
		return []string{sel.Memory}
	case schemas.CategoryPSU:
		return []string{sel.PSU}
	case schemas.CategoryCase:
		return []string{sel.Case}
	case schemas.CategoryCooler:
		return []string{sel.Cooler}
	case schemas.CategorySSD:
		skus := make([]string, 0, len(sel.SSDs))
		for _, ssd := range sel.SSDs {
			skus = append(skus, ssd.SKU)
		}
		sort.Strings(skus)
		return skus
	default:
		return nil
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]int, len(a))
	for _, s := range a {
		set[s]++
	}
	for _, s := range b {
		set[s]--
		if set[s] < 0 {
			return false
		}
	}
	return true
}

func truncate(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "…"
}

func assertDraftShape(draft schemas.BuildDraft) string {
	sel := draft.Selection
	var empty []string
	for _, slot := range []struct {
		name string
		sku  string
	}{{"cpu", sel.CPU}, {"motherboard", sel.Motherboard}, {"memory", sel.Memory},
		{"psu", sel.PSU}, {"case", sel.Case}, {"cooler", sel.Cooler}} {
		if strings.TrimSpace(slot.sku) == "" {
			empty = append(empty, slot.name)
		}
	}
	if len(empty) > 0 {
		return "必填品类 SKU 为空:" + strings.Join(empty, ",")
	}
	for _, ssd := range sel.SSDs {
		if ssd.Quantity <= 0 {
			return fmt.Sprintf("ssd %q 数量=%d 必须为正", ssd.SKU, ssd.Quantity)
		}
	}
	if sel.GPU != nil && strings.TrimSpace(*sel.GPU) == "" {
		return "gpu 显式为 null 或给出 SKU,不接受空串"
	}
	return ""
}

// assertBudgetWindow 与 buildharness.inBudgetWindow 同口径:分整数闭区间。
func assertBudgetWindow(spec schemas.RequirementSpec, quote validate.Quote) string {
	quote = validate.BudgetQuote(spec, quote)
	total, ok := parseCents(quote.TotalCNY)
	if !ok {
		return fmt.Sprintf("合计 %q 无法解析为金额", quote.TotalCNY)
	}
	budget := int64(spec.BudgetCNY) * 100
	flex := int64(math.Round(float64(budget) * spec.BudgetFlex))
	if total < budget-flex || total > budget+flex {
		return fmt.Sprintf("合计 %s 落在 [%s, %s] 之外(预算 %d 弹性 %.2f)",
			quote.TotalCNY, formatCents(budget-flex), formatCents(budget+flex), spec.BudgetCNY, spec.BudgetFlex)
	}
	return ""
}

func assertBrandPref(spec schemas.RequirementSpec, sel schemas.BuildSelection, snap SnapshotView) string {
	var failures []string
	if spec.ConstraintStrengths["brand_pref.cpu"] != "prefer" && spec.BrandPref.CPU != "" && spec.BrandPref.CPU != schemas.CPUBrandAny {
		vendor := snap.CPUVendorBySKU[sel.CPU]
		if vendor == "" || vendor != string(spec.BrandPref.CPU) {
			failures = append(failures, fmt.Sprintf("cpu %q 推断厂商=%q,要求 %s", sel.CPU, vendor, spec.BrandPref.CPU))
		}
	}
	if spec.ConstraintStrengths["brand_pref.gpu"] != "prefer" && spec.BrandPref.GPU != "" && spec.BrandPref.GPU != schemas.GPUBrandAny {
		if sel.GPU == nil {
			failures = append(failures, fmt.Sprintf("gpu 为 null,但要求家族 %s", spec.BrandPref.GPU))
		} else if family := snap.GPUFamilyBySKU[*sel.GPU]; family == "" || family != string(spec.BrandPref.GPU) {
			failures = append(failures, fmt.Sprintf("gpu %q 推断家族=%q,要求 %s", *sel.GPU, family, spec.BrandPref.GPU))
		}
	}
	return strings.Join(failures, ";")
}

func assertQuoteIntegrity(quote validate.Quote) string {
	total, ok := parseCents(quote.TotalCNY)
	if !ok {
		return fmt.Sprintf("合计 %q 无法解析", quote.TotalCNY)
	}
	var sum int64
	for _, line := range quote.Lines {
		if line.SubtotalCNY == nil {
			continue // 缺价行已由 A4 拦截
		}
		cents, ok := parseCents(*line.SubtotalCNY)
		if !ok {
			return fmt.Sprintf("行 %q 小计 %q 无法解析", line.SKU, *line.SubtotalCNY)
		}
		sum += cents
	}
	if sum != total {
		return fmt.Sprintf("分项小计之和 %s ≠ 合计 %s", formatCents(sum), quote.TotalCNY)
	}
	return ""
}

func selectionSKUs(sel schemas.BuildSelection) []string {
	skus := []string{sel.CPU, sel.Motherboard, sel.Memory, sel.PSU, sel.Case, sel.Cooler}
	for _, ssd := range sel.SSDs {
		skus = append(skus, ssd.SKU)
	}
	if sel.GPU != nil {
		skus = append(skus, *sel.GPU)
	}
	return skus
}

func overallStatus(result buildharness.BuildResult) string {
	if result.Result.Report.OverallStatus == "" {
		return "(空)"
	}
	return string(result.Result.Report.OverallStatus)
}

// parseCents 解析 "1299.00" 形式的金额为分整数;与 validate 的分整数口径一致。
func parseCents(text string) (int64, bool) {
	parts := strings.Split(text, ".")
	if len(parts) > 2 {
		return 0, false
	}
	yuan, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || yuan < 0 {
		return 0, false
	}
	var fen int64
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return 0, false
		}
		fen, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, false
		}
		for i := len(parts[1]); i < 2; i++ {
			fen *= 10
		}
	}
	return yuan*100 + fen, true
}

func formatCents(cents int64) string {
	return strconv.FormatInt(cents/100, 10) + "." + fmt.Sprintf("%02d", cents%100)
}
