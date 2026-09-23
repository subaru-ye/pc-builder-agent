package planningeval

// Requirement v2 runner：确定性层零模型执行，模型层走当前产品路径。
// 复用 planningeval 既有的 Prepare/store/gateway/waitRun 机制，不建第二套
// runner。当前实现是 v1 语义，预期在 v2 条目上 red；失败分类记录差距，
// 不修改金标换取通过。
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
)

// 装载侧机械适配:仅升 schema_version 并补 configuration_scope,不改值、
// 不生成用户事实;产品路径不得调用。
func upgradeLegacyEvalSpec(raw json.RawMessage) json.RawMessage {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if json.Unmarshal(raw, &header) != nil || header.SchemaVersion != 1 {
		return raw
	}
	decoded := map[string]json.RawMessage{}
	if json.Unmarshal(raw, &decoded) != nil {
		return raw
	}
	version, _ := json.Marshal(schemas.RequirementSpecSchemaVersion)
	scope, _ := json.Marshal([]string{schemas.ConfigurationScopeTower})
	decoded["schema_version"] = version
	decoded["configuration_scope"] = scope
	if out, err := json.Marshal(decoded); err == nil {
		return out
	}
	return raw
}

// reqV2UpgradeState 把冻结 fixture 的 v1 外形机械升格为 v2 领域输入:
// 只改 schema_version,不改写、不补造任何字段或用户事实。
// 本适配仅限 planningeval 消费冻结 fixture;产品路径不得调用
// (产品 decoder 以稳定错误拒绝 v1,见 schemas.DecodeRequirementState)。
func reqV2UpgradeState(state schemas.RequirementState) schemas.RequirementState {
	state.SchemaVersion = schemas.RequirementStateSchemaVersion
	return state
}

// CurrentReadiness 走 v2 领域 readiness:missing/blocking/unsupported/defaults
// 全部来自 schemas.EvaluateRequirementReadiness,不再有契约缺口标记。
func CurrentReadiness(state schemas.RequirementState) ReqV2ReadinessResult {
	r, err := schemas.EvaluateRequirementReadiness(reqV2UpgradeState(state))
	out := ReqV2ReadinessResult{Status: r.Status, MissingFields: r.MissingFields, BlockingConflicts: r.BlockingConflicts,
		UnsupportedCapabilities: r.UnsupportedCapabilities, ConfirmationEligible: r.ConfirmationEligible}
	for _, d := range r.EffectiveDefaults {
		out.EffectiveDefaults = append(out.EffectiveDefaults, ReqV2DefaultGold{Field: d.Field, Value: d.Value, Origin: d.Origin})
	}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}

// ReqV2RunOptions 控制一次评估。
type ReqV2RunOptions struct {
	DSN       string // 空 → 跳过需要数据库的层并记录限制
	Splits    map[string]bool
	Repeats   int
	Screening model.LLM // nil → extraction/conversations 记为 skipped
	MaxCalls  int
	Journal   func(any) error
}

type ReqV2CaseObservation struct {
	Turns              []ReqV2TurnObservation `json:"turns,omitempty"`
	Readiness          *ReqV2ReadinessResult  `json:"readiness,omitempty"`
	UI                 *ReqV2UIObservation    `json:"ui,omitempty"`
	FinalState         *ReqV2StateProjection  `json:"final_state,omitempty"`
	BuilderEverStarted bool                   `json:"builder_ever_started"`
	Skipped            string                 `json:"skipped,omitempty"` // skip 原因（no_model/no_database）
	Error              string                 `json:"error,omitempty"`
}

type ReqV2UIObservation struct {
	Turn               ReqV2TurnObservation `json:"turn"`
	ReadinessBlock     bool                 `json:"readiness_block_present"`
	RequirementStatus  string               `json:"requirement_status"`
	ConfirmationStatus string               `json:"confirmation_status"`
	BuildRelation      string               `json:"build_relation"`
	ConfirmPayloadKeys []string             `json:"confirm_payload_keys"`
}

type ReqV2CaseResult struct {
	Layer       string               `json:"layer"`
	ID          string               `json:"id"`
	Split       string               `json:"split"`
	Session     string               `json:"session"`
	Repeat      int                  `json:"repeat"`
	Pass        bool                 `json:"pass"`
	Assertions  []ReqV2Assertion     `json:"assertions"`
	Observation ReqV2CaseObservation `json:"observation"`
	Vetoes      []string             `json:"vetoes,omitempty"`
}

type ReqV2LayerSummary struct {
	Cases    int            `json:"cases"`   // pass^k 口径的 case 数（跳过层不计）
	Passed   int            `json:"passed"`  // 全部 repeat 通过
	Skipped  int            `json:"skipped"` // 因缺模型/数据库跳过的 case
	Vetoes   int            `json:"vetoes"`
	Failures map[string]int `json:"failure_classifications"`
}

type ReqV2Usage struct {
	ModelCalls     int            `json:"model_calls"`
	ProviderErrors map[string]int `json:"provider_errors"`
	TokensKnown    int64          `json:"tokens_known"`
	TokensAllKnown bool           `json:"tokens_all_known"`
	LatencyP50MS   int64          `json:"latency_p50_ms"`
	LatencyP95MS   int64          `json:"latency_p95_ms"`
}

type ReqV2Report struct {
	SchemaVersion  int                          `json:"schema_version"`
	Mode           string                       `json:"mode"`
	GraderVersion  string                       `json:"grader_version"`
	ManifestSHA256 string                       `json:"manifest_sha256"`
	GatesSHA256    string                       `json:"gates_sha256"`
	Splits         []string                     `json:"splits"`
	Repeats        int                          `json:"repeats"`
	PerLayer       map[string]ReqV2LayerSummary `json:"per_layer"`
	ModelQuality   map[string]ReqV2ModelQuality `json:"model_quality"`
	GateVerdicts   []ReqV2GateVerdict           `json:"gate_verdicts"`
	GatePassed     *bool                        `json:"gate_passed"`
	Gates          *ReqV2Gates                  `json:"gates,omitempty"`
	Cases          []ReqV2CaseResult            `json:"cases"`
	Usage          ReqV2Usage                   `json:"usage"`
	// MaxModelRequests 是 plan.json 的显式调用预算；0 表示未声明（预算
	// 一致性门槛判 UNEVALUABLE，不得默认通过）。
	MaxModelRequests int      `json:"max_model_requests,omitempty"`
	Limitations      []string `json:"limitations"`
	Conclusion       string   `json:"conclusion"`
	DurationMS       int64    `json:"duration_ms"`
}

// ReqV2ModelQuality 是模型层（extraction/conversations）的汇总指标；
// OpPrecision=matched/emitted，OpRecall=matched/expected，按 op 池化。
type ReqV2ModelQuality struct {
	Cases             int      `json:"cases"`
	OpEmitted         int      `json:"op_emitted"`
	OpMatched         int      `json:"op_matched"`
	OpExpected        int      `json:"op_expected"`
	OpPrecision       *float64 `json:"op_precision"`
	OpRecall          *float64 `json:"op_recall"`
	SignalTurns       int      `json:"signal_turns"`
	SignalMatches     int      `json:"signal_matches"`
	TaskTotal         int      `json:"task_total"`
	TaskPassed        int      `json:"task_passed"`
	ForbiddenOps      int      `json:"forbidden_op_failures"`
	RepeatedQuestions int      `json:"repeated_questions"`
	KeyFieldWrites    int      `json:"key_field_wrong_writes"`
}

// ReqV2GateVerdict 是单条冻结门槛的判定：实际值、阈值与结论；
// Evaluable=false 表示样本不足无法评估（同样视为不可发布）。
type ReqV2GateVerdict struct {
	Layer     string `json:"layer"`
	Metric    string `json:"metric"`
	Actual    string `json:"actual"`
	Threshold string `json:"threshold"`
	Passed    bool   `json:"passed"`
	Evaluable bool   `json:"evaluable"`
	Note      string `json:"note,omitempty"`
}

// countOpMatches 计算实际操作与金标的双向匹配数（贪心）。
func countOpMatches(emitted []schemas.RequirementOperation, expected []ReqV2OpGold) int {
	used := map[int]bool{}
	matched := 0
	for _, w := range expected {
		for i := range emitted {
			if used[i] {
				continue
			}
			if opMatchesGold(emitted[i], w) {
				used[i] = true
				matched++
				break
			}
		}
	}
	return matched
}

// gradeCase 是 run 与 replay 共用的判卷入口：给定 gold 与冻结/新鲜观测，
// 产出断言。replay 不信任旧分数，走同一路径重判。
func gradeCase(dataset *ReqV2Dataset, layer, id string, obs ReqV2CaseObservation) []ReqV2Assertion {
	var assertions []ReqV2Assertion
	matched := false
	appendAll := func(as []ReqV2Assertion) { assertions = append(assertions, as...) }
	firstTurn := func() ReqV2TurnObservation {
		if len(obs.Turns) > 0 {
			return obs.Turns[0]
		}
		return ReqV2TurnObservation{}
	}
	switch layer {
	case "reducer":
		for _, c := range dataset.Reducer {
			if c.ID == id {
				matched = true
				appendAll(GradeReqV2Reducer(c, firstTurn()))
			}
		}
	case "readiness":
		for _, c := range dataset.Readiness {
			if c.ID == id && obs.Readiness != nil {
				matched = true
				appendAll(GradeReqV2Readiness(c, *obs.Readiness))
			}
		}
	case "policy":
		for _, c := range dataset.Policy {
			if c.ID == id {
				matched = true
				appendAll(GradeReqV2Policy(c, firstTurn()))
			}
		}
	case "ui-contract":
		for _, c := range dataset.UIContract {
			if c.ID == id && obs.UI != nil {
				matched = true
				appendAll(GradeReqV2UI(c, obs.UI.Turn, obs.UI.ReadinessBlock, obs.UI.ConfirmationStatus, obs.UI.BuildRelation, obs.UI.ConfirmPayloadKeys))
			}
		}
	case "extraction":
		for _, c := range dataset.Extraction {
			if c.ID == id {
				matched = true
				watch := mentionsUnsupportedPeripheral(c.UserMessage)
				appendAll(GradeReqV2Turn("extraction", c.Expected, firstTurn(), nil, watch))
			}
		}
	case "conversations":
		for _, c := range dataset.Conversations {
			if c.ID != id {
				continue
			}
			matched = true
			watch := false
			for _, t := range c.Turns {
				watch = watch || mentionsUnsupportedPeripheral(t.Text)
			}
			for i, t := range c.Turns {
				turn := ReqV2TurnObservation{}
				if i < len(obs.Turns) {
					turn = obs.Turns[i]
				}
				appendAll(GradeReqV2Turn("conversations", t.Expected, turn, nil, watch))
			}
			final := ReqV2TurnObservation{BuilderStarted: obs.BuilderEverStarted}
			if obs.FinalState != nil {
				final.State = *obs.FinalState
			}
			if len(obs.Turns) > 0 && obs.Turns[len(obs.Turns)-1].Readiness != nil {
				final.Readiness = obs.Turns[len(obs.Turns)-1].Readiness
			}
			appendAll(gradeConversationFinal(c, final))
		}
	}
	if !matched {
		// 冻结记录引用了数据集没有的 case：不得空判卷静默通过。
		assertions = append(assertions, ReqV2Assertion{Name: layer + ":gold_missing", Pass: false, Classification: reqV2ClassFault, Detail: id})
	}
	return assertions
}

func gradeConversationFinal(c ReqV2ConversationCase, final ReqV2TurnObservation) []ReqV2Assertion {
	g := &reqV2Grader{}
	g.gradeStateFields("conversations:final", c.ID, final.State, ReqV2StateProjection{Fields: map[string]ReqV2FieldProjection{}}, c.Final.StateFields, false)
	if c.Final.MissingFields != nil && final.Readiness != nil {
		g.check("conversations:final:missing_fields", equalStringList(final.Readiness.MissingFields, c.Final.MissingFields), reqV2ClassBehavior, "", fmt.Sprint(final.Readiness.MissingFields))
	}
	if final.Readiness != nil {
		g.check("conversations:final:confirmation_eligible", final.Readiness.ConfirmationEligible == c.Final.ConfirmationEligible, reqV2ClassBehavior, "", fmt.Sprint(final.Readiness.ConfirmationEligible))
	}
	g.check("conversations:final:builder_must_run", final.BuilderStarted == c.Final.BuilderMustRun, reqV2ClassBehavior, "", fmt.Sprintf("started=%v", final.BuilderStarted))
	return g.assertions
}

// RunRequirementV2 执行一次评估。确定性层零模型；模型层需要 Screening 与
// DSN。技术错误以 error 返回；行为 red 是报告内容。
func RunRequirementV2(ctx context.Context, dataset *ReqV2Dataset, opts ReqV2RunOptions) (*ReqV2Report, error) {
	start := time.Now()
	if opts.Repeats < 1 {
		return nil, fmt.Errorf("repeats must be positive")
	}
	if opts.Screening != nil && opts.MaxCalls <= 0 {
		return nil, fmt.Errorf("live screening requires a positive shared call budget")
	}
	mode := "deterministic"
	if opts.Screening != nil {
		mode = "live_screening"
	}
	report := &ReqV2Report{
		SchemaVersion: 1, Mode: mode, GraderVersion: ReqV2GraderVersion,
		ManifestSHA256: Hash(dataset.ManifestRaw), GatesSHA256: Hash(dataset.GatesRaw),
		PerLayer: map[string]ReqV2LayerSummary{}, ModelQuality: map[string]ReqV2ModelQuality{"extraction": {}, "conversations": {}},
		Gates: &dataset.Gates, Repeats: opts.Repeats, MaxModelRequests: opts.MaxCalls,
		Usage: ReqV2Usage{ProviderErrors: map[string]int{}, TokensAllKnown: true},
		Limitations: []string{
			"当前产品是 Requirement v1 语义；v2 条目上的 red 是预期证据，不是回归。",
			"Builder 输出质量不在本评估范围（planning-v2 已覆盖）；v2 驱动器用 scripted builder 观察 admission 行为。",
			"V5 需要成功 build 才有可比 hash；当前基线只能在 grader 金丝雀层验证该 veto。",
		},
	}
	for split := range opts.Splits {
		report.Splits = append(report.Splits, split)
	}
	sort.Strings(report.Splits)

	var st *store.Store
	if opts.DSN != "" {
		if err := Prepare(ctx, opts.DSN, dataset.Catalog); err != nil {
			return report, err
		}
		s, err := store.New(ctx, opts.DSN)
		if err != nil {
			return report, err
		}
		defer s.Close()
		st = s
	} else {
		report.Limitations = append(report.Limitations, "未提供隔离数据库：policy/ui-contract/extraction/conversations 层跳过。")
	}
	if opts.Screening == nil {
		report.Limitations = append(report.Limitations, "未配置 Screening 模型：extraction/conversations 层跳过。")
	}

	type pending struct {
		layer, id, split, session string
	}
	var order []pending
	add := func(layer, id, split, session string) {
		if opts.Splits[split] {
			order = append(order, pending{layer, id, split, session})
		}
	}
	for _, c := range dataset.Reducer {
		add("reducer", c.ID, c.Split, c.Session)
	}
	for _, c := range dataset.Readiness {
		add("readiness", c.ID, c.Split, c.Session)
	}
	for _, c := range dataset.Policy {
		add("policy", c.ID, c.Split, c.Session)
	}
	for _, c := range dataset.UIContract {
		add("ui-contract", c.ID, c.Split, c.Session)
	}
	for _, c := range dataset.Extraction {
		add("extraction", c.ID, c.Split, c.Session)
	}
	for _, c := range dataset.Conversations {
		add("conversations", c.ID, c.Split, c.Session)
	}

	for _, p := range order {
		for repeat := 1; repeat <= opts.Repeats; repeat++ {
			obs, err := observeCase(ctx, dataset, st, opts, p.layer, p.id)
			if err != nil {
				return report, fmt.Errorf("%s/%s: %w", p.layer, p.id, err)
			}
			result := finalizeCaseResult(dataset, p.layer, p.id, p.split, p.session, repeat, obs)
			accumulateModelQuality(dataset, report, p.layer, p.id, result)
			report.Cases = append(report.Cases, result)
			if opts.Journal != nil {
				if err := opts.Journal(map[string]any{"event": "case_graded", "layer": p.layer, "case": p.id, "repeat": repeat, "pass": result.Pass, "vetoes": result.Vetoes}); err != nil {
					return report, err
				}
			}
		}
	}
	finalizeReport(report)
	evaluateQualityGates(report, dataset.Gates)
	report.DurationMS = time.Since(start).Milliseconds()
	return report, nil
}

// keyFieldWrongWrites 统计关键字段的错值/额外写入：先按金标贪心匹配，
// 之后仍未匹配且落在关键字段上的实际操作计为错写；完全漏记不计（归 recall）。
func keyFieldWrongWrites(emitted []schemas.RequirementOperation, expected []ReqV2OpGold, keyFields map[string]bool) int {
	used := map[int]bool{}
	for _, w := range expected {
		for i := range emitted {
			if used[i] {
				continue
			}
			if opMatchesGold(emitted[i], w) {
				used[i] = true
				break
			}
		}
	}
	n := 0
	for i := range emitted {
		if !used[i] && keyFields[emitted[i].Field] {
			n++
		}
	}
	return n
}

// accumulateModelQuality 汇总模型层 op 级 precision/recall、signal 精确匹配
// 与任务成功率；repeat 折叠为 pass^k 的 case 级口径。
func accumulateModelQuality(dataset *ReqV2Dataset, report *ReqV2Report, layer, id string, result ReqV2CaseResult) {
	if layer != "extraction" && layer != "conversations" {
		return
	}
	q := report.ModelQuality[layer]
	golds := []ReqV2ExtractionGold{}
	switch layer {
	case "extraction":
		for _, c := range dataset.Extraction {
			if c.ID == id {
				golds = append(golds, c.Expected)
			}
		}
	case "conversations":
		for _, c := range dataset.Conversations {
			if c.ID == id {
				for _, t := range c.Turns {
					golds = append(golds, t.Expected)
				}
			}
		}
	}
	keyFields := map[string]bool{}
	for _, f := range dataset.Gates.ModelQualityThresholds.KeyFields {
		keyFields[f] = true
	}
	turns := result.Observation.Turns
	for i, gold := range golds {
		if i >= len(turns) {
			break
		}
		t := turns[i]
		q.OpExpected += len(gold.Operations)
		q.OpEmitted += len(t.Operations)
		q.OpMatched += countOpMatches(t.Operations, gold.Operations)
		q.KeyFieldWrites += keyFieldWrongWrites(t.Operations, gold.Operations, keyFields)
		if gold.TurnSignals != nil {
			q.SignalTurns++
			if t.SignalsKnown && *t.Signals == *gold.TurnSignals {
				q.SignalMatches++
			}
		}
	}
	for _, a := range result.Assertions {
		if !a.Pass && strings.HasPrefix(a.Name, layer+":forbidden_op:") {
			q.ForbiddenOps++
		}
		if !a.Pass && strings.HasPrefix(a.Name, layer+":repeated_question:") {
			q.RepeatedQuestions++
		}
	}
	report.ModelQuality[layer] = q
}

// evaluateQualityGates 逐项执行冻结门槛并产出结构化 verdict；任何失败或
// 不可评估都使候选不可发布。veto 与确定性层结论也一并汇入最终结论。
func evaluateQualityGates(report *ReqV2Report, gates ReqV2Gates) {
	ratio := func(m, d int) *float64 {
		if d == 0 {
			return nil
		}
		v := float64(m) / float64(d)
		return &v
	}
	for layer := range report.ModelQuality {
		q := report.ModelQuality[layer]
		q.OpPrecision = ratio(q.OpMatched, q.OpEmitted)
		q.OpRecall = ratio(q.OpMatched, q.OpExpected)
		report.ModelQuality[layer] = q
	}
	verdict := func(layer, metric, actual, threshold string, passed, evaluable bool, note string) {
		report.GateVerdicts = append(report.GateVerdicts, ReqV2GateVerdict{
			Layer: layer, Metric: metric, Actual: actual, Threshold: threshold,
			Passed: passed, Evaluable: evaluable, Note: note,
		})
	}
	insufficient := func(layer, metric string, actual, want int) {
		verdict(layer, metric, fmt.Sprintf("%d", actual), fmt.Sprintf("≥%d", want), false, false, "样本不足：门槛不可评估，按不可发布处理")
	}
	e := gates.ModelQualityThresholds.Extraction
	c := gates.ModelQualityThresholds.Conversations
	ex := report.ModelQuality["extraction"]
	cv := report.ModelQuality["conversations"]

	// extraction：样本门槛
	if ex.Cases < e.MinCases {
		insufficient("extraction", "min_cases", ex.Cases, e.MinCases)
	} else {
		verdict("extraction", "min_cases", fmt.Sprintf("%d", ex.Cases), fmt.Sprintf("≥%d", e.MinCases), true, true, "")
	}
	// op precision / recall（池化）
	if ex.OpEmitted == 0 {
		insufficient("extraction", "operation_precision", 0, 1)
	} else {
		verdict("extraction", "operation_precision", fmt.Sprintf("%.3f", *ex.OpPrecision), fmt.Sprintf("≥%.2f", e.OperationPrecisionMin), *ex.OpPrecision >= e.OperationPrecisionMin, true, "")
	}
	if ex.OpExpected == 0 {
		insufficient("extraction", "operation_recall", 0, 1)
	} else {
		verdict("extraction", "operation_recall", fmt.Sprintf("%.3f", *ex.OpRecall), fmt.Sprintf("≥%.2f", e.OperationRecallMin), *ex.OpRecall >= e.OperationRecallMin, true, "")
	}
	verdict("extraction", "forbidden_op_total", fmt.Sprintf("%d", ex.ForbiddenOps), fmt.Sprintf("≤%d", e.ForbiddenOpTotalMax), ex.ForbiddenOps <= e.ForbiddenOpTotalMax, true, "")
	// turn signal：独立样本门槛 min_signal_turns
	if ex.SignalTurns < e.MinSignalTurns {
		insufficient("extraction", "min_signal_turns", ex.SignalTurns, e.MinSignalTurns)
	} else {
		rate := float64(ex.SignalMatches) / float64(ex.SignalTurns)
		verdict("extraction", "turn_signal_exact_match", fmt.Sprintf("%.3f (%d/%d)", rate, ex.SignalMatches, ex.SignalTurns), fmt.Sprintf("≥%.2f", e.TurnSignalExactMatchMin), rate >= e.TurnSignalExactMatchMin, true, "")
	}

	// extraction 补充门槛（数据不足一律 UNEVALUABLE，不得默认通过）。
	// case_success：case 级 Pass^k 折叠成功率（TaskTotal/TaskPassed 已折叠）。
	if ex.TaskTotal == 0 {
		insufficient("extraction", "case_success", 0, 1)
	} else {
		rate := float64(ex.TaskPassed) / float64(ex.TaskTotal)
		verdict("extraction", "case_success", fmt.Sprintf("%.3f (%d/%d)", rate, ex.TaskPassed, ex.TaskTotal), fmt.Sprintf("≥%.2f", e.CaseSuccessMin), rate >= e.CaseSuccessMin, true, "")
	}
	// final_state_exact_match：断言了最终状态的 case 中，全部 repeat 的
	// state 断言都通过的占比。
	type stateFold struct {
		hasState, allPass bool
	}
	stateCases := map[string]*stateFold{}
	for _, cr := range report.Cases {
		if cr.Layer != "extraction" || cr.Observation.Skipped != "" {
			continue
		}
		f, ok := stateCases[cr.ID]
		if !ok {
			f = &stateFold{allPass: true}
			stateCases[cr.ID] = f
		}
		turnHasState := false
		for _, a := range cr.Assertions {
			if strings.HasPrefix(a.Name, "extraction:state:") {
				turnHasState = true
				if !a.Pass {
					f.allPass = false
				}
			}
		}
		f.hasState = f.hasState || turnHasState
	}
	stateTotal, stateExact := 0, 0
	for _, f := range stateCases {
		if !f.hasState {
			continue
		}
		stateTotal++
		if f.allPass {
			stateExact++
		}
	}
	if stateTotal == 0 {
		insufficient("extraction", "final_state_exact_match", 0, 1)
	} else {
		rate := float64(stateExact) / float64(stateTotal)
		verdict("extraction", "final_state_exact_match", fmt.Sprintf("%.3f (%d/%d)", rate, stateExact, stateTotal), fmt.Sprintf("≥%.2f", e.FinalStateExactMatchMin), rate >= e.FinalStateExactMatchMin, true, "")
	}
	// provider_success / latency_p95 / max_model_calls_per_turn / 预算一致性：
	// 统计 extraction+conversations 的全部真实 provider 轮（ScreenModelCalled），
	// 与 report.usage 的延迟/调用样本范围一致。
	providerTurns, providerOK := 0, 0
	var latencies []int64
	maxPerTurn := 0
	for _, cr := range report.Cases {
		if cr.Layer != "extraction" && cr.Layer != "conversations" {
			continue
		}
		for _, t := range cr.Observation.Turns {
			if !t.ScreenModelCalled {
				continue
			}
			providerTurns++
			if t.ProviderError == "" {
				providerOK++
			}
			latencies = append(latencies, t.DurationMS)
			if t.ProviderRequests > maxPerTurn {
				maxPerTurn = t.ProviderRequests
			}
		}
	}
	if providerTurns == 0 {
		insufficient("model", "provider_success", 0, 1)
	} else {
		rate := float64(providerOK) / float64(providerTurns)
		verdict("model", "provider_success", fmt.Sprintf("%.3f (%d/%d)", rate, providerOK, providerTurns), fmt.Sprintf("≥%.2f", e.ProviderSuccessMin), rate >= e.ProviderSuccessMin, true, "")
	}
	if len(latencies) == 0 {
		insufficient("model", "latency_p95", 0, 1)
	} else {
		_, p95 := percentiles(latencies)
		verdict("model", "latency_p95_ms", fmt.Sprintf("%dms", p95), fmt.Sprintf("≤%dms", e.LatencyP95MaxMS), p95 <= e.LatencyP95MaxMS, true, "")
	}
	if providerTurns == 0 {
		insufficient("model", "max_model_calls_per_turn", 0, 1)
	} else {
		verdict("model", "max_model_calls_per_turn", fmt.Sprintf("%d", maxPerTurn), fmt.Sprintf("≤%d", e.MaxModelCallsPerTurn), maxPerTurn <= e.MaxModelCallsPerTurn, true, "")
	}
	// 预算一致性：整轮总调用不得超过 plan 显式预算；预算未声明即不可评估。
	if report.MaxModelRequests <= 0 {
		verdict("model", "total_calls_within_budget", fmt.Sprintf("%d", report.Usage.ModelCalls), "plan 未声明预算", false, false, "max_model_requests 缺失：门槛不可评估，按不可发布处理")
	} else {
		verdict("model", "total_calls_within_budget", fmt.Sprintf("%d", report.Usage.ModelCalls), fmt.Sprintf("≤%d", report.MaxModelRequests), report.Usage.ModelCalls <= report.MaxModelRequests, true, "")
	}

	// conversations：样本门槛与任务成功
	if cv.Cases < c.MinCases {
		insufficient("conversations", "min_cases", cv.Cases, c.MinCases)
	} else {
		verdict("conversations", "min_cases", fmt.Sprintf("%d", cv.Cases), fmt.Sprintf("≥%d", c.MinCases), true, true, "")
	}
	if cv.TaskTotal == 0 {
		insufficient("conversations", "task_success", 0, 1)
	} else {
		rate := float64(cv.TaskPassed) / float64(cv.TaskTotal)
		verdict("conversations", "task_success", fmt.Sprintf("%.3f (%d/%d)", rate, cv.TaskPassed, cv.TaskTotal), fmt.Sprintf("≥%.2f", c.TaskSuccessMin), rate >= c.TaskSuccessMin, true, "")
	}
	// 重复追问：按 case 的最差 repeat 计数（repeat 折叠口径）
	worstRepeat := 0
	perCase := map[string]map[int]int{}
	for _, cr := range report.Cases {
		if cr.Layer != "conversations" {
			continue
		}
		if perCase[cr.ID] == nil {
			perCase[cr.ID] = map[int]int{}
		}
		for _, a := range cr.Assertions {
			if !a.Pass && strings.HasPrefix(a.Name, "conversations:repeated_question:") {
				perCase[cr.ID][cr.Repeat]++
			}
		}
	}
	for _, repeats := range perCase {
		for _, n := range repeats {
			if n > worstRepeat {
				worstRepeat = n
			}
		}
	}
	verdict("conversations", "repeated_question_max_per_case", fmt.Sprintf("%d", worstRepeat), fmt.Sprintf("≤%d", c.RepeatedQuestionMaxPerCase), worstRepeat <= c.RepeatedQuestionMaxPerCase, true, "")

	// veto 零容忍（全层）
	totalVetoes := 0
	for _, layer := range ReqV2Layers {
		totalVetoes += report.PerLayer[layer].Vetoes
	}
	verdict("all", "veto_total", fmt.Sprintf("%d", totalVetoes), fmt.Sprintf("≤%d", c.ForbiddenVetoTotalMax), totalVetoes <= c.ForbiddenVetoTotalMax, true, "")

	// 关键字段错写零容忍（模型层池化）
	keyWrong := ex.KeyFieldWrites + cv.KeyFieldWrites
	k := gates.ModelQualityThresholds
	verdict("extraction+conversations", "key_field_wrong_write_total", fmt.Sprintf("%d", keyWrong), fmt.Sprintf("≤%d", k.KeyFieldWrongWriteTotalMax), keyWrong <= k.KeyFieldWrongWriteTotalMax, ex.OpEmitted+cv.OpEmitted > 0, "")

	// 最终结论：veto / 确定性层 / 任一质量门槛失败或不可评估 → 不可发布。
	passed := true
	var failures, unevaluable []string
	for _, v := range report.GateVerdicts {
		if v.Passed && v.Evaluable {
			continue
		}
		passed = false
		name := v.Layer + "." + v.Metric
		if !v.Evaluable {
			unevaluable = append(unevaluable, name)
		} else {
			failures = append(failures, fmt.Sprintf("%s（实际 %s，门槛 %s）", name, v.Actual, v.Threshold))
		}
	}
	deterministicAll := true
	for _, layer := range ReqV2DeterministicLayers {
		sl := report.PerLayer[layer]
		if sl.Cases == 0 || sl.Passed != sl.Cases {
			deterministicAll = false
		}
	}
	if !deterministicAll {
		passed = false
	}
	report.GatePassed = &passed
	switch {
	case totalVetoes > 0:
		report.Conclusion = fmt.Sprintf("veto 违规 %d 项：候选不可发布。", totalVetoes)
	case !passed:
		parts := append(append([]string{}, failures...), unevaluable...)
		report.Conclusion = fmt.Sprintf("门槛未全过（%d 项失败/不可评估）：候选不可发布。明细：%s", len(parts), strings.Join(parts, "；"))
	default:
		report.Conclusion = "全部冻结门槛通过：确定性层 100%、无 veto、模型层指标达标。"
	}
}

func finalizeCaseResult(dataset *ReqV2Dataset, layer, id, split, session string, repeat int, obs ReqV2CaseObservation) ReqV2CaseResult {
	result := ReqV2CaseResult{Layer: layer, ID: id, Split: split, Session: session, Repeat: repeat, Pass: true, Observation: obs}
	if obs.Skipped != "" {
		result.Pass = false
		result.Assertions = []ReqV2Assertion{{Name: layer + ":skipped", Pass: false, Classification: "skipped", Detail: obs.Skipped}}
		return result
	}
	result.Assertions = gradeCase(dataset, layer, id, obs)
	vetoSet := map[string]bool{}
	for _, a := range result.Assertions {
		if !a.Pass {
			result.Pass = false
		}
		if a.Veto != "" {
			vetoSet[a.Veto] = true
		}
	}
	for v := range vetoSet {
		result.Vetoes = append(result.Vetoes, v)
	}
	sort.Strings(result.Vetoes)
	return result
}

func finalizeReport(report *ReqV2Report) {
	// pass^k：同一 case 的全部 repeat 都通过才算通过。
	passK := map[string]map[string]bool{}
	skipped := map[string]map[string]bool{}
	vetoes := map[string]int{}
	ensure := func(m map[string]map[string]bool, layer, key string) {
		if m[layer] == nil {
			m[layer] = map[string]bool{}
		}
		if _, ok := m[layer][key]; !ok {
			m[layer][key] = true
		}
	}
	for _, c := range report.Cases {
		key := c.ID
		if c.Observation.Skipped != "" {
			ensure(skipped, c.Layer, key)
			skipped[c.Layer][key] = true
			continue
		}
		ensure(passK, c.Layer, key)
		passK[c.Layer][key] = passK[c.Layer][key] && c.Pass
		vetoes[c.Layer] += len(c.Vetoes)
	}
	var latencies []int64
	for _, c := range report.Cases {
		for _, t := range c.Observation.Turns {
			report.Usage.ModelCalls += t.ProviderRequests
			if t.ScreenModelCalled && t.ProviderError != "" {
				report.Usage.ProviderErrors[t.ProviderErrorClass]++
			}
			if t.ScreenModelCalled {
				if t.InputTokens != nil && t.OutputTokens != nil {
					report.Usage.TokensKnown += int64(*t.InputTokens) + int64(*t.OutputTokens)
				} else {
					report.Usage.TokensAllKnown = false
				}
				latencies = append(latencies, t.DurationMS)
			}
		}
	}
	report.Usage.LatencyP50MS, report.Usage.LatencyP95MS = percentiles(latencies)
	// 任务成功按 pass^k 折叠结果统计（repeat 不是独立样本）。
	for _, layer := range []string{"extraction", "conversations"} {
		if report.ModelQuality == nil {
			break
		}
		q := report.ModelQuality[layer]
		for _, pass := range passK[layer] {
			q.Cases++
			q.TaskTotal++
			if pass {
				q.TaskPassed++
			}
		}
		report.ModelQuality[layer] = q
	}
	for _, layer := range ReqV2Layers {
		summary := ReqV2LayerSummary{Failures: map[string]int{}}
		for _, pass := range passK[layer] {
			summary.Cases++
			if pass {
				summary.Passed++
			}
		}
		for range skipped[layer] {
			summary.Skipped++
		}
		summary.Vetoes = vetoes[layer]
		for _, c := range report.Cases {
			if c.Layer != layer || c.Observation.Skipped != "" {
				continue
			}
			for _, a := range c.Assertions {
				if !a.Pass && a.Classification != "" {
					summary.Failures[a.Classification]++
				}
			}
		}
		report.PerLayer[layer] = summary
	}
	// 结论统一在 evaluateQualityGates 里汇合（veto/确定性/质量门槛）。
}

// percentiles 使用 nearest-rank 定义（idx = ceil(p*n)-1）：小样本时更保守，
// 适合作为延迟上限门槛，不会因样本少而漏掉最慢轮。
func percentiles(values []int64) (int64, int64) {
	if len(values) == 0 {
		return 0, 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	pick := func(p float64) int64 {
		idx := int(math.Ceil(p*float64(len(sorted)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return sorted[idx]
	}
	return pick(0.5), pick(0.95)
}

func observeCase(ctx context.Context, dataset *ReqV2Dataset, st *store.Store, opts ReqV2RunOptions, layer, id string) (ReqV2CaseObservation, error) {
	switch layer {
	case "reducer":
		return observeReducerCase(dataset, id)
	case "readiness":
		return observeReadinessCase(dataset, id)
	case "policy":
		return observePolicyCase(ctx, dataset, st, opts, id)
	case "ui-contract":
		return observeUICase(ctx, dataset, st, opts, id)
	case "extraction":
		return observeExtractionCase(ctx, dataset, st, opts, id)
	case "conversations":
		return observeConversationCase(ctx, dataset, st, opts, id)
	}
	return ReqV2CaseObservation{}, fmt.Errorf("unknown layer %q", layer)
}

func observeReducerCase(dataset *ReqV2Dataset, id string) (ReqV2CaseObservation, error) {
	for _, c := range dataset.Reducer {
		if c.ID != id {
			continue
		}
		var state schemas.RequirementState
		if len(c.InitialState) > 0 {
			if err := json.Unmarshal(c.InitialState, &state); err != nil {
				return ReqV2CaseObservation{}, fmt.Errorf("initial_state: %w", err)
			}
		}
		if state.Fields == nil {
			state = schemas.NewRequirementState()
		}
		// 判卷的 initial 投影保持 fixture 原外形;只有进入领域 API 的副本
		// 做机械版本升格,错误路径仍按原始状态断言"输入不变"。
		source := schemas.RequirementSource{Kind: c.Update.Source, MessageID: "turn-1", Quote: c.UserMessage}
		next, err := schemas.ApplyRequirementUpdate(reqV2UpgradeState(state), schemas.RequirementUpdate{Operations: c.Update.Operations}, source)
		obs := ReqV2TurnObservation{}
		if err != nil {
			obs.Error = err.Error()
			obs.State = ProjectRequirementState(state)
		} else {
			obs.State = ProjectRequirementState(next)
		}
		return ReqV2CaseObservation{Turns: []ReqV2TurnObservation{obs}}, nil
	}
	return ReqV2CaseObservation{}, fmt.Errorf("reducer case %q not found", id)
}

func observeReadinessCase(dataset *ReqV2Dataset, id string) (ReqV2CaseObservation, error) {
	for _, c := range dataset.Readiness {
		if c.ID != id {
			continue
		}
		var state schemas.RequirementState
		if err := json.Unmarshal(c.State, &state); err != nil {
			return ReqV2CaseObservation{}, fmt.Errorf("state: %w", err)
		}
		if state.Fields == nil {
			state = schemas.NewRequirementState()
		}
		readiness := CurrentReadiness(state)
		return ReqV2CaseObservation{Readiness: &readiness}, nil
	}
	return ReqV2CaseObservation{}, fmt.Errorf("readiness case %q not found", id)
}

// v2Driver 在隔离数据库里驱动真实 product.Service；scripted screening 轮
// 是适配器输入（当前 v1 协议的模型输出替身），不是金标。
type v2Driver struct {
	g                *gateway
	svc              *product.Service
	owner, sessionID string
	live             bool
	journal          func(any) error
}

func newDriver(ctx context.Context, st *store.Store, opts ReqV2RunOptions, screening model.LLM, holdBuilder bool) (*v2Driver, error) {
	g := &gateway{store: st, models: Models{Screening: screening, MaxCalls: opts.MaxCalls, Journal: opts.Journal, BuilderHold: holdBuilder}}
	svc, err := product.NewService(ctx, st, g, runevents.NewMemory())
	if err != nil {
		return nil, err
	}
	owner := uuid.NewString()
	ws, err := svc.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		_ = svc.Shutdown(ctx)
		return nil, err
	}
	return &v2Driver{g: g, svc: svc, owner: owner, sessionID: ws.ID, live: screening != nil, journal: opts.Journal}, nil
}

func (d *v2Driver) shutdown(ctx context.Context) { _ = d.svc.Shutdown(ctx) }

func (d *v2Driver) currentState(ctx context.Context) (schemas.RequirementState, error) {
	detail, err := d.svc.GetSession(ctx, d.owner, d.sessionID)
	if err != nil {
		return schemas.RequirementState{}, err
	}
	var state schemas.RequirementState
	if len(detail.Session.RequirementState) == 0 {
		return schemas.NewRequirementState(), nil
	}
	if err := json.Unmarshal(detail.Session.RequirementState, &state); err != nil {
		return schemas.RequirementState{}, err
	}
	return state, nil
}

// scriptTurn 执行一轮 scripted screening 消息（零 provider 调用）。
// operations 必须序列化为数组，空批用 []，否则 legacy turn 解码会拒绝。
func (d *v2Driver) scriptTurn(ctx context.Context, text string, turn pipeline.LegacyRequirementTurn) error {
	raw, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	d.g.begin(Step{Kind: "message", Text: text, Screen: raw, ScreenFallback: scriptFallback(turn)})
	started, err := d.svc.StartMessage(ctx, d.owner, d.sessionID, uuid.NewString(), text)
	if err != nil {
		return err
	}
	return waitRun(ctx, d.svc, d.owner, started.Run.ID, false)
}

// turn 是所有产品驱动轮的统一入口。scripted 非空时用它作为该轮的模型输出
// 替身；否则该轮需要真实 Screening。wait=false 用于保持 builder 运行中。
func (d *v2Driver) turn(ctx context.Context, kind, text string, scripted, scriptedFallback json.RawMessage, editOps []schemas.RequirementOperation, expectedRevision int, wait bool) (ReqV2TurnObservation, error) {
	before, err := d.svc.GetSession(ctx, d.owner, d.sessionID)
	if err != nil {
		return ReqV2TurnObservation{}, err
	}
	var beforeState schemas.RequirementState
	_ = json.Unmarshal(before.Session.RequirementState, &beforeState)
	d.g.begin(Step{Kind: kind, Text: text, Screen: scripted, ScreenFallback: scriptedFallback})
	requestID := uuid.NewString()
	var started product.StartResult
	switch kind {
	case "message":
		started, err = d.svc.StartMessage(ctx, d.owner, d.sessionID, requestID, text)
	case "confirm":
		started, err = d.svc.StartConfirm(ctx, d.owner, d.sessionID, requestID)
	case "edit":
		_, err = d.svc.EditRequirement(ctx, d.owner, d.sessionID, requestID, product.RequirementEdit{ExpectedRevision: expectedRevision, Operations: editOps})
	}
	runErr := err
	if err == nil && kind != "edit" && started.Run.ID != "" && wait {
		runErr = waitRun(ctx, d.svc, d.owner, started.Run.ID, d.live)
	}
	record := d.g.snapshot()
	after, readErr := d.svc.GetSession(ctx, d.owner, d.sessionID)
	if readErr != nil {
		return ReqV2TurnObservation{}, readErr
	}
	var state schemas.RequirementState
	_ = json.Unmarshal(after.Session.RequirementState, &state)
	obs := ReqV2TurnObservation{}
	obs.State = ProjectRequirementState(state)
	readiness := CurrentReadiness(state)
	obs.Readiness = &readiness
	if len(after.Messages) > 0 && after.Messages[len(after.Messages)-1].Role == "assistant" {
		obs.Reply = after.Messages[len(after.Messages)-1].Content
	}
	// 上一轮的异步 builder 可能把 PlanningInput 写进本轮的共享 record；
	// edit 轮不可能启动 builder，按轮次归属避免污染。
	obs.BuilderStarted = record.PlanningInput != nil && kind != "edit"
	obs.BuilderStartedViaChat = obs.BuilderStarted && kind == "message"
	if record.PlanningInput != nil {
		if raw, e := schemas.PlanningRequirement(record.PlanningInput.State); e == nil {
			obs.BuilderInputHash = normalizedHash(raw)
		}
	}
	if len(after.Session.ConfirmedRequirement) > 0 {
		obs.ConfirmationSnapshotHash = normalizedHash(after.Session.ConfirmedRequirement)
	}
	if after.ActiveRun != nil {
		obs.BuildersActive = 1
	}
	if runErr != nil {
		if obs.BuilderStarted {
			// scripted builder 的失败是 v2 驱动器刻意行为：admission 已被观测，
			// builder 结果不在本评估范围，不记为本轮技术错误。
			obs.Error = ""
		} else {
			obs.Error = runErr.Error()
		}
	}
	// 过期 revision 的 edit 仍成功即是 V7。
	if kind == "edit" && err == nil && expectedRevision < beforeState.Revision {
		obs.StaleEditAccepted = true
	}
	var tokensIn, tokensOut int64
	tokensKnown := true
	for _, trace := range record.Trace {
		if trace.Role != "screening" {
			continue
		}
		obs.ScreenModelCalled = obs.ScreenModelCalled || trace.ProviderCalled
		if trace.ProviderCalled {
			obs.ProviderRequests++
		}
		obs.DurationMS += trace.DurationMS
		if trace.InputTokens != nil && trace.OutputTokens != nil {
			tokensIn += int64(*trace.InputTokens)
			tokensOut += int64(*trace.OutputTokens)
		} else {
			tokensKnown = false
		}
		if trace.Error != "" {
			obs.ProviderError = trace.Error
			obs.ProviderErrorClass = classifyProviderError(trace.Error)
		}
		if trace.Response != nil {
			text := ""
			for _, p := range trace.Response.Parts {
				if p != nil && !p.Thought {
					text += p.Text
				}
			}
			if turn, e := pipeline.DecodeLegacyRequirementTurn(pipeline.ExtractPayload(text)); e == nil && turn.Operations != nil {
				obs.Operations = turn.Operations
				for _, o := range turn.Observations {
					obs.Observations = append(obs.Observations, ReqV2ObservationProjection{Field: o.Field, Text: o.Quote, Reason: o.Reason})
				}
			} else if e != nil && trace.ProviderCalled {
				obs.ProviderError = fmt.Sprintf("%s; decode: %s", obs.ProviderError, e.Error())
				if obs.ProviderErrorClass == "" {
					obs.ProviderErrorClass = "contract_failure"
				}
			}
		}
	}
	if tokensKnown && (tokensIn > 0 || tokensOut > 0) {
		in, out := int32(tokensIn), int32(tokensOut)
		obs.InputTokens, obs.OutputTokens = &in, &out
	} else if !tokensKnown {
		obs.InputTokens, obs.OutputTokens = nil, nil
	}
	// scripted 轮的 operations 来自脚本本身（产品确实应用了同一份输出）。
	if !obs.ScreenModelCalled && kind == "message" && len(scripted) > 0 {
		var turn pipeline.LegacyRequirementTurn
		if json.Unmarshal(scripted, &turn) == nil {
			obs.Operations = turn.Operations
		}
	}
	if d.journal != nil {
		_ = d.journal(map[string]any{"event": "turn_observed", "session": d.sessionID, "kind": kind, "builder_started": obs.BuilderStarted, "state_revision": obs.State.Revision, "duration_ms": obs.DurationMS})
	}
	return obs, nil
}

func classifyProviderError(err string) string {
	switch {
	case strings.Contains(err, "evaluation model-call limit"):
		return "budget_exhausted"
	case strings.Contains(err, "429") || strings.Contains(strings.ToLower(err), "rate limit"):
		return "rate_limit"
	case strings.Contains(err, "timeout") || strings.Contains(err, "context deadline"):
		return "timeout"
	case strings.Contains(err, "decode"):
		return "contract_failure"
	default:
		return "provider_other"
	}
}

// normalizedHash 对 JSON 做语义规范化后取 SHA256；key 顺序差异不改变 hash。
func normalizedHash(raw []byte) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(normalized))
}

// seedTurn 组装 scripted screening 输出(legacy v1 传输外形,含 reply/next_action
// 声明);空操作批序列化为 []，否则 DecodeLegacyRequirementTurn 会拒绝。
func seedTurn(ops []schemas.RequirementOperation, nextAction string) pipeline.LegacyRequirementTurn {
	if ops == nil {
		ops = []schemas.RequirementOperation{}
	}
	return pipeline.LegacyRequirementTurn{Operations: ops, NextAction: nextAction}
}

// scriptFallback 为 guard 的"collect+已齐备"纠偏重调准备第二条 scripted
// 输出：同一批操作与回复，next_action 改为 confirm，避免 oracle 耗尽。
func scriptFallback(update pipeline.LegacyRequirementTurn) json.RawMessage {
	if update.NextAction != "collect" {
		return nil
	}
	fallback := update
	fallback.NextAction = "confirm"
	raw, err := json.Marshal(fallback)
	if err != nil {
		return nil
	}
	return raw
}

func observePolicyCase(ctx context.Context, dataset *ReqV2Dataset, st *store.Store, opts ReqV2RunOptions, id string) (ReqV2CaseObservation, error) {
	if st == nil {
		return ReqV2CaseObservation{Skipped: "no_database"}, nil
	}
	for _, c := range dataset.Policy {
		if c.ID != id {
			continue
		}
		d, err := newDriver(ctx, st, opts, nil, c.HoldBuilder)
		if err != nil {
			return ReqV2CaseObservation{}, err
		}
		defer d.shutdown(ctx)
		if len(c.Seed) > 0 {
			if err := d.scriptTurn(ctx, c.SeedUserMessage, seedTurn(c.Seed, c.StateNextAction)); err != nil {
				return ReqV2CaseObservation{Error: err.Error()}, nil
			}
		}
		// PreTurns 建立运行中/已确认等前置状态；hold 时保持 builder 不结束。
		for _, pre := range c.PreTurns {
			if _, err := d.runPolicyTurn(ctx, pre, c, !c.HoldBuilder); err != nil {
				return ReqV2CaseObservation{Error: err.Error()}, nil
			}
		}
		obs, err := d.runPolicyTurn(ctx, c.Turn, c, !c.HoldBuilder)
		if err != nil {
			return ReqV2CaseObservation{Error: err.Error()}, nil
		}
		if c.HoldBuilder {
			if active, e := d.svc.GetSession(ctx, d.owner, d.sessionID); e == nil && active.ActiveRun != nil {
				_, _, _ = d.svc.RequestCancel(ctx, d.owner, d.sessionID, active.ActiveRun.ID)
				_ = waitRun(ctx, d.svc, d.owner, active.ActiveRun.ID, false)
			}
		}
		return ReqV2CaseObservation{Turns: []ReqV2TurnObservation{obs}}, nil
	}
	return ReqV2CaseObservation{}, fmt.Errorf("policy case %q not found", id)
}

// runPolicyTurn 执行一个 policy 轮并返回观测；消息轮的 scripted screening
// 输出来自 case 的 next_action/ops/reply 声明（适配器输入，不是金标）。
func (d *v2Driver) runPolicyTurn(ctx context.Context, t ReqV2PolicyTurn, c ReqV2PolicyCase, wait bool) (ReqV2TurnObservation, error) {
	switch t.Kind {
	case "message":
		turn := seedTurn(t.Ops, c.StateNextAction)
		turn.Reply = t.ScriptedReply
		raw, err := json.Marshal(turn)
		if err != nil {
			return ReqV2TurnObservation{}, err
		}
		return d.turn(ctx, "message", t.Text, raw, scriptFallback(turn), nil, 0, wait)
	case "confirm":
		return d.turn(ctx, "confirm", "", nil, nil, nil, 0, wait)
	case "edit":
		state, e := d.currentState(ctx)
		if e != nil {
			return ReqV2TurnObservation{}, e
		}
		return d.turn(ctx, "edit", "", nil, nil, t.Edit, state.Revision+t.EditExpectedRevisionDelta, wait)
	}
	return ReqV2TurnObservation{}, fmt.Errorf("policy turn kind %q", t.Kind)
}

func (d *v2Driver) confirmTurn(ctx context.Context, wait bool) (ReqV2TurnObservation, error) {
	return d.turn(ctx, "confirm", "", nil, nil, nil, 0, wait)
}

func (d *v2Driver) editTurn(ctx context.Context, ops []schemas.RequirementOperation, expectedRevision int, wait bool) (ReqV2TurnObservation, error) {
	return d.turn(ctx, "edit", "", nil, nil, ops, expectedRevision, wait)
}

func observeUICase(ctx context.Context, dataset *ReqV2Dataset, st *store.Store, opts ReqV2RunOptions, id string) (ReqV2CaseObservation, error) {
	if st == nil {
		return ReqV2CaseObservation{Skipped: "no_database"}, nil
	}
	for _, c := range dataset.UIContract {
		if c.ID != id {
			continue
		}
		d, err := newDriver(ctx, st, opts, nil, false)
		if err != nil {
			return ReqV2CaseObservation{}, err
		}
		defer d.shutdown(ctx)
		if len(c.Seed) > 0 {
			if err := d.scriptTurn(ctx, c.SeedUserMessage, seedTurn(c.Seed, c.StateNextAction)); err != nil {
				return ReqV2CaseObservation{Error: err.Error()}, nil
			}
		}
		// 触发一次 scripted next_action 轮，让 DTO 进入目标可观察状态。
		if c.StateNextAction != "" && c.StateNextAction != "collect" {
			if err := d.scriptTurn(ctx, "继续", seedTurn(nil, c.StateNextAction)); err != nil {
				return ReqV2CaseObservation{Error: err.Error()}, nil
			}
		}
		detail, err := d.svc.GetSession(ctx, d.owner, d.sessionID)
		if err != nil {
			return ReqV2CaseObservation{}, err
		}
		var state schemas.RequirementState
		_ = json.Unmarshal(detail.Session.RequirementState, &state)
		turn := ReqV2TurnObservation{State: ProjectRequirementState(state)}
		// DTO 真值：requirementLifecycle 的状态与 missing 就是前端可见内容。
		dtoReadiness := ReqV2ReadinessResult{Status: "incomplete", MissingFields: []string{}, BlockingConflicts: []string{}}
		switch detail.RequirementStatus {
		case "ready_to_confirm", "confirmed", "modified":
			dtoReadiness.Status = "ready"
			dtoReadiness.ConfirmationEligible = true
		}
		dtoReadiness.MissingFields = detail.MissingFields
		turn.Readiness = &dtoReadiness
		confirmation := "unconfirmed"
		if len(detail.Session.ConfirmedRequirement) > 0 {
			confirmation = "confirmed"
		}
		build := "none"
		if detail.ActiveRun != nil {
			build = "running"
		} else if detail.Session.VersionCount > 0 {
			build = "current_or_outdated_undistinguished"
		}
		ui := ReqV2UIObservation{
			Turn: turn, ReadinessBlock: false, RequirementStatus: detail.RequirementStatus,
			ConfirmationStatus: confirmation, BuildRelation: build,
			// 当前 confirm API 只携带 request id；没有 expected_revision payload。
			ConfirmPayloadKeys: []string{"request_id"},
		}
		return ReqV2CaseObservation{UI: &ui}, nil
	}
	return ReqV2CaseObservation{}, fmt.Errorf("ui case %q not found", id)
}

func observeExtractionCase(ctx context.Context, dataset *ReqV2Dataset, st *store.Store, opts ReqV2RunOptions, id string) (ReqV2CaseObservation, error) {
	if st == nil {
		return ReqV2CaseObservation{Skipped: "no_database"}, nil
	}
	if opts.Screening == nil {
		return ReqV2CaseObservation{Skipped: "no_model"}, nil
	}
	for _, c := range dataset.Extraction {
		if c.ID != id {
			continue
		}
		d, err := newDriver(ctx, st, opts, opts.Screening, false)
		if err != nil {
			return ReqV2CaseObservation{}, err
		}
		defer d.shutdown(ctx)
		if len(c.Seed) > 0 {
			if err := d.scriptTurn(ctx, c.SeedUserMessage, seedTurn(c.Seed, "collect")); err != nil {
				return ReqV2CaseObservation{Error: err.Error()}, nil
			}
		}
		if c.PriorAssistantTurn != nil {
			if err := d.scriptTurn(ctx, c.PriorAssistantTurn.UserMessage, pipeline.LegacyRequirementTurn{Operations: []schemas.RequirementOperation{}, Reply: c.PriorAssistantTurn.Reply}); err != nil {
				return ReqV2CaseObservation{Error: err.Error()}, nil
			}
		}
		obs, err := d.turn(ctx, "message", c.UserMessage, nil, nil, nil, 0, true)
		if err != nil {
			return ReqV2CaseObservation{Error: err.Error()}, nil
		}
		return ReqV2CaseObservation{Turns: []ReqV2TurnObservation{obs}}, nil
	}
	return ReqV2CaseObservation{}, fmt.Errorf("extraction case %q not found", id)
}

func observeConversationCase(ctx context.Context, dataset *ReqV2Dataset, st *store.Store, opts ReqV2RunOptions, id string) (ReqV2CaseObservation, error) {
	if st == nil {
		return ReqV2CaseObservation{Skipped: "no_database"}, nil
	}
	if opts.Screening == nil {
		return ReqV2CaseObservation{Skipped: "no_model"}, nil
	}
	for _, c := range dataset.Conversations {
		if c.ID != id {
			continue
		}
		d, err := newDriver(ctx, st, opts, opts.Screening, false)
		if err != nil {
			return ReqV2CaseObservation{}, err
		}
		defer d.shutdown(ctx)
		var turns []ReqV2TurnObservation
		builderEver := false
		for _, t := range c.Turns {
			obs, err := d.turn(ctx, "message", t.Text, nil, nil, nil, 0, true)
			if err != nil {
				return ReqV2CaseObservation{Error: err.Error(), Turns: turns}, nil
			}
			builderEver = builderEver || obs.BuilderStarted
			turns = append(turns, obs)
		}
		state, err := d.currentState(ctx)
		if err != nil {
			return ReqV2CaseObservation{Error: err.Error(), Turns: turns}, nil
		}
		projection := ProjectRequirementState(state)
		return ReqV2CaseObservation{Turns: turns, FinalState: &projection, BuilderEverStarted: builderEver}, nil
	}
	return ReqV2CaseObservation{}, fmt.Errorf("conversation case %q not found", id)
}

// ReplayRequirementV2 零模型重判冻结观测；结论变化逐条列出。
type ReqV2ReplayOutcome struct {
	Report  *ReqV2Report
	Changed []string
}

func ReplayRequirementV2(dataset *ReqV2Dataset, lines [][]byte, maxModelRequests int) (*ReqV2ReplayOutcome, error) {
	report := &ReqV2Report{
		SchemaVersion: 1, Mode: "replay", GraderVersion: ReqV2GraderVersion,
		ManifestSHA256: Hash(dataset.ManifestRaw), GatesSHA256: Hash(dataset.GatesRaw),
		PerLayer: map[string]ReqV2LayerSummary{}, ModelQuality: map[string]ReqV2ModelQuality{"extraction": {}, "conversations": {}},
		Gates:       &dataset.Gates,
		Usage:       ReqV2Usage{ProviderErrors: map[string]int{}, TokensAllKnown: true},
		Limitations: []string{"replay 零模型：只重判冻结观测，不重新执行产品路径；model_quality 与 gate verdict 按同一规则从冻结观测重算。"},
	}
	outcome := &ReqV2ReplayOutcome{Report: report}
	for _, line := range lines {
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var stored struct {
			Layer, ID, Split, Session string
			Repeat                    int
			Pass                      bool
			Observation               ReqV2CaseObservation
		}
		if err := json.Unmarshal(line, &stored); err != nil {
			return nil, err
		}
		result := finalizeCaseResult(dataset, stored.Layer, stored.ID, stored.Split, stored.Session, stored.Repeat, stored.Observation)
		if result.Pass != stored.Pass {
			outcome.Changed = append(outcome.Changed, fmt.Sprintf("%s/%s repeat %d: %v -> %v", stored.Layer, stored.ID, stored.Repeat, stored.Pass, result.Pass))
		}
		accumulateModelQuality(dataset, report, stored.Layer, stored.ID, result)
		report.Cases = append(report.Cases, result)
	}
	// repeats 从冻结记录恢复（逐 case 的最大 repeat 号），不固定写 1。
	repeats := 1
	for _, line := range lines {
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var stored struct {
			Repeat int
		}
		if json.Unmarshal(line, &stored) == nil && stored.Repeat > repeats {
			repeats = stored.Repeat
		}
	}
	report.Repeats = repeats
	report.MaxModelRequests = maxModelRequests
	report.Splits = []string{"as-recorded"}
	finalizeReport(report)
	evaluateQualityGates(report, dataset.Gates)
	return outcome, nil
}

// CompareRequirementV2 零模型配对对照两轮产物。统计纪律：
//   - 模型质量比较只统计 extraction/conversations 的独立 case（session 级）；
//   - repeat 不是独立样本：同一 case 的全部 repeat 折叠为 pass^k 后再配对；
//   - 确定性层单独报告，不参与配对数与 minimum_paired_samples；
//   - 模型层独立样本不足 30 时必须声明“样本不足，不能宣称改善”。
type ReqV2CompareOutcome struct {
	ManifestSame bool     `json:"manifest_same"`
	GraderSame   bool     `json:"grader_same"`
	ModelPairs   int      `json:"model_pairs"` // 独立 case 数（repeat 已折叠）
	StillPass    int      `json:"still_pass"`
	StillFail    int      `json:"still_fail"`
	Regressed    []string `json:"regressed"`
	NewlyPassing []string `json:"newly_passing"`
	McNemarP     *float64 `json:"mcnemar_p,omitempty"`
	McNemarNote  string   `json:"mcnemar_note,omitempty"`
	SampleNote   string   `json:"sample_note,omitempty"`
	// Deterministic 是确定性层的单独对照；不进入配对统计与显著性检验。
	Deterministic map[string]ReqV2CompareLayerDelta `json:"deterministic_layers"`
	ModelLayers   map[string]ReqV2CompareLayerDelta `json:"model_layers"`
}

type ReqV2CompareLayerDelta struct {
	BaselinePass  int `json:"baseline_pass"`
	CandidatePass int `json:"candidate_pass"`
	Total         int `json:"total"`
}

func CompareRequirementV2(gates ReqV2Gates, baseline, candidate [][]byte) (*ReqV2CompareOutcome, error) {
	// fold 把逐 repeat 记录折叠为 case 级 pass^k；layer/id 缺任一侧的 case 不配对。
	fold := func(lines [][]byte) (map[string]bool, error) {
		seen := map[string]bool{}
		out := map[string]bool{}
		for _, line := range lines {
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			var stored struct {
				Layer, ID string
				Repeat    int
				Pass      bool
			}
			if err := json.Unmarshal(line, &stored); err != nil {
				return nil, err
			}
			key := stored.Layer + "/" + stored.ID
			if _, ok := seen[key]; !ok {
				seen[key] = true
				out[key] = stored.Pass
			} else {
				out[key] = out[key] && stored.Pass
			}
		}
		return out, nil
	}
	base, err := fold(baseline)
	if err != nil {
		return nil, err
	}
	cand, err := fold(candidate)
	if err != nil {
		return nil, err
	}
	out := &ReqV2CompareOutcome{
		ManifestSame: true, GraderSame: true,
		Regressed: []string{}, NewlyPassing: []string{},
		Deterministic: map[string]ReqV2CompareLayerDelta{},
		ModelLayers:   map[string]ReqV2CompareLayerDelta{},
	}
	b, c := 0, 0 // discordant pairs
	for key, wasPass := range base {
		nowPass, ok := cand[key]
		if !ok {
			continue
		}
		layer := strings.SplitN(key, "/", 2)[0]
		modelLayer := layer == "extraction" || layer == "conversations"
		target := out.Deterministic
		if modelLayer {
			target = out.ModelLayers
			out.ModelPairs++
		}
		delta := target[layer]
		delta.Total++
		if wasPass {
			delta.BaselinePass++
		}
		if nowPass {
			delta.CandidatePass++
		}
		target[layer] = delta
		if !modelLayer {
			continue // 确定性层只记录增减，不进入配对与检验
		}
		switch {
		case wasPass && nowPass:
			out.StillPass++
		case !wasPass && !nowPass:
			out.StillFail++
		case wasPass && !nowPass:
			b++
			out.Regressed = append(out.Regressed, key)
		case !wasPass && nowPass:
			c++
			out.NewlyPassing = append(out.NewlyPassing, key)
		}
	}
	sort.Strings(out.Regressed)
	sort.Strings(out.NewlyPassing)
	switch {
	case out.ModelPairs < gates.MinimumPairedSamples:
		out.SampleNote = fmt.Sprintf("模型层独立样本 %d < %d：样本不足，不能宣称改善；差异只记观察。", out.ModelPairs, gates.MinimumPairedSamples)
	case b+c >= 6:
		pv := mcnemarExact(b, c)
		out.McNemarP = &pv
	default:
		out.McNemarNote = "不一致对不足 6，McNemar 无意义；差异只记观察。"
	}
	return out, nil
}

// mcnemarExact 是精确二项 McNemar（无外部依赖）。
func mcnemarExact(b, c int) float64 {
	n := b + c
	if n == 0 {
		return 1
	}
	k := b
	if c < b {
		k = c
	}
	prob := 0.0
	comb := 1.0
	for i := 0; i <= k; i++ {
		if i > 0 {
			comb = comb * float64(n-i+1) / float64(i)
		}
		prob += comb
	}
	for i := 0; i < n; i++ {
		prob /= 2
	}
	if p := prob * 2; p > 1 {
		return 1
	} else {
		return p
	}
}

// RenderReqV2Markdown 输出人读报告；只描述事实与差距，不宣称 v1 达标。
func RenderReqV2Markdown(report *ReqV2Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Requirement v2 评估报告（%s）\n\n", report.Mode)
	fmt.Fprintf(&b, "- grader: `%s`；manifest `%s`；gates `%s`\n", report.GraderVersion, report.ManifestSHA256[:12], report.GatesSHA256[:12])
	fmt.Fprintf(&b, "- splits: %s；repeats: %d；用时 %dms\n\n", strings.Join(report.Splits, ", "), report.Repeats, report.DurationMS)
	fmt.Fprintf(&b, "## 结论\n\n%s\n\n", report.Conclusion)
	if len(report.GateVerdicts) > 0 {
		b.WriteString("## 冻结门槛逐项判定\n\n| Layer | Metric | 实际 | 门槛 | 结果 |\n|---|---|---|---|---|\n")
		for _, v := range report.GateVerdicts {
			status := "PASS"
			if !v.Evaluable {
				status = "UNEVALUABLE"
			} else if !v.Passed {
				status = "FAIL"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", v.Layer, v.Metric, v.Actual, v.Threshold, status)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## 分层指标（pass^%d 口径）\n\n", report.Repeats)
	b.WriteString("| Layer | Cases | Passed | Skipped | Vetoes | v2_contract_gap | behavior_failure | provider |\n|---|---|---|---|---|---|---|---|\n")
	for _, layer := range ReqV2Layers {
		s := report.PerLayer[layer]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d |\n", layer, s.Cases, s.Passed, s.Skipped, s.Vetoes, s.Failures[reqV2ClassGap], s.Failures[reqV2ClassBehavior], s.Failures[reqV2ClassProvider])
	}
	fmt.Fprintf(&b, "\n## 用量\n\n- 模型调用 %d（错误分类 %v）；已知 token %d（全部已知：%v）；延迟 p50=%dms p95=%dms\n\n", report.Usage.ModelCalls, report.Usage.ProviderErrors, report.Usage.TokensKnown, report.Usage.TokensAllKnown, report.Usage.LatencyP50MS, report.Usage.LatencyP95MS)
	fmt.Fprintf(&b, "## 限制\n\n")
	for _, l := range report.Limitations {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	fmt.Fprintf(&b, "\n## 失败明细（按 case）\n\n")
	for _, c := range report.Cases {
		if c.Pass {
			continue
		}
		fmt.Fprintf(&b, "### %s/%s（split=%s repeat=%d）\n\n", c.Layer, c.ID, c.Split, c.Repeat)
		if c.Observation.Skipped != "" {
			fmt.Fprintf(&b, "- skipped: %s\n", c.Observation.Skipped)
		}
		if c.Observation.Error != "" {
			fmt.Fprintf(&b, "- error: %s\n", c.Observation.Error)
		}
		for _, a := range c.Assertions {
			if !a.Pass {
				veto := ""
				if a.Veto != "" {
					veto = " **" + a.Veto + "**"
				}
				detail := a.Detail
				if len(detail) > 220 {
					detail = detail[:220] + "..."
				}
				fmt.Fprintf(&b, "- `%s`%s（%s）：%s\n", a.Name, veto, a.Classification, detail)
			}
		}
		fmt.Fprintf(&b, "\n")
	}
	return b.String()
}
