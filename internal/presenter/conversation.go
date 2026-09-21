package presenter

import (
	"context"
	"fmt"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// ConversationSummary 只使用指定版本及其父版本，不读会话当前草稿或原始工具回复。
func (s *Service) ConversationSummary(ctx context.Context, sessionID string, version int) (string, error) {
	view, err := s.Build(ctx, sessionID, version)
	if err != nil {
		return "", err
	}
	var parent *BuildView
	if view.Summary.ParentVersion != nil {
		previous, err := s.Build(ctx, sessionID, *view.Summary.ParentVersion)
		if err != nil {
			return "", err
		}
		parent = &previous
	}
	return RenderConversation(view, parent), nil
}

var conversationCategories = map[schemas.Category]string{
	schemas.CategoryCPU: "处理器", schemas.CategoryGPU: "显卡", schemas.CategoryMotherboard: "主板",
	schemas.CategoryMemory: "内存", schemas.CategorySSD: "固态硬盘", schemas.CategoryPSU: "电源",
	schemas.CategoryCase: "机箱", schemas.CategoryCooler: "散热器",
}

func conversationName(part PartLine) string {
	if part.Name == "" || part.Name == part.SKU {
		return conversationCategories[part.Category] + "（型号待核实）"
	}
	// 产品名称来自目录，不让名称中的 Markdown 改变消息结构。
	return strings.NewReplacer("\n", " ", "\r", " ", "*", "", "`", "", "[", "", "]", "", "<", "", ">", "").Replace(part.Name)
}

func quoteAmount(view BuildView) (string, string) {
	if view.Quote.BudgetBasis == "new_purchase" {
		if view.Quote.PurchaseTotalCNY == nil {
			return "本次新购价格尚未齐全", ""
		}
		return "本次新购参考总价", *view.Quote.PurchaseTotalCNY
	}
	if view.Quote.MissingCount > 0 {
		return "已知价格小计", view.Quote.TotalCNY
	}
	return "参考总价", view.Quote.TotalCNY
}

// RenderConversation 不复述 SKU、原始 rationale、规则 detail 或内部持久化信息。
func RenderConversation(view BuildView, parent *BuildView) string {
	var b strings.Builder
	if parent == nil {
		fmt.Fprintf(&b, "已生成方案（v%d）。\n\n", view.Summary.Version)
		var core []string
		for _, p := range view.Parts {
			if p.Category == schemas.CategoryCPU || p.Category == schemas.CategoryGPU || p.Category == schemas.CategoryMemory {
				core = append(core, conversationName(p))
			}
		}
		if len(core) > 0 {
			fmt.Fprintf(&b, "核心搭配：**%s**。\n\n", strings.Join(core, " + "))
		}
	} else {
		fmt.Fprintf(&b, "已更新方案（v%d）。\n\n", view.Summary.Version)
		currentSpec, currentErr := schemas.DecodeRequirementSpec(view.Requirement)
		previousSpec, previousErr := schemas.DecodeRequirementSpec(parent.Requirement)
		if currentErr == nil && previousErr == nil {
			var reasons []string
			if currentSpec.BudgetCNY != previousSpec.BudgetCNY {
				reasons = append(reasons, fmt.Sprintf("预算由 ¥%d 调整为 ¥%d", previousSpec.BudgetCNY, currentSpec.BudgetCNY))
			}
			if currentSpec.BrandPref.GPU != previousSpec.BrandPref.GPU {
				brand := map[string]string{"amd": "AMD", "nvidia": "NVIDIA", "intel": "Intel"}[string(currentSpec.BrandPref.GPU)]
				if brand == "" {
					reasons = append(reasons, "显卡品牌不再限定")
				} else if currentSpec.ConstraintStrengths["brand_pref.gpu"] == "must" {
					reasons = append(reasons, "显卡必须使用 "+brand)
				} else {
					reasons = append(reasons, "显卡优先选择 "+brand)
				}
			}
			if len(reasons) > 0 {
				fmt.Fprintf(&b, "本次调整：%s。\n\n", strings.Join(reasons, "；"))
			}
		}
		before := make(map[schemas.Category][]PartLine)
		after := make(map[schemas.Category][]PartLine)
		for _, p := range parent.Parts {
			before[p.Category] = append(before[p.Category], p)
		}
		for _, p := range view.Parts {
			after[p.Category] = append(after[p.Category], p)
		}
		changed := false
		for _, category := range schemas.AllCategories {
			old, next := before[category], after[category]
			key := func(parts []PartLine) string {
				var keys []string
				for _, p := range parts {
					keys = append(keys, fmt.Sprintf("%s:%d:%t", p.SKU, p.Quantity, p.Owned))
				}
				return strings.Join(keys, "|")
			}
			if key(old) == key(next) {
				continue
			}
			changed = true
			names := func(parts []PartLine) string {
				var values []string
				for _, p := range parts {
					name := conversationName(p)
					if p.Quantity > 1 {
						name += fmt.Sprintf(" ×%d", p.Quantity)
					}
					if p.Owned {
						name += "（已有）"
					}
					values = append(values, name)
				}
				if len(values) == 0 {
					return "无"
				}
				return strings.Join(values, "、")
			}
			fmt.Fprintf(&b, "- **%s**：%s → %s\n", conversationCategories[category], names(old), names(next))
		}
		if !changed {
			b.WriteString("配件保持不变，本次更新了需求或报价。\n")
		}
		b.WriteString("\n")
	}
	label, amount := quoteAmount(view)
	if amount != "" {
		fmt.Fprintf(&b, "%s **¥%s**", label, amount)
	} else {
		b.WriteString(label)
	}
	if parent != nil {
		_, old := quoteAmount(*parent)
		x, ok := ParseFen(amount)
		y, valid := ParseFen(old)
		if view.Quote.BudgetBasis != parent.Quote.BudgetBasis {
			b.WriteString("（预算口径已变更，不直接比较金额）")
		} else if ok && valid && view.Quote.MissingCount == 0 && parent.Quote.MissingCount == 0 {
			if x < y {
				fmt.Fprintf(&b, "，比上一版减少 **¥%s**", FormatFen(y-x))
			} else if x > y {
				fmt.Fprintf(&b, "，比上一版增加 **¥%s**", FormatFen(x-y))
			}
		}
	}
	b.WriteString("。\n")
	var notices []string
	if delta, ok := ParseFen(view.Quote.BudgetDeltaCNY); ok && delta < 0 {
		notices = append(notices, "超出预算 ¥"+FormatFen(-delta))
	}
	if view.Quote.MissingCount > 0 {
		notices = append(notices, fmt.Sprintf("%d 项价格缺失，实际支出尚不能确定", view.Quote.MissingCount))
	}
	labels := map[schemas.RuleID]string{
		schemas.RuleSocketMatch: "处理器与主板接口", schemas.RuleChipsetSupport: "主板对处理器的支持", schemas.RuleMemoryGeneration: "内存代际", schemas.RuleMemorySpeed: "内存频率",
		schemas.RuleGPUClearance: "显卡安装空间", schemas.RuleCoolerClearance: "散热器安装空间", schemas.RulePSUHeadroom: "电源功率余量", schemas.RuleFormFactorSupport: "主板与机箱尺寸",
		schemas.RuleM2SlotCapacity: "固态硬盘插槽数量", schemas.RuleGPUPowerConnectors: "显卡供电接口", schemas.RuleDisplayOutput: "显示输出", schemas.RuleCoolerThermalCapacity: "散热能力",
	}
	var failed, unknown []string
	for _, check := range view.Validation.Checks {
		label := labels[check.RuleID]
		if label == "" {
			label = "配件适配情况"
		}
		switch check.Outcome {
		case schemas.OutcomeFail:
			failed = append(failed, label)
		case schemas.OutcomeUnknown:
			unknown = append(unknown, label)
		}
	}
	if len(failed) > 0 {
		notices = append(notices, "需要调整："+strings.Join(failed, "、"))
	}
	if len(unknown) > 0 {
		notices = append(notices, "资料不足，仍需核实："+strings.Join(unknown, "、"))
	}
	if view.Validation.OverallStatus != schemas.OverallPass && len(failed)+len(unknown) == 0 {
		notices = append(notices, "配件适配情况尚未确认，请查看配置详情")
	}
	if spec, err := schemas.DecodeRequirementSpec(view.Requirement); err == nil {
		if string(spec.NoisePref) == "silent" {
			if spec.ConstraintStrengths["noise_pref"] == "must" {
				notices = append(notices, "静音是必须条件，但整机静音表现尚缺少实测依据，不能确认满足")
			} else {
				notices = append(notices, "整机静音表现仍缺少实测依据，购买前需核对")
			}
		}
	}
	if len(notices) > 0 {
		b.WriteString("\n需要留意：\n")
		for _, notice := range notices {
			fmt.Fprintf(&b, "- %s。\n", notice)
		}
	}
	if view.Quote.SnapshotDate != "" {
		fmt.Fprintf(&b, "\n价格快照 %s，购买前请重新核价。", view.Quote.SnapshotDate)
	}
	return strings.TrimSpace(b.String())
}
