package schemas

// 确定性 Readiness、系统默认与追问计划:给定相同 RequirementState 必须得到
// 字节级相同的结论。本文件是最低确认矩阵、blocking conflict 规则、有效默认
// 与追问优先级的唯一领域实现;产品与前端只消费,不得复制第二份清单。
import (
	"encoding/json"
	"sort"
	"strings"
)

// RequirementDefault 是被展开的系统默认;origin 固定 system_default,
// 供确认页展示"本项未由用户表达"。
type RequirementDefault struct {
	Field  string          `json:"field"`
	Value  json.RawMessage `json:"value"`
	Origin string          `json:"origin"`
}

// RequirementReadiness 是就绪判定的完整结论。ConfirmationEligible 只表示
// "无缺失、无阻塞冲突、无未解决 unsupported capability 且状态可投影",
// 不表示用户本轮请求确认。
type RequirementReadiness struct {
	Status                  string               `json:"status"` // incomplete | ready
	MissingFields           []string             `json:"missing_fields"`
	BlockingConflicts       []string             `json:"blocking_conflicts"`
	UnsupportedCapabilities []string             `json:"unsupported_capabilities"`
	ConfirmationEligible    bool                 `json:"confirmation_eligible"`
	EffectiveDefaults       []RequirementDefault `json:"effective_defaults"`
}

// requirementQuestionOrder 是 missing/blocking 排序与追问计划共用的优先级
// (单一出处):用途 → 预算 → 已有件 → 条件必填(分辨率/软件任务) →
// 已有件型号 → 预算口径。owned_parts.<category>.model 共用型号组优先级。
var requirementQuestionOrder = []string{
	"use_case.type", "budget_cny", "existing_parts", "use_case.resolution", "use_case.titles",
	"owned_parts", "budget_basis",
}

func requirementPriority(key string) (int, bool) {
	group := key
	if strings.HasPrefix(key, "owned_parts.") {
		group = "owned_parts"
	}
	for i, known := range requirementQuestionOrder {
		if group == known {
			return i, true
		}
	}
	return len(requirementQuestionOrder), false
}

func sortRequirementFields(fields []string) {
	rank := func(key string) [3]int {
		priority, _ := requirementPriority(key)
		category := len(AllCategories)
		if strings.HasPrefix(key, "owned_parts.") {
			name := strings.TrimSuffix(strings.TrimPrefix(key, "owned_parts."), ".model")
			for i, known := range AllCategories {
				if name == string(known) {
					category = i
					break
				}
			}
		}
		return [3]int{priority, category, 0}
	}
	sort.Slice(fields, func(i, j int) bool {
		a, b := rank(fields[i]), rank(fields[j])
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		if a[1] != b[1] {
			return a[1] < b[1]
		}
		return fields[i] < fields[j]
	})
}

// gamingResolutionSatisfied: gaming 最低矩阵只接受 1080p/2K/4K;
// 明确 any 是有证据的用户值,但不满足确认条件。
func gamingResolutionSatisfied(value json.RawMessage) bool {
	switch string(value) {
	case `"1080p"`, `"2K"`, `"4K"`:
		return true
	}
	return false
}

// EvaluateRequirementReadiness 按最低确认矩阵计算缺失项、阻塞冲突、
// 有效系统默认与确认资格。非法 active 值返回 schema error,不降级为 unknown。
func EvaluateRequirementReadiness(state RequirementState) (RequirementReadiness, error) {
	r := RequirementReadiness{Status: "incomplete", MissingFields: []string{}, BlockingConflicts: []string{}, UnsupportedCapabilities: []string{}}
	for key, field := range state.Fields {
		if field.Status == "active" {
			if err := validateRequirementValue(key, field.Value); err != nil {
				return r, err
			}
		}
	}
	unsupported := map[string]bool{}
	for _, observation := range state.Observations {
		if observation.Resolved {
			continue
		}
		if name, structured := strings.CutPrefix(observation.Reason, unsupportedCapabilityPrefix); structured && containsString(unsupportedCapabilityNames, name) {
			unsupported[name] = true
		}
	}
	for _, name := range unsupportedCapabilityNames {
		if unsupported[name] {
			r.UnsupportedCapabilities = append(r.UnsupportedCapabilities, name)
		}
	}

	missing := map[string]bool{}
	blocking := map[string]bool{}
	// 必填:预算、用途、已有件([] 是明确 active 值,unknown/removed 按缺失处理)。
	for _, key := range []string{"use_case.type", "budget_cny", "existing_parts"} {
		switch state.Fields[key].Status {
		case "active":
		case "conflict":
			blocking[key] = true
		default:
			missing[key] = true
		}
	}
	useCaseType := ""
	if field := state.Fields["use_case.type"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &useCaseType)
	}
	switch useCaseType {
	case "gaming":
		field := state.Fields["use_case.resolution"]
		switch {
		case field.Status == "active" && gamingResolutionSatisfied(field.Value):
		case field.Status == "conflict":
			blocking["use_case.resolution"] = true
		default:
			missing["use_case.resolution"] = true
		}
	case "productivity":
		field := state.Fields["use_case.titles"]
		satisfied := field.Status == "active" && len(nonEmptyTitles(field.Value)) > 0
		switch {
		case satisfied:
		case field.Status == "conflict":
			blocking["use_case.titles"] = true
		default:
			missing["use_case.titles"] = true
		}
	}
	// 条件必填:已有件型号与预算口径。条件必填字段发生 conflict 时直接进
	// BlockingConflicts(与 must/prefer 无关),不再按缺失逐项列出,
	// 避免同一冲突同时伪装成普通缺失。
	var existing []Category
	var owned []OwnedPart
	if field := state.Fields["existing_parts"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &existing)
	}
	if field := state.Fields["owned_parts"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &owned)
	}
	if len(existing) > 0 || len(owned) > 0 {
		if state.Fields["owned_parts"].Status == "conflict" {
			blocking["owned_parts"] = true
		} else {
			for _, category := range MissingOwnedModels(existing, owned) {
				missing["owned_parts."+string(category)+".model"] = true
			}
		}
		switch state.Fields["budget_basis"].Status {
		case "active":
		case "conflict":
			blocking["budget_basis"] = true
		default:
			missing["budget_basis"] = true
		}
	}
	// 必填/条件必填之外的 must 约束冲突同样阻塞;普通 prefer 冲突不阻塞。
	for _, key := range sortedFieldKeys(state.Fields) {
		field := state.Fields[key]
		if field.Status == "conflict" && field.Strength == "must" && !blocking[key] && !missing[key] {
			blocking[key] = true
		}
	}
	for key := range missing {
		r.MissingFields = append(r.MissingFields, key)
	}
	for key := range blocking {
		r.BlockingConflicts = append(r.BlockingConflicts, key)
	}
	sortRequirementFields(r.MissingFields)
	sortRequirementFields(r.BlockingConflicts)
	r.EffectiveDefaults = effectiveRequirementDefaults(state, useCaseType)
	r.ConfirmationEligible = len(r.MissingFields) == 0 && len(r.BlockingConflicts) == 0 && len(r.UnsupportedCapabilities) == 0
	if r.ConfirmationEligible {
		r.Status = "ready"
	}
	return r, nil
}

func nonEmptyTitles(raw json.RawMessage) []string {
	var titles []string
	if json.Unmarshal(raw, &titles) != nil {
		return nil
	}
	return NonEmptyTitles(titles)
}

// NonEmptyTitles 过滤 trim 后为空的标题;readiness 与 spec 解码共用。
func NonEmptyTitles(titles []string) []string {
	out := make([]string, 0, len(titles))
	for _, title := range titles {
		if strings.TrimSpace(title) != "" {
			out = append(out, title)
		}
	}
	return out
}

func sortedFieldKeys(fields map[string]RequirementField) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// effectiveRequirementDefaults 仅列出实际被展开的默认;已有 active 用户值的
// 字段不列入。输出顺序固定:budget_flex、size_pref、noise_pref、brand_pref.cpu、
// brand_pref.gpu、configuration_scope,gaming 最后追加 performance_goal。
func effectiveRequirementDefaults(state RequirementState, useCaseType string) []RequirementDefault {
	defaults := []RequirementDefault{}
	add := func(field string, value any) {
		if state.Fields[field].Status == "active" {
			return
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return
		}
		defaults = append(defaults, RequirementDefault{Field: field, Value: raw, Origin: "system_default"})
	}
	add("budget_flex", defaultBudgetFlex)
	add("size_pref", SizePrefAny)
	add("noise_pref", NoisePrefAny)
	add("brand_pref.cpu", CPUBrandAny)
	add("brand_pref.gpu", GPUBrandAny)
	add("configuration_scope", []string{ConfigurationScopeTower})
	if useCaseType == "gaming" && state.Fields["use_case.performance_goal"].Status != "active" {
		add("performance_goal", PerformanceGoalBalanced)
	}
	return defaults
}

// RequirementQuestion 是确定性追问计划的一轮输出:只选择字段和稳定原因码,
// 不生成自然语言。ready 或无阻塞时返回 nil。
type RequirementQuestion struct {
	ReasonCode string   `json:"reason_code"`
	Fields     []string `json:"fields"`
}

// NextRequirementQuestion 按固定优先级选择下一个追问组;Readiness 的
// missing/blocking 顺序与该计划共用 requirementQuestionOrder,不维护第二份列表。
func NextRequirementQuestion(state RequirementState) (*RequirementQuestion, error) {
	readiness, err := EvaluateRequirementReadiness(state)
	if err != nil {
		return nil, err
	}
	if readiness.Status == "ready" {
		return nil, nil
	}
	if len(readiness.BlockingConflicts) > 0 {
		return &RequirementQuestion{ReasonCode: "requirement_conflict", Fields: readiness.BlockingConflicts[:1]}, nil
	}
	if len(readiness.UnsupportedCapabilities) > 0 {
		return &RequirementQuestion{ReasonCode: "unsupported_capability", Fields: readiness.UnsupportedCapabilities}, nil
	}
	if len(readiness.MissingFields) == 0 {
		return nil, nil
	}
	first := readiness.MissingFields[0]
	fields := []string{first}
	// 缺失的已有件型号可合并成一个请求;其余组每轮只选一个问题组。
	if strings.HasPrefix(first, "owned_parts.") {
		for _, key := range readiness.MissingFields[1:] {
			if strings.HasPrefix(key, "owned_parts.") {
				fields = append(fields, key)
			}
		}
	}
	reason := map[string]string{
		"use_case.type":       "missing_use_case_type",
		"budget_cny":          "missing_budget_cny",
		"existing_parts":      "missing_existing_parts",
		"use_case.resolution": "missing_use_case_resolution",
		"use_case.titles":     "missing_use_case_titles",
		"budget_basis":        "missing_budget_basis",
	}[first]
	if reason == "" {
		reason = "missing_owned_part_models"
	}
	return &RequirementQuestion{ReasonCode: reason, Fields: fields}, nil
}

// unsupportedCapabilityName 供评估与测试检查原因码解析;不识别自由文本。
func unsupportedCapabilityName(reason string) (string, bool) {
	name, structured := strings.CutPrefix(reason, unsupportedCapabilityPrefix)
	if !structured || !containsString(unsupportedCapabilityNames, name) {
		return "", false
	}
	return name, true
}
