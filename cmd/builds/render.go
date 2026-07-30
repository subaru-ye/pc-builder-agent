package main

// 渲染层(纯函数,无 DB 依赖,可单测):builds 表 JSONB → 回放文本 / diff 文本 / 导出 Markdown。
// 金额一律按「分」整数运算(工程纪律:不用浮点算钱);版本号只用 DB 的 vN,
// draft 内模型自报的 build_ref 仅作展示注记。

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// wireSSD / wireSel / wireDraft:builds.draft JSONB 的解码形态(写入方为
// 校验节点 draftWireJSON,字段口径=设计方案 §四.2,宽松解码即可)。
type wireSSD struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type wireSel struct {
	CPU         string    `json:"cpu"`
	Motherboard string    `json:"motherboard"`
	Memory      string    `json:"memory"`
	SSD         []wireSSD `json:"ssd"`
	GPU         *string   `json:"gpu"`
	PSU         string    `json:"psu"`
	Case        string    `json:"case"`
	Cooler      string    `json:"cooler"`
}

type wireDraft struct {
	BuildRef  string            `json:"build_ref"`
	Selection wireSel           `json:"selection"`
	Rationale map[string]string `json:"rationale"`
}

// buildRow 一个版本解码后的渲染素材。
type buildRow struct {
	ID        int64
	Version   int
	ParentID  *int64
	CreatedAt time.Time
	Intent    string // "整单生成" 或 ChangeRequest intent
	Draft     wireDraft
	Report    schemas.ValidationReport
	Quote     validate.Quote
	Spec      json.RawMessage
}

// decodeBuild builds 表行 + 需求单原文 → 渲染行;JSONB 解不动如实报错。
func decodeBuild(b store.BuildVersion, spec json.RawMessage) (buildRow, error) {
	row := buildRow{
		ID: b.ID, Version: b.Version, ParentID: b.ParentID,
		CreatedAt: b.CreatedAt, Intent: "整单生成", Spec: spec,
	}
	if err := json.Unmarshal(b.Draft, &row.Draft); err != nil {
		return buildRow{}, fmt.Errorf("v%d draft 解码失败: %w", b.Version, err)
	}
	if err := json.Unmarshal(b.Validation, &row.Report); err != nil {
		return buildRow{}, fmt.Errorf("v%d validation 解码失败: %w", b.Version, err)
	}
	if err := json.Unmarshal(b.Quote, &row.Quote); err != nil {
		return buildRow{}, fmt.Errorf("v%d quote 解码失败: %w", b.Version, err)
	}
	if len(b.Change) > 0 {
		var w struct {
			Intent string `json:"intent"`
		}
		if err := json.Unmarshal(b.Change, &w); err == nil && w.Intent != "" {
			row.Intent = w.Intent
		} else {
			row.Intent = "改单"
		}
	}
	return row, nil
}

// decodeBuilds 批量解码(list 用;需求单摘要 list 不展示,传 nil)。
func decodeBuilds(builds []store.BuildVersion) ([]buildRow, error) {
	out := make([]buildRow, 0, len(builds))
	for _, b := range builds {
		row, err := decodeBuild(b, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// skus 本版本 selection 涉及的全部 SKU(export 查名称用)。
func (r buildRow) skus() []string {
	sel := r.Draft.Selection
	out := []string{sel.CPU, sel.Motherboard, sel.Memory, sel.PSU, sel.Case, sel.Cooler}
	for _, s := range sel.SSD {
		out = append(out, s.SKU)
	}
	if sel.GPU != nil {
		out = append(out, *sel.GPU)
	}
	return out
}

// budgetCNY 从需求单解预算;解不出返回 0(优雅降级不崩)。
func (r buildRow) budgetCNY() int {
	if len(r.Spec) == 0 {
		return 0
	}
	spec, err := schemas.DecodeRequirementSpec(r.Spec)
	if err != nil {
		return 0
	}
	return spec.BudgetCNY
}

// renderSessions 会话列表(list 无 -session)。
func renderSessions(sessions []store.SessionSummary) string {
	if len(sessions) == 0 {
		return "还没有任何会话落库过配置版本。\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个会话:\n", len(sessions))
	for _, s := range sessions {
		fmt.Fprintf(&b, "- %s:%d 个版本,最新 v%d(%s)\n",
			s.SessionID, s.VersionCount, s.LatestVersion, s.LatestAt.Local().Format("2006-01-02 15:04"))
	}
	return b.String()
}

// renderList 版本树回放:version/parent/时间/意图/合计/快照。
func renderList(session string, rows []buildRow) string {
	versionByID := make(map[int64]int, len(rows))
	for _, r := range rows {
		versionByID[r.ID] = r.Version
	}
	var b strings.Builder
	fmt.Fprintf(&b, "会话 %s:%d 个版本\n", session, len(rows))
	for _, r := range rows {
		parent := "根"
		if r.ParentID != nil {
			if pv, ok := versionByID[*r.ParentID]; ok {
				parent = fmt.Sprintf("v%d", pv)
			} else {
				parent = fmt.Sprintf("#%d", *r.ParentID) // 父版本不在本会话,按 ID 如实展示
			}
		}
		snapshot := r.Quote.SnapshotDate
		if snapshot == "" {
			snapshot = "无"
		}
		fmt.Fprintf(&b, "v%d ← %s  %s  %s  合计 ¥%s(快照 %s,校验 %s)\n",
			r.Version, parent, r.CreatedAt.Local().Format("2006-01-02 15:04"),
			r.Intent, r.Quote.TotalCNY, snapshot, r.Report.OverallStatus)
	}
	return b.String()
}

// skuRepr 品类在 selection 中的展示值(diff 比对键)。
func skuRepr(sel wireSel, c schemas.Category) string {
	switch c {
	case schemas.CategoryCPU:
		return sel.CPU
	case schemas.CategoryMotherboard:
		return sel.Motherboard
	case schemas.CategoryMemory:
		return sel.Memory
	case schemas.CategoryPSU:
		return sel.PSU
	case schemas.CategoryCase:
		return sel.Case
	case schemas.CategoryCooler:
		return sel.Cooler
	case schemas.CategoryGPU:
		if sel.GPU == nil {
			return "无独显"
		}
		return *sel.GPU
	case schemas.CategorySSD:
		parts := make([]string, len(sel.SSD))
		for i, s := range sel.SSD {
			parts[i] = fmt.Sprintf("%s×%d", s.SKU, s.Quantity)
		}
		return strings.Join(parts, " + ")
	}
	return ""
}

// categoryFen 报价里某品类的小计(分);任何一行缺价则 known=false。
// 品类无报价行(gpu 为 null)按 0 分处理。
func categoryFen(q validate.Quote, c schemas.Category) (fen int, known bool) {
	known = true
	for _, ln := range q.Lines {
		if ln.Category != c {
			continue
		}
		if ln.SubtotalCNY == nil {
			return 0, false
		}
		f, ok := parseFen(*ln.SubtotalCNY)
		if !ok {
			return 0, false
		}
		fen += f
	}
	return fen, known
}

// renderDiff 逐品类文本 diff + 合计差 + 预算变化(设计 §6)。
func renderDiff(from, to buildRow) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "版本对比:v%d → v%d\n", from.Version, to.Version)
	for _, c := range schemas.AllCategories {
		oldRepr, newRepr := skuRepr(from.Draft.Selection, c), skuRepr(to.Draft.Selection, c)
		if oldRepr == newRepr {
			fmt.Fprintf(&b, "- %s: 不变(%s)\n", c, oldRepr)
			continue
		}
		oldFen, oldOK := categoryFen(from.Quote, c)
		newFen, newOK := categoryFen(to.Quote, c)
		if oldOK && newOK {
			fmt.Fprintf(&b, "- %s: %s → %s(¥%s → ¥%s,%s)\n",
				c, oldRepr, newRepr, formatFen(oldFen), formatFen(newFen), signedFen(newFen-oldFen))
		} else {
			fmt.Fprintf(&b, "- %s: %s → %s(存在缺价,差额未知)\n", c, oldRepr, newRepr)
		}
	}

	oldTotal, oldOK := parseFen(from.Quote.TotalCNY)
	newTotal, newOK := parseFen(to.Quote.TotalCNY)
	if oldOK && newOK {
		fmt.Fprintf(&b, "合计:¥%s → ¥%s(%s)\n", from.Quote.TotalCNY, to.Quote.TotalCNY, signedFen(newTotal-oldTotal))
	} else {
		fmt.Fprintf(&b, "合计:¥%s → ¥%s\n", from.Quote.TotalCNY, to.Quote.TotalCNY)
	}

	oldBudget, newBudget := from.budgetCNY(), to.budgetCNY()
	switch {
	case oldBudget > 0 && newBudget > 0 && oldBudget != newBudget:
		fmt.Fprintf(&b, "预算:¥%d → ¥%d(%+d)\n", oldBudget, newBudget, newBudget-oldBudget)
	case oldBudget > 0 && newBudget > 0:
		fmt.Fprintf(&b, "预算:¥%d(不变)\n", oldBudget)
	default:
		b.WriteString("预算:需求单解不出预算,略。\n")
	}

	if from.Quote.SnapshotDate != to.Quote.SnapshotDate {
		fmt.Fprintf(&b, "注意:两版本报价快照不同(%s vs %s),差额受快照影响。\n",
			from.Quote.SnapshotDate, to.Quote.SnapshotDate)
	}
	return b.String(), nil
}

// renderExport Markdown 配置单(用例 E):需求摘要、配置表、合计+快照日期、
// 校验摘要、免责边界(设计 §6 冻结三条)。
func renderExport(row buildRow, names map[string]string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# 装机配置单 v%d\n\n", row.Version)
	fmt.Fprintf(&b, "- 生成时间:%s\n", row.CreatedAt.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "- 来源:%s\n", row.Intent)
	if spec, err := schemas.DecodeRequirementSpec(row.Spec); err == nil {
		fmt.Fprintf(&b, "- 需求:预算 ¥%d,用途 %s", spec.BudgetCNY, spec.UseCase.Type)
		if spec.UseCase.Resolution != "" {
			fmt.Fprintf(&b, "(%s)", spec.UseCase.Resolution)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n| 品类 | SKU | 品牌型号 | 数量 | 单价(¥) | 小计(¥) |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, ln := range row.Quote.Lines {
		name := names[ln.SKU]
		if name == "" {
			name = "-"
		}
		unit, subtotal := "缺价", "缺价"
		if ln.UnitPriceCNY != nil {
			unit = *ln.UnitPriceCNY
		}
		if ln.SubtotalCNY != nil {
			subtotal = *ln.SubtotalCNY
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %s |\n",
			ln.Category, ln.SKU, name, ln.Quantity, unit, subtotal)
	}
	if row.Draft.Selection.GPU == nil {
		b.WriteString("| gpu | -(无独显,核显点亮) | - | - | - | - |\n")
	}

	fmt.Fprintf(&b, "\n**合计:¥%s**", row.Quote.TotalCNY)
	if row.Quote.SnapshotDate != "" {
		fmt.Fprintf(&b, "(价格快照 %s)", row.Quote.SnapshotDate)
	}
	b.WriteString("\n")
	if row.Quote.MissingCount > 0 {
		fmt.Fprintf(&b, "\n> 注意:%d 件零件缺价未计入合计(%s)。\n",
			row.Quote.MissingCount, strings.Join(row.Quote.MissingSKUs, ", "))
	}

	fmt.Fprintf(&b, "\n## 校验结果\n\n- 总体:%s\n", row.Report.OverallStatus)
	flagged := 0
	for _, c := range row.Report.Checks {
		if c.Outcome == schemas.OutcomePass {
			continue
		}
		fmt.Fprintf(&b, "- %s(%s/%s):%s\n", c.RuleID, c.Outcome, c.Severity, c.Detail)
		flagged++
	}
	if flagged == 0 {
		fmt.Fprintf(&b, "- 全部 %d 条规则通过。\n", len(row.Report.Checks))
	}

	b.WriteString("\n## 免责边界\n\n")
	snapshot := row.Quote.SnapshotDate
	if snapshot == "" {
		snapshot = "未知日期"
	}
	fmt.Fprintf(&b, "- 报价为 %s 快照参考价,非实时价格。\n", snapshot)
	b.WriteString("- 兼容性结论基于本库收录参数,下单前请以官方规格页复核。\n")
	b.WriteString("- 本文档不构成购买建议。\n")
	return b.String(), nil
}

// parseFen 精确十进制金额文本 → 分;最多两位小数,解不动返回 false。
func parseFen(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if len(frac) > 2 {
		return 0, false
	}
	var yuan, fen int
	if _, err := fmt.Sscanf(intPart, "%d", &yuan); err != nil {
		return 0, false
	}
	if _, err := fmt.Sscanf(frac, "%d", &fen); err != nil {
		return 0, false
	}
	total := yuan*100 + fen
	if neg {
		total = -total
	}
	return total, true
}

// formatFen 分 → 精确十进制文本(无符号处理由 signedFen 负责)。
func formatFen(fen int) string {
	sign := ""
	if fen < 0 {
		sign = "-"
		fen = -fen
	}
	return fmt.Sprintf("%s%d.%02d", sign, fen/100, fen%100)
}

// signedFen 带显式正负号的金额差(diff 展示)。
func signedFen(fen int) string {
	if fen >= 0 {
		return "+" + formatFen(fen)
	}
	return formatFen(fen)
}
