package planning

import (
	"encoding/json"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

var unsupportedThermalTerms = []string{
	"足够", "充足", "绰绰有余", "压制", "压得住", "完全合理", "没问题", "不影响", "保障", "保证",
}

func unsupportedThermalClaim(text string) bool {
	if !strings.Contains(text, "散热") && !strings.Contains(text, "水冷") &&
		!strings.Contains(text, "风冷") && !strings.Contains(text, "温控") {
		return false
	}
	for _, term := range unsupportedThermalTerms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

// normalizeUnresolvedClaims 保留运行轨迹中的模型原文，但移除保存方案中没有证据的散热保证。
func (x *execution) normalizeUnresolvedClaims() {
	thermalUnresolved := false
	if x.result.Validation != nil {
		for _, check := range x.result.Validation.Checks {
			if check.RuleID == schemas.RuleCoolerThermalCapacity && check.Outcome != schemas.OutcomePass {
				thermalUnresolved = true
				break
			}
		}
	}
	if !thermalUnresolved {
		return
	}

	filtered := x.result.Issues[:0]
	for _, issue := range x.result.Issues {
		if !unsupportedThermalClaim(issue) {
			filtered = append(filtered, issue)
		}
	}
	x.result.Issues = filtered

	for i, assumption := range x.result.Assumptions {
		if unsupportedThermalClaim(assumption) {
			x.result.Assumptions[i] = "散热能力缺少可核验规格，当前不能确认温控表现。"
		}
	}

	var draft map[string]any
	if json.Unmarshal(x.result.Draft, &draft) != nil {
		return
	}
	rationale, _ := draft["rationale"].(map[string]any)
	text, _ := rationale["cooler"].(string)
	if !unsupportedThermalClaim(text) {
		return
	}
	rationale["cooler"] = "散热能力缺少可核验规格，当前不能确认温控表现。"
	if raw, err := json.Marshal(draft); err == nil {
		x.result.Draft = raw
	}
}
