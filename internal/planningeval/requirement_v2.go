package planningeval

// Requirement v2 评估契约：dataset 布局、manifest/split 校验与 gates 读取。
// 本文件只定义与加载数据；判卷在 requirement_v2_grade.go，执行在
// requirement_v2_run.go。扩展 planningeval 而不是另建 runner。
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// ReqV2GraderVersion：v2 变更（2026-09-22）——provider_success、latency_p95、
// max_model_calls_per_turn 与预算一致性改为统计 extraction+conversations 的
// 全部真实 provider 轮（verdict layer=model），样本范围与 report.usage 一致。
// v1 产物按 superseded 归档，跨版本判卷走显式 regrade。
const ReqV2GraderVersion = "reqv2-grader-v2"

// ReqV2Layer 是 fixture 的判定对象分层；live 层需要模型，其余零模型。
var ReqV2Layers = []string{"extraction", "reducer", "readiness", "policy", "conversations", "ui-contract"}

// ReqV2DeterministicLayers 是必须 100% 通过的程序 oracle 层。
var ReqV2DeterministicLayers = []string{"reducer", "readiness", "policy", "ui-contract"}

type ReqV2Manifest struct {
	Dataset       string              `json:"dataset"`
	GraderVersion string              `json:"grader_version"`
	FrozenAt      string              `json:"frozen_at"`
	Files         []ReqV2ManifestFile `json:"files"`
	SplitSessions map[string][]string `json:"split_sessions"`
}

type ReqV2ManifestFile struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Cases    int    `json:"cases"`
	Sessions int    `json:"sessions"`
}

type ReqV2Gates struct {
	Version                string                      `json:"version"`
	FrozenAt               string                      `json:"frozen_at"`
	Vetoes                 []ReqV2Veto                 `json:"vetoes"`
	DeterministicLayers    ReqV2GateSet                `json:"deterministic_layers"`
	Repetition             ReqV2GateRepeat             `json:"repetition"`
	MinimumPairedSamples   int                         `json:"minimum_paired_samples"`
	ModelQualityThresholds ReqV2ModelQualityThresholds `json:"model_quality_thresholds"`
	Limitations            []string                    `json:"limitations,omitempty"`
}

// ReqV2ModelQualityThresholds 是冻结的模型层质量门槛；precision 优先于
// recall：错误写入污染需求真值，漏记可由追问补救。
type ReqV2ModelQualityThresholds struct {
	Status                     string                `json:"status"`
	FrozenAt                   string                `json:"frozen_at"`
	KeyFields                  []string              `json:"key_fields"`
	KeyFieldWrongWriteTotalMax int                   `json:"key_field_wrong_write_total_max"`
	Extraction                 ReqV2ExtractionGate   `json:"extraction"`
	Conversations              ReqV2ConversationGate `json:"conversations"`
	Notes                      string                `json:"notes"`
}

type ReqV2ExtractionGate struct {
	OperationPrecisionMin   float64 `json:"operation_precision_min"`
	OperationRecallMin      float64 `json:"operation_recall_min"`
	ForbiddenOpTotalMax     int     `json:"forbidden_op_total_max"`
	TurnSignalExactMatchMin float64 `json:"turn_signal_exact_match_min"`
	// MinSignalTurns 是 turn signal 门槛的独立样本量（断言 signals 的轮数）。
	MinSignalTurns int `json:"min_signal_turns"`
	// CaseSuccessMin 按 case 的 Pass^k 折叠成功率。
	CaseSuccessMin float64 `json:"case_success_min"`
	// FinalStateExactMatchMin 按 case 的最终状态断言全过率。
	FinalStateExactMatchMin float64 `json:"final_state_exact_match_min"`
	// ProviderSuccessMin 真实 provider 轮的无失败占比。
	ProviderSuccessMin float64 `json:"provider_success_min"`
	// LatencyP95MaxMS 对齐 PRD 追问响应上限。
	LatencyP95MaxMS int64 `json:"latency_p95_max_ms"`
	// MaxModelCallsPerTurn 含 guard 纠偏重调的单轮调用上限。
	MaxModelCallsPerTurn int `json:"max_model_calls_per_turn"`
	MinCases             int `json:"min_cases"`
}

type ReqV2ConversationGate struct {
	TaskSuccessMin             float64 `json:"task_success_min"`
	RepeatedQuestionMaxPerCase int     `json:"repeated_question_max_per_case"`
	ForbiddenVetoTotalMax      int     `json:"forbidden_veto_total_max"`
	MinCases                   int     `json:"min_cases"`
}

type ReqV2Veto struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
}

type ReqV2GateSet struct {
	Names            []string `json:"names"`
	RequiredPassRate float64  `json:"required_pass_rate"`
}

type ReqV2GateRepeat struct {
	ReleaseRepeats int    `json:"release_repeats"`
	Metric         string `json:"metric"`
	BestOfKAsGate  bool   `json:"best_of_k_as_gate"`
}

// 公共 case 头：split 按 session 分配；variants_of 标记同模板实例以便泄漏检查。
// ReqV2CaseHead 是各层 case 的公共头；嵌入导出类型让 encoding/json 提升 id/split 等字段。
type ReqV2CaseHead struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Split      string `json:"split"`
	Session    string `json:"session"`
	VariantsOf string `json:"variants_of,omitempty"`
	Rationale  string `json:"rationale"`
}

func (h ReqV2CaseHead) validate(layer string, seen map[string]string) error {
	if h.ID == "" || h.Title == "" || h.Session == "" || h.Rationale == "" {
		return fmt.Errorf("%s case %q: id/title/session/rationale required", layer, h.ID)
	}
	if h.Split != "development" && h.Split != "calibration" && h.Split != "holdout" {
		return fmt.Errorf("%s case %q: invalid split %q", layer, h.ID, h.Split)
	}
	if other, dup := seen[h.ID]; dup {
		return fmt.Errorf("duplicate case id %q in %s and %s", h.ID, other, layer)
	}
	seen[h.ID] = layer
	return nil
}

type ReqV2ReducerCase struct {
	ReqV2CaseHead
	InitialState json.RawMessage      `json:"initial_state"`
	UserMessage  string               `json:"user_message"`
	Update       ReqV2ReducerUpdate   `json:"update"`
	Expected     ReqV2ReducerExpected `json:"expected"`
}

// ReqV2ReducerUpdate 是喂给 reducer 的操作批；source 决定证据绑定方式。
type ReqV2ReducerUpdate struct {
	Operations []schemas.RequirementOperation `json:"operations"`
	Source     string                         `json:"source"` // chat | edit
}

type ReqV2ReducerExpected struct {
	// Reject 非空时期望明确拒绝；稳定原因码见 grader 的映射。
	Reject string `json:"reject,omitempty"`
	// 成功期望：canonical projection 的显式断言。fields 未列出的字段必须与
	// initial_state 的 projection 完全一致（完整下一状态 = 未变 + 列出项）。
	SchemaVersion         int                       `json:"schema_version"`
	RevisionDelta         int                       `json:"revision_delta"`
	ReplyNextActionAbsent bool                      `json:"reply_next_action_absent"`
	Fields                map[string]ReqV2FieldGold `json:"fields,omitempty"`
	Alternatives          []ReqV2AlternativeGold    `json:"alternatives,omitempty"`
	Observations          []ReqV2ObservationGold    `json:"observations,omitempty"`
	// ForbiddenActive 显式列出不得成为 active 的字段，供 V1/V2 veto 检测。
	ForbiddenActive []string `json:"forbidden_active,omitempty"`
}

type ReqV2FieldGold struct {
	Status   string           `json:"status,omitempty"`
	Value    json.RawMessage  `json:"value,omitempty"`
	Kind     string           `json:"kind,omitempty"`
	Strength string           `json:"strength,omitempty"`
	Scope    string           `json:"scope,omitempty"`
	Evidence string           `json:"evidence,omitempty"`
	Source   *ReqV2SourceGold `json:"source,omitempty"`
	Previous *ReqV2FieldGold  `json:"previous,omitempty"`
	// Loose 语义（extraction/conversations 用）：只断言列出的键。
	Contains []string `json:"contains,omitempty"`
}

type ReqV2SourceGold struct {
	Kind      string `json:"kind,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Quote     string `json:"quote,omitempty"`
}

type ReqV2AlternativeGold struct {
	Field    string          `json:"field"`
	Value    json.RawMessage `json:"value"`
	Strength string          `json:"strength,omitempty"`
	Kind     string          `json:"kind,omitempty"`
}

type ReqV2ObservationGold struct {
	Field          string `json:"field,omitempty"`
	TextContains   string `json:"text_contains"`
	ReasonContains string `json:"reason_contains,omitempty"`
}

type ReqV2ReadinessCase struct {
	ReqV2CaseHead
	State    json.RawMessage    `json:"state"`
	Expected ReqV2ReadinessGold `json:"expected"`
}

type ReqV2ReadinessGold struct {
	Status               string             `json:"status"` // incomplete | ready
	MissingFields        []string           `json:"missing_fields"`
	BlockingConflicts    []string           `json:"blocking_conflicts"`
	ConfirmationEligible bool               `json:"confirmation_eligible"`
	EffectiveDefaults    []ReqV2DefaultGold `json:"effective_defaults"`
	// UnsupportedCapabilities：v2 要求把不支持能力单列为阻塞原因。
	UnsupportedCapabilities []string `json:"unsupported_capabilities,omitempty"`
}

type ReqV2DefaultGold struct {
	Field  string          `json:"field"`
	Value  json.RawMessage `json:"value"`
	Origin string          `json:"origin"`
}

type ReqV2PolicyCase struct {
	ReqV2CaseHead
	// Seed 是通过 scripted screening 轮建立初始状态的 edit 语义操作（适配器
	// 输入，不是金标；quotes 必须出现在 seed_user_message 里）。
	Seed            []schemas.RequirementOperation `json:"seed"`
	SeedUserMessage string                         `json:"seed_user_message"`
	// StateNextAction 是 scripted screening 声称的动作，代表"模型宣称"的
	// 极端输入；v2 要求 policy 不受其左右。seed 轮与 message 轮共用。
	StateNextAction string            `json:"state_next_action"`
	PreTurns        []ReqV2PolicyTurn `json:"pre_turns,omitempty"`
	Turn            ReqV2PolicyTurn   `json:"turn"`
	// HoldBuilder 让 scripted builder 阻塞到取消，用于观测并发 admission。
	HoldBuilder bool            `json:"hold_builder,omitempty"`
	Expected    ReqV2PolicyGold `json:"expected"`
}

type ReqV2PolicyTurn struct {
	Kind string                         `json:"kind"` // message | confirm | edit
	Text string                         `json:"text,omitempty"`
	Edit []schemas.RequirementOperation `json:"edit,omitempty"`
	// Ops/ScriptedReply 只用于 message 轮的 scripted screening 输出。
	Ops           []schemas.RequirementOperation `json:"ops,omitempty"`
	ScriptedReply string                         `json:"scripted_reply,omitempty"`
	// EditExpectedRevisionDelta 相对当前 revision；负数构造过期 revision。
	EditExpectedRevisionDelta int              `json:"edit_expected_revision_delta"`
	Signals                   ReqV2TurnSignals `json:"signals,omitempty"`
}

type ReqV2PolicyGold struct {
	BuilderAdmission   bool     `json:"builder_admission"`
	AdmissionReason    string   `json:"admission_reason,omitempty"` // ok | readiness_incomplete | no_confirmation | builder_running | stale_revision
	PresentationAction string   `json:"presentation_action,omitempty"`
	AllowedActions     []string `json:"allowed_actions,omitempty"`
	// EditAccepted 断言 edit 请求本身是否应被接受（生成中草稿仍可编辑）。
	EditAccepted    *bool    `json:"edit_accepted,omitempty"`
	ForbiddenVetoes []string `json:"forbidden_vetoes,omitempty"`
}

type ReqV2UICase struct {
	ReqV2CaseHead
	Seed            []schemas.RequirementOperation `json:"seed"`
	SeedUserMessage string                         `json:"seed_user_message"`
	StateNextAction string                         `json:"state_next_action"`
	Expected        ReqV2UIGold                    `json:"expected"`
}

type ReqV2UIGold struct {
	Readiness          ReqV2ReadinessGold `json:"requirement_readiness"`
	ConfirmationStatus string             `json:"requirement_confirmation_status"` // unconfirmed | confirmed | modified
	BuildRelation      string             `json:"build_relation"`                  // none | running | current | outdated | failed
	ConfirmPayloadKeys []string           `json:"confirm_request_payload,omitempty"`
}

type ReqV2ExtractionCase struct {
	ReqV2CaseHead
	Seed            []schemas.RequirementOperation `json:"seed"`
	SeedUserMessage string                         `json:"seed_user_message"`
	// PriorAssistantTurn 用 scripted screening 轮建立"上一轮助手可见回复"
	// （accepted-proposal 边界的上下文；适配器输入，不是金标）。
	PriorAssistantTurn *ReqV2ScriptedTurn  `json:"prior_assistant_turn,omitempty"`
	UserMessage        string              `json:"user_message"`
	Expected           ReqV2ExtractionGold `json:"expected"`
}

type ReqV2ScriptedTurn struct {
	UserMessage string `json:"user_message"`
	Reply       string `json:"reply"`
}

type ReqV2ExtractionGold struct {
	Operations           []ReqV2OpGold             `json:"expected_operations"`
	Observations         []ReqV2ObservationGold    `json:"expected_observations,omitempty"`
	TurnSignals          *ReqV2TurnSignals         `json:"expected_turn_signals,omitempty"`
	ForbiddenOperations  []ReqV2OpGold             `json:"forbidden_operations,omitempty"`
	StateFields          map[string]ReqV2FieldGold `json:"expected_state,omitempty"`
	MissingFields        []string                  `json:"expected_missing_fields,omitempty"`
	ConfirmationEligible *bool                     `json:"expected_confirmation_eligible,omitempty"`
	ForbiddenQuestions   []string                  `json:"forbidden_questions,omitempty"`
	ForbiddenVetoes      []string                  `json:"forbidden_vetoes,omitempty"`
	// NextActionAbsent 断言模型 next_action 不再进入状态真值（v2 删除该权威）。
	NextActionAbsent bool `json:"expected_next_action_absent,omitempty"`
}

// ReqV2OpGold 断言一条 operation 的字段/op/值/强度/证据；空字符串与 nil 跳过该项。
type ReqV2OpGold struct {
	Op            string          `json:"op,omitempty"`
	Field         string          `json:"field,omitempty"`
	Value         json.RawMessage `json:"value,omitempty"`
	Strength      string          `json:"strength,omitempty"`
	Evidence      string          `json:"evidence,omitempty"`
	QuoteContains string          `json:"quote_contains,omitempty"`
}

type ReqV2TurnSignals struct {
	AsksQuestion   bool `json:"asks_question"`
	RequestsReview bool `json:"requests_review"`
	RequestsBuild  bool `json:"requests_build"`
	Ambiguous      bool `json:"ambiguous"`
}

type ReqV2ConversationCase struct {
	ReqV2CaseHead
	Turns []ReqV2ConversationTurn `json:"turns"`
	Final ReqV2ConversationFinal  `json:"final"`
}

type ReqV2ConversationTurn struct {
	Text     string              `json:"text"`
	Expected ReqV2ExtractionGold `json:"expect"`
}

type ReqV2ConversationFinal struct {
	StateFields          map[string]ReqV2FieldGold `json:"expected_state,omitempty"`
	MissingFields        []string                  `json:"expected_missing_fields,omitempty"`
	ConfirmationEligible bool                      `json:"expected_confirmation_eligible"`
	BuilderMustRun       bool                      `json:"builder_must_run"`
	ForbiddenVetoes      []string                  `json:"forbidden_vetoes,omitempty"`
}

// ReqV2SelftestEntry 是 grader 金丝雀：fabricated actual 直接喂给判卷器，
// expect=fail 必须以指定 veto 失败，expect=pass 必须通过。
type ReqV2SelftestEntry struct {
	ID          string          `json:"id"`
	Layer       string          `json:"layer"`
	Veto        string          `json:"veto,omitempty"`
	Expect      string          `json:"expect"` // fail | pass
	Description string          `json:"description"`
	Gold        json.RawMessage `json:"gold"`
	Actual      json.RawMessage `json:"actual"`
}

type ReqV2SelftestFile struct {
	Entries []ReqV2SelftestEntry `json:"entries"`
}

type ReqV2Dataset struct {
	Root          string
	Manifest      ReqV2Manifest
	ManifestRaw   []byte
	Gates         ReqV2Gates
	GatesRaw      []byte
	ProvenanceRaw []byte
	Extraction    []ReqV2ExtractionCase
	Reducer       []ReqV2ReducerCase
	Readiness     []ReqV2ReadinessCase
	Policy        []ReqV2PolicyCase
	Conversations []ReqV2ConversationCase
	UIContract    []ReqV2UICase
	Selftest      []ReqV2SelftestEntry
	Catalog       CatalogFixture
}

func decodeStrictReqV2(raw []byte, into any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("must contain exactly one JSON document")
	}
	return nil
}

// LoadRequirementV2 读取 dataset 根目录并执行 manifest/split/gates 校验。
// 任何 fixture 漂移（哈希不符、未登记文件、split 泄漏）都会在评估前失败。
func LoadRequirementV2(root string) (*ReqV2Dataset, error) {
	d := &ReqV2Dataset{Root: root}
	var err error
	if d.ManifestRaw, err = os.ReadFile(filepath.Join(root, "manifest.json")); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err = decodeStrictReqV2(d.ManifestRaw, &d.Manifest); err != nil {
		return nil, err
	}
	if d.Manifest.Dataset != "requirement-v2" || d.Manifest.GraderVersion != ReqV2GraderVersion || d.Manifest.FrozenAt == "" {
		return nil, fmt.Errorf("manifest identity mismatch")
	}
	if d.GatesRaw, err = os.ReadFile(filepath.Join(root, "gates.json")); err != nil {
		return nil, fmt.Errorf("gates: %w", err)
	}
	if err = decodeStrictReqV2(d.GatesRaw, &d.Gates); err != nil {
		return nil, err
	}
	if d.ProvenanceRaw, err = os.ReadFile(filepath.Join(root, "provenance.json")); err != nil {
		return nil, fmt.Errorf("provenance: %w", err)
	}
	listed := map[string]bool{}
	for _, f := range d.Manifest.Files {
		listed[f.Path] = true
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		if sum := Hash(raw); sum != f.SHA256 {
			return nil, fmt.Errorf("%s: frozen SHA256 differs from manifest; create and review a new fixture revision", f.Path)
		}
		if err = d.loadLayerFile(f.Path, raw); err != nil {
			return nil, err
		}
	}
	if err = d.rejectUnlistedCaseFiles(listed); err != nil {
		return nil, err
	}
	if err = d.verifySplits(); err != nil {
		return nil, err
	}
	if err = d.verifyGates(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *ReqV2Dataset) loadLayerFile(path string, raw []byte) error {
	switch path {
	case "extraction/cases.json":
		return decodeStrictReqV2(raw, &d.Extraction)
	case "reducer/cases.json":
		return decodeStrictReqV2(raw, &d.Reducer)
	case "readiness/cases.json":
		return decodeStrictReqV2(raw, &d.Readiness)
	case "policy/cases.json":
		return decodeStrictReqV2(raw, &d.Policy)
	case "conversations/cases.json":
		return decodeStrictReqV2(raw, &d.Conversations)
	case "ui-contract/cases.json":
		return decodeStrictReqV2(raw, &d.UIContract)
	case "selftest.json":
		var file ReqV2SelftestFile
		if err := decodeStrictReqV2(raw, &file); err != nil {
			return err
		}
		d.Selftest = file.Entries
		return nil
	case "catalog.json":
		return decodeStrictReqV2(raw, &d.Catalog)
	}
	return fmt.Errorf("manifest lists unknown layer file %q", path)
}

// rejectUnlistedCaseFiles 保证 manifest 覆盖全部 case 内容；新增文件必须走
// 显式 freeze-manifest，不能静默游离在清单之外。
func (d *ReqV2Dataset) rejectUnlistedCaseFiles(listed map[string]bool) error {
	var found []string
	for _, layer := range append([]string{"selftest.json", "catalog.json"}, ReqV2Layers...) {
		p := layer
		if !strings.HasSuffix(p, ".json") {
			p = filepath.ToSlash(filepath.Join(p, "cases.json"))
		}
		if _, err := os.Stat(filepath.Join(d.Root, filepath.FromSlash(p))); err == nil {
			found = append(found, p)
		}
	}
	for _, p := range found {
		if !listed[p] {
			return fmt.Errorf("%s exists but is not listed in manifest; run freeze-manifest and review", p)
		}
	}
	return nil
}

// verifySplits 校验会话级隔离：每个 case 的 split 必须与其 session 的
// manifest 分配一致；同一 session / 同一模板实例不得跨 split。
func (d *ReqV2Dataset) verifySplits() error {
	assign := map[string]string{}
	for split, sessions := range d.Manifest.SplitSessions {
		for _, s := range sessions {
			if other, dup := assign[s]; dup {
				return fmt.Errorf("session %q listed in both %s and %s", s, other, split)
			}
			assign[s] = split
		}
	}
	seen := map[string]string{}
	template := map[string]string{}
	check := func(layer string, h ReqV2CaseHead) error {
		if err := h.validate(layer, seen); err != nil {
			return err
		}
		if want, ok := assign[h.Session]; ok && want != h.Split {
			return fmt.Errorf("%s case %q: split %q contradicts manifest session %q in %q", layer, h.ID, h.Split, h.Session, want)
		}
		if _, ok := assign[h.Session]; !ok {
			return fmt.Errorf("%s case %q: session %q missing from manifest split_sessions", layer, h.ID, h.Session)
		}
		if h.VariantsOf != "" {
			if other, dup := template[h.VariantsOf]; dup && other != h.Split {
				return fmt.Errorf("template %q spans splits %s and %s (near-duplicate leak)", h.VariantsOf, other, h.Split)
			}
			template[h.VariantsOf] = h.Split
		}
		return nil
	}
	for _, c := range d.Reducer {
		if err := check("reducer", c.ReqV2CaseHead); err != nil {
			return err
		}
	}
	for _, c := range d.Readiness {
		if err := check("readiness", c.ReqV2CaseHead); err != nil {
			return err
		}
	}
	for _, c := range d.Policy {
		if err := check("policy", c.ReqV2CaseHead); err != nil {
			return err
		}
	}
	for _, c := range d.UIContract {
		if err := check("ui-contract", c.ReqV2CaseHead); err != nil {
			return err
		}
	}
	for _, c := range d.Extraction {
		if err := check("extraction", c.ReqV2CaseHead); err != nil {
			return err
		}
	}
	for _, c := range d.Conversations {
		if err := check("conversations", c.ReqV2CaseHead); err != nil {
			return err
		}
	}
	for _, e := range d.Selftest {
		if e.ID == "" || e.Expect != "fail" && e.Expect != "pass" {
			return fmt.Errorf("selftest entry invalid: %+v", e.ID)
		}
		if e.Expect == "fail" && e.Veto == "" {
			return fmt.Errorf("selftest %q: fail entries must name a veto", e.ID)
		}
	}
	for _, split := range []string{"development", "calibration", "holdout"} {
		if len(d.Manifest.SplitSessions[split]) == 0 {
			return fmt.Errorf("split %q is empty; a locked holdout with real gold is part of the frozen contract", split)
		}
	}
	return nil
}

func (d *ReqV2Dataset) verifyGates() error {
	if len(d.Gates.Vetoes) == 0 {
		return fmt.Errorf("gates: veto list required")
	}
	ids := map[string]bool{}
	for _, v := range d.Gates.Vetoes {
		if ids[v.ID] {
			return fmt.Errorf("gates: duplicate veto %s", v.ID)
		}
		ids[v.ID] = true
	}
	for _, want := range []string{"V1", "V2", "V3", "V4", "V5", "V6", "V7", "V8", "V9"} {
		if !ids[want] {
			return fmt.Errorf("gates: veto %s missing", want)
		}
	}
	if len(d.Gates.DeterministicLayers.Names) == 0 || d.Gates.DeterministicLayers.RequiredPassRate != 1.0 {
		return fmt.Errorf("gates: deterministic layers must require 100%%")
	}
	if d.Gates.Repetition.ReleaseRepeats < 3 || d.Gates.Repetition.BestOfKAsGate {
		return fmt.Errorf("gates: release metric must be pass^k with k>=3 and no best-of-k")
	}
	if d.Gates.MinimumPairedSamples < 30 {
		return fmt.Errorf("gates: minimum_paired_samples must be at least 30")
	}
	q := d.Gates.ModelQualityThresholds
	if q.Status != "frozen" {
		return fmt.Errorf("gates: model_quality_thresholds.status must be frozen, got %q", q.Status)
	}
	if q.FrozenAt == "" {
		return fmt.Errorf("gates: model_quality_thresholds.frozen_at required")
	}
	e, c := q.Extraction, q.Conversations
	ratio := func(v float64) bool { return v > 0 && v <= 1 }
	if !ratio(e.OperationPrecisionMin) || !ratio(e.OperationRecallMin) || !ratio(e.TurnSignalExactMatchMin) {
		return fmt.Errorf("gates: extraction thresholds must be ratios in (0,1]")
	}
	if e.OperationPrecisionMin < e.OperationRecallMin {
		return fmt.Errorf("gates: 错误写入门槛必须优先于信息遗漏：operation_precision_min 不得低于 operation_recall_min")
	}
	if e.ForbiddenOpTotalMax != 0 || e.MinCases < 20 {
		return fmt.Errorf("gates: forbidden ops 零容忍且 extraction 最小样本不得低于 20")
	}
	if !ratio(c.TaskSuccessMin) || c.ForbiddenVetoTotalMax != 0 || c.MinCases < 5 {
		return fmt.Errorf("gates: conversations 门槛非法（task_success 需在 (0,1]，veto 零容忍，最小样本 ≥5）")
	}
	if c.RepeatedQuestionMaxPerCase != 0 {
		return fmt.Errorf("gates: repeated_question_max_per_case 必须冻结为 0（零容忍）")
	}
	if e.MinSignalTurns < 10 {
		return fmt.Errorf("gates: min_signal_turns 不得低于 10")
	}
	if !ratio(e.CaseSuccessMin) || !ratio(e.FinalStateExactMatchMin) || !ratio(e.ProviderSuccessMin) {
		return fmt.Errorf("gates: case/final_state/provider success thresholds must be ratios in (0,1]")
	}
	if e.LatencyP95MaxMS <= 0 || e.MaxModelCallsPerTurn <= 0 {
		return fmt.Errorf("gates: latency_p95_max_ms 与 max_model_calls_per_turn 必须为正")
	}
	if len(q.KeyFields) == 0 || q.KeyFieldWrongWriteTotalMax != 0 {
		return fmt.Errorf("gates: 关键字段清单不得为空且错写必须零容忍")
	}
	for _, f := range q.KeyFields {
		if f == "" {
			return fmt.Errorf("gates: key_fields 含空字段名")
		}
	}
	return nil
}

// CasesPerLayer 返回每层选中 split 的 case 数，用于 check 摘要。
func (d *ReqV2Dataset) CasesPerLayer(splits map[string]bool) map[string]int {
	out := map[string]int{}
	take := func(split string) bool { return splits[split] }
	for _, c := range d.Reducer {
		if take(c.Split) {
			out["reducer"]++
		}
	}
	for _, c := range d.Readiness {
		if take(c.Split) {
			out["readiness"]++
		}
	}
	for _, c := range d.Policy {
		if take(c.Split) {
			out["policy"]++
		}
	}
	for _, c := range d.UIContract {
		if take(c.Split) {
			out["ui-contract"]++
		}
	}
	for _, c := range d.Extraction {
		if take(c.Split) {
			out["extraction"]++
		}
	}
	for _, c := range d.Conversations {
		if take(c.Split) {
			out["conversations"]++
		}
	}
	return out
}

// FreezeManifest 从当前 case 文件重算 manifest；只在显式
// freeze-manifest 模式调用，产物需要人工复核后提交。
func FreezeManifest(root string) (*ReqV2Manifest, error) {
	type layerFile struct {
		path  string
		heads []ReqV2CaseHead
	}
	files := []layerFile{
		{path: "reducer/cases.json"},
		{path: "readiness/cases.json"},
		{path: "policy/cases.json"},
		{path: "ui-contract/cases.json"},
		{path: "extraction/cases.json"},
		{path: "conversations/cases.json"},
	}
	read := func(path string, into any) error {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if err := decodeStrictReqV2(raw, into); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	var rs []ReqV2ReducerCase
	var rds []ReqV2ReadinessCase
	var ps []ReqV2PolicyCase
	var us []ReqV2UICase
	var es []ReqV2ExtractionCase
	var cs []ReqV2ConversationCase
	collect := map[string]func(){
		"reducer/cases.json": func() {
			for _, c := range rs {
				files[0].heads = append(files[0].heads, c.ReqV2CaseHead)
			}
		},
		"readiness/cases.json": func() {
			for _, c := range rds {
				files[1].heads = append(files[1].heads, c.ReqV2CaseHead)
			}
		},
		"policy/cases.json": func() {
			for _, c := range ps {
				files[2].heads = append(files[2].heads, c.ReqV2CaseHead)
			}
		},
		"ui-contract/cases.json": func() {
			for _, c := range us {
				files[3].heads = append(files[3].heads, c.ReqV2CaseHead)
			}
		},
		"extraction/cases.json": func() {
			for _, c := range es {
				files[4].heads = append(files[4].heads, c.ReqV2CaseHead)
			}
		},
		"conversations/cases.json": func() {
			for _, c := range cs {
				files[5].heads = append(files[5].heads, c.ReqV2CaseHead)
			}
		},
	}
	for path, into := range map[string]any{
		"reducer/cases.json":       &rs,
		"readiness/cases.json":     &rds,
		"policy/cases.json":        &ps,
		"ui-contract/cases.json":   &us,
		"extraction/cases.json":    &es,
		"conversations/cases.json": &cs,
	} {
		if err := read(path, into); err != nil {
			return nil, err
		}
		collect[path]()
	}
	var ss ReqV2SelftestFile
	if err := read("selftest.json", &ss); err != nil {
		return nil, err
	}
	var cat CatalogFixture
	if err := read("catalog.json", &cat); err != nil {
		return nil, err
	}
	m := &ReqV2Manifest{Dataset: "requirement-v2", GraderVersion: ReqV2GraderVersion, FrozenAt: time.Now().UTC().Format("2006-01-02"), SplitSessions: map[string][]string{}}
	// session → split 来自 case 文件自身；同 session 跨 split 在 Load 时拒绝。
	sessionSplit := map[string]string{}
	for _, f := range files {
		for _, h := range f.heads {
			if other, dup := sessionSplit[h.Session]; dup && other != h.Split {
				return nil, fmt.Errorf("session %q spans splits %s and %s", h.Session, other, h.Split)
			}
			sessionSplit[h.Session] = h.Split
		}
	}
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.path)))
		if err != nil {
			return nil, err
		}
		sessions := map[string]bool{}
		for _, h := range f.heads {
			sessions[h.Session] = true
		}
		m.Files = append(m.Files, ReqV2ManifestFile{Path: f.path, SHA256: Hash(raw), Cases: len(f.heads), Sessions: len(sessions)})
	}
	for session, split := range sessionSplit {
		m.SplitSessions[split] = appendUnique(m.SplitSessions[split], session)
	}
	raw, err := os.ReadFile(filepath.Join(root, "selftest.json"))
	if err != nil {
		return nil, err
	}
	m.Files = append(m.Files, ReqV2ManifestFile{Path: "selftest.json", SHA256: Hash(raw), Cases: len(ss.Entries), Sessions: 0})
	raw, err = os.ReadFile(filepath.Join(root, "catalog.json"))
	if err != nil {
		return nil, err
	}
	m.Files = append(m.Files, ReqV2ManifestFile{Path: "catalog.json", SHA256: Hash(raw), Cases: 0, Sessions: 0})
	for split := range m.SplitSessions {
		sort.Strings(m.SplitSessions[split])
	}
	return m, nil
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
