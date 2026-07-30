// Package rules 实现 P1 兼容性规则引擎:纯 Go、零 LLM、零 IO。
// 只允许 import 标准库与 internal/schemas(golangci-lint depguard 强制)。
// 规则条目与判定口径的唯一权威出处:docs/装机Agent设计方案.md §四.1 / §五。
//
// 执行模型(冻结):12 条规则按 schemas.AllRuleIDs 固定顺序全部执行、不短路;
// Go error 仅表示输入结构非法,规则判定结果(pass|fail|unknown)一律进 CheckResult。
package rules

import (
	"fmt"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// Rule 单条兼容性规则。Check 不得 panic、不得做 IO;
// 字段缺失时输出 outcome=unknown 并列出 missing fields,而非报错。
type Rule interface {
	ID() schemas.RuleID
	Check(schemas.ResolvedBuild) schemas.CheckResult
}

// Engine 按固定顺序执行注册规则并聚合报告。
type Engine struct {
	rules []Rule
}

// NewDefaultEngine 返回装配了全部 12 条规则的引擎,顺序与 schemas.AllRuleIDs 一致。
func NewDefaultEngine() *Engine {
	return &Engine{rules: []Rule{
		socketMatchRule{},
		chipsetSupportRule{},
		memoryGenerationRule{},
		memorySpeedRule{},
		gpuClearanceRule{},
		coolerClearanceRule{},
		psuHeadroomRule{},
		formFactorSupportRule{},
		m2SlotCapacityRule{},
		gpuPowerConnectorsRule{},
		displayOutputRule{},
		coolerThermalCapacityRule{},
	}}
}

// Validate 对 ResolvedBuild 执行全部规则。error 仅在输入结构非法时返回
// (此时不产出报告);正常路径永远返回完整的 ValidationReport。
func (e *Engine) Validate(b schemas.ResolvedBuild) (schemas.ValidationReport, error) {
	if err := validateStructure(b); err != nil {
		return schemas.ValidationReport{}, err
	}
	checks := make([]schemas.CheckResult, 0, len(e.rules))
	for _, r := range e.rules {
		checks = append(checks, r.Check(b))
	}
	return schemas.ValidationReport{
		BuildRef:      b.BuildRef,
		OverallStatus: schemas.AggregateOverallStatus(checks),
		Checks:        checks,
	}, nil
}

// validateStructure 输入结构合法性(schema error 类):这些问题应在
// schemas/store 层被拦截,进到这里说明调用方违约,必须显式报错而非静默 unknown。
func validateStructure(b schemas.ResolvedBuild) error {
	if b.BuildRef == "" {
		return fmt.Errorf("rules: build_ref 不得为空")
	}
	if len(b.SSDs) == 0 {
		return fmt.Errorf("rules: ssd 为必选部件,不得为空")
	}
	for i, s := range b.SSDs {
		if s.Quantity <= 0 {
			return fmt.Errorf("rules: ssd[%d].quantity 必须为正整数,得到 %d", i, s.Quantity)
		}
	}
	return nil
}
