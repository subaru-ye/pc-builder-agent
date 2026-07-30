package rules

import (
	"fmt"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// memoryGenerationRule 规则 #3 MEMORY_GENERATION(错误级):内存代际 == 主板支持代际。
type memoryGenerationRule struct{}

func (memoryGenerationRule) ID() schemas.RuleID { return schemas.RuleMemoryGeneration }

func (memoryGenerationRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	observed := map[string]any{}
	var missing []string
	if b.Memory.Generation == nil {
		missing = append(missing, "memory.generation")
	} else {
		observed["memory_generation"] = *b.Memory.Generation
	}
	if b.Motherboard.MemoryGeneration == nil {
		missing = append(missing, "motherboard.memory_generation")
	} else {
		observed["motherboard_memory_generation"] = *b.Motherboard.MemoryGeneration
	}
	if len(missing) > 0 {
		return unknown(schemas.RuleMemoryGeneration, observed, missing, "内存代际字段缺失,无法判定")
	}
	if *b.Memory.Generation == *b.Motherboard.MemoryGeneration {
		return pass(schemas.RuleMemoryGeneration, observed, "内存代际与主板一致")
	}
	return fail(schemas.RuleMemoryGeneration, schemas.SeverityError, observed,
		fmt.Sprintf("内存代际 %s 与主板支持代际 %s 不匹配", *b.Memory.Generation, *b.Motherboard.MemoryGeneration))
}

// memorySpeedRule 规则 #4 MEMORY_SPEED(警告级):内存额定频率 > 主板标称上限时告警。
// 等于上限即通过(设计方案 §五冻结:上限比较类等于边界不告警)。
type memorySpeedRule struct{}

func (memorySpeedRule) ID() schemas.RuleID { return schemas.RuleMemorySpeed }

func (memorySpeedRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	observed := map[string]any{}
	var missing []string
	if b.Memory.SpeedMTS == nil {
		missing = append(missing, "memory.speed_mts")
	} else {
		observed["memory_speed_mts"] = *b.Memory.SpeedMTS
	}
	if b.Motherboard.MemorySpeedMaxMTS == nil {
		missing = append(missing, "motherboard.memory_speed_max_mts")
	} else {
		observed["motherboard_memory_speed_max_mts"] = *b.Motherboard.MemorySpeedMaxMTS
	}
	if len(missing) > 0 {
		return unknown(schemas.RuleMemorySpeed, observed, missing, "内存频率字段缺失,无法判定")
	}
	if *b.Memory.SpeedMTS <= *b.Motherboard.MemorySpeedMaxMTS {
		return pass(schemas.RuleMemorySpeed, observed, "内存额定频率不超过主板标称上限")
	}
	return fail(schemas.RuleMemorySpeed, schemas.SeverityWarning, observed,
		fmt.Sprintf("内存额定 %d MT/s 超过主板标称上限 %d MT/s,可能需要降频运行",
			*b.Memory.SpeedMTS, *b.Motherboard.MemorySpeedMaxMTS))
}
