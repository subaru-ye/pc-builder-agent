package product

import (
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
)

// buildFailureProblem projects deterministic business reasons, never the model's
// raw text or Decision.Message (which may contain internal validation output).
func buildFailureProblem(decision *buildharness.Decision, requestID string) Problem {
	title := "本轮未生成配置"
	detail := "本轮没有得到通过核验的配置。当前需求已保存，已有配置版本保持不变。可以重试；如仍未成功，请补充用途或可核验的商品资料。"
	if decision == nil {
		return NewProblem("generation_failed", title, 422, detail, requestID)
	}
	fields := buildFailureFields(decision.Fields)
	switch decision.Kind + "/" + decision.Reason {
	case "data_unavailable/requirement_evidence_missing":
		title = "必须条件仍需核验"
		detail = "当前目录缺少核验" + fields + "的证据，本轮未生成配置。当前需求及其必须条件已保存，已有配置版本保持不变。请补充准确型号、规格或实测资料后继续；也可以在需求面板纠正理解。"
	case "clarify/requirement_semantics_missing", "data_unavailable/requirement_semantics_missing":
		title = "需要确认补充说明的含义"
		detail = "已保存补充说明，但还需确认它是用途事实、背景说明，还是配置必须满足的条件。请在需求面板选择信息用途，或在聊天中说明，随后继续生成。已有配置版本保持不变。"
	case "clarify/missing_owned_information":
		title = "已有配件信息尚不完整"
		detail = "需要补充或确认" + fields + "，才能核验已有件与新配置。当前需求已保存，已有配置版本保持不变。请在聊天或需求面板补充后继续。"
	case "clarify/mandatory_locked_conflict", "clarify/owned_lock_conflict":
		title = "已有配件与必须条件有冲突"
		detail = "请确认需要保留的已有配件及" + fields + "。当前需求和已有配置版本均已保留；确认后继续，不会自动替换锁定配件或取消必须条件。"
	case "data_unavailable/owned_model_unresolved":
		title = "已有配件型号仍需核验"
		detail = "当前目录无法唯一核验" + fields + "。请补充完整型号或规格资料后继续。当前需求已保存，已有配置版本保持不变。"
	case "catalog_infeasible/budget_lower_bound", "catalog_infeasible/platform_budget_lower_bound":
		title = "当前目录无法满足预算"
		detail = "按当前目录的可核验报价，满足要求的配置超过预算上限。当前需求已保存，已有配置版本保持不变。可核对预算或补充可核验商品资料；这不代表整个市场没有方案。"
	case "data_unavailable/catalog_category_missing":
		title = "当前目录缺少可核验配件"
		detail = "当前目录缺少" + fields + "候选，无法核验完整配置。当前需求已保存，已有配置版本保持不变。可补充准确型号和报价资料后继续。"
	case "data_unavailable/required_candidates_missing", "data_unavailable/required_gpu_missing":
		title = "当前目录缺少满足必须条件的候选"
		detail = "当前目录没有可核验且满足" + fields + "的候选。当前需求及其必须条件已保存，已有配置版本保持不变。请补充规格或候选资料后继续；这不代表整个市场没有方案。"
	case "data_unavailable/selected_price_missing":
		title = "配置报价仍需核验"
		detail = "所选配件缺少可核验报价，本轮未生成配置。当前需求已保存，已有配置版本保持不变。请补充报价资料后继续。"
	case "search_exhausted/no_verified_solution":
		title = "本轮未找到通过核验的配置"
		detail = "当前搜索尚未找到可交付方案，不能据此认定整个目录或市场无解。当前需求已保存，已有配置版本保持不变。可重试，或补充可核验的商品资料后继续。"
	case "invalid_output/mandatory_selection_missing":
		title = "生成结果尚未满足必须条件"
		detail = "本轮配置没有完整满足" + fields + "，因此未保存为新版本。当前需求及其必须条件已保存，已有配置版本保持不变。可以重新生成，或补充可核验的候选资料。"
	}
	return NewProblem("generation_failed", title, 422, detail, requestID)
}

func buildFailureFields(fields []string) string {
	labels := map[string]string{
		"budget_basis": "预算口径", "budget_cny": "预算", "noise_pref": "静音要求", "appearance": "外观要求", "notes": "补充条件",
		"brand_pref.cpu": "CPU 品牌要求", "brand_pref.gpu": "显卡品牌要求", "size_pref": "尺寸要求",
		"cpu": "CPU", "gpu": "显卡", "motherboard": "主板", "memory": "内存", "ssd": "SSD", "psu": "电源", "case": "机箱", "cooler": "散热器",
	}
	var names []string
	for _, field := range fields {
		name := labels[field]
		if strings.HasPrefix(field, "owned_parts.") && strings.HasSuffix(field, ".model") {
			category := strings.TrimSuffix(strings.TrimPrefix(field, "owned_parts."), ".model")
			if label := labels[category]; label != "" {
				name = "已有" + label + "完整型号"
			}
		}
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "相关要求"
	}
	return strings.Join(names, "、")
}
