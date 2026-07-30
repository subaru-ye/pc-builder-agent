package schemas

import "testing"

func check(id RuleID, o Outcome, s Severity) CheckResult {
	return CheckResult{RuleID: id, Outcome: o, Severity: s}
}

func TestAggregateOverallStatus(t *testing.T) {
	cases := []struct {
		name   string
		checks []CheckResult
		want   OverallStatus
	}{
		{"全部 pass", []CheckResult{
			check(RuleSocketMatch, OutcomePass, SeverityNone),
			check(RulePSUHeadroom, OutcomePass, SeverityNone),
		}, OverallPass},
		{"warning 级 fail → review", []CheckResult{
			check(RuleSocketMatch, OutcomePass, SeverityNone),
			check(RuleMemorySpeed, OutcomeFail, SeverityWarning),
		}, OverallReview},
		{"unknown → review", []CheckResult{
			check(RuleSocketMatch, OutcomePass, SeverityNone),
			check(RuleGPUClearance, OutcomeUnknown, SeverityNone),
		}, OverallReview},
		{"error 级 fail → fail", []CheckResult{
			check(RuleSocketMatch, OutcomeFail, SeverityError),
		}, OverallFail},
		{"error 优先于 unknown 与 warning", []CheckResult{
			check(RuleMemorySpeed, OutcomeFail, SeverityWarning),
			check(RuleGPUClearance, OutcomeUnknown, SeverityNone),
			check(RuleSocketMatch, OutcomeFail, SeverityError),
		}, OverallFail},
		{"空检查列表 → pass", nil, OverallPass},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AggregateOverallStatus(c.checks); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestAllRuleIDsOrder(t *testing.T) {
	want := []RuleID{
		"SOCKET_MATCH", "CHIPSET_SUPPORT", "MEMORY_GENERATION", "MEMORY_SPEED",
		"GPU_CLEARANCE", "COOLER_CLEARANCE", "PSU_HEADROOM", "FORM_FACTOR_SUPPORT",
		"M2_SLOT_CAPACITY", "GPU_POWER_CONNECTORS", "DISPLAY_OUTPUT", "COOLER_THERMAL_CAPACITY",
	}
	if len(AllRuleIDs) != len(want) {
		t.Fatalf("规则数应为 %d,得到 %d", len(want), len(AllRuleIDs))
	}
	for i, id := range want {
		if AllRuleIDs[i] != id {
			t.Errorf("第 %d 条规则应为 %s,得到 %s", i+1, id, AllRuleIDs[i])
		}
	}
}
