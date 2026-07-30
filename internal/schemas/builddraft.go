package schemas

import "fmt"

// BuildDraftSchemaVersion 当前唯一支持的 BuildDraft schema 版本。
const BuildDraftSchemaVersion = 1

// BuildDraft 生成 Agent → 校验 Agent 的结构化产出(设计方案 §四.2)。
// 唯一权威出处为设计方案 §四.2,字段变更先改文档再改本包(CLAUDE.md 工程纪律)。
//
// Selection 复用 §四.1 的 BuildSelection parts 口径(七类必选、gpu 显式 SKU 或
// null、ssd 为 {sku,quantity} 数组),校验 Agent 由此走 store.ResolveBuild → 规则引擎。
// Rationale/BudgetAllocation 是生成 Agent 的自报信息,纯展示:不进规则层、不进
// golden 断言,仅出口翻译时透传给用户(工程实践指引 §四.1:真值不采信模型自报)。
type BuildDraft struct {
	SchemaVersion    int
	RequirementRef   string
	BuildRef         string
	Selection        BuildSelection
	Rationale        map[string]string  // 每件一句理由,可选,纯展示
	BudgetAllocation map[string]float64 // 生成 Agent 自报分配,可选,纯展示
}

// buildDraftWire 线上格式:指针区分「键缺失」与「显式给值」;selection 复用
// selectionPartsWire 走同一套 parts 校验。
type buildDraftWire struct {
	SchemaVersion    *int                `json:"schema_version"`
	RequirementRef   *string             `json:"requirement_ref"`
	BuildRef         *string             `json:"build_ref"`
	Selection        *selectionPartsWire `json:"selection"`
	Rationale        map[string]string   `json:"rationale"`
	BudgetAllocation map[string]float64  `json:"budget_allocation"`
}

// DecodeBuildDraft 严格解码生成 Agent 产出的 JSON;违反契约返回 error(schema error)。
// selection 的解码与约束完全等同 §四.1(经 resolvePartsWire 复用)。
// rationale/budget_allocation 为可选装饰字段,原样透传,不校验其内容。
func DecodeBuildDraft(data []byte) (BuildDraft, error) {
	var w buildDraftWire
	if err := decodeStrict(data, &w); err != nil {
		return BuildDraft{}, fmt.Errorf("build draft: %w", err)
	}

	if w.SchemaVersion == nil {
		return BuildDraft{}, fmt.Errorf("build draft: 缺少 schema_version")
	}
	if *w.SchemaVersion != BuildDraftSchemaVersion {
		return BuildDraft{}, fmt.Errorf("build draft: 不支持的 schema_version %d(当前仅 %d)", *w.SchemaVersion, BuildDraftSchemaVersion)
	}
	if w.RequirementRef == nil || *w.RequirementRef == "" {
		return BuildDraft{}, fmt.Errorf("build draft: requirement_ref 缺失或为空")
	}
	if w.BuildRef == nil || *w.BuildRef == "" {
		return BuildDraft{}, fmt.Errorf("build draft: build_ref 缺失或为空")
	}
	if w.Selection == nil {
		return BuildDraft{}, fmt.Errorf("build draft: 缺少 selection")
	}

	sel, err := resolvePartsWire(w.Selection, *w.BuildRef)
	if err != nil {
		return BuildDraft{}, err
	}

	return BuildDraft{
		SchemaVersion:    *w.SchemaVersion,
		RequirementRef:   *w.RequirementRef,
		BuildRef:         *w.BuildRef,
		Selection:        sel,
		Rationale:        w.Rationale,
		BudgetAllocation: w.BudgetAllocation,
	}, nil
}
