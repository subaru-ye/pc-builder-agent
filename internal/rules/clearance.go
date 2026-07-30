package rules

import (
	"fmt"
	"slices"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// gpuClearanceRule 规则 #5 GPU_CLEARANCE(错误级):显卡长度 ≤ 机箱显卡限长。
// gpu 显式为 null(无独显)时无物可校,视为 pass;等于限长即通过。
type gpuClearanceRule struct{}

func (gpuClearanceRule) ID() schemas.RuleID { return schemas.RuleGPUClearance }

func (gpuClearanceRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	if b.GPU == nil {
		return pass(schemas.RuleGPUClearance, map[string]any{"gpu_present": false}, "无独显,无需校验限长")
	}
	observed := map[string]any{"gpu_present": true}
	var missing []string
	if b.GPU.LengthMM == nil {
		missing = append(missing, "gpu.length_mm")
	} else {
		observed["gpu_length_mm"] = *b.GPU.LengthMM
	}
	if b.Case.GPULengthMaxMM == nil {
		missing = append(missing, "case.gpu_length_max_mm")
	} else {
		observed["case_gpu_length_max_mm"] = *b.Case.GPULengthMaxMM
	}
	if len(missing) > 0 {
		return unknown(schemas.RuleGPUClearance, observed, missing, "显卡限长字段缺失,无法判定")
	}
	if *b.GPU.LengthMM <= *b.Case.GPULengthMaxMM {
		return pass(schemas.RuleGPUClearance, observed, "显卡长度在机箱限长内")
	}
	return fail(schemas.RuleGPUClearance, schemas.SeverityError, observed,
		fmt.Sprintf("显卡长 %d mm 超过机箱限长 %d mm", *b.GPU.LengthMM, *b.Case.GPULengthMaxMM))
}

// coolerClearanceRule 规则 #6 COOLER_CLEARANCE(错误级):
// 风冷:散热器高度 ≤ 机箱限高;AIO:冷排尺寸 ∈ 机箱支持位。
// cooler.type 未知时整条 unknown(分支本身无法选择)。
type coolerClearanceRule struct{}

func (coolerClearanceRule) ID() schemas.RuleID { return schemas.RuleCoolerClearance }

func (coolerClearanceRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	if b.Cooler.Type == nil {
		return unknown(schemas.RuleCoolerClearance, map[string]any{},
			[]string{"cooler.type"}, "散热器类型缺失,无法选择风冷/AIO 校验分支")
	}
	observed := map[string]any{"cooler_type": string(*b.Cooler.Type)}

	switch *b.Cooler.Type {
	case schemas.CoolerTypeAir:
		var missing []string
		if b.Cooler.HeightMM == nil {
			missing = append(missing, "cooler.height_mm")
		} else {
			observed["cooler_height_mm"] = *b.Cooler.HeightMM
		}
		if b.Case.CoolerHeightMaxMM == nil {
			missing = append(missing, "case.cooler_height_max_mm")
		} else {
			observed["case_cooler_height_max_mm"] = *b.Case.CoolerHeightMaxMM
		}
		if len(missing) > 0 {
			return unknown(schemas.RuleCoolerClearance, observed, missing, "风冷限高字段缺失,无法判定")
		}
		if *b.Cooler.HeightMM <= *b.Case.CoolerHeightMaxMM {
			return pass(schemas.RuleCoolerClearance, observed, "风冷高度在机箱限高内")
		}
		return fail(schemas.RuleCoolerClearance, schemas.SeverityError, observed,
			fmt.Sprintf("风冷高 %d mm 超过机箱限高 %d mm", *b.Cooler.HeightMM, *b.Case.CoolerHeightMaxMM))

	case schemas.CoolerTypeAIO:
		var missing []string
		if b.Cooler.RadiatorSizeMM == nil {
			missing = append(missing, "cooler.radiator_size_mm")
		} else {
			observed["cooler_radiator_size_mm"] = *b.Cooler.RadiatorSizeMM
		}
		if b.Case.RadiatorSizesMM == nil {
			missing = append(missing, "case.radiator_sizes_mm")
		} else {
			observed["case_radiator_sizes_mm"] = b.Case.RadiatorSizesMM
		}
		if len(missing) > 0 {
			return unknown(schemas.RuleCoolerClearance, observed, missing, "冷排字段缺失,无法判定")
		}
		if slices.Contains(b.Case.RadiatorSizesMM, *b.Cooler.RadiatorSizeMM) {
			return pass(schemas.RuleCoolerClearance, observed, "冷排尺寸在机箱支持位内")
		}
		return fail(schemas.RuleCoolerClearance, schemas.SeverityError, observed,
			fmt.Sprintf("冷排 %d mm 不在机箱支持位 %v 内", *b.Cooler.RadiatorSizeMM, b.Case.RadiatorSizesMM))
	}
	// schemas 层枚举校验保证不会到达;防御性返回 unknown 而非 panic。
	return unknown(schemas.RuleCoolerClearance, observed, []string{"cooler.type"}, "未识别的散热器类型")
}

// formFactorSupportRule 规则 #8 FORM_FACTOR_SUPPORT(错误级):主板板型 ∈ 机箱支持板型。
type formFactorSupportRule struct{}

func (formFactorSupportRule) ID() schemas.RuleID { return schemas.RuleFormFactorSupport }

func (formFactorSupportRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	observed := map[string]any{}
	var missing []string
	if b.Motherboard.FormFactor == nil {
		missing = append(missing, "motherboard.form_factor")
	} else {
		observed["motherboard_form_factor"] = string(*b.Motherboard.FormFactor)
	}
	if b.Case.SupportedFormFactors == nil {
		missing = append(missing, "case.supported_form_factors")
	} else {
		observed["case_supported_form_factors"] = b.Case.SupportedFormFactors
	}
	if len(missing) > 0 {
		return unknown(schemas.RuleFormFactorSupport, observed, missing, "板型字段缺失,无法判定")
	}
	if slices.Contains(b.Case.SupportedFormFactors, *b.Motherboard.FormFactor) {
		return pass(schemas.RuleFormFactorSupport, observed, "主板板型在机箱支持范围内")
	}
	return fail(schemas.RuleFormFactorSupport, schemas.SeverityError, observed,
		fmt.Sprintf("主板板型 %s 不在机箱支持板型 %v 内", *b.Motherboard.FormFactor, b.Case.SupportedFormFactors))
}
