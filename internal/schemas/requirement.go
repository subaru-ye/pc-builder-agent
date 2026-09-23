package schemas

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RequirementSpecSchemaVersion 当前唯一支持的 RequirementSpec schema 版本。
// v2 一次性切换:新增 configuration_scope 与 use_case.performance_goal,
// resolution 允许 any(仅 RequirementState 有证据语义;gaming 组合校验拒绝 any)。
// v1 由解码器以稳定错误拒绝,不做运行时迁移。
const RequirementSpecSchemaVersion = 2

// defaultBudgetFlex 预算弹性缺省值;maxBudgetFlex 为上限(设计方案 §四.2)。
const (
	defaultBudgetFlex = 0.1
	maxBudgetFlex     = 0.3
)

// ConfigurationScopeTower 当前唯一支持的配置能力边界。
const ConfigurationScopeTower = "tower"

// UseCaseType 单一主用途(当前不做多用途混合,见 docs/product/PRD.md)。
type UseCaseType string

const (
	UseCaseGaming       UseCaseType = "gaming"
	UseCaseProductivity UseCaseType = "productivity"
	UseCaseGeneral      UseCaseType = "general"
)

func (t *UseCaseType) UnmarshalJSON(b []byte) error {
	s, err := unmarshalString(b, "use_case.type")
	if err != nil {
		return err
	}
	switch UseCaseType(s) {
	case UseCaseGaming, UseCaseProductivity, UseCaseGeneral:
		*t = UseCaseType(s)
		return nil
	}
	return fmt.Errorf("非法用途枚举 %q(仅 gaming|productivity|general)", s)
}

// Resolution 目标分辨率(gaming 用途必填)。
type Resolution string

const (
	Resolution1080p Resolution = "1080p"
	Resolution2K    Resolution = "2K"
	Resolution4K    Resolution = "4K"
	// ResolutionAny 是用户明确表示不限(有证据的 active 用户值,不是 unknown)。
	// gaming 的最低确认矩阵不接受 any,组合校验在解码边界拒绝 gaming+any。
	ResolutionAny Resolution = "any"
)

func (r *Resolution) UnmarshalJSON(b []byte) error {
	s, err := unmarshalString(b, "resolution")
	if err != nil {
		return err
	}
	switch Resolution(s) {
	case Resolution1080p, Resolution2K, Resolution4K, ResolutionAny:
		*r = Resolution(s)
		return nil
	}
	return fmt.Errorf("非法分辨率枚举 %q(仅 1080p|2K|4K|any)", s)
}

// PerformanceGoal 用途内取舍目标。仅 gaming 在未明确时使用系统默认 balanced;
// 用户明确的 goal 可作为其他用途的取舍目标保留。
type PerformanceGoal string

const (
	PerformanceGoalBalanced     PerformanceGoal = "balanced"
	PerformanceGoalFPSFirst     PerformanceGoal = "fps_first"
	PerformanceGoalQualityFirst PerformanceGoal = "quality_first"
)

func (g *PerformanceGoal) UnmarshalJSON(b []byte) error {
	s, err := unmarshalString(b, "use_case.performance_goal")
	if err != nil {
		return err
	}
	switch PerformanceGoal(s) {
	case PerformanceGoalBalanced, PerformanceGoalFPSFirst, PerformanceGoalQualityFirst:
		*g = PerformanceGoal(s)
		return nil
	}
	return fmt.Errorf("非法性能目标枚举 %q(仅 balanced|fps_first|quality_first)", s)
}

// SizePref 尺寸偏好:板型三选一或 any(不限)。
type SizePref string

const (
	SizePrefATX  SizePref = "atx"
	SizePrefMATX SizePref = "matx"
	SizePrefITX  SizePref = "itx"
	SizePrefAny  SizePref = "any"
)

func (p *SizePref) UnmarshalJSON(b []byte) error {
	s, err := unmarshalString(b, "size_pref")
	if err != nil {
		return err
	}
	switch SizePref(s) {
	case SizePrefATX, SizePrefMATX, SizePrefITX, SizePrefAny:
		*p = SizePref(s)
		return nil
	}
	return fmt.Errorf("非法尺寸偏好枚举 %q(仅 atx|matx|itx|any)", s)
}

// NoisePref 噪音偏好。
type NoisePref string

const (
	NoisePrefSilent NoisePref = "silent"
	NoisePrefNormal NoisePref = "normal"
	NoisePrefAny    NoisePref = "any"
)

func (p *NoisePref) UnmarshalJSON(b []byte) error {
	s, err := unmarshalString(b, "noise_pref")
	if err != nil {
		return err
	}
	switch NoisePref(s) {
	case NoisePrefSilent, NoisePrefNormal, NoisePrefAny:
		*p = NoisePref(s)
		return nil
	}
	return fmt.Errorf("非法噪音偏好枚举 %q(仅 silent|normal|any)", s)
}

// CPUBrand CPU 品牌偏好。MVP 数据仅 AM5,实际恒 amd,但枚举保留 intel 供扩展。
type CPUBrand string

const (
	CPUBrandAny   CPUBrand = "any"
	CPUBrandIntel CPUBrand = "intel"
	CPUBrandAMD   CPUBrand = "amd"
)

func (b *CPUBrand) UnmarshalJSON(data []byte) error {
	s, err := unmarshalString(data, "brand_pref.cpu")
	if err != nil {
		return err
	}
	switch CPUBrand(s) {
	case CPUBrandAny, CPUBrandIntel, CPUBrandAMD:
		*b = CPUBrand(s)
		return nil
	}
	return fmt.Errorf("非法 CPU 品牌枚举 %q(仅 any|intel|amd)", s)
}

// GPUBrand GPU 品牌偏好。
type GPUBrand string

const (
	GPUBrandAny    GPUBrand = "any"
	GPUBrandNvidia GPUBrand = "nvidia"
	GPUBrandAMD    GPUBrand = "amd"
)

func (b *GPUBrand) UnmarshalJSON(data []byte) error {
	s, err := unmarshalString(data, "brand_pref.gpu")
	if err != nil {
		return err
	}
	switch GPUBrand(s) {
	case GPUBrandAny, GPUBrandNvidia, GPUBrandAMD:
		*b = GPUBrand(s)
		return nil
	}
	return fmt.Errorf("非法 GPU 品牌枚举 %q(仅 any|nvidia|amd)", s)
}

// UseCase 单一主用途及其参数。Resolution 在 gaming 时为 1080p/2K/4K,
// 用户明确不限时可为 any(仅 state 语义;spec 组合校验拒绝 gaming+any)。
type UseCase struct {
	Type            UseCaseType
	Titles          []string
	Resolution      Resolution
	FPSTarget       *int            // nil = 未指定
	PerformanceGoal PerformanceGoal // 空 = 未明确;gaming 由解码填充系统默认 balanced
}

// BrandPref 品牌偏好;缺省均为 any。
type BrandPref struct {
	CPU CPUBrand
	GPU GPUBrand
}

// RequirementSpec 初筛 Agent → 生成 Agent 的结构化需求单(设计方案 §四.2)。
// 唯一权威出处为设计方案 §四.2,字段变更先改文档再改本包(CLAUDE.md 工程纪律)。
type RequirementSpec struct {
	SchemaVersion int
	BudgetCNY     int
	BudgetFlex    float64 // 缺省 0.1
	UseCase       UseCase
	SizePref      SizePref  // 缺省 any
	NoisePref     NoisePref // 缺省 any
	BrandPref     BrandPref // 缺省 {any, any}
	// ConfigurationScope 是产品能力边界(当前仅 tower),由 Projection 注入的
	// 系统默认,不是用户陈述;直接解码也必须携带,禁止绕过 Projection 构造。
	ConfigurationScope      []string
	ExistingParts           []Category
	OwnedParts              []OwnedPart
	BudgetBasis             string
	Priority                []Category
	Notes                   string
	ConstraintStrengths     map[string]string          // 当前会话明确的 must/prefer；缺失沿用旧需求单语义。
	RequirementDetails      map[string]json.RawMessage // 外观、装机对象与 free.* 有效补充，不含历史/备选。
	RequirementSemantics    map[string]string          // fact/context/constraint 独立于 must/prefer。
	RequirementObservations []RequirementObservation   // 尚未结构化的用户原文，仅作未确认上下文。
}

// requirementSpecWire 线上格式:指针区分「键缺失」与「显式给值」,支持缺省填充。
type requirementSpecWire struct {
	SchemaVersion           *int                       `json:"schema_version"`
	BudgetCNY               *int                       `json:"budget_cny"`
	BudgetFlex              *float64                   `json:"budget_flex"`
	UseCase                 *useCaseWire               `json:"use_case"`
	SizePref                *SizePref                  `json:"size_pref"`
	NoisePref               *NoisePref                 `json:"noise_pref"`
	BrandPref               *brandPrefWire             `json:"brand_pref"`
	ConfigurationScope      []string                   `json:"configuration_scope"`
	ExistingParts           []Category                 `json:"existing_parts"`
	OwnedParts              []OwnedPart                `json:"owned_parts,omitempty"`
	BudgetBasis             string                     `json:"budget_basis,omitempty"`
	Priority                []Category                 `json:"priority"`
	Notes                   *string                    `json:"notes"`
	ConstraintStrengths     map[string]string          `json:"constraint_strengths,omitempty"`
	RequirementDetails      map[string]json.RawMessage `json:"requirement_details,omitempty"`
	RequirementSemantics    map[string]string          `json:"requirement_semantics,omitempty"`
	RequirementObservations []RequirementObservation   `json:"requirement_observations,omitempty"`
}

type useCaseWire struct {
	Type            *UseCaseType     `json:"type"`
	Titles          []string         `json:"titles"`
	Resolution      *Resolution      `json:"resolution"`
	FPSTarget       *int             `json:"fps_target"`
	PerformanceGoal *PerformanceGoal `json:"performance_goal"`
}

type brandPrefWire struct {
	CPU *CPUBrand `json:"cpu"`
	GPU *GPUBrand `json:"gpu"`
}

// EncodeRequirementSpec 输出已展开缺省值的稳定线上 JSON。
// Agent 可以省略 budget_flex 等可选键,但产品 API 的需求卡必须拿到解码后的
// 实际语义,否则浏览器表单会把“缺省 ±10%”误当成第一个选项“严格预算”。
func EncodeRequirementSpec(spec RequirementSpec) (json.RawMessage, error) {
	titles := spec.UseCase.Titles
	if titles == nil {
		titles = []string{}
	}
	existing := spec.ExistingParts
	if existing == nil {
		existing = []Category{}
	}
	priority := spec.Priority
	if priority == nil {
		priority = []Category{}
	}

	type canonicalUseCase struct {
		Type            UseCaseType     `json:"type"`
		Titles          []string        `json:"titles"`
		Resolution      Resolution      `json:"resolution,omitempty"`
		FPSTarget       *int            `json:"fps_target,omitempty"`
		PerformanceGoal PerformanceGoal `json:"performance_goal,omitempty"`
	}
	type canonicalBrandPref struct {
		CPU CPUBrand `json:"cpu"`
		GPU GPUBrand `json:"gpu"`
	}
	type canonicalRequirement struct {
		SchemaVersion           int                        `json:"schema_version"`
		BudgetCNY               int                        `json:"budget_cny"`
		BudgetFlex              float64                    `json:"budget_flex"`
		UseCase                 canonicalUseCase           `json:"use_case"`
		SizePref                SizePref                   `json:"size_pref"`
		NoisePref               NoisePref                  `json:"noise_pref"`
		BrandPref               canonicalBrandPref         `json:"brand_pref"`
		ConfigurationScope      []string                   `json:"configuration_scope"`
		ExistingParts           []Category                 `json:"existing_parts"`
		OwnedParts              []OwnedPart                `json:"owned_parts,omitempty"`
		BudgetBasis             string                     `json:"budget_basis,omitempty"`
		Priority                []Category                 `json:"priority"`
		Notes                   string                     `json:"notes"`
		ConstraintStrengths     map[string]string          `json:"constraint_strengths,omitempty"`
		RequirementDetails      map[string]json.RawMessage `json:"requirement_details,omitempty"`
		RequirementSemantics    map[string]string          `json:"requirement_semantics,omitempty"`
		RequirementObservations []RequirementObservation   `json:"requirement_observations,omitempty"`
	}

	scope := spec.ConfigurationScope
	if scope == nil {
		scope = []string{ConfigurationScopeTower}
	}
	encoded, err := json.Marshal(canonicalRequirement{
		SchemaVersion: spec.SchemaVersion,
		BudgetCNY:     spec.BudgetCNY,
		BudgetFlex:    spec.BudgetFlex,
		UseCase: canonicalUseCase{
			Type: spec.UseCase.Type, Titles: titles, Resolution: spec.UseCase.Resolution,
			FPSTarget: spec.UseCase.FPSTarget, PerformanceGoal: spec.UseCase.PerformanceGoal,
		},
		SizePref: spec.SizePref, NoisePref: spec.NoisePref,
		BrandPref:          canonicalBrandPref{CPU: spec.BrandPref.CPU, GPU: spec.BrandPref.GPU},
		ConfigurationScope: scope,
		ExistingParts:      existing, OwnedParts: spec.OwnedParts, BudgetBasis: spec.BudgetBasis, Priority: priority, Notes: spec.Notes,
		ConstraintStrengths: spec.ConstraintStrengths, RequirementDetails: spec.RequirementDetails,
		RequirementSemantics: spec.RequirementSemantics, RequirementObservations: spec.RequirementObservations,
	})
	if err != nil {
		return nil, fmt.Errorf("requirement spec: 编码失败: %w", err)
	}
	return encoded, nil
}

// DecodeRequirementSpec 严格解码需求单 JSON;违反契约返回 error(schema error)。
// 可缺省字段(budget_flex/size_pref/noise_pref/brand_pref)缺失时填充默认值。
// v2 组合不变量(最低矩阵与 ownership 约束)在此边界强制执行。
func DecodeRequirementSpec(data []byte) (RequirementSpec, error) {
	spec, err := decodeRequirementSpec(data, true)
	if err != nil {
		return RequirementSpec{}, fmt.Errorf("requirement spec: %w", err)
	}
	return spec, nil
}

// DecodeLegacyRequirementSpec 仅供评估工具回放冻结 v1 产物:对 schema_version=1
// 做机械升格(仅补 schema_version/configuration_scope,不改值、不造事实),
// 并按 v1 组合语义解码——v2 新增的组合不变量(生产力 titles、已有件型号与
// 口径)不追溯适用于历史用例,否则冻结 v1 资产需要编造用户事实才能通过。
// 产品路径不得调用;产品 decoder 对 v1 保持稳定拒绝。
func DecodeLegacyRequirementSpec(data []byte) (RequirementSpec, error) {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if json.Unmarshal(data, &header) != nil {
		return RequirementSpec{}, fmt.Errorf("requirement spec: 非法 JSON")
	}
	if header.SchemaVersion == 1 {
		decoded := map[string]json.RawMessage{}
		if json.Unmarshal(data, &decoded) != nil {
			return RequirementSpec{}, fmt.Errorf("requirement spec: 非法 JSON")
		}
		version, _ := json.Marshal(RequirementSpecSchemaVersion)
		scope, _ := json.Marshal([]string{ConfigurationScopeTower})
		decoded["schema_version"] = version
		decoded["configuration_scope"] = scope
		if out, err := json.Marshal(decoded); err == nil {
			data = out
		}
	}
	spec, err := decodeRequirementSpec(data, false)
	if err != nil {
		return RequirementSpec{}, fmt.Errorf("requirement spec: %w", err)
	}
	return spec, nil
}

func decodeRequirementSpec(data []byte, enforceCombination bool) (RequirementSpec, error) {
	var w requirementSpecWire
	if err := decodeStrict(data, &w); err != nil {
		return RequirementSpec{}, err
	}

	if w.SchemaVersion == nil {
		return RequirementSpec{}, fmt.Errorf("缺少 schema_version")
	}
	if *w.SchemaVersion != RequirementSpecSchemaVersion {
		return RequirementSpec{}, fmt.Errorf("不支持的 schema_version %d(当前仅 %d)", *w.SchemaVersion, RequirementSpecSchemaVersion)
	}
	if w.BudgetCNY == nil {
		return RequirementSpec{}, fmt.Errorf("缺少 budget_cny")
	}
	if *w.BudgetCNY <= 0 {
		return RequirementSpec{}, fmt.Errorf("budget_cny 必须为正整数,得到 %d", *w.BudgetCNY)
	}

	out := RequirementSpec{
		SchemaVersion: *w.SchemaVersion,
		BudgetCNY:     *w.BudgetCNY,
		BudgetFlex:    defaultBudgetFlex,
		SizePref:      SizePrefAny,
		NoisePref:     NoisePrefAny,
		BrandPref:     BrandPref{CPU: CPUBrandAny, GPU: GPUBrandAny},
	}

	if w.BudgetFlex != nil {
		if *w.BudgetFlex < 0 || *w.BudgetFlex > maxBudgetFlex {
			return RequirementSpec{}, fmt.Errorf("budget_flex 必须在 [0,%.1f],得到 %v", maxBudgetFlex, *w.BudgetFlex)
		}
		out.BudgetFlex = *w.BudgetFlex
	}

	// configuration_scope 是必填能力边界:仅接受精确 ["tower"],不静默修复。
	if len(w.ConfigurationScope) != 1 || w.ConfigurationScope[0] != ConfigurationScopeTower {
		return RequirementSpec{}, fmt.Errorf("configuration_scope 当前仅支持 [%q],得到 %v", ConfigurationScopeTower, w.ConfigurationScope)
	}
	out.ConfigurationScope = append([]string(nil), w.ConfigurationScope...)

	uc, err := resolveUseCase(w.UseCase)
	if err != nil {
		return RequirementSpec{}, err
	}
	out.UseCase = uc

	if w.SizePref != nil {
		out.SizePref = *w.SizePref
	}
	if w.NoisePref != nil {
		out.NoisePref = *w.NoisePref
	}
	if w.BrandPref != nil {
		if w.BrandPref.CPU != nil {
			out.BrandPref.CPU = *w.BrandPref.CPU
		}
		if w.BrandPref.GPU != nil {
			out.BrandPref.GPU = *w.BrandPref.GPU
		}
	}

	if err := validateCategories("existing_parts", w.ExistingParts); err != nil {
		return RequirementSpec{}, err
	}
	if err := validateCategories("priority", w.Priority); err != nil {
		return RequirementSpec{}, err
	}
	out.OwnedParts, out.BudgetBasis = w.OwnedParts, w.BudgetBasis
	if err := validateOwned(&out); err != nil {
		return RequirementSpec{}, err
	}
	out.ExistingParts = w.ExistingParts
	out.Priority = w.Priority
	if enforceCombination {
		if err := validateSpecCombination(&out); err != nil {
			return RequirementSpec{}, err
		}
	}

	if w.Notes != nil {
		out.Notes = *w.Notes
	}
	for key, strength := range w.ConstraintStrengths {
		if !knownRequirementField(key) || (strength != "must" && strength != "prefer") {
			return RequirementSpec{}, fmt.Errorf("非法约束强度 %s=%s", key, strength)
		}
	}
	for key, value := range w.RequirementDetails {
		if key != "appearance" && key != "recipient" && !FreeField(key) {
			return RequirementSpec{}, fmt.Errorf("非法补充字段 %s", key)
		}
		if err := validateRequirementValue(key, value); err != nil {
			return RequirementSpec{}, err
		}
	}
	out.ConstraintStrengths, out.RequirementDetails = w.ConstraintStrengths, w.RequirementDetails
	for key, kind := range w.RequirementSemantics {
		if !knownRequirementField(key) || !ValidRequirementKind(kind) {
			return RequirementSpec{}, fmt.Errorf("非法语义分类 %s=%s", key, kind)
		}
	}
	for _, observation := range w.RequirementObservations {
		if observation.Resolved || strings.TrimSpace(observation.Text) == "" || observation.Text != observation.Source.Quote || (observation.Field != "" && !knownRequirementField(observation.Field)) {
			return RequirementSpec{}, fmt.Errorf("非法原文观察")
		}
	}
	out.RequirementSemantics, out.RequirementObservations = w.RequirementSemantics, w.RequirementObservations

	return out, nil
}

// resolveUseCase 校验并展开 use_case;gaming 要求 resolution 为 1080p/2K/4K
// (组合不变量:明确 any 不满足 gaming 最低矩阵,直接拒绝),未明确
// performance_goal 时填系统默认 balanced;其余用途保留用户显式 goal。
func resolveUseCase(w *useCaseWire) (UseCase, error) {
	if w == nil {
		return UseCase{}, fmt.Errorf("缺少 use_case")
	}
	if w.Type == nil {
		return UseCase{}, fmt.Errorf("use_case.type 缺失")
	}
	uc := UseCase{Type: *w.Type, Titles: w.Titles}

	if w.Resolution != nil {
		uc.Resolution = *w.Resolution
	}
	if *w.Type == UseCaseGaming {
		if w.Resolution == nil {
			return UseCase{}, fmt.Errorf("gaming 用途必须提供 resolution")
		}
		if *w.Resolution == ResolutionAny {
			return UseCase{}, fmt.Errorf("gaming 用途的 resolution 不能为 any(需 1080p|2K|4K)")
		}
		if w.PerformanceGoal == nil {
			uc.PerformanceGoal = PerformanceGoalBalanced
		}
	}
	if w.PerformanceGoal != nil {
		uc.PerformanceGoal = *w.PerformanceGoal
	}

	if w.FPSTarget != nil {
		if *w.FPSTarget <= 0 {
			return UseCase{}, fmt.Errorf("use_case.fps_target 必须为正整数,得到 %d", *w.FPSTarget)
		}
		uc.FPSTarget = w.FPSTarget
	}
	return uc, nil
}

// validateSpecCombination 在解码边界执行与 readiness 相同的组合不变量,
// 避免调用方绕过 Projection 构造 gaming+any、existing/owned 不一致、
// 生产力缺软件任务或已有件缺口径等非法 spec。谓词与 readiness 共用,
// 不复制第二套业务矩阵。
func validateSpecCombination(spec *RequirementSpec) error {
	if spec.UseCase.Type == UseCaseProductivity && len(NonEmptyTitles(spec.UseCase.Titles)) == 0 {
		return fmt.Errorf("productivity 用途必须提供至少一个非空软件或任务(use_case.titles)")
	}
	existing := map[Category]bool{}
	for _, category := range spec.ExistingParts {
		existing[category] = true
	}
	if len(existing) == 0 && len(spec.OwnedParts) > 0 {
		return fmt.Errorf("existing_parts 为空时不能携带 owned_parts")
	}
	for _, part := range spec.OwnedParts {
		if !existing[part.Category] {
			return fmt.Errorf("owned_parts.%s 不在 existing_parts 内", part.Category)
		}
	}
	if missing := MissingOwnedModels(spec.ExistingParts, spec.OwnedParts); len(missing) > 0 {
		return fmt.Errorf("existing_parts 品类缺少对应 owned_parts 准确型号:%v", missing)
	}
	if (len(spec.ExistingParts) > 0 || len(spec.OwnedParts) > 0) && spec.BudgetBasis == "" {
		return fmt.Errorf("已有件时必须提供 budget_basis(new_purchase 或 full_build)")
	}
	return nil
}

// validateCategories 校验品类列表每项均为八大类合法枚举(existing_parts / priority)。
func validateCategories(field string, cats []Category) error {
	for _, c := range cats {
		if !isValidCategory(c) {
			return fmt.Errorf("%s 含非法品类 %q", field, c)
		}
	}
	return nil
}

// isValidCategory 判定是否为八大类之一。
func isValidCategory(c Category) bool {
	for _, known := range AllCategories {
		if c == known {
			return true
		}
	}
	return false
}
