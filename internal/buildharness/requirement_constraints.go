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

// 事实与背景不是商品属性断言，强度 must 也不能把剪辑素材等说明变成
// 目录证明义务。只有明确的配置条件才需核验；旧记录语义不明时单独确认。
// 语义判断来自本轮 Screening 与服务端状态，不在这里重建自然语言词表。
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
		switch notesRequirementKind(spec) {
		case "fact", "context":
			// 用途事实与说明完整保留在 Builder 输入中。
		case "constraint":
			fields = append(fields, "notes")
			labels = append(labels, "补充条件「"+trimRunes(strings.TrimSpace(spec.Notes), 240)+"」")
		default:
			return &Decision{Kind: "clarify", Reason: "requirement_semantics_missing", Fields: []string{"notes"}, Scope: "requirement",
				Message: "已保留补充内容「" + trimRunes(strings.TrimSpace(spec.Notes), 240) + "」及必须满足标记，但旧记录尚未区分用途说明和配置条件。请确认它是用途或背景说明，还是必须满足的具体配置条件；其他已知需求保持不变。"}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &Decision{Kind: "data_unavailable", Reason: "requirement_evidence_missing", Fields: fields, Scope: "current_catalog",
		Message: "已保留必须满足的" + strings.Join(labels, "、") + "，但当前目录缺少对应的可核验规格，尚待核验，本轮未生成配置。请补充这些条件的准确商品规格或可核验资料后继续。"}
}

// 兼容旧记录时，只认可当前结构化用途中已存在的同一事实；不凭关键词
// 猜测任意自由文本的含义，也不修改旧状态、强度或确认快照。
func notesRequirementKind(spec schemas.RequirementSpec) string {
	if kind := spec.RequirementSemantics["notes"]; kind != "" {
		return kind
	}
	notes := strings.TrimSpace(spec.Notes)
	for _, title := range spec.UseCase.Titles {
		if notes != "" && notes == strings.TrimSpace(title) {
			return "fact"
		}
	}
	return ""
}

func mandatoryGPU(spec schemas.RequirementSpec) bool {
	return spec.ConstraintStrengths["brand_pref.gpu"] == "must" && spec.BrandPref.GPU != "" && spec.BrandPref.GPU != schemas.GPUBrandAny
}

// 候选筛空也属于可解释的业务结果；未知规格不能被夸大为市场无解。
func requiredCandidatesMissing(spec schemas.RequirementSpec, category schemas.Category) *Decision {
	field := string(category)
	labels := map[schemas.Category]string{
		schemas.CategoryCPU: "CPU 品牌", schemas.CategoryGPU: "显卡品牌", schemas.CategoryMotherboard: "主板平台或尺寸",
		schemas.CategoryMemory: "内存代际", schemas.CategorySSD: "SSD", schemas.CategoryPSU: "电源",
		schemas.CategoryCase: "机箱尺寸", schemas.CategoryCooler: "散热器",
	}
	switch category {
	case schemas.CategoryCPU:
		if spec.ConstraintStrengths["brand_pref.cpu"] == "must" {
			field = "brand_pref.cpu"
		}
	case schemas.CategoryGPU:
		if mandatoryGPU(spec) {
			field = "brand_pref.gpu"
		}
	case schemas.CategoryMotherboard, schemas.CategoryCase:
		if spec.ConstraintStrengths["size_pref"] == "must" && sizeFormFactor(spec.SizePref) != "" {
			field = "size_pref"
		}
	}
	return &Decision{Kind: "data_unavailable", Reason: "required_candidates_missing", Fields: []string{field}, Scope: "current_catalog",
		Message: "当前目录没有可核验且满足" + labels[category] + "条件的候选，本轮未生成配置。已保留当前要求，请补充准确规格或候选商品资料后继续；这不代表市场上没有可行方案。"}
}
