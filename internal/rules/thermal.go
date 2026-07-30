package rules

import (
	"fmt"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// displayOutputRule 规则 #11 DISPLAY_OUTPUT(错误级):整机必须有显示输出:
// 有独显直接通过;gpu 显式为 null 时要求 CPU 核显存在(has_igpu 三态:true/false/未知)。
type displayOutputRule struct{}

func (displayOutputRule) ID() schemas.RuleID { return schemas.RuleDisplayOutput }

func (displayOutputRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	if b.GPU != nil {
		return pass(schemas.RuleDisplayOutput, map[string]any{"gpu_present": true}, "有独显,具备显示输出")
	}
	observed := map[string]any{"gpu_present": false}
	if b.CPU.HasIGPU == nil {
		return unknown(schemas.RuleDisplayOutput, observed,
			[]string{"cpu.has_igpu"}, "无独显且核显情况未知,无法判定显示输出")
	}
	observed["cpu_has_igpu"] = *b.CPU.HasIGPU
	if *b.CPU.HasIGPU {
		return pass(schemas.RuleDisplayOutput, observed, "无独显但 CPU 带核显,具备显示输出")
	}
	return fail(schemas.RuleDisplayOutput, schemas.SeverityError, observed,
		"无独显且 CPU 无核显,整机没有显示输出")
}

// coolerThermalCapacityRule 规则 #12 COOLER_THERMAL_CAPACITY(警告级):
// 散热器解热能力 ≥ CPU TDP;等于边界即通过,不足只产生 warning。
type coolerThermalCapacityRule struct{}

func (coolerThermalCapacityRule) ID() schemas.RuleID { return schemas.RuleCoolerThermalCapacity }

func (coolerThermalCapacityRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	observed := map[string]any{}
	var missing []string
	if b.Cooler.CoolingCapacityW == nil {
		missing = append(missing, "cooler.cooling_capacity_w")
	} else {
		observed["cooler_cooling_capacity_w"] = *b.Cooler.CoolingCapacityW
	}
	if b.CPU.TDPW == nil {
		missing = append(missing, "cpu.tdp_w")
	} else {
		observed["cpu_tdp_w"] = *b.CPU.TDPW
	}
	if len(missing) > 0 {
		return unknown(schemas.RuleCoolerThermalCapacity, observed, missing, "散热能力字段缺失,无法判定")
	}
	if *b.Cooler.CoolingCapacityW >= *b.CPU.TDPW {
		return pass(schemas.RuleCoolerThermalCapacity, observed, "散热器解热能力不低于 CPU TDP")
	}
	return fail(schemas.RuleCoolerThermalCapacity, schemas.SeverityWarning, observed,
		fmt.Sprintf("散热器解热 %dW 低于 CPU TDP %dW,高负载可能撞温度墙",
			*b.Cooler.CoolingCapacityW, *b.CPU.TDPW))
}
