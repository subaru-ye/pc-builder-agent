package schemas

// 当前会话的需求记忆与生成用 RequirementSpec 分开建模：未知不等于 any，
// 撤销保留墓碑，临时例外保留覆盖前值；确认快照由产品层单独保存。
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const RequirementStateSchemaVersion = 1

// ErrRequirementContextField distinguishes background from executable fields.
// Callers may retain the original text without adopting an incompatible value.
var ErrRequirementContextField = errors.New("补充背景请使用 notes 或独立 free.* 条目，不能作为固定需求字段")

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
	Kind     string             `json:"kind,omitempty"`
	Evidence string             `json:"evidence,omitempty"`
	Strength string             `json:"strength,omitempty"`
	Scope    string             `json:"scope,omitempty"`
	Source   *RequirementSource `json:"source,omitempty"`
	Previous *RequirementField  `json:"previous,omitempty"`
	// DerivedFrom links an ownership update to its category/model counterpart,
	// so restoring a temporary exception does not overwrite a later direct edit.
	DerivedFrom string `json:"derived_from,omitempty"`
}

type RequirementAlternative struct {
	Field    string            `json:"field"`
	Value    json.RawMessage   `json:"value"`
	Strength string            `json:"strength"`
	Scope    string            `json:"scope"`
	Source   RequirementSource `json:"source"`
	Kind     string            `json:"kind,omitempty"`
}

// 未能可靠归入字段的信息保留原文与来源。Resolved 只由 reducer 随后续字段更新设置。
type RequirementObservation struct {
	Field    string            `json:"field,omitempty"`
	Text     string            `json:"text"`
	Reason   string            `json:"reason"`
	Source   RequirementSource `json:"source"`
	Resolved bool              `json:"resolved,omitempty"`
}

// 模型不能指定消息 ID 或来源类型；Quote 必须出自本轮用户消息。
type RequirementObservationInput struct {
	Field  string `json:"field,omitempty"`
	Quote  string `json:"quote"`
	Reason string `json:"reason"`
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
	Reply         string                      `json:"reply,omitempty"`
	NextAction    string                      `json:"next_action,omitempty"`
	SchemaVersion int                         `json:"schema_version"`
	Revision      int                         `json:"revision"`
	Fields        map[string]RequirementField `json:"fields"`
	Alternatives  []RequirementAlternative    `json:"alternatives"`
	Changes       []RequirementChange         `json:"changes"`
	History       []RequirementChange         `json:"history"`
	Observations  []RequirementObservation    `json:"observations,omitempty"`
}

// RequirementOperation 的证据只允许来自本轮用户原文；消息 ID 不由模型提供。
type RequirementOperation struct {
	Op       string          `json:"op"`
	Field    string          `json:"field"`
	Value    json.RawMessage `json:"value,omitempty"`
	Strength string          `json:"strength,omitempty"`
	Scope    string          `json:"scope,omitempty"`
	Quote    string          `json:"quote,omitempty"`
	Kind     string          `json:"kind,omitempty"`
	Evidence string          `json:"evidence,omitempty"`
}

type RequirementUpdate struct {
	Reply        string                        `json:"reply,omitempty"`
	NextAction   string                        `json:"next_action,omitempty"`
	Operations   []RequirementOperation        `json:"operations"`
	Observations []RequirementObservationInput `json:"observations,omitempty"`
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
	if update.Operations == nil || len(update.Operations) > 32 || len(update.Observations) > 32 {
		return update, fmt.Errorf("requirement update: operations 必须为数组且最多 32 项")
	}
	if update.NextAction != "" && update.NextAction != "collect" && update.NextAction != "confirm" && update.NextAction != "plan" {
		return update, fmt.Errorf("requirement update: next_action 无效")
	}
	return update, nil
}

// ApplyRequirementUpdate 是聊天和界面编辑的唯一 reducer。完整校验成功才返回新状态，
// 未出现的字段保持，仅联动已有件品类与型号；传入值不被修改，可保留为已确认快照。
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
	next.Reply, next.NextAction = update.Reply, update.NextAction
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
		if !ok && FreeField(op.Field) {
			before = RequirementField{Status: "unknown"}
			next.Fields[op.Field] = before
			ok = true
		}
		if !ok || !knownRequirementField(op.Field) {
			return state, fmt.Errorf("requirement update: 未知字段 %q", op.Field)
		}
		if op.Evidence != "" && op.Evidence != "stated" && op.Evidence != "uncertain" && op.Evidence != "inferred" {
			return state, fmt.Errorf("requirement update: 非法 evidence")
		}
		if op.Evidence == "inferred" || (op.Evidence == "uncertain" && op.Op != "conflict") {
			return state, fmt.Errorf("requirement update: 推断或不确定信息不能作为已表达要求")
		}
		kind := op.Kind
		// 强度编辑沿用同一值的语义；换成新文本却缺少分类时不得把旧 fact/context
		// 偷带到可能的新硬条件上。旧协议的未分类状态由下游明确处理。
		if kind == "" && (op.Op != "set" || bytes.Equal(bytes.TrimSpace(before.Value), bytes.TrimSpace(op.Value))) {
			kind = before.Kind
		}
		if kind != "" && !ValidRequirementKind(kind) {
			return state, fmt.Errorf("requirement update: kind 仅允许 fact、context 或 constraint")
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
		if kind == "context" && op.Field != "notes" && !FreeField(op.Field) && (op.Op == "set" || op.Op == "alternative" || op.Op == "conflict") {
			return state, fmt.Errorf("%w: %s", ErrRequirementContextField, op.Field)
		}
		if op.Op == "set" || op.Op == "alternative" || (op.Op == "conflict" && len(op.Value) > 0) {
			if err := validateRequirementValue(op.Field, op.Value); err != nil {
				return state, err
			}
		}
		after := RequirementField{Value: append(json.RawMessage(nil), op.Value...), Status: "active", Strength: strength, Scope: scope, Source: &evidence, Kind: kind, Evidence: op.Evidence}
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
			if after.Kind == "context" && op.Field != "notes" && !FreeField(op.Field) {
				return state, fmt.Errorf("%w: %s", ErrRequirementContextField, op.Field)
			}
			after.Source = &evidence
		case "alternative":
			next.Alternatives = append(next.Alternatives, RequirementAlternative{Field: op.Field, Value: after.Value, Strength: strength, Scope: scope, Source: evidence, Kind: kind})
			after = before // 备选不参与当前字段投影。
		case "conflict":
			after.Status = "conflict"
			after.Previous = &before
		default:
			return state, fmt.Errorf("requirement update: 未知操作 %q", op.Op)
		}
		recordRequirementChange(&next, op.Field, op.Op, before, after, evidence)
		syncOwnershipFields(&next, op, after, evidence)
	}
	if len(update.Observations) > 32 {
		return state, fmt.Errorf("requirement update: observations 最多 32 项")
	}
	for _, observation := range update.Observations {
		if observation.Field == "" {
			observation.Field = "notes"
		}
		if strings.TrimSpace(observation.Quote) == "" || !strings.Contains(source.Quote, observation.Quote) || len([]rune(observation.Quote)) > 4000 {
			return state, fmt.Errorf("requirement update: observation 缺少本轮原文证据")
		}
		if observation.Field != "" && !knownRequirementField(observation.Field) {
			return state, fmt.Errorf("requirement update: observation 未知字段")
		}
		if len([]rune(observation.Reason)) > 500 {
			return state, fmt.Errorf("requirement update: observation 原因过长")
		}
		evidence := source
		evidence.Quote = observation.Quote
		removedThisTurn := false
		if next.Fields[observation.Field].Status == "removed" {
			for _, change := range next.Changes {
				if change.Field == observation.Field && change.Op == "remove" {
					removedThisTurn = true
				}
			}
		}
		next.Observations = append(next.Observations, RequirementObservation{Field: observation.Field, Text: observation.Quote, Reason: observation.Reason, Source: evidence, Resolved: removedThisTurn})
	}
	return next, nil
}

func ValidRequirementKind(kind string) bool {
	return kind == "fact" || kind == "context" || kind == "constraint"
}

func knownRequirementField(key string) bool {
	if FreeField(key) {
		return true
	}
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
	if FreeField(key) {
		key = "notes"
	}
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
		if err == nil && n < 0 {
			err = fmt.Errorf("必须为非负数")
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
	semantics := map[string]string{}
	details := map[string]json.RawMessage{}
	var missing []string
	for _, key := range RequirementFieldKeys {
		field := state.Fields[key]
		if field.Status == "conflict" {
			if requirementConflictNeedsConfirmation(state, key, field) {
				missing = append(missing, key)
			}
			continue
		}
		if field.Status != "active" {
			continue
		}
		if err := validateRequirementValue(key, field.Value); err != nil {
			return nil, nil, err
		}
		strengths[key] = field.Strength
		if field.Kind != "" {
			semantics[key] = field.Kind
		}
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
	if len(semantics) > 0 {
		values["requirement_semantics"] = semantics
	}
	var observations []RequirementObservation
	for _, observation := range state.Observations {
		if !observation.Resolved {
			observations = append(observations, observation)
		}
	}
	// 可选软字段的歧义保留给后续理解，不把旧值继续作为 active，也不阻塞
	// 已充分的预算/用途；真实硬条件与生成依赖字段仍需确认后才能投影。
	for _, key := range RequirementFieldKeys {
		field := state.Fields[key]
		if field.Status == "conflict" && !requirementConflictNeedsConfirmation(state, key, field) && field.Source != nil && field.Source.Quote != "" {
			alreadyRecorded := false
			for _, observation := range observations {
				if observation.Field == key && observation.Text == field.Source.Quote && observation.Source.MessageID == field.Source.MessageID {
					alreadyRecorded = true
				}
			}
			if !alreadyRecorded {
				observations = append(observations, RequirementObservation{Field: key, Text: field.Source.Quote, Reason: "可选信息尚未明确，未作为当前偏好采用", Source: *field.Source})
			}
		}
	}
	if len(observations) > 0 {
		values["requirement_observations"] = observations
	}
	if len(details) > 0 {
		values["requirement_details"] = details
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

func requirementConflictNeedsConfirmation(state RequirementState, key string, field RequirementField) bool {
	switch key {
	case "budget_cny", "budget_flex", "budget_basis", "use_case.type", "existing_parts", "owned_parts":
		return true
	case "use_case.resolution":
		if string(state.Fields["use_case.type"].Value) == `"gaming"` {
			return true
		}
	case "notes":
		if field.Kind == "fact" || field.Kind == "context" {
			return false
		}
	}
	return field.Strength == "must"
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
	if FreeField(key) {
		return "补充要求"
	}
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
	for key := range state.Fields {
		if FreeField(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	fields := map[string]any{}
	for _, key := range keys {
		field := state.Fields[key]
		var previous any
		if field.Previous != nil {
			previous = map[string]any{"status": field.Previous.Status, "value": field.Previous.Value, "strength": field.Previous.Strength, "scope": field.Previous.Scope, "kind": field.Previous.Kind}
		}
		fields[key] = map[string]any{"status": field.Status, "value": field.Value, "strength": field.Strength, "scope": field.Scope, "kind": field.Kind, "evidence": field.Evidence, "previous": previous}
	}
	var observations []map[string]string
	for _, observation := range state.Observations {
		if !observation.Resolved {
			observations = append(observations, map[string]string{"field": observation.Field, "text": observation.Text, "reason": observation.Reason})
		}
	}
	raw, _ := json.Marshal(map[string]any{"revision": state.Revision, "fields": fields, "alternatives": state.Alternatives, "observations": observations})
	return raw
}
