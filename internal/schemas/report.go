package schemas

// RuleID 兼容性规则标识,12 条固定(设计方案 §五,2026-07-28 冻结)。
type RuleID string

const (
	RuleSocketMatch           RuleID = "SOCKET_MATCH"
	RuleChipsetSupport        RuleID = "CHIPSET_SUPPORT"
	RuleMemoryGeneration      RuleID = "MEMORY_GENERATION"
	RuleMemorySpeed           RuleID = "MEMORY_SPEED"
	RuleGPUClearance          RuleID = "GPU_CLEARANCE"
	RuleCoolerClearance       RuleID = "COOLER_CLEARANCE"
	RulePSUHeadroom           RuleID = "PSU_HEADROOM"
	RuleFormFactorSupport     RuleID = "FORM_FACTOR_SUPPORT"
	RuleM2SlotCapacity        RuleID = "M2_SLOT_CAPACITY"
	RuleGPUPowerConnectors    RuleID = "GPU_POWER_CONNECTORS"
	RuleDisplayOutput         RuleID = "DISPLAY_OUTPUT"
	RuleCoolerThermalCapacity RuleID = "COOLER_THERMAL_CAPACITY"
)

// AllRuleIDs 12 条规则的固定执行顺序(= 设计方案 §五表内顺序)。
var AllRuleIDs = []RuleID{
	RuleSocketMatch,
	RuleChipsetSupport,
	RuleMemoryGeneration,
	RuleMemorySpeed,
	RuleGPUClearance,
	RuleCoolerClearance,
	RulePSUHeadroom,
	RuleFormFactorSupport,
	RuleM2SlotCapacity,
	RuleGPUPowerConnectors,
	RuleDisplayOutput,
	RuleCoolerThermalCapacity,
}

// Outcome 单条规则判定结果。
type Outcome string

const (
	OutcomePass    Outcome = "pass"
	OutcomeFail    Outcome = "fail"
	OutcomeUnknown Outcome = "unknown"
)

// Severity 单条结果的级别:pass/unknown → none;fail → 规则级别(warning|error)。
type Severity string

const (
	SeverityNone    Severity = "none"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// OverallStatus 报告聚合结论。
type OverallStatus string

const (
	OverallPass   OverallStatus = "pass"
	OverallReview OverallStatus = "review"
	OverallFail   OverallStatus = "fail"
)

// CheckResult 单条规则输出。Observed 只放稳定键值(golden 可比对);
// MissingFields 必须排序;Detail 为自然语言,不进 golden 断言。
type CheckResult struct {
	RuleID        RuleID         `json:"rule_id"`
	Outcome       Outcome        `json:"outcome"`
	Severity      Severity       `json:"severity"`
	Observed      map[string]any `json:"observed"`
	MissingFields []string       `json:"missing_fields"`
	Detail        string         `json:"detail"`
}

// ValidationReport 规则引擎输出:12 条 CheckResult 按固定顺序 + 聚合结论。
type ValidationReport struct {
	BuildRef      string        `json:"build_ref"`
	OverallStatus OverallStatus `json:"overall_status"`
	Checks        []CheckResult `json:"checks"`
}

// AggregateOverallStatus 报告聚合(冻结):存在 error 级 fail → fail;
// 否则存在 warning 级 fail 或任一 unknown → review;全部 pass → pass。
func AggregateOverallStatus(checks []CheckResult) OverallStatus {
	status := OverallPass
	for _, c := range checks {
		if c.Outcome == OutcomeFail && c.Severity == SeverityError {
			return OverallFail
		}
		if c.Outcome == OutcomeFail || c.Outcome == OutcomeUnknown {
			status = OverallReview
		}
	}
	return status
}
