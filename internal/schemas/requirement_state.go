package schemas

// 当前会话的需求记忆与生成用 RequirementSpec 分开建模：未知不等于 any，
// 撤销保留墓碑，临时例外保留覆盖前值；确认快照由产品层单独保存。
import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const RequirementStateSchemaVersion = 1

var RequirementFieldKeys = []string{
	"budget_cny", "budget_flex", "budget_basis", "use_case.type", "use_case.titles",
	"use_case.resolution", "use_case.fps_target", "existing_parts", "owned_parts",
	"brand_pref.cpu", "brand_pref.gpu", "noise_pref", "size_pref", "appearance", "notes", "recipient",
	"priority",
}

type RequirementSource struct {
	Kind      string `json:"kind"`
	MessageID string `json:"message_id"`
	Quote     string `json:"quote"`
}

type RequirementField struct {
	Value    json.RawMessage    `json:"value,omitempty"`
	Status   string             `json:"status"`
	Strength string             `json:"strength,omitempty"`
	Scope    string             `json:"scope,omitempty"`
	Source   *RequirementSource `json:"source,omitempty"`
	Previous *RequirementField  `json:"previous,omitempty"`
}

type RequirementAlternative struct {
	Field    string            `json:"field"`
	Value    json.RawMessage   `json:"value"`
	Strength string            `json:"strength"`
	Scope    string            `json:"scope"`
	Source   RequirementSource `json:"source"`
}

type RequirementChange struct {
	Revision int               `json:"revision"`
	Op       string            `json:"op"`
	Field    string            `json:"field"`
	Before   *RequirementField `json:"before,omitempty"`
	After    *RequirementField `json:"after,omitempty"`
	Source   RequirementSource `json:"source"`
}

type RequirementState struct {
	SchemaVersion int                         `json:"schema_version"`
	Revision      int                         `json:"revision"`
	Fields        map[string]RequirementField `json:"fields"`
	Alternatives  []RequirementAlternative    `json:"alternatives"`
	Changes       []RequirementChange         `json:"changes"`
	History       []RequirementChange         `json:"history"`
}

// RequirementOperation 的证据只允许来自本轮用户原文；消息 ID 不由模型提供。
type RequirementOperation struct {
	Op       string          `json:"op"`
	Field    string          `json:"field"`
	Value    json.RawMessage `json:"value,omitempty"`
	Strength string          `json:"strength,omitempty"`
	Scope    string          `json:"scope,omitempty"`
	Quote    string          `json:"quote,omitempty"`
}

type RequirementUpdate struct {
	Operations []RequirementOperation `json:"operations"`
}

func NewRequirementState() RequirementState {
	s := RequirementState{SchemaVersion: RequirementStateSchemaVersion,
		Fields: map[string]RequirementField{}, Alternatives: []RequirementAlternative{},
		Changes: []RequirementChange{}, History: []RequirementChange{}}
	for _, key := range RequirementFieldKeys {
		s.Fields[key] = RequirementField{Status: "unknown"}
	}
	return s
}

func DecodeRequirementUpdate(raw []byte) (RequirementUpdate, error) {
	var update RequirementUpdate
	if err := decodeStrict(raw, &update); err != nil {
		return update, fmt.Errorf("requirement update: %w", err)
	}
	if update.Operations == nil || len(update.Operations) > 32 {
		return update, fmt.Errorf("requirement update: operations 必须为数组且最多 32 项")
	}
	return update, nil
}

// ApplyRequirementUpdate 是聊天和界面编辑的唯一 reducer。完整校验成功才返回新状态，
// 未出现的字段永远保持；传入值不被修改，可直接保留为已确认快照。
func ApplyRequirementUpdate(state RequirementState, update RequirementUpdate, source RequirementSource) (RequirementState, error) {
	if source.Kind != "chat" && source.Kind != "edit" {
		return state, fmt.Errorf("requirement source: kind 仅允许 chat 或 edit")
	}
	if len(update.Operations) > 32 {
		return state, fmt.Errorf("requirement update: 每轮最多 32 项")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return state, err
	}
	var next RequirementState
	if err := json.Unmarshal(raw, &next); err != nil {
		return state, err
	}
	if next.SchemaVersion != 0 && next.SchemaVersion != RequirementStateSchemaVersion {
		return state, fmt.Errorf("requirement state: 不支持的 schema_version")
	}
	if next.Fields == nil {
		next = NewRequirementState()
	}
	next.SchemaVersion = RequirementStateSchemaVersion
	for _, key := range RequirementFieldKeys {
		if _, ok := next.Fields[key]; !ok {
			next.Fields[key] = RequirementField{Status: "unknown"}
		}
	}
	next.Revision++
	next.Changes = []RequirementChange{}
	if next.Alternatives == nil {
		next.Alternatives = []RequirementAlternative{}
	}
	if next.History == nil {
		next.History = []RequirementChange{}
	}
	for _, op := range update.Operations {
		before, ok := next.Fields[op.Field]
		if !ok || !knownRequirementField(op.Field) {
			return state, fmt.Errorf("requirement update: 未知字段 %q", op.Field)
		}
		evidence := source
		if source.Kind == "chat" {
			if strings.TrimSpace(op.Quote) == "" || !strings.Contains(source.Quote, op.Quote) {
				return state, fmt.Errorf("requirement update: %s 缺少本轮用户原文证据", op.Field)
			}
			evidence.Quote = op.Quote
		} else if op.Quote != "" {
			evidence.Quote = op.Quote
		}
		strength := op.Strength
		if strength == "" {
			strength = before.Strength
			if strength == "" {
				strength = defaultRequirementStrength(op.Field)
			}
		}
		if strength != "must" && strength != "prefer" {
			return state, fmt.Errorf("requirement update: strength 仅允许 must 或 prefer")
		}
		scope := op.Scope
		if scope == "" {
			scope = before.Scope
			if scope == "" {
				scope = "session"
			}
		}
		if scope != "session" && scope != "temporary" {
			return state, fmt.Errorf("requirement update: scope 仅允许 session 或 temporary")
		}
		if op.Op == "set" || op.Op == "alternative" || op.Op == "conflict" {
			if err := validateRequirementValue(op.Field, op.Value); err != nil {
				return state, err
			}
		}
		after := RequirementField{Value: append(json.RawMessage(nil), op.Value...), Status: "active", Strength: strength, Scope: scope, Source: &evidence}
		switch op.Op {
		case "set":
			if scope == "temporary" {
				if before.Scope == "temporary" && before.Previous != nil {
					after.Previous = before.Previous
				} else {
					after.Previous = &before
				}
			}
		case "remove":
			after = RequirementField{Status: "removed", Source: &evidence}
		case "restore":
			if before.Previous == nil {
				return state, fmt.Errorf("requirement update: %s 没有可恢复的临时覆盖", op.Field)
			}
			after = *before.Previous
			after.Source = &evidence
		case "alternative":
			next.Alternatives = append(next.Alternatives, RequirementAlternative{Field: op.Field, Value: after.Value, Strength: strength, Scope: scope, Source: evidence})
			after = before // 备选不参与当前字段投影。
		case "conflict":
			after.Status = "conflict"
			after.Previous = &before
		default:
			return state, fmt.Errorf("requirement update: 未知操作 %q", op.Op)
		}
		next.Fields[op.Field] = after
		change := RequirementChange{Revision: next.Revision, Op: op.Op, Field: op.Field, Before: &before, After: &after, Source: evidence}
		next.Changes = append(next.Changes, change)
		next.History = append(next.History, change)
		// 撤销整个已有件事实时，型号也不再有效；仅撤回型号时仍保留已知品类。
		if op.Op == "remove" && op.Field == "existing_parts" && next.Fields["owned_parts"].Status == "active" {
			ownedBefore := next.Fields["owned_parts"]
			ownedAfter := RequirementField{Status: "removed", Source: &evidence}
			next.Fields["owned_parts"] = ownedAfter
			ownedChange := RequirementChange{Revision: next.Revision, Op: "remove", Field: "owned_parts", Before: &ownedBefore, After: &ownedAfter, Source: evidence}
			next.Changes = append(next.Changes, ownedChange)
			next.History = append(next.History, ownedChange)
		}
	}
	return next, nil
}

func knownRequirementField(key string) bool {
	for _, known := range RequirementFieldKeys {
		if key == known {
			return true
		}
	}
	return false
}

func defaultRequirementStrength(key string) string {
	switch key {
	case "budget_cny", "budget_flex", "budget_basis", "use_case.type", "use_case.resolution", "existing_parts", "owned_parts", "recipient":
		return "must"
	default:
		return "prefer"
	}
}

func validateRequirementValue(key string, raw json.RawMessage) error {
	if !json.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("requirement update: %s 必须提供非空 JSON 值", key)
	}
	var value any
	var err error
	switch key {
	case "budget_cny", "use_case.fps_target":
		var n int
		err = json.Unmarshal(raw, &n)
		if err == nil && n <= 0 {
			err = fmt.Errorf("必须为正整数")
		}
	case "budget_flex":
		var n float64
		err = json.Unmarshal(raw, &n)
		if err == nil && (n < 0 || n > maxBudgetFlex) {
			err = fmt.Errorf("必须在 [0,0.3] 内")
		}
	case "use_case.type":
		value = new(UseCaseType)
	case "use_case.resolution":
		value = new(Resolution)
	case "size_pref":
		value = new(SizePref)
	case "noise_pref":
		value = new(NoisePref)
	case "brand_pref.cpu":
		value = new(CPUBrand)
	case "brand_pref.gpu":
		value = new(GPUBrand)
	case "existing_parts", "priority":
		var cats []Category
		err = json.Unmarshal(raw, &cats)
		if err == nil {
			err = validateCategories(key, cats)
		}
	case "owned_parts":
		var parts []OwnedPart
		err = decodeStrict(raw, &parts)
		if err == nil {
			err = validateOwned(&RequirementSpec{OwnedParts: parts})
		}
	case "use_case.titles":
		var titles []string
		err = json.Unmarshal(raw, &titles)
	case "budget_basis":
		var basis string
		err = json.Unmarshal(raw, &basis)
		if err == nil && basis != "new_purchase" && basis != "full_build" {
			err = fmt.Errorf("仅允许 new_purchase 或 full_build")
		}
	case "appearance", "notes", "recipient":
		var s string
		err = json.Unmarshal(raw, &s)
		if err == nil && (strings.TrimSpace(s) == "" || len([]rune(s)) > 2000) {
			err = fmt.Errorf("必须为 1–2000 字文本")
		}
	default:
		err = fmt.Errorf("未知字段")
	}
	if value != nil {
		err = json.Unmarshal(raw, value)
	}
	if err != nil {
		return fmt.Errorf("requirement update: %s: %w", key, err)
	}
	return nil
}

// RequirementStateSpec 只投影 active 字段，不从历史、备选或默认值生成用户偏好。
// 返回 nil payload 表示尚缺必要信息；生成侧默认值只在最终 spec 解码时展开。
func RequirementStateSpec(state RequirementState) (json.RawMessage, []string, error) {
	values := map[string]any{"schema_version": RequirementSpecSchemaVersion}
	strengths := map[string]string{}
	details := map[string]json.RawMessage{}
	var missing []string
	for _, key := range RequirementFieldKeys {
		field := state.Fields[key]
		if field.Status == "conflict" {
			missing = append(missing, key)
			continue
		}
		if field.Status != "active" {
			continue
		}
		if err := validateRequirementValue(key, field.Value); err != nil {
			return nil, nil, err
		}
		strengths[key] = field.Strength
		if key == "appearance" || key == "recipient" {
			details[key] = field.Value
			continue
		}
		parts := strings.SplitN(key, ".", 2)
		if len(parts) == 2 {
			object, _ := values[parts[0]].(map[string]any)
			if object == nil {
				object = map[string]any{}
				values[parts[0]] = object
			}
			object[parts[1]] = field.Value
		} else {
			values[key] = field.Value
		}
	}
	for _, key := range []string{"budget_cny", "use_case.type"} {
		if state.Fields[key].Status != "active" && state.Fields[key].Status != "conflict" {
			missing = append(missing, key)
		}
	}
	if state.Fields["use_case.type"].Status == "active" && string(state.Fields["use_case.type"].Value) == `"gaming"` && state.Fields["use_case.resolution"].Status != "active" && state.Fields["use_case.resolution"].Status != "conflict" {
		missing = append(missing, "use_case.resolution")
	}
	var existing []Category
	var owned []OwnedPart
	if field := state.Fields["existing_parts"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &existing)
	}
	if field := state.Fields["owned_parts"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &owned)
	}
	basis := ""
	if field := state.Fields["budget_basis"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &basis)
	}
	for _, key := range MissingOwnedFields(RequirementSpec{ExistingParts: existing, OwnedParts: owned, BudgetBasis: basis}) {
		if !containsString(missing, key) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, missing, nil
	}
	values["constraint_strengths"] = strengths
	if len(details) > 0 {
		values["requirement_details"] = details
	}
	// 额外外观诉求进入已有语义检索入口，明确强度，不把备选/撤销重新带入。
	if appearance := state.Fields["appearance"]; appearance.Status == "active" {
		var text, notes string
		_ = json.Unmarshal(appearance.Value, &text)
		if field := state.Fields["notes"]; field.Status == "active" {
			_ = json.Unmarshal(field.Value, &notes)
		}
		label := "尽量满足"
		if appearance.Strength == "must" {
			label = "必须满足"
		}
		values["notes"] = strings.TrimSpace(notes + "\n外观（" + label + "）：" + text)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, nil, err
	}
	spec, err := DecodeRequirementSpec(raw)
	if err != nil {
		return nil, nil, err
	}
	raw, err = EncodeRequirementSpec(spec)
	return raw, nil, err
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func RequirementStateQuestions(state RequirementState) string {
	_, missing, err := RequirementStateSpec(state)
	if err != nil {
		return "需求记录存在无法处理的字段，请检查本轮修改。"
	}
	var questions []string
	for _, key := range missing {
		if state.Fields[key].Status == "conflict" {
			questions = append(questions, "请确认"+RequirementFieldLabel(key)+"应采用哪个要求。")
			continue
		}
		switch key {
		case "budget_cny":
			questions = append(questions, "请提供预算金额，单位元。")
		case "use_case.type":
			questions = append(questions, "这台电脑主要用于什么用途？")
		case "use_case.resolution":
			questions = append(questions, "主要使用的游戏分辨率是1080p、2K还是4K？")
		case "budget_basis":
			questions = append(questions, "这笔预算是只用于新增购买配件，还是包含已有配件价值的整机参考总价？")
		default:
			questions = append(questions, "请补充"+RequirementFieldLabel(key)+"。")
		}
	}
	return strings.Join(questions, "")
}

func RequirementFieldLabel(key string) string {
	labels := map[string]string{"budget_cny": "预算", "budget_flex": "预算弹性", "budget_basis": "预算口径", "use_case.type": "用途", "use_case.titles": "游戏或软件", "use_case.resolution": "分辨率", "use_case.fps_target": "目标帧率", "existing_parts": "已有配件", "owned_parts": "已有配件型号", "brand_pref.cpu": "CPU 品牌", "brand_pref.gpu": "显卡品牌", "noise_pref": "静音", "size_pref": "尺寸", "appearance": "外观", "notes": "补充说明", "recipient": "装机对象"}
	if label := labels[key]; label != "" {
		return label
	}
	if strings.HasPrefix(key, "owned_parts.") {
		return "已有 " + strings.TrimSuffix(strings.TrimPrefix(key, "owned_parts."), ".model") + " 的完整型号"
	}
	return key
}

// RequirementStatePromptView 不把完整历史送给模型，只有当前值、撤销墓碑和备选。
// 来源按需展示在产品里；模型通过本轮原文增量更新，不能再次提取过去的旧要求。
func RequirementStatePromptView(state RequirementState) json.RawMessage {
	keys := append([]string(nil), RequirementFieldKeys...)
	sort.Strings(keys)
	fields := map[string]any{}
	for _, key := range keys {
		field := state.Fields[key]
		fields[key] = map[string]any{"status": field.Status, "value": field.Value, "strength": field.Strength, "scope": field.Scope, "previous": field.Previous}
	}
	raw, _ := json.Marshal(map[string]any{"revision": state.Revision, "fields": fields, "alternatives": state.Alternatives})
	return raw
}
