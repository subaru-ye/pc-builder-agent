package buildharness

import (
	"encoding/json"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func mandatoryLockedConflict(spec schemas.RequirementSpec, category schemas.Category, candidate store.Candidate) *Decision {
	field := ""
	switch category {
	case schemas.CategoryCPU:
		if spec.ConstraintStrengths["brand_pref.cpu"] == "must" && spec.BrandPref.CPU != schemas.CPUBrandAny && cpuVendor(candidate) != string(spec.BrandPref.CPU) {
			field = "brand_pref.cpu"
		}
	case schemas.CategoryGPU:
		if spec.ConstraintStrengths["brand_pref.gpu"] == "must" && spec.BrandPref.GPU != schemas.GPUBrandAny && gpuFamily(candidate) != string(spec.BrandPref.GPU) {
			field = "brand_pref.gpu"
		}
	case schemas.CategoryMotherboard:
		if spec.ConstraintStrengths["size_pref"] == "must" && sizeFormFactor(spec.SizePref) != "" {
			var board struct {
				FormFactor string `json:"form_factor"`
			}
			if json.Unmarshal(candidate.Specs, &board) != nil || board.FormFactor != sizeFormFactor(spec.SizePref) {
				field = "size_pref"
			}
		}
	}
	if field == "" {
		return nil
	}
	return &Decision{Kind: "clarify", Reason: "mandatory_locked_conflict", Fields: []string{field}, Scope: "requirement",
		Message: "必须满足的" + schemas.RequirementFieldLabel(field) + "与要保留的配件不一致。请确认是保留该配件，还是调整此项要求后继续；本轮不会擅自换掉已有或锁定配件。"}
}

// 旧需求没有强度标记时保留原有板型硬约束，避免改写历史运行语义。
func hardSizePreference(spec schemas.RequirementSpec) schemas.SizePref {
	if spec.ConstraintStrengths["size_pref"] == "prefer" {
		return schemas.SizePrefAny
	}
	return spec.SizePref
}

func softConstraintMatch(spec schemas.RequirementSpec, category schemas.Category, candidate Candidate) bool {
	identity := store.Candidate{SKU: candidate.SKU, Brand: candidate.Brand, Model: candidate.Model}
	switch category {
	case schemas.CategoryCPU:
		return spec.ConstraintStrengths["brand_pref.cpu"] == "prefer" && cpuVendor(identity) == string(spec.BrandPref.CPU)
	case schemas.CategoryGPU:
		return spec.ConstraintStrengths["brand_pref.gpu"] == "prefer" && gpuFamily(identity) == string(spec.BrandPref.GPU)
	case schemas.CategoryMotherboard:
		return spec.ConstraintStrengths["size_pref"] == "prefer" && sizeFormFactor(spec.SizePref) != "" && candidate.Specs["form_factor"] == sizeFormFactor(spec.SizePref)
	case schemas.CategoryCase:
		if spec.ConstraintStrengths["size_pref"] == "prefer" && sizeFormFactor(spec.SizePref) != "" {
			for _, form := range stringSlice(candidate.Specs["supported_form_factors"]) {
				if form == sizeFormFactor(spec.SizePref) {
					return true
				}
			}
		}
	}
	return false
}

// 当前目录没有统一噪声/外观证明契约。语义命中与型号文字不能证明
// 必须条件已经满足，先返回明确的待核验结果，不耗费一次选配调用。
func unverifiedMandatoryRequirements(spec schemas.RequirementSpec) *Decision {
	var fields, labels []string
	if spec.ConstraintStrengths["noise_pref"] == "must" && spec.NoisePref != "" && spec.NoisePref != schemas.NoisePrefAny {
		fields = append(fields, "noise_pref")
		labels = append(labels, "静音")
	}
	if spec.ConstraintStrengths["appearance"] == "must" && len(spec.RequirementDetails["appearance"]) > 0 {
		fields = append(fields, "appearance")
		labels = append(labels, "外观")
	}
	if spec.ConstraintStrengths["notes"] == "must" && strings.TrimSpace(spec.Notes) != "" {
		fields = append(fields, "notes")
		labels = append(labels, "补充要求")
	}
	if len(fields) == 0 {
		return nil
	}
	return &Decision{Kind: "data_unavailable", Reason: "requirement_evidence_missing", Fields: fields, Scope: "current_catalog",
		Message: "已保留必须满足的" + strings.Join(labels, "、") + "要求，但当前目录缺少统一可核验的证据，尚待核验，本轮未生成配置。可补充准确商品资料后继续；若允许取舍，可将对应要求改为尽量满足。"}
}
