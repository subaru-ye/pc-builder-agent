package schemas

// 当前会话的需求记忆与生成用 RequirementSpec 分开建模：未知不等于 any，
// 撤销保留墓碑，临时例外保留覆盖前值；确认快照由产品层单独保存。
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// RequirementStateSchemaVersion 当前唯一支持的需求状态版本。v2 一次性切换:
// 移除持久化 reply/next_action(模型不再拥有需求完整性或流程决策权),
// 新增 use_case.performance_goal。v1 由 DecodeRequirementState 以稳定错误拒绝。
const RequirementStateSchemaVersion = 2

// ErrRequirementContextField distinguishes background from executable fields.
// Callers may retain the original text without adopting an incompatible value.
var ErrRequirementContextField = errors.New("补充背景请使用 notes、recipient 或独立 free.* 条目，不能作为固定执行字段")

// ErrRequirementStateUnsupported 是读取旧版本需求状态的稳定错误:
// 不静默重建、补默认或自动迁移;开发数据通过显式运维步骤重建。
var ErrRequirementStateUnsupported = errors.New("requirement state: 不支持的 schema_version(当前仅 2;旧会话需按运维步骤重建)")

// unsupportedCapabilityPrefix 是 observation 的稳定结构化原因码前缀;
// 自由文本 reason 不做关键词推断,name 只允许 monitor|keyboard|mouse。
const unsupportedCapabilityPrefix = "unsupported_capability:"

var unsupportedCapabilityNames = []string{"monitor", "keyboard", "mouse"}

// unsupportedCapabilityField 解析 reserved 撤销键 unsupported.<capability>:
// 该键不是需求字段,只承载"用户明确放弃指定 capability"的撤销操作,
// 由 Reducer 直接解除同名 unsupported 观察记录,不写入 fields。
func unsupportedCapabilityField(field string) (string, bool) {
	name, ok := strings.CutPrefix(field, "unsupported.")
	if !ok || !containsString(unsupportedCapabilityNames, name) {
		return "", false
	}
	return name, true
}

var RequirementFieldKeys = []string{
	"budget_cny", "budget_flex", "budget_basis", "use_case.type", "use_case.titles",
	"use_case.resolution", "use_case.performance_goal", "use_case.fps_target",
	"existing_parts", "owned_parts",
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

// RequirementUpdate 是领域需求合同:只含 operations/observations,
// 不含会话回复与流程动作;reply/next_action 只存在于 pipeline 的临时
// legacy turn 传输适配中,进入 Reducer 前必须剥离。
type RequirementUpdate struct {
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
	return update, nil
}

// DecodeRequirementState 是产品读取持久化需求状态的唯一严格入口:
// v1(含 reply/next_action 外形或 schema_version=1)以稳定错误拒绝。
func DecodeRequirementState(raw []byte) (RequirementState, error) {
	var state RequirementState
	if len(raw) == 0 {
		return state, nil
	}
	if err := decodeStrict(raw, &state); err != nil {
		return state, fmt.Errorf("requirement state: %w", err)
	}
	if state.SchemaVersion != RequirementStateSchemaVersion {
		return state, ErrRequirementStateUnsupported
	}
	return state, nil
}

// ApplyRequirementUpdate 是聊天和界面编辑的唯一 reducer。完整校验成功才返回新状态，
// 未出现的字段保持，仅联动已有件品类与型号；传入值不被修改，可保留为已确认快照。
// v2 语义:operations 与 observations 均为空时是字节级 no-op(不增 revision);
// 每个成功的非空批次只将 revision 增加 1;任一项失败则输入状态原样返回。
func ApplyRequirementUpdate(state RequirementState, update RequirementUpdate, source RequirementSource) (RequirementState, error) {
	if source.Kind != "chat" && source.Kind != "edit" {
		return state, fmt.Errorf("requirement source: kind 仅允许 chat 或 edit")
	}
	if len(update.Operations) > 32 {
		return state, fmt.Errorf("requirement update: 每轮最多 32 项")
	}
	if len(update.Observations) > 32 {
		return state, fmt.Errorf("requirement update: observations 最多 32 项")
	}
	if len(update.Operations) == 0 && len(update.Observations) == 0 {
		return state, nil
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
		return state, ErrRequirementStateUnsupported
	}
	if next.Fields == nil {
		next = NewRequirementState()
	}
	next.SchemaVersion = RequirementStateSchemaVersion
	next.Revision++
	next.Changes = []RequirementChange{}
	if next.Alternatives == nil {
		next.Alternatives = []RequirementAlternative{}
	}
	if next.History == nil {
		next.History = []RequirementChange{}
	}
	for _, op := range update.Operations {
		if capability, reserved := unsupportedCapabilityField(op.Field); reserved {
			if op.Op != "remove" || len(op.Value) > 0 {
				return state, fmt.Errorf("requirement update: %s 仅支持携带本轮证据的撤销(remove)", op.Field)
			}
			evidence := source
			if source.Kind == "chat" {
				if strings.TrimSpace(op.Quote) == "" || !strings.Contains(source.Quote, op.Quote) {
					return state, fmt.Errorf("requirement update: %s 缺少本轮用户原文证据", op.Field)
				}
				evidence.Quote = op.Quote
			}
			withdrawn := false
			for i := range next.Observations {
				if name, coded := unsupportedCapabilityName(next.Observations[i].Reason); coded && name == capability && !next.Observations[i].Resolved {
					next.Observations[i].Resolved = true
					withdrawn = true
				}
			}
			if !withdrawn {
				return state, fmt.Errorf("requirement update: %s 没有可撤销的 unsupported 观察记录", op.Field)
			}
			change := RequirementChange{Revision: next.Revision, Op: "remove", Field: op.Field, Source: evidence}
			next.Changes = append(next.Changes, change)
			next.History = append(next.History, change)
			continue
		}
		before, ok := next.Fields[op.Field]
		if !ok && knownRequirementField(op.Field) {
			// 已知字段允许缺席于旧状态映射(v1 外形/部分构造状态):
			// 按 unknown 起算,只有操作实际落地时才写入该键,
			// 不为未触碰字段凭空补 unknown 键。
			before = RequirementField{Status: "unknown"}
			ok = true
		}
		if !ok && FreeField(op.Field) {
			before = RequirementField{Status: "unknown"}
			next.Fields[op.Field] = before
			ok = true
		}
		if !ok || !knownRequirementField(op.Field) {
			return state, fmt.Errorf("requirement update: 未知字段 %q", op.Field)
		}
		if op.Evidence != "" && op.Evidence != "stated" && op.Evidence != "uncertain" && op.Evidence != "inferred" && op.Evidence != "accepted_proposal" {
			return state, fmt.Errorf("requirement update: 非法 evidence")
		}
		if op.Evidence == "inferred" || (op.Evidence == "uncertain" && op.Op != "conflict") {
			return state, fmt.Errorf("requirement update: 推断或不确定信息不能作为已表达要求")
		}
		// accepted_proposal 是用户明确接受助手建议的表达:领域层只做合同校验
		// (仅 set、仅 chat 来源、绑定本轮原文);proposal 的存在、字段/值/归属
		// 与紧邻轮次由产品层在持久化边界验证,模型标签本身不构成证据。
		if op.Evidence == "accepted_proposal" && (op.Op != "set" || source.Kind != "chat") {
			return state, fmt.Errorf("requirement update: accepted_proposal 仅支持聊天轮的 set 操作")
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
		if kind == "context" && op.Field != "notes" && op.Field != "recipient" && !FreeField(op.Field) && (op.Op == "set" || op.Op == "alternative" || op.Op == "conflict") {
			return state, fmt.Errorf("%w: %s", ErrRequirementContextField, op.Field)
		}
		if op.Op == "set" || op.Op == "alternative" || (op.Op == "conflict" && len(op.Value) > 0) {
			if err := validateRequirementValue(op.Field, op.Value); err != nil {
				return state, err
			}
			op.Value = normalizeRequirementValue(op.Field, op.Value)
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
			if after.Kind == "context" && op.Field != "notes" && op.Field != "recipient" && !FreeField(op.Field) {
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
		if name, structured := strings.CutPrefix(observation.Reason, unsupportedCapabilityPrefix); structured &&
			!slices.Contains(unsupportedCapabilityNames, name) {
			return state, fmt.Errorf("requirement update: unsupported_capability 仅允许 %s", strings.Join(unsupportedCapabilityNames, "|"))
		}
		evidence := source
		evidence.Quote = observation.Quote
		removedThisTurn := false
		if _, coded := unsupportedCapabilityName(observation.Reason); !coded && next.Fields[observation.Field].Status == "removed" {
			for _, change := range next.Changes {
				if change.Field == observation.Field && change.Op == "remove" {
					removedThisTurn = true
				}
			}
		}
		next.Observations = append(next.Observations, RequirementObservation{Field: observation.Field, Text: observation.Quote, Reason: observation.Reason, Source: evidence, Resolved: removedThisTurn})
	}
	if err := validateOwnershipInvariant(next); err != nil {
		return state, err
	}
	return next, nil
}

// validateOwnershipInvariant 拒绝 existing/owned 联动被破坏的批结果:
// 不允许 existing=[] 而 owned 非空,owned 品类必须落在 existing 内。
func validateOwnershipInvariant(state RequirementState) error {
	var existing []Category
	var owned []OwnedPart
	if field := state.Fields["existing_parts"]; field.Status == "active" {
		if json.Unmarshal(field.Value, &existing) != nil {
			return fmt.Errorf("requirement update: existing_parts 值无法解析")
		}
	}
	if field := state.Fields["owned_parts"]; field.Status == "active" {
		if json.Unmarshal(field.Value, &owned) != nil {
			return fmt.Errorf("requirement update: owned_parts 值无法解析")
		}
	}
	if len(existing) == 0 && len(owned) > 0 {
		return fmt.Errorf("requirement update: existing_parts 为空时不能保留 owned_parts")
	}
	categories := map[Category]bool{}
	for _, c := range existing {
		categories[c] = true
	}
	for _, part := range owned {
		if !categories[part.Category] {
			return fmt.Errorf("requirement update: owned_parts.%s 不在 existing_parts 内", part.Category)
		}
	}
	return nil
}

// normalizeRequirementValue 在校验通过后做确定性规范化:titles 修剪去重保序,
// 品类数组去重并按领域固定顺序输出,owned_parts 按品类固定顺序排列。
// 只做形状规范化,不从文本推断任何事实。
func normalizeRequirementValue(key string, raw json.RawMessage) json.RawMessage {
	switch key {
	case "use_case.titles":
		var titles []string
		if json.Unmarshal(raw, &titles) != nil {
			return raw
		}
		seen := map[string]bool{}
		normalized := make([]string, 0, len(titles))
		for _, title := range titles {
			title = strings.TrimSpace(title)
			if title == "" || seen[title] {
				continue
			}
			seen[title] = true
			normalized = append(normalized, title)
		}
		out, err := json.Marshal(normalized)
		if err != nil {
			return raw
		}
		return out
	case "existing_parts", "priority":
		var cats []Category
		if json.Unmarshal(raw, &cats) != nil {
			return raw
		}
		seen := map[Category]bool{}
		normalized := make([]Category, 0, len(cats))
		for _, known := range AllCategories {
			for _, c := range cats {
				if c == known && !seen[c] {
					seen[c] = true
					normalized = append(normalized, c)
				}
			}
		}
		out, err := json.Marshal(normalized)
		if err != nil {
			return raw
		}
		return out
	case "owned_parts":
		var parts []OwnedPart
		if json.Unmarshal(raw, &parts) != nil {
			return raw
		}
		normalized := make([]OwnedPart, 0, len(parts))
		for _, known := range AllCategories {
			for _, part := range parts {
				if part.Category == known {
					normalized = append(normalized, part)
				}
			}
		}
		out, err := json.Marshal(normalized)
		if err != nil {
			return raw
		}
		return out
	}
	return raw
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
		if err == nil && (n < 0 || n > maxBudgetFlex) {
			err = fmt.Errorf("必须在 0–%.1f 之间", maxBudgetFlex)
		}
	case "use_case.type":
		value = new(UseCaseType)
	case "use_case.resolution":
		value = new(Resolution)
	case "use_case.performance_goal":
		value = new(PerformanceGoal)
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
		if err == nil {
			for _, title := range titles {
				if strings.TrimSpace(title) == "" {
					err = fmt.Errorf("元素 trim 后必须非空")
					break
				}
			}
		}
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

// RequirementStateSpec 是 Planning、确认与 API 读取共用的唯一投影入口:
// 先走同一 readiness 规则,任何 incomplete(缺失、阻塞冲突、unsupported)
// 都返回 nil spec 和完整 readiness,不生成可供 Builder 使用的 RequirementSpec。
// 调用方必须以 readiness.ConfirmationEligible 判定就绪,不得自行数 missing;
// ready 时只投影 active 用户字段,系统默认由 RequirementSpec v2 解码展开。
// 传入状态不被修改。
func RequirementStateSpec(state RequirementState) (json.RawMessage, RequirementReadiness, error) {
	readiness, err := EvaluateRequirementReadiness(state)
	if err != nil {
		return nil, readiness, err
	}
	if !readiness.ConfirmationEligible {
		return nil, readiness, nil
	}
	values := map[string]any{"schema_version": RequirementSpecSchemaVersion, "configuration_scope": []string{ConfigurationScopeTower}}
	strengths := map[string]string{}
	semantics := map[string]string{}
	details := map[string]json.RawMessage{}
	for _, key := range RequirementFieldKeys {
		field := state.Fields[key]
		if field.Status != "active" {
			continue
		}
		if err := validateRequirementValue(key, field.Value); err != nil {
			return nil, readiness, err
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
	// free.* 是有来源、可独立修改的用户事实,整体进入 requirement_details;
	// 不满足结构化必填,也不生成 RequirementSpec 未定义的嵌套对象。
	for _, key := range sortedFieldKeys(state.Fields) {
		if !FreeField(key) || state.Fields[key].Status != "active" {
			continue
		}
		if err := validateRequirementValue(key, state.Fields[key].Value); err != nil {
			return nil, readiness, err
		}
		details[key] = state.Fields[key].Value
		strengths[key] = state.Fields[key].Strength
		if kind := state.Fields[key].Kind; kind != "" {
			semantics[key] = kind
		}
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
	// 可选软字段的歧义保留给后续理解,不把旧值继续作为 active,也不阻塞
	// 已充分的预算/用途;真实硬条件与生成依赖字段仍需确认后才能投影。
	for _, key := range RequirementFieldKeys {
		field := state.Fields[key]
		if field.Status == "conflict" && field.Strength != "must" && field.Source != nil && field.Source.Quote != "" {
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
		return nil, readiness, err
	}
	spec, err := DecodeRequirementSpec(raw)
	if err != nil {
		return nil, readiness, err
	}
	raw, err = EncodeRequirementSpec(spec)
	return raw, readiness, err
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

// RequirementReviewSpec 是核定预览的唯一入口:在 RequirementStateSpec 投影
// 之上把 readiness 列出的有效系统默认显式展开,得到"用户看到什么就冻结什么"
// 的规范化有效 RequirementSpec v2。review_hash 基于该投影:默认规则或配置
// 范围变化即使 revision 不变也会改变 hash,从而拒绝旧核定预览。
func RequirementReviewSpec(state RequirementState) (json.RawMessage, RequirementReadiness, error) {
	spec, readiness, err := RequirementStateSpec(state)
	if err != nil || spec == nil {
		return spec, readiness, err
	}
	decoded, err := DecodeRequirementSpec(spec)
	if err != nil {
		return nil, readiness, err
	}
	for _, d := range readiness.EffectiveDefaults {
		switch d.Field {
		case "budget_flex":
			_ = json.Unmarshal(d.Value, &decoded.BudgetFlex)
		case "size_pref":
			_ = json.Unmarshal(d.Value, &decoded.SizePref)
		case "noise_pref":
			_ = json.Unmarshal(d.Value, &decoded.NoisePref)
		case "brand_pref.cpu":
			_ = json.Unmarshal(d.Value, &decoded.BrandPref.CPU)
		case "brand_pref.gpu":
			_ = json.Unmarshal(d.Value, &decoded.BrandPref.GPU)
		case "performance_goal":
			_ = json.Unmarshal(d.Value, &decoded.UseCase.PerformanceGoal)
		}
	}
	raw, err := EncodeRequirementSpec(decoded)
	return raw, readiness, err
}

// RequirementReviewDiffEntry 是核定差异的一行:field 与 RequirementState
// 字段键一致,before/after 是规范化有效 JSON 值(未表达一侧为 null)。
// 前端只把字段名和值映射为用户可读标签,不自行比较 JSON。
type RequirementReviewDiffEntry struct {
	Field  string          `json:"field"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// RequirementReviewDiff 按规范化有效字段比较确认快照与当前核定预览的
// review_spec。任一侧不可投影(空或无法解码)时返回错误,由调用方降级为
// “差异不可用”,不编造比较结果;可比较但无差异时返回空列表。
func RequirementReviewDiff(beforeSpec, afterSpec json.RawMessage) ([]RequirementReviewDiffEntry, error) {
	before, err := flattenRequirementSpecFields(beforeSpec)
	if err != nil {
		return nil, err
	}
	after, err := flattenRequirementSpecFields(afterSpec)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(before)+len(after))
	for key := range before {
		keys = append(keys, key)
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var diff []RequirementReviewDiffEntry
	for _, key := range keys {
		beforeValue, afterValue := before[key], after[key]
		if len(beforeValue) == len(afterValue) && bytes.Equal(beforeValue, afterValue) {
			continue
		}
		diff = append(diff, RequirementReviewDiffEntry{Field: key, Before: beforeValue, After: afterValue})
	}
	return diff, nil
}

// flattenRequirementSpecFields 把规范化(EncodeRequirementSpec 展开)的
// RequirementSpec v2 摊平为用户字段路径 → 紧凑 JSON 值。仅比较有效字段;
// 空输入返回错误,因为确认快照与预览都不该为空。
func flattenRequirementSpecFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("requirement review diff: 缺少可比较的规范化需求单")
	}
	var spec requirementSpecWire
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("requirement review diff: %w", err)
	}
	// 规范化 review_spec 必然携带 schema_version(EncodeRequirementSpec 展开);
	// 缺失说明快照损坏,不可比较。
	if spec.SchemaVersion == nil {
		return nil, fmt.Errorf("requirement review diff: 缺少规范化 schema_version")
	}
	values := map[string]json.RawMessage{}
	must := func(key string, value any) {
		encoded, err := json.Marshal(value)
		if err == nil {
			values[key] = compactJSON(encoded)
		}
	}
	optionalString := func(key string, value *string) {
		if value != nil && *value != "" {
			must(key, *value)
		} else {
			values[key] = compactJSON([]byte("null"))
		}
	}
	if spec.BudgetCNY != nil && *spec.BudgetCNY > 0 {
		must("budget_cny", *spec.BudgetCNY)
	} else {
		values["budget_cny"] = compactJSON([]byte("null"))
	}
	if spec.BudgetFlex != nil {
		must("budget_flex", *spec.BudgetFlex)
	}
	if spec.BudgetBasis != "" {
		must("budget_basis", spec.BudgetBasis)
	} else {
		values["budget_basis"] = compactJSON([]byte("null"))
	}
	if spec.UseCase != nil {
		if spec.UseCase.Type != nil {
			must("use_case.type", *spec.UseCase.Type)
		}
		must("use_case.titles", spec.UseCase.Titles)
		if spec.UseCase.Resolution != nil {
			must("use_case.resolution", *spec.UseCase.Resolution)
		} else {
			values["use_case.resolution"] = compactJSON([]byte("null"))
		}
		if spec.UseCase.FPSTarget != nil {
			must("use_case.fps_target", *spec.UseCase.FPSTarget)
		} else {
			values["use_case.fps_target"] = compactJSON([]byte("null"))
		}
		if spec.UseCase.PerformanceGoal != nil {
			must("use_case.performance_goal", *spec.UseCase.PerformanceGoal)
		}
	}
	if spec.SizePref != nil {
		must("size_pref", *spec.SizePref)
	}
	if spec.NoisePref != nil {
		must("noise_pref", *spec.NoisePref)
	}
	if spec.BrandPref != nil {
		if spec.BrandPref.CPU != nil {
			must("brand_pref.cpu", *spec.BrandPref.CPU)
		}
		if spec.BrandPref.GPU != nil {
			must("brand_pref.gpu", *spec.BrandPref.GPU)
		}
	}
	must("configuration_scope", spec.ConfigurationScope)
	must("existing_parts", spec.ExistingParts)
	if spec.OwnedParts != nil {
		must("owned_parts", spec.OwnedParts)
	} else {
		values["owned_parts"] = compactJSON([]byte("[]"))
	}
	must("priority", spec.Priority)
	optionalString("notes", spec.Notes)
	for key, value := range spec.RequirementDetails {
		values[key] = compactJSON(value)
	}
	if spec.ConstraintStrengths != nil {
		must("constraint_strengths", spec.ConstraintStrengths)
	}
	return values, nil
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, raw); err != nil {
		return raw
	}
	return buffer.Bytes()
}

// NormalizeRequirementValue 校验并做确定性规范化(供 proposal 值比对等
// 持久化边界使用):非法值返回错误,不改写、不丢弃。
func NormalizeRequirementValue(key string, raw json.RawMessage) (json.RawMessage, error) {
	if err := validateRequirementValue(key, raw); err != nil {
		return nil, err
	}
	normalized := normalizeRequirementValue(key, raw)
	if len(normalized) == 0 {
		return nil, fmt.Errorf("requirement update: %s 必须提供非空 JSON 值", key)
	}
	return append(json.RawMessage(nil), normalized...), nil
}

// RequirementStateQuestions 是产品层的展示文案:字段与顺序完全来自
// readiness 的 missing/blocking 结论,不维护第二份优先级清单。
func RequirementStateQuestions(state RequirementState) string {
	readiness, err := EvaluateRequirementReadiness(state)
	if err != nil {
		return "需求记录存在无法处理的字段，请检查本轮修改。"
	}
	conflicts := map[string]bool{}
	for _, key := range readiness.BlockingConflicts {
		conflicts[key] = true
	}
	var questions []string
	for _, key := range readiness.MissingFields {
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
	for _, key := range readiness.BlockingConflicts {
		questions = append(questions, "请确认"+RequirementFieldLabel(key)+"应采用哪个要求。")
	}
	if len(readiness.UnsupportedCapabilities) > 0 {
		questions = append(questions, "当前配置范围仅支持主机（tower），显示器、键盘、鼠标暂不在本次范围内；如需包含请说明可先放弃，或等待后续支持。")
	}
	return strings.Join(questions, "")
}

func RequirementFieldLabel(key string) string {
	if FreeField(key) {
		return "补充要求"
	}
	labels := map[string]string{"budget_cny": "预算", "budget_flex": "预算弹性", "budget_basis": "预算口径", "use_case.type": "用途", "use_case.titles": "游戏或软件", "use_case.resolution": "分辨率", "use_case.performance_goal": "性能取向", "use_case.fps_target": "目标帧率", "existing_parts": "已有配件", "owned_parts": "已有配件型号", "brand_pref.cpu": "CPU 品牌", "brand_pref.gpu": "显卡品牌", "noise_pref": "静音", "size_pref": "尺寸", "appearance": "外观", "notes": "补充说明", "recipient": "装机对象"}
	if label := labels[key]; label != "" {
		return label
	}
	if strings.HasPrefix(key, "owned_parts.") {
		return "已有 " + strings.TrimSuffix(strings.TrimPrefix(key, "owned_parts."), ".model") + " 的完整型号"
	}
	return key
}

// promptViewMaxBytes 是需求视图的总量上限:超限按活跃度与来源强度裁剪,
// 并在视图里显式声明,未展示不等于不存在(完整状态始终在产品面板)。
const promptViewMaxBytes = 24 << 10

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
	alternatives := append([]RequirementAlternative(nil), state.Alternatives...)
	// 未知值字段是样板,先整体折叠;其余裁剪逐项进行,给声明留出字节余量。
	for key := range fields {
		if field := state.Fields[key]; field.Status == "unknown" && len(field.Value) == 0 && field.Previous == nil {
			delete(fields, key)
		}
	}
	note := ""
	build := func() []byte {
		payload := map[string]any{"revision": state.Revision, "fields": fields, "alternatives": alternatives, "observations": observations}
		if note != "" {
			payload["view_note"] = note
		}
		raw, _ := json.Marshal(payload)
		return raw
	}
	raw := build()
	for len(raw) > promptViewMaxBytes {
		if !trimPromptViewItem(fields, state, &observations, &alternatives) {
			return raw // 已无可裁剪项:保留声明,按原样送出。
		}
		if note == "" {
			note = "需求视图超过展示上限已被裁剪：未展示字段按未知处理，历史观察未完整展开；完整状态在需求面板，裁剪不改变权威需求。"
		}
		raw = build()
	}
	return raw
}

// trimPromptViewItem 每次按优先级裁掉一项:撤销墓碑 → 最旧观察 → 最旧备选 →
// 低强度自由条目 → 自由必须条件(结构化字段始终保留)。无可裁剪项返回 false。
func trimPromptViewItem(fields map[string]any, state RequirementState, observations *[]map[string]string, alternatives *[]RequirementAlternative) bool {
	for key := range fields {
		if field := state.Fields[key]; field.Status == "removed" && field.Previous == nil {
			delete(fields, key)
			return true
		}
	}
	if len(*observations) > 0 {
		*observations = (*observations)[1:]
		return true
	}
	if len(*alternatives) > 0 {
		*alternatives = (*alternatives)[1:]
		return true
	}
	for _, strength := range []string{"prefer", "must", ""} {
		for key := range fields {
			if FreeField(key) && state.Fields[key].Strength == strength {
				delete(fields, key)
				return true
			}
		}
	}
	return false
}
