// Package presenter 提供 CLI 与产品 API 共用的确定性配置展示、diff 和 Markdown 渲染。
package presenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type WireSSD struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type WireSelection struct {
	CPU         string    `json:"cpu"`
	Motherboard string    `json:"motherboard"`
	Memory      string    `json:"memory"`
	SSD         []WireSSD `json:"ssd"`
	GPU         *string   `json:"gpu"`
	PSU         string    `json:"psu"`
	Case        string    `json:"case"`
	Cooler      string    `json:"cooler"`
}

type WireDraft struct {
	BuildRef  string            `json:"build_ref"`
	Selection WireSelection     `json:"selection"`
	Rationale map[string]string `json:"rationale"`
}

type BuildRow struct {
	ID        int64
	Version   int
	ParentID  *int64
	CreatedAt time.Time
	Intent    string
	Draft     WireDraft
	Report    schemas.ValidationReport
	Quote     validate.Quote
	Spec      json.RawMessage
}

func DecodeBuild(b store.BuildVersion, spec json.RawMessage) (BuildRow, error) {
	row := BuildRow{ID: b.ID, Version: b.Version, ParentID: b.ParentID, CreatedAt: b.CreatedAt, Intent: "整单生成", Spec: spec}
	if err := json.Unmarshal(b.Draft, &row.Draft); err != nil {
		return BuildRow{}, fmt.Errorf("v%d draft 解码失败: %w", b.Version, err)
	}
	if err := json.Unmarshal(b.Validation, &row.Report); err != nil {
		return BuildRow{}, fmt.Errorf("v%d validation 解码失败: %w", b.Version, err)
	}
	if err := json.Unmarshal(b.Quote, &row.Quote); err != nil {
		return BuildRow{}, fmt.Errorf("v%d quote 解码失败: %w", b.Version, err)
	}
	if len(b.Change) > 0 {
		var change struct {
			Intent string `json:"intent"`
		}
		if err := json.Unmarshal(b.Change, &change); err == nil && change.Intent != "" {
			row.Intent = change.Intent
		} else {
			row.Intent = "改单"
		}
	}
	return row, nil
}

func DecodeBuilds(builds []store.BuildVersion) ([]BuildRow, error) {
	out := make([]BuildRow, 0, len(builds))
	for _, build := range builds {
		row, err := DecodeBuild(build, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (r BuildRow) SKUs() []string {
	sel := r.Draft.Selection
	out := []string{sel.CPU, sel.Motherboard, sel.Memory, sel.PSU, sel.Case, sel.Cooler}
	for _, item := range sel.SSD {
		out = append(out, item.SKU)
	}
	if sel.GPU != nil {
		out = append(out, *sel.GPU)
	}
	return out
}

func (r BuildRow) BudgetCNY() int {
	if len(r.Spec) == 0 {
		return 0
	}
	spec, err := schemas.DecodeRequirementSpec(r.Spec)
	if err != nil {
		return 0
	}
	return spec.BudgetCNY
}

func RenderSessions(sessions []store.SessionSummary) string {
	if len(sessions) == 0 {
		return "还没有任何会话落库过配置版本。\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个会话:\n", len(sessions))
	for _, session := range sessions {
		fmt.Fprintf(&b, "- %s:%d 个版本,最新 v%d(%s)\n", session.SessionID, session.VersionCount,
			session.LatestVersion, session.LatestAt.Local().Format("2006-01-02 15:04"))
	}
	return b.String()
}

func RenderList(session string, rows []BuildRow) string {
	versionByID := make(map[int64]int, len(rows))
	for _, row := range rows {
		versionByID[row.ID] = row.Version
	}
	var b strings.Builder
	fmt.Fprintf(&b, "会话 %s:%d 个版本\n", session, len(rows))
	for _, row := range rows {
		parent := "根"
		if row.ParentID != nil {
			if version, ok := versionByID[*row.ParentID]; ok {
				parent = fmt.Sprintf("v%d", version)
			} else {
				parent = fmt.Sprintf("#%d", *row.ParentID)
			}
		}
		snapshot := row.Quote.SnapshotDate
		if snapshot == "" {
			snapshot = "无"
		}
		fmt.Fprintf(&b, "v%d ← %s  %s  %s  合计 ¥%s(快照 %s,校验 %s)\n", row.Version, parent,
			row.CreatedAt.Local().Format("2006-01-02 15:04"), row.Intent, row.Quote.TotalCNY, snapshot, row.Report.OverallStatus)
	}
	return b.String()
}

func SKURepr(sel WireSelection, category schemas.Category) string {
	switch category {
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
		for i, item := range sel.SSD {
			parts[i] = fmt.Sprintf("%s×%d", item.SKU, item.Quantity)
		}
		return strings.Join(parts, " + ")
	}
	return ""
}

func CategoryFen(q validate.Quote, category schemas.Category) (int, bool) {
	fen := 0
	for _, line := range q.Lines {
		if line.Category != category {
			continue
		}
		if line.SubtotalCNY == nil {
			return 0, false
		}
		value, ok := ParseFen(*line.SubtotalCNY)
		if !ok {
			return 0, false
		}
		fen += value
	}
	return fen, true
}

func RenderDiff(from, to BuildRow) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "版本对比:v%d → v%d\n", from.Version, to.Version)
	for _, category := range schemas.AllCategories {
		before, after := SKURepr(from.Draft.Selection, category), SKURepr(to.Draft.Selection, category)
		if before == after {
			fmt.Fprintf(&b, "- %s: 不变(%s)\n", category, before)
			continue
		}
		beforeFen, beforeOK := CategoryFen(from.Quote, category)
		afterFen, afterOK := CategoryFen(to.Quote, category)
		if beforeOK && afterOK {
			fmt.Fprintf(&b, "- %s: %s → %s(¥%s → ¥%s,%s)\n", category, before, after,
				FormatFen(beforeFen), FormatFen(afterFen), SignedFen(afterFen-beforeFen))
		} else {
			fmt.Fprintf(&b, "- %s: %s → %s(存在缺价,差额未知)\n", category, before, after)
		}
	}
	beforeTotal, beforeOK := ParseFen(from.Quote.TotalCNY)
	afterTotal, afterOK := ParseFen(to.Quote.TotalCNY)
	if beforeOK && afterOK {
		fmt.Fprintf(&b, "合计:¥%s → ¥%s(%s)\n", from.Quote.TotalCNY, to.Quote.TotalCNY, SignedFen(afterTotal-beforeTotal))
	} else {
		fmt.Fprintf(&b, "合计:¥%s → ¥%s\n", from.Quote.TotalCNY, to.Quote.TotalCNY)
	}
	beforeBudget, afterBudget := from.BudgetCNY(), to.BudgetCNY()
	switch {
	case beforeBudget > 0 && afterBudget > 0 && beforeBudget != afterBudget:
		fmt.Fprintf(&b, "预算:¥%d → ¥%d(%+d)\n", beforeBudget, afterBudget, afterBudget-beforeBudget)
	case beforeBudget > 0 && afterBudget > 0:
		fmt.Fprintf(&b, "预算:¥%d(不变)\n", beforeBudget)
	default:
		b.WriteString("预算:需求单解不出预算,略。\n")
	}
	if from.Quote.SnapshotDate != to.Quote.SnapshotDate {
		fmt.Fprintf(&b, "注意:两版本报价快照不同(%s vs %s),差额受快照影响。\n", from.Quote.SnapshotDate, to.Quote.SnapshotDate)
	}
	return b.String(), nil
}

func RenderExport(row BuildRow, names map[string]string) (string, error) {
	return RenderExportWithFreshness(row, names, PriceFreshnessSummary{})
}

// RenderExportWithFreshness 保持 CLI 既有正文格式，并为产品 API 导出追加动态价格时效。
func RenderExportWithFreshness(row BuildRow, names map[string]string, freshness PriceFreshnessSummary) (string, error) {
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
	for _, line := range row.Quote.Lines {
		name := names[line.SKU]
		if name == "" {
			name = "-"
		}
		unit, subtotal := "缺价", "缺价"
		if line.UnitPriceCNY != nil {
			unit = *line.UnitPriceCNY
		}
		if line.SubtotalCNY != nil {
			subtotal = *line.SubtotalCNY
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %s |\n", line.Category, line.SKU, name, line.Quantity, unit, subtotal)
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
		fmt.Fprintf(&b, "\n> 注意:%d 件零件缺价未计入合计(%s)。\n", row.Quote.MissingCount, strings.Join(row.Quote.MissingSKUs, ", "))
	}
	if freshness.Overall != "" {
		message := priceFreshnessMessage(freshness.Overall)
		fmt.Fprintf(&b, "\n> 价格时效:%s", message)
		if freshness.OldestObservedDate != nil {
			fmt.Fprintf(&b, "（最早观察于 %s", *freshness.OldestObservedDate)
			if freshness.MaxAgeDays != nil {
				fmt.Fprintf(&b, "，距今 %d 天", *freshness.MaxAgeDays)
			}
			b.WriteString("）")
		}
		b.WriteString("。\n")
	}
	fmt.Fprintf(&b, "\n## 校验结果\n\n- 总体:%s\n", row.Report.OverallStatus)
	flagged := 0
	for _, check := range row.Report.Checks {
		if check.Outcome == schemas.OutcomePass {
			continue
		}
		fmt.Fprintf(&b, "- %s(%s/%s):%s\n", check.RuleID, check.Outcome, check.Severity, check.Detail)
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

func priceFreshnessMessage(value PriceFreshness) string {
	switch value {
	case PriceFreshnessFresh:
		return "价格观察在 7 天内，仅供参考，非实时价格"
	case PriceFreshnessAging:
		return "价格可能已变化，请购买前重新核价"
	case PriceFreshnessStale:
		return "价格快照已过期，请购买前重新核价"
	default:
		return "部分价格无法关联观察日期，请购买前重新核价"
	}
}

func ParseFen(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	integer, fraction := value, ""
	if i := strings.IndexByte(value, '.'); i >= 0 {
		integer, fraction = value[:i], value[i+1:]
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	if len(fraction) > 2 {
		return 0, false
	}
	var yuan, fen int
	if _, err := fmt.Sscanf(integer, "%d", &yuan); err != nil {
		return 0, false
	}
	if _, err := fmt.Sscanf(fraction, "%d", &fen); err != nil {
		return 0, false
	}
	total := yuan*100 + fen
	if negative {
		total = -total
	}
	return total, true
}

func FormatFen(fen int) string {
	sign := ""
	if fen < 0 {
		sign, fen = "-", -fen
	}
	return fmt.Sprintf("%s%d.%02d", sign, fen/100, fen%100)
}

func SignedFen(fen int) string {
	if fen >= 0 {
		return "+" + FormatFen(fen)
	}
	return FormatFen(fen)
}
