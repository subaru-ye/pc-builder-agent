package planningeval

// Requirement v2 grader（reqv2-grader-v1）：以结构化真值判卷，不比较措辞。
// 判卷输入是 ReqV2TurnObservation / 层级观测；当前实现不可表达的概念记
// v2_contract_gap，不冒充通过。veto 检测独立于分数：任一 veto 出现即候选
// 不可发布，不能用模型层平均分抵消。
import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// 失败分类：分开通 v2 契约缺口与当前语义都认可的行错误。
const (
	reqV2ClassGap      = "v2_contract_gap"  // 当前实现没有该概念/合同
	reqV2ClassBehavior = "behavior_failure" // 双方契约共有但行为错误
	reqV2ClassProvider = "provider_failure" // 模型/网络失败
	reqV2ClassFault    = "technical_fault"  // 评估设施自身错误
)

type ReqV2Assertion struct {
	Name           string `json:"name"`
	Pass           bool   `json:"pass"`
	Detail         string `json:"detail,omitempty"`
	Veto           string `json:"veto,omitempty"`
	Classification string `json:"classification,omitempty"`
}

// ReqV2TurnObservation 是 grader 的统一消费对象：每层 runner 与 selftest
// 都产出该形状，replay 也按它重判。
type ReqV2TurnObservation struct {
	Operations   []schemas.RequirementOperation `json:"operations"`
	Observations []ReqV2ObservationProjection   `json:"observations,omitempty"`
	State        ReqV2StateProjection           `json:"state"`
	Readiness    *ReqV2ReadinessResult          `json:"readiness,omitempty"`
	// SignalsKnown=false 表示当前路径没有 turn signal 合同（区别于全 false）。
	Signals        *ReqV2TurnSignals `json:"turn_signals,omitempty"`
	SignalsKnown   bool              `json:"signals_known"`
	Reply          string            `json:"reply,omitempty"`
	BuilderStarted bool              `json:"builder_started"`
	// BuilderStartedViaChat：本轮为聊天消息却启动了 Builder；聊天文字不能
	// 替代 confirm API，ContinueScreeningRun 的自动 confirmed 不是用户核定。
	BuilderStartedViaChat    bool   `json:"builder_started_via_chat"`
	BuildersActive           int    `json:"builders_active"`
	ConfirmationSnapshotHash string `json:"confirmation_snapshot_hash,omitempty"`
	BuilderInputHash         string `json:"builder_input_hash,omitempty"`
	StaleEditAccepted        bool   `json:"stale_edit_accepted"`
	// WatchUnsupportedScope 是 selftest 的金丝雀开关：让 V9 检测在本观测上生效。
	WatchUnsupportedScope bool   `json:"watch_unsupported_scope,omitempty"`
	Error                 string `json:"error,omitempty"`
	ProviderError         string `json:"provider_error,omitempty"`
	ProviderErrorClass    string `json:"provider_error_class,omitempty"`
	ScreenModelCalled     bool   `json:"screen_model_called"`
	// ProviderRequests 含 guard 纠偏重调在内的真实 screening 请求数。
	ProviderRequests int    `json:"provider_requests,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
	InputTokens      *int32 `json:"input_tokens,omitempty"`
	OutputTokens     *int32 `json:"output_tokens,omitempty"`
}

type ReqV2SourceProjection struct {
	Kind      string `json:"kind,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Quote     string `json:"quote,omitempty"`
}

type ReqV2FieldProjection struct {
	Status   string                 `json:"status"`
	Value    json.RawMessage        `json:"value,omitempty"`
	Kind     string                 `json:"kind,omitempty"`
	Strength string                 `json:"strength,omitempty"`
	Scope    string                 `json:"scope,omitempty"`
	Evidence string                 `json:"evidence,omitempty"`
	Source   *ReqV2SourceProjection `json:"source,omitempty"`
	Previous *ReqV2FieldProjection  `json:"previous,omitempty"`
}

type ReqV2AlternativeProjection struct {
	Field    string                `json:"field"`
	Value    json.RawMessage       `json:"value"`
	Strength string                `json:"strength,omitempty"`
	Scope    string                `json:"scope,omitempty"`
	Kind     string                `json:"kind,omitempty"`
	Source   ReqV2SourceProjection `json:"source"`
}

type ReqV2ObservationProjection struct {
	Field    string                `json:"field,omitempty"`
	Text     string                `json:"text"`
	Reason   string                `json:"reason"`
	Source   ReqV2SourceProjection `json:"source"`
	Resolved bool                  `json:"resolved,omitempty"`
}

// ReqV2StateProjection 是判卷用的 canonical 状态视图：字段按 key 排序，
// source 保留 kind/message/quote，去掉随机 id 之外的噪声。
type ReqV2StateProjection struct {
	SchemaVersion int                             `json:"schema_version"`
	Revision      int                             `json:"revision"`
	Reply         string                          `json:"reply,omitempty"`
	NextAction    string                          `json:"next_action,omitempty"`
	Fields        map[string]ReqV2FieldProjection `json:"fields"`
	Alternatives  []ReqV2AlternativeProjection    `json:"alternatives"`
	Observations  []ReqV2ObservationProjection    `json:"observations"`
}

// ReqV2ReadinessResult 是 readiness 层与各层共用的就绪观测。
type ReqV2ReadinessResult struct {
	Status                  string             `json:"status"`
	MissingFields           []string           `json:"missing_fields"`
	BlockingConflicts       []string           `json:"blocking_conflicts"`
	ConfirmationEligible    bool               `json:"confirmation_eligible"`
	EffectiveDefaults       []ReqV2DefaultGold `json:"effective_defaults"`
	UnsupportedCapabilities []string           `json:"unsupported_capabilities,omitempty"`
	// ContractGaps 记录当前适配器无法表达的概念（blocking 分离、默认值），
	// 只作归因，不影响 veto 判定。
	ContractGaps []string `json:"contract_gaps,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func projectSource(s *schemas.RequirementSource) *ReqV2SourceProjection {
	if s == nil {
		return nil
	}
	return &ReqV2SourceProjection{Kind: s.Kind, MessageID: s.MessageID, Quote: s.Quote}
}

func projectField(f schemas.RequirementField) ReqV2FieldProjection {
	var previous *ReqV2FieldProjection
	if f.Previous != nil {
		p := projectField(*f.Previous)
		previous = &p
	}
	return ReqV2FieldProjection{
		Status: f.Status, Value: append(json.RawMessage(nil), f.Value...), Kind: f.Kind,
		Strength: f.Strength, Scope: f.Scope, Evidence: f.Evidence,
		Source: projectSource(f.Source), Previous: previous,
	}
}

func ProjectRequirementState(s schemas.RequirementState) ReqV2StateProjection {
	p := ReqV2StateProjection{
		SchemaVersion: s.SchemaVersion, Revision: s.Revision, Reply: s.Reply, NextAction: s.NextAction,
		Fields: map[string]ReqV2FieldProjection{},
	}
	for name, f := range s.Fields {
		p.Fields[name] = projectField(f)
	}
	for _, a := range s.Alternatives {
		p.Alternatives = append(p.Alternatives, ReqV2AlternativeProjection{
			Field: a.Field, Value: append(json.RawMessage(nil), a.Value...), Strength: a.Strength,
			Scope: a.Scope, Kind: a.Kind, Source: ReqV2SourceProjection{Kind: a.Source.Kind, MessageID: a.Source.MessageID, Quote: a.Source.Quote},
		})
	}
	for _, o := range s.Observations {
		p.Observations = append(p.Observations, ReqV2ObservationProjection{
			Field: o.Field, Text: o.Text, Reason: o.Reason, Resolved: o.Resolved,
			Source: ReqV2SourceProjection{Kind: o.Source.Kind, MessageID: o.Source.MessageID, Quote: o.Source.Quote},
		})
	}
	return p
}

// sameFieldShape 比较字段的值语义（status/value/kind/strength/scope），
// 不比较来源与墓碑指针。
func sameFieldShape(a, b ReqV2FieldProjection) bool {
	return a.Status == b.Status && a.Kind == b.Kind && a.Strength == b.Strength && a.Scope == b.Scope && rawEqual(a.Value, b.Value)
}

// ---------- 判卷 ----------

type reqV2Grader struct {
	assertions []ReqV2Assertion
}

func (g *reqV2Grader) check(name string, pass bool, classification, veto, detail string) {
	g.assertions = append(g.assertions, ReqV2Assertion{Name: name, Pass: pass, Classification: classification, Veto: veto, Detail: detail})
}

func rawEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func fieldGoldMatch(got ReqV2FieldProjection, want ReqV2FieldGold) bool {
	match := (want.Status == "" || got.Status == want.Status) &&
		(want.Kind == "" || got.Kind == want.Kind) &&
		(want.Strength == "" || got.Strength == want.Strength) &&
		(want.Scope == "" || got.Scope == want.Scope) &&
		(want.Evidence == "" || got.Evidence == want.Evidence) &&
		(len(want.Value) == 0 || rawEqual(got.Value, want.Value))
	if len(want.Contains) > 0 {
		var value string
		match = match && json.Unmarshal(got.Value, &value) == nil
		for _, fragment := range want.Contains {
			match = match && strings.Contains(value, fragment)
		}
	}
	if want.Source != nil {
		if got.Source == nil {
			match = false
		} else {
			match = match && (want.Source.Kind == "" || got.Source.Kind == want.Source.Kind) &&
				(want.Source.MessageID == "" || got.Source.MessageID == want.Source.MessageID) &&
				(want.Source.Quote == "" || strings.Contains(got.Source.Quote, want.Source.Quote))
		}
	}
	return match
}

// gradeStateFields 断言 gold 列出的字段；loose 模式只查列出项，
// strict 模式还要求未列出的字段与初始投影一致（完整下一状态）。
func (g *reqV2Grader) gradeStateFields(layer, caseID string, state ReqV2StateProjection, initial ReqV2StateProjection, gold map[string]ReqV2FieldGold, strict bool) {
	untouched := map[string]bool{}
	for name := range state.Fields {
		untouched[name] = true
	}
	for name, want := range gold {
		delete(untouched, name)
		got, ok := state.Fields[name]
		if want.Status == "absent" || want.Status == "unknown" && !ok {
			g.check(layer+":state:"+name, !ok || got.Status == "unknown", reqV2ClassBehavior, "", fmt.Sprint(got))
			continue
		}
		if !ok {
			g.check(layer+":state:"+name, false, reqV2ClassBehavior, "", "field missing")
			continue
		}
		g.check(layer+":state:"+name, fieldGoldMatch(got, want), reqV2ClassBehavior, "", fmt.Sprint(got))
	}
	if strict {
		for name := range untouched {
			// 联动会刷新伙伴字段的 source 元数据；"未变"只比较值语义。
			g.check(layer+":state_unchanged:"+name, sameFieldShape(state.Fields[name], initial.Fields[name]), reqV2ClassBehavior, "", fmt.Sprint(state.Fields[name]))
		}
	}
}

// veto 检测：独立于逐项断言，任一命中即记录 veto。
func (g *reqV2Grader) detectVetoes(obs ReqV2TurnObservation, goldForbiddenActive []string, pendingProposalFields map[string]bool, watchUnsupportedScope bool) {
	for name, f := range obs.State.Fields {
		if f.Status != "active" {
			continue
		}
		forbidden := false
		for _, x := range goldForbiddenActive {
			if x == name {
				forbidden = true
			}
		}
		// 用户证据只认 chat/edit 来源且绑定本轮原文；assistant 来源或空 quote
		// 都不能证明该值来自用户。
		sourceless := f.Source == nil || (f.Source.Kind != "chat" && f.Source.Kind != "edit") || (f.Source.Kind == "chat" && strings.TrimSpace(f.Source.Quote) == "")
		if forbidden || sourceless {
			veto := "V1"
			if pendingProposalFields[name] {
				// 只有助手提案可解释的未授权值是建议冒充需求。
				veto = "V2"
			}
			detail := fmt.Sprintf("field %s active without verifiable user evidence", name)
			if sourceless && f.Source != nil {
				detail += fmt.Sprintf(" (source kind=%q quote=%q)", f.Source.Kind, f.Source.Quote)
			} else if sourceless {
				detail += " (no source)"
			}
			g.check("veto:"+veto, false, reqV2ClassBehavior, veto, detail)
		}
	}
	if obs.Readiness != nil && obs.Readiness.ConfirmationEligible {
		blocked := len(obs.Readiness.MissingFields) > 0 || len(obs.Readiness.BlockingConflicts) > 0
		if blocked {
			g.check("veto:V3", false, reqV2ClassBehavior, "V3", "confirmation_eligible=true while missing/blocking conflicts exist")
		}
	}
	if obs.BuilderStartedViaChat {
		g.check("veto:V4", false, reqV2ClassBehavior, "V4", "builder started from chat text; chat is not the confirm API")
	}
	if obs.BuilderStarted && !obs.BuilderStartedViaChat && obs.ConfirmationSnapshotHash == "" {
		g.check("veto:V4", false, reqV2ClassBehavior, "V4", "builder started without a current valid confirmation snapshot")
	}
	if obs.BuilderStarted && obs.ConfirmationSnapshotHash != "" && obs.BuilderInputHash != "" && obs.ConfirmationSnapshotHash != obs.BuilderInputHash {
		g.check("veto:V5", false, reqV2ClassBehavior, "V5", "builder input hash differs from confirmed snapshot hash")
	}
	if obs.BuildersActive > 1 {
		g.check("veto:V6", false, reqV2ClassBehavior, "V6", fmt.Sprintf("%d concurrent builders", obs.BuildersActive))
	}
	if obs.StaleEditAccepted {
		g.check("veto:V7", false, reqV2ClassBehavior, "V7", "stale revision edit overwrote newer requirement state")
	}
	if !obs.BuilderStarted && claimsGenerationStarted(obs.Reply) {
		g.check("veto:V8", false, reqV2ClassBehavior, "V8", "reply claims generation started without a legal build run")
	}
	if watchUnsupportedScope && mentionsUnsupportedPeripheral(obs.Reply) {
		g.check("veto:V9", false, reqV2ClassBehavior, "V9", "reply mentions monitor/keyboard/mouse beyond current tower scope")
	}
}

var reqV2StartedPattern = regexp.MustCompile(`已开始生成|开始生成|正在生成|已经开始|已为您生成`)
var reqV2PeripheralPattern = regexp.MustCompile(`显示器|键盘|鼠标|键鼠`)

func claimsGenerationStarted(reply string) bool {
	return reply != "" && reqV2StartedPattern.MatchString(reply)
}

func mentionsUnsupportedPeripheral(reply string) bool {
	return reply != "" && reqV2PeripheralPattern.MatchString(reply)
}

// GradeReqV2Reducer 判 reducer 层：成功走完整下一状态断言，拒绝走稳定原因码。
func GradeReqV2Reducer(c ReqV2ReducerCase, obs ReqV2TurnObservation) []ReqV2Assertion {
	g := &reqV2Grader{}
	var initial schemas.RequirementState
	if len(c.InitialState) > 0 {
		if err := json.Unmarshal(c.InitialState, &initial); err != nil {
			g.check("reducer:initial_decodes", false, reqV2ClassFault, "", err.Error())
			return g.assertions
		}
	}
	initialProjection := ProjectRequirementState(initial)
	if c.Expected.Reject != "" {
		kind := rejectKind(obs.Error)
		g.check("reducer:reject", obs.Error != "" && kind == c.Expected.Reject, classForReject(kind, c.Expected.Reject), "", fmt.Sprintf("error=%q want=%s", obs.Error, c.Expected.Reject))
		g.check("reducer:reject_keeps_state", obs.State.Revision == initialProjection.Revision && reflect.DeepEqual(obs.State.Fields, initialProjection.Fields), reqV2ClassBehavior, "", fmt.Sprintf("revision=%d", obs.State.Revision))
		return g.assertions
	}
	g.check("reducer:no_error", obs.Error == "", classForUnexpectedReject(obs.Error), "", obs.Error)
	if obs.Error != "" {
		return g.assertions
	}
	g.check("reducer:schema_version", obs.State.SchemaVersion == c.Expected.SchemaVersion, reqV2ClassGap, "", fmt.Sprintf("got=%d want=%d", obs.State.SchemaVersion, c.Expected.SchemaVersion))
	g.check("reducer:revision", obs.State.Revision-initialProjection.Revision == c.Expected.RevisionDelta, reqV2ClassBehavior, "", fmt.Sprintf("got delta=%d", obs.State.Revision-initialProjection.Revision))
	if c.Expected.ReplyNextActionAbsent {
		g.check("reducer:reply_next_action_absent", obs.State.Reply == "" && obs.State.NextAction == "", reqV2ClassGap, "", fmt.Sprintf("reply=%q next_action=%q", obs.State.Reply, obs.State.NextAction))
	}
	g.gradeStateFields("reducer", c.ID, obs.State, initialProjection, c.Expected.Fields, true)
	if len(c.Expected.Alternatives) == 0 && len(obs.State.Alternatives) == 0 {
		g.check("reducer:alternatives", true, "", "", "")
	} else {
		match := len(c.Expected.Alternatives) == len(obs.State.Alternatives)
		for i, want := range c.Expected.Alternatives {
			if i >= len(obs.State.Alternatives) {
				match = false
				break
			}
			got := obs.State.Alternatives[i]
			match = match && got.Field == want.Field && rawEqual(got.Value, want.Value) && (want.Strength == "" || got.Strength == want.Strength) && (want.Kind == "" || got.Kind == want.Kind)
		}
		g.check("reducer:alternatives", match, reqV2ClassBehavior, "", fmt.Sprint(obs.State.Alternatives))
	}
	g.check("reducer:observations", observationsMatch(obs.State.Observations, c.Expected.Observations), reqV2ClassBehavior, "", fmt.Sprint(obs.State.Observations))
	g.detectVetoes(obs, c.Expected.ForbiddenActive, nil, false)
	return g.assertions
}

func classForReject(got, want string) string {
	if got == "unknown_field" && want == "v2_field_unsupported" {
		return reqV2ClassGap
	}
	if got == "invalid_value_or_op" && want == "v2_value_unsupported" {
		return reqV2ClassGap
	}
	return reqV2ClassBehavior
}

// classForUnexpectedReject：金标期望成功但当前 reducer 拒绝时，v2 专属
// 字段/枚举缺失记契约缺口，其余记行为失败。
func classForUnexpectedReject(err string) string {
	switch rejectKind(err) {
	case "unknown_field":
		return reqV2ClassGap
	case "invalid_value_or_op":
		if strings.Contains(err, "非法") {
			return reqV2ClassGap
		}
	}
	return reqV2ClassFault
}

// rejectKind 把 reducer 错误映射到稳定原因码；文本变化不应改变分类。
func rejectKind(err string) string {
	switch {
	case err == "":
		return ""
	case strings.Contains(err, "未知字段"):
		return "unknown_field"
	case strings.Contains(err, "缺少本轮用户原文证据"):
		return "missing_evidence"
	case strings.Contains(err, "推断或不确定信息"):
		return "inferred_evidence"
	case strings.Contains(err, "没有可恢复的临时覆盖"):
		return "nothing_to_restore"
	default:
		return "invalid_value_or_op"
	}
}

func observationsMatch(got []ReqV2ObservationProjection, want []ReqV2ObservationGold) bool {
	if len(want) == 0 {
		return len(got) == 0
	}
	if len(got) != len(want) {
		return false
	}
	for i, w := range want {
		o := got[i]
		if (w.Field != "" && o.Field != w.Field) || !strings.Contains(o.Text, w.TextContains) || (w.ReasonContains != "" && !strings.Contains(o.Reason, w.ReasonContains)) {
			return false
		}
	}
	return true
}

// GradeReqV2Readiness 判 readiness 层。适配器无法求值时（如 v1 枚举拒绝
// v2 合法值 any）不早退：eligible 必须保持 false，缺口按契约差距记录。
func GradeReqV2Readiness(c ReqV2ReadinessCase, obs ReqV2ReadinessResult) []ReqV2Assertion {
	g := &reqV2Grader{}
	if obs.Error != "" {
		class := reqV2ClassFault
		// v2 合法而 v1 枚举拒绝的值（resolution=any）记契约缺口；
		// 其余真非法值仍是评估事实错误。
		if strings.Contains(obs.Error, "非法") && strings.Contains(obs.Error, "any") {
			class = reqV2ClassGap
		}
		g.check("readiness:schema_error", false, class, "", obs.Error)
		// 适配器失败时无法给出 missing 清单：按不可比对处理，
		// 但绝不能因此变成 eligible。
		g.check("readiness:missing_fields", false, class, "", "adapter failed; missing list unavailable")
		g.check("readiness:confirmation_eligible", false == c.Expected.ConfirmationEligible, class, "", fmt.Sprintf("eligible=%v (adapter failed)", obs.ConfirmationEligible))
		return g.assertions
	}
	g.check("readiness:status", obs.Status == c.Expected.Status, reqV2ClassBehavior, "", fmt.Sprintf("got=%s", obs.Status))
	g.check("readiness:missing_fields", equalStringList(obs.MissingFields, c.Expected.MissingFields), reqV2ClassBehavior, "", fmt.Sprint(obs.MissingFields))
	if len(c.Expected.BlockingConflicts) > 0 || len(obs.BlockingConflicts) > 0 {
		g.check("readiness:blocking_conflicts", equalStringList(obs.BlockingConflicts, c.Expected.BlockingConflicts), gapWhen(c.Expected.BlockingConflicts, obs.BlockingConflicts), "", fmt.Sprint(obs.BlockingConflicts))
	}
	g.check("readiness:confirmation_eligible", obs.ConfirmationEligible == c.Expected.ConfirmationEligible, reqV2ClassBehavior, "", fmt.Sprint(obs.ConfirmationEligible))
	if len(c.Expected.EffectiveDefaults) > 0 {
		g.check("readiness:effective_defaults", defaultsMatch(obs.EffectiveDefaults, c.Expected.EffectiveDefaults), reqV2ClassGap, "", fmt.Sprint(obs.EffectiveDefaults))
	}
	if len(c.Expected.UnsupportedCapabilities) > 0 {
		g.check("readiness:unsupported_capabilities", reflect.DeepEqual(normalizeList(obs.UnsupportedCapabilities), normalizeList(c.Expected.UnsupportedCapabilities)), reqV2ClassGap, "", fmt.Sprint(obs.UnsupportedCapabilities))
	}
	turn := ReqV2TurnObservation{Readiness: &obs}
	g.detectVetoes(turn, nil, nil, false)
	return g.assertions
}

func gapWhen(want, got []string) string {
	if len(want) > 0 && len(got) == 0 {
		return reqV2ClassGap
	}
	return reqV2ClassBehavior
}

func defaultsMatch(got, want []ReqV2DefaultGold) bool {
	if len(got) != len(want) {
		return false
	}
	index := map[string]ReqV2DefaultGold{}
	for _, d := range want {
		index[d.Field] = d
	}
	for _, d := range got {
		w, ok := index[d.Field]
		if !ok || !rawEqual(d.Value, w.Value) || d.Origin != w.Origin {
			return false
		}
	}
	return true
}

func normalizeList(in []string) []string {
	if in == nil {
		return []string{}
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// equalStringList 是顺序敏感的列表比较；v2 要求 missing 按追问优先级稳定排序。
func equalStringList(a, b []string) bool {
	if a == nil {
		a = []string{}
	}
	if b == nil {
		b = []string{}
	}
	return reflect.DeepEqual(a, b)
}

// GradeReqV2Policy 判 policy 层：admission、presentation 与 edit 接受性。
func GradeReqV2Policy(c ReqV2PolicyCase, obs ReqV2TurnObservation) []ReqV2Assertion {
	g := &reqV2Grader{}
	g.check("policy:builder_admission", obs.BuilderStarted == c.Expected.BuilderAdmission, reqV2ClassBehavior, "", fmt.Sprintf("started=%v admission_error=%q", obs.BuilderStarted, obs.Error))
	if c.Expected.AdmissionReason != "" {
		// 当前实现没有稳定 reason code；只断言拒绝语义本身。
		g.check("policy:admission_reason", !c.Expected.BuilderAdmission || obs.BuilderStarted, reqV2ClassGap, "", "current path exposes no stable admission reason code")
	}
	if c.Expected.PresentationAction != "" {
		g.check("policy:presentation_action", false, reqV2ClassGap, "", "current path has no presentation action contract")
	}
	if c.Expected.EditAccepted != nil {
		accepted := obs.Error == "" && !obs.StaleEditAccepted
		g.check("policy:edit_accepted", accepted == *c.Expected.EditAccepted, reqV2ClassBehavior, "", fmt.Sprintf("accepted=%v error=%q", accepted, obs.Error))
	}
	watch := mentionsUnsupportedPeripheral(c.Turn.Text) || mentionsUnsupportedPeripheral(c.Turn.ScriptedReply)
	g.detectVetoes(obs, nil, nil, watch)
	return g.assertions
}

// GradeReqV2UI 判 ui-contract 层：后端 DTO 真值，不含浏览器呈现。
// blockPresent 表示 DTO 是否携带结构化 requirement_readiness 块。
func GradeReqV2UI(c ReqV2UICase, obs ReqV2TurnObservation, blockPresent bool, confirmationStatus, buildRelation string, confirmPayloadKeys []string) []ReqV2Assertion {
	g := &reqV2Grader{}
	g.check("ui:readiness_block", blockPresent, reqV2ClassGap, "", "session DTO carries no structured requirement_readiness block")
	if obs.Readiness != nil {
		sub := GradeReqV2Readiness(ReqV2ReadinessCase{ReqV2CaseHead: ReqV2CaseHead{ID: c.ID}, Expected: c.Expected.Readiness}, *obs.Readiness)
		for _, a := range sub {
			a.Name = "ui:" + a.Name
			g.assertions = append(g.assertions, a)
		}
	}
	g.check("ui:confirmation_status", confirmationStatus == c.Expected.ConfirmationStatus, classForStatus(confirmationStatus, c.Expected.ConfirmationStatus), "", fmt.Sprintf("got=%q", confirmationStatus))
	g.check("ui:build_relation", buildRelation == c.Expected.BuildRelation, classForStatus(buildRelation, c.Expected.BuildRelation), "", fmt.Sprintf("got=%q", buildRelation))
	if len(c.Expected.ConfirmPayloadKeys) > 0 {
		g.check("ui:confirm_payload", reflect.DeepEqual(normalizeList(confirmPayloadKeys), normalizeList(c.Expected.ConfirmPayloadKeys)), reqV2ClassGap, "", fmt.Sprint(confirmPayloadKeys))
	}
	g.detectVetoes(obs, nil, nil, false)
	return g.assertions
}

func classForStatus(got, want string) string {
	if got == "" {
		return reqV2ClassGap
	}
	return reqV2ClassBehavior
}

// GradeReqV2Turn 判 extraction / conversation 单轮：operations、signals、
// state、readiness、forbidden questions 与 veto。
func GradeReqV2Turn(layer string, gold ReqV2ExtractionGold, obs ReqV2TurnObservation, pendingProposalFields map[string]bool, watchUnsupportedScope bool) []ReqV2Assertion {
	g := &reqV2Grader{}
	if obs.ProviderError != "" {
		g.check(layer+":provider", false, reqV2ClassProvider, "", obs.ProviderError)
	}
	g.check(layer+":run_completed", obs.Error == "", reqV2ClassFault, "", obs.Error)
	// operation exact match：字段/op/值/强度/证据逐条比对。
	g.check(layer+":operations", operationsMatch(obs.Operations, gold.Operations), reqV2ClassBehavior, "", describeOperations(obs.Operations))
	for _, forbidden := range gold.ForbiddenOperations {
		hit := false
		for _, op := range obs.Operations {
			if opMatchesGold(op, forbidden) {
				hit = true
			}
		}
		g.check(layer+":forbidden_op:"+forbidden.Field, !hit, reqV2ClassBehavior, "", fmt.Sprintf("forbidden %s %s emitted", forbidden.Op, forbidden.Field))
	}
	g.check(layer+":observations", observationsMatch(obs.Observations, gold.Observations), reqV2ClassBehavior, "", fmt.Sprint(obs.Observations))
	if gold.TurnSignals != nil {
		if !obs.SignalsKnown {
			g.check(layer+":turn_signals", false, reqV2ClassGap, "", "current path has no turn signal contract")
		} else {
			g.check(layer+":turn_signals", *obs.Signals == *gold.TurnSignals, reqV2ClassBehavior, "", fmt.Sprint(*obs.Signals))
		}
	}
	g.gradeStateFields(layer, "", obs.State, ReqV2StateProjection{Fields: map[string]ReqV2FieldProjection{}}, gold.StateFields, false)
	if gold.NextActionAbsent {
		g.check(layer+":next_action_absent", obs.State.NextAction == "", reqV2ClassGap, "", fmt.Sprintf("next_action=%q", obs.State.NextAction))
	}
	if gold.MissingFields != nil && obs.Readiness != nil {
		g.check(layer+":missing_fields", equalStringList(obs.Readiness.MissingFields, gold.MissingFields), reqV2ClassBehavior, "", fmt.Sprint(obs.Readiness.MissingFields))
	}
	if gold.ConfirmationEligible != nil && obs.Readiness != nil {
		g.check(layer+":confirmation_eligible", obs.Readiness.ConfirmationEligible == *gold.ConfirmationEligible, reqV2ClassBehavior, "", fmt.Sprint(obs.Readiness.ConfirmationEligible))
	}
	for _, field := range gold.ForbiddenQuestions {
		g.check(layer+":repeated_question:"+field, !asksAboutField(obs.Reply, field, obs.State), reqV2ClassBehavior, "", obs.Reply)
	}
	g.detectVetoes(obs, forbiddenActiveFromOps(gold.ForbiddenOperations), pendingProposalFields, watchUnsupportedScope)
	return g.assertions
}

func forbiddenActiveFromOps(ops []ReqV2OpGold) []string {
	var fields []string
	for _, op := range ops {
		if op.Field != "" && (op.Op == "" || op.Op == "set") {
			fields = append(fields, op.Field)
		}
	}
	return fields
}

func operationsMatch(got []schemas.RequirementOperation, want []ReqV2OpGold) bool {
	if len(want) == 0 {
		return len(got) == 0
	}
	if len(got) != len(want) {
		return false
	}
	used := map[int]bool{}
	for _, w := range want {
		found := false
		for i, op := range got {
			if used[i] {
				continue
			}
			if opMatchesGold(op, w) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func opMatchesGold(op schemas.RequirementOperation, w ReqV2OpGold) bool {
	match := (w.Op == "" || op.Op == w.Op) &&
		(w.Field == "" || op.Field == w.Field) &&
		(w.Strength == "" || op.Strength == w.Strength) &&
		(w.Evidence == "" || op.Evidence == w.Evidence) &&
		(len(w.Value) == 0 || rawEqual(op.Value, w.Value)) &&
		(w.QuoteContains == "" || strings.Contains(op.Quote, w.QuoteContains))
	return match
}

func describeOperations(ops []schemas.RequirementOperation) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, fmt.Sprintf("%s %s=%s", op.Op, op.Field, string(op.Value)))
	}
	return strings.Join(parts, "; ")
}

// asksAboutField 是追问检测的保守关键字启发式；命中需人工复核原文。
var reqV2QuestionKeywords = map[string][]string{
	"budget_cny":          {"预算"},
	"use_case.type":       {"用途", "用来做", "主要用于", "做什么用"},
	"use_case.resolution": {"分辨率"},
	"use_case.titles":     {"软件", "游戏名", "玩什么", "任务"},
	"existing_parts":      {"已有", "旧配件", "复用", "旧电脑"},
	"owned_parts":         {"型号"},
	"budget_basis":        {"口径", "整机", "包含"},
}

func asksAboutField(reply, field string, state ReqV2StateProjection) bool {
	keywords, ok := reqV2QuestionKeywords[field]
	if !ok {
		return false
	}
	if strings.HasSuffix(field, ".model") {
		keywords = reqV2QuestionKeywords["owned_parts"]
	}
	active := false
	if f, exists := state.Fields[field]; exists && f.Status == "active" {
		active = true
	}
	if !active {
		return false // 字段未 active 时追问是合法的。
	}
	for _, k := range keywords {
		if strings.Contains(reply, k) {
			return true
		}
	}
	return false
}

// GradeReqV2Selftest 执行金丝雀：expect=fail 必须命中指定 veto，
// expect=pass 必须全部通过。任何不符合都让 check 失败。
func GradeReqV2Selftest(entries []ReqV2SelftestEntry) error {
	for _, e := range entries {
		var assertions []ReqV2Assertion
		switch e.Layer {
		case "readiness":
			var gold ReqV2ReadinessGold
			var actual ReqV2ReadinessResult
			if err := decodeStrictReqV2(e.Gold, &gold); err != nil {
				return fmt.Errorf("selftest %s gold: %w", e.ID, err)
			}
			if err := decodeStrictReqV2(e.Actual, &actual); err != nil {
				return fmt.Errorf("selftest %s actual: %w", e.ID, err)
			}
			assertions = GradeReqV2Readiness(ReqV2ReadinessCase{ReqV2CaseHead: ReqV2CaseHead{ID: e.ID}, Expected: gold}, actual)
		case "turn":
			var gold ReqV2ExtractionGold
			var actual ReqV2TurnObservation
			if err := decodeStrictReqV2(e.Gold, &gold); err != nil {
				return fmt.Errorf("selftest %s gold: %w", e.ID, err)
			}
			if err := decodeStrictReqV2(e.Actual, &actual); err != nil {
				return fmt.Errorf("selftest %s actual: %w", e.ID, err)
			}
			assertions = GradeReqV2Turn("selftest", gold, actual, pendingFieldsFromGold(gold), actual.WatchUnsupportedScope)
		default:
			return fmt.Errorf("selftest %s: unsupported layer %q", e.ID, e.Layer)
		}
		vetoHit := map[string]bool{}
		allPass := true
		for _, a := range assertions {
			if !a.Pass {
				allPass = false
			}
			if a.Veto != "" {
				vetoHit[a.Veto] = true
			}
		}
		switch e.Expect {
		case "fail":
			if allPass {
				return fmt.Errorf("selftest %s: expected veto %s but grader passed (grader rubber-stamps)", e.ID, e.Veto)
			}
			if !vetoHit[e.Veto] {
				return fmt.Errorf("selftest %s: expected veto %s, got %v", e.ID, e.Veto, vetoHit)
			}
		case "pass":
			if !allPass {
				failed := []string{}
				for _, a := range assertions {
					if !a.Pass {
						failed = append(failed, a.Name)
					}
				}
				return fmt.Errorf("selftest %s: expected pass, failed %v (grader cannot pass)", e.ID, failed)
			}
		}
	}
	return nil
}

func pendingFieldsFromGold(gold ReqV2ExtractionGold) map[string]bool {
	fields := map[string]bool{}
	for _, op := range gold.Operations {
		if op.Evidence == "accepted_proposal" {
			fields[op.Field] = true
		}
	}
	return fields
}
