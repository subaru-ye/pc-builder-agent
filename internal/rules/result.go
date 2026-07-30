package rules

import (
	"sort"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// missing fields 命名约定(冻结):`<品类>.<canonical 字段名>`,如 "cpu.socket"。
// unknown 结果的 MissingFields 必须非空且排序;observed 只放已知的稳定键值。

func pass(id schemas.RuleID, observed map[string]any, detail string) schemas.CheckResult {
	return schemas.CheckResult{
		RuleID:        id,
		Outcome:       schemas.OutcomePass,
		Severity:      schemas.SeverityNone,
		Observed:      observed,
		MissingFields: []string{},
		Detail:        detail,
	}
}

func fail(id schemas.RuleID, sev schemas.Severity, observed map[string]any, detail string) schemas.CheckResult {
	return schemas.CheckResult{
		RuleID:        id,
		Outcome:       schemas.OutcomeFail,
		Severity:      sev,
		Observed:      observed,
		MissingFields: []string{},
		Detail:        detail,
	}
}

func unknown(id schemas.RuleID, observed map[string]any, missing []string, detail string) schemas.CheckResult {
	sorted := make([]string, len(missing))
	copy(sorted, missing)
	sort.Strings(sorted)
	return schemas.CheckResult{
		RuleID:        id,
		Outcome:       schemas.OutcomeUnknown,
		Severity:      schemas.SeverityNone,
		Observed:      observed,
		MissingFields: sorted,
		Detail:        detail,
	}
}
