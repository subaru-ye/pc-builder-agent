// Fastlane 实验运行与指标聚合：三路对比（原 Screening／纯规则／规则＋Jev）、
// 冻结产物落盘与阈值选择。阈值只在校准集（corpus calibration + setb-cal）
// 上选；report 集只用选定阈值报告一次。全部数字可由冻结观测重放。
package planningeval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/decision"
	"github.com/subaru-ye/pc-builder-agent/internal/providers/jev"
)

// fastlanePassesJev 判定一条观测在阈值 t 下是否给出有效 determine 快走。
// Jev 故障/超时/不确定一律回退（fail-closed）。
func fastlanePassesJev(r FastlaneTurnRecord, t float64) bool {
	return r.RuleC8Pass && r.JevError == "" &&
		r.JevVerdict == string(decision.VerdictDetermine) && r.JevProb >= t
}

// FastlaneThresholdGrid 是预注册的阈值网格：校准集上逐点评估后选择。
var FastlaneThresholdGrid = []float64{0.40, 0.50, 0.60, 0.70, 0.80, 0.90, 0.95}

// FastlaneOptions 控制一次实验运行。JevJudge 为 nil 时是零模型 audit。
type FastlaneOptions struct {
	OutDir               string
	ScreeningBaselineDir string
	JevJudge             decision.FastlaneJudge
	// JevReplayPath 非空时从该目录的 jev.jsonl 重放观测（零网络），不再
	// 调用 Jev；用于对冻结观测重算统计。
	JevReplayPath string
	JevMaxCalls          int
	JevInputPricePerMTok float64
	JevOutputPricePerMTok float64
}

// FastlaneJevObservation 是一次 Jev 判定的冻结观测（jev.jsonl 一行）。
type FastlaneJevObservation struct {
	Set       string  `json:"set"`
	CaseID    string  `json:"case_id"`
	TurnIndex int     `json:"turn_index"`
	Input     string  `json:"input"`
	CandidateField string `json:"candidate_field"`
	CandidateValue int `json:"candidate_value"`
	CacheHit  bool    `json:"cache_hit"`
	Verdict   string  `json:"verdict,omitempty"`
	ProbDetermine float64 `json:"prob_determine,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	SelectedProbability float64 `json:"selected_probability,omitempty"`
	RequestedModel string `json:"requested_model,omitempty"`
	ResponseModel string `json:"response_model,omitempty"`
	InputTokens int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	DurationMS int64 `json:"duration_ms,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
	Error string `json:"error,omitempty"`
}

// FastlaneTurnRecord 是逐轮冻结观测（turns.jsonl 一行）。
type FastlaneTurnRecord struct {
	Set        string `json:"set"`
	CaseID     string `json:"case_id"`
	TurnIndex  int    `json:"turn_index"`
	Split      string `json:"split"`
	Quote      string `json:"quote"`
	LabelFast  bool   `json:"label_fast"`
	LabelValue *int   `json:"label_value,omitempty"`
	LabelReason string `json:"label_reason"`
	RuleC8Pass bool   `json:"rule_c8_pass"`
	RuleStage  string `json:"rule_stage"`
	RuleFast   bool   `json:"rule_fast"`
	JevCalled  bool   `json:"jev_called"`
	JevVerdict string `json:"jev_verdict,omitempty"`
	JevProb    float64 `json:"jev_prob_determine,omitempty"`
	JevError   string  `json:"jev_error_class,omitempty"`
	RuleJevFast bool  `json:"rule_jev_fast"`
	FastValue  *int   `json:"fast_value,omitempty"`
	ValueMatch *bool  `json:"value_match,omitempty"`
	OpSemanticDivergence bool `json:"op_semantic_divergence,omitempty"`
	Screening  *FrozenScreeningTurn `json:"screening,omitempty"`
}

// FastlaneRouteStats 是一路路由在一个轮次集合上的汇总。
type FastlaneRouteStats struct {
	Turns                int `json:"turns"`
	FastRouted           int `json:"fast_routed"`
	FastCorrect          int `json:"fast_correct"`
	FastWrongValue       int `json:"fast_wrong_value"`
	FalseFast            int `json:"false_fast"`
	FalseFallback        int `json:"false_fallback"`
	OpSemanticDivergence int `json:"op_semantic_divergence"`
	ScreeningCallsSaved  int `json:"screening_calls_saved"`
}

// FastlaneJevStats 汇总 Jev 观测。
type FastlaneJevStats struct {
	Requested int            `json:"requested"`
	CacheHits int            `json:"cache_hits"`
	Success   int            `json:"success"`
	Failures  int            `json:"failures"`
	ErrorClasses map[string]int `json:"error_classes,omitempty"`
	Verdicts  map[string]int  `json:"verdicts,omitempty"`
	LatencyP50MS int64        `json:"latency_p50_ms,omitempty"`
	LatencyP95MS int64        `json:"latency_p95_ms,omitempty"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	CostCNY    *float64       `json:"cost_cny_estimate,omitempty"`
	CostRateNote string        `json:"cost_rate_note,omitempty"`
	Rescues    int            `json:"rescues"`
	Vetoes     int            `json:"vetoes"`
}

// FastlaneAudit 是零模型机会审计结果（audit.json）。
type FastlaneAudit struct {
	CorpusTurns        int `json:"corpus_turns"`
	CorpusLabelFast    int `json:"corpus_label_fast"`
	CorpusC1Candidates int `json:"corpus_c1_candidates"`
	CorpusC8Pass       int `json:"corpus_c8_pass"`
	CorpusRuleEligible int `json:"corpus_rule_eligible"`
	CorpusRuleFalseFast int `json:"corpus_rule_false_fast"`
	CorpusBudgetGoldTurns int `json:"corpus_budget_gold_turns"`
	SetBTurns          int `json:"setb_turns"`
	SetBLabelFast      int `json:"setb_label_fast"`
	SetBC1Candidates   int `json:"setb_c1_candidates"`
	SetBC8Pass         int `json:"setb_c8_pass"`
	SetBRuleEligible   int `json:"setb_rule_eligible"`
	SetBRuleFalseFast  int `json:"setb_rule_false_fast"`
	TheoreticalBypassRatio float64 `json:"theoretical_bypass_ratio_corpus"`
}

// FastlaneReport 是一次运行的完整报告（report.json 的骨架）。
type FastlaneReport struct {
	Mode        string                 `json:"mode"`
	GeneratedAt string                 `json:"generated_at"`
	Audit       FastlaneAudit          `json:"audit"`
	Corpus      map[string]FastlaneRouteStats `json:"corpus"`
	SetB        map[string]FastlaneRouteStats `json:"setb"`
	Threshold   *FastlaneThresholdChoice `json:"threshold,omitempty"`
	ThresholdCurve []FastlaneThresholdPoint `json:"threshold_curve,omitempty"`
	Jev         *FastlaneJevStats      `json:"jev,omitempty"`
	ScreeningBaseline *FastlaneScreeningBaselineStats `json:"screening_baseline,omitempty"`
	Predicted   *FastlanePrediction    `json:"predicted_end_to_end,omitempty"`
	Limitations []string               `json:"limitations"`
}

type FastlaneThresholdPoint struct {
	Threshold  float64 `json:"threshold"`
	FalseFastCal int   `json:"false_fast_cal"`
	FastCorrectCal int `json:"fast_correct_cal"`
	CoverageCal int    `json:"coverage_cal"`
}

type FastlaneThresholdChoice struct {
	Threshold  float64 `json:"threshold"`
	Rule       string  `json:"rule"`
	CalFalseFast int   `json:"cal_false_fast"`
	CalFastCorrect int `json:"cal_fast_correct"`
}

type FastlaneScreeningBaselineStats struct {
	Dir           string `json:"dir"`
	LabelFastTurns int   `json:"label_fast_turns"`
	ScreeningObserved int `json:"screening_observed_turns"`
	ScreeningCorrectFast int `json:"screening_budget_correct_on_label_fast"`
	LatencyP50MS  int64  `json:"latency_p50_ms,omitempty"`
	LatencyP95MS  int64  `json:"latency_p95_ms,omitempty"`
}

// FastlanePrediction 是模拟的端到端收益，全部为预测口径，不是生产实测。
type FastlanePrediction struct {
	Note string `json:"note"`
	FastCorrectTurns int `json:"fast_correct_turns"`
	SavedScreeningCalls int `json:"saved_screening_calls"`
	PerTurnSavingP50MS int64 `json:"per_turn_saving_p50_ms"`
	PerTurnSavingP95MS int64 `json:"per_turn_saving_p95_ms"`
	TotalSavingP50MS int64 `json:"total_saving_p50_ms"`
	TotalSavingP95MS int64 `json:"total_saving_p95_ms"`
}

// RunFastlane 执行实验。judge 为 nil 时是零模型 audit；否则做有界影子对比。
func RunFastlane(ctx context.Context, dataset *ReqV2Dataset, setb []FastlaneSetBCase, opts FastlaneOptions) (*FastlaneReport, error) {
	corpus, err := BuildFastlaneCorpus(dataset)
	if err != nil {
		return nil, err
	}
	turns := append(append([]FastlaneTurn{}, corpus...), BuildFastlaneSetBTurns(setb)...)
	baseline := map[string]FrozenScreeningTurn{}
	if opts.ScreeningBaselineDir != "" {
		baseline, err = LoadFrozenScreeningBaseline(filepath.Join(opts.ScreeningBaselineDir, "results.jsonl"))
		if err != nil {
			return nil, fmt.Errorf("screening baseline: %w", err)
		}
	}

	records := make([]FastlaneTurnRecord, 0, len(turns))
	var jevObs []FastlaneJevObservation
	callsUsed := 0
	cache := map[string]FastlaneJevObservation{}
	replay := map[string]FastlaneJevObservation{}
	if opts.JevReplayPath != "" {
		var err error
		replay, err = loadFastlaneJevObservations(filepath.Join(opts.JevReplayPath, "jev.jsonl"))
		if err != nil {
			return nil, fmt.Errorf("jev replay: %w", err)
		}
	}
	for _, turn := range turns {
		record := FastlaneTurnRecord{
			Set: turn.Set, CaseID: turn.CaseID, TurnIndex: turn.TurnIndex, Split: turn.Split,
			Quote: turn.Quote, LabelFast: turn.LabelFast, LabelValue: turn.LabelValue, LabelReason: turn.LabelReason,
		}
		if frozen, ok := baseline[fastlaneTurnKey(turn.CaseID, turn.TurnIndex, turn.Set)]; ok {
			screener := frozen
			record.Screening = &screener
		}
		// 纯规则路由（C1–C9）。
		rules := FastlaneRules(turn)
		record.RuleStage = rules.Stage
		record.RuleFast = rules.Eligible
		// R+J 路由的确定性守卫（C1,C2,C6,C7,C8）。
		guard := FastlaneRulesThroughC8(turn)
		record.RuleC8Pass = guard.Eligible
		fastValue, valueMatch, divergence := fastlaneLandAndCompare(turn, rules)
		record.FastValue, record.ValueMatch, record.OpSemanticDivergence = fastValue, valueMatch, divergence
		if guard.Eligible && (opts.JevJudge != nil || len(replay) > 0) {
			candidate := guard.Value
			cacheKey := turn.Quote + "\x00" + fmt.Sprint(candidate)
			obs := FastlaneJevObservation{
				Set: turn.Set, CaseID: turn.CaseID, TurnIndex: turn.TurnIndex, Input: turn.Quote,
				CandidateField: decision.FastlaneField, CandidateValue: candidate,
			}
			if cached, ok := replay[replayKey(turn, candidate)]; ok {
				obs = cached
			} else if cached, ok := cache[cacheKey]; ok {
				obs.CacheHit = true
				obs.Verdict, obs.ProbDetermine, obs.ErrorClass = cached.Verdict, cached.ProbDetermine, cached.ErrorClass
			} else if opts.JevJudge == nil {
				return nil, fmt.Errorf("fastlane: 重放缺少轮次 %s#%d 的 Jev 观测", turn.CaseID, turn.TurnIndex)
			} else {
				if callsUsed >= opts.JevMaxCalls {
					return nil, fmt.Errorf("fastlane: Jev 调用上限 %d 已耗尽（轮次 %s#%d），停止运行", opts.JevMaxCalls, turn.CaseID, turn.TurnIndex)
				}
				callsUsed++
				value := json.RawMessage(fmt.Sprintf("%d", candidate))
				result, err := opts.JevJudge.Judge(ctx, decision.FastlaneInput{
					CurrentTurn: turn.Quote, CandidateField: decision.FastlaneField, CandidateValue: value,
				})
				obs.RequestedModel = result.RequestedModel
				if err != nil {
					obs.ErrorClass = jev.Class(err)
					obs.Error = err.Error()
				} else {
					obs.Verdict = string(result.Verdict)
					obs.ProbDetermine = result.Probabilities[decision.VerdictDetermine]
					obs.Confidence = result.Confidence
					obs.SelectedProbability = result.SelectedProbability
					obs.ResponseModel = result.ResponseModel
					obs.InputTokens = result.InputTokens
					obs.OutputTokens = result.OutputTokens
					obs.DurationMS = result.Duration.Milliseconds()
				}
				cache[cacheKey] = obs
			}
			jevObs = append(jevObs, obs)
			record.JevCalled = true
			record.JevVerdict = obs.Verdict
			record.JevProb = obs.ProbDetermine
			record.JevError = obs.ErrorClass
		}
		records = append(records, record)
	}
	// R+J 快走判定依赖选定阈值；阈值先在校准集上选，再应用到全部轮次。
	report := fastlaneAssembleReport(opts, len(replay) > 0, turns, records, jevObs, baseline)
	// 落盘。
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return nil, err
	}
	if err := writeFastlaneJSONL(filepath.Join(opts.OutDir, "turns.jsonl"), records); err != nil {
		return nil, err
	}
	if len(jevObs) > 0 {
		if err := writeFastlaneJSONL(filepath.Join(opts.OutDir, "jev.jsonl"), jevObs); err != nil {
			return nil, err
		}
	}
	return report, writeFastlaneJSON(filepath.Join(opts.OutDir, "report.json"), report)
}

func fastlaneTurnKey(caseID string, turnIndex int, set string) string {
	if set == "corpus" {
		return fmt.Sprintf("%s#%d", caseID, turnIndex)
	}
	return "" // Set B 是独立标注集，没有 Screening 基线。
}

// fastlaneLandAndCompare 对纯规则快走做真实 Reducer 落地并与金标比较。
// 返回 (快走值, 值匹配, 操作语义分歧)。
func fastlaneLandAndCompare(turn FastlaneTurn, rules FastlaneRuleResult) (*int, *bool, bool) {
	if !rules.Eligible {
		return nil, nil, false
	}
	next, err := FastlaneApplyOp(turn.PriorState, turn.Quote, rules.Value)
	if err != nil {
		value := rules.Value
		match := false
		return &value, &match, false
	}
	value := rules.Value
	match := turn.LabelFast && turn.LabelValue != nil && *turn.LabelValue == value
	// 操作语义分歧：金标不是 set（如 restore/conflict）却被判为可 set。
	divergence := false
	if !turn.LabelFast && len(turn.GoldOps) == 1 && turn.GoldOps[0].Field == decision.FastlaneField && turn.GoldOps[0].Op != "set" {
		divergence = true
	}
	_ = next
	return &value, &match, divergence
}

func isCalibration(t FastlaneTurn) bool {
	return t.Split == "calibration" || t.Split == "setb-cal"
}

// fastlaneAssembleReport 计算三路统计。R+J 阈值：先在全部阈值点计算校准集
// 表现，选择第一个 false_fast==0 且 fast_correct>0 的点；若无则选第一个
// false_fast==0 的点；并在报告里给出整条曲线。
func fastlaneAssembleReport(opts FastlaneOptions, replayMode bool, turns []FastlaneTurn, records []FastlaneTurnRecord, jevObs []FastlaneJevObservation, baseline map[string]FrozenScreeningTurn) *FastlaneReport {
	report := &FastlaneReport{
		Mode: "audit", GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Corpus: map[string]FastlaneRouteStats{}, SetB: map[string]FastlaneRouteStats{},
		Limitations: []string{
			"语料（requirement-v2 development+calibration）全部用于过开发，不能称为真实流量或盲测；Set B 为 AI 起草、待人工确认的独立标注。",
			"端到端收益全部为预测口径：按冻结 Screening 延迟与观测到的 Jev 延迟推算，不是生产实测。",
			"先验状态由金标操作回放重建，与真实会话可能存在偏差。",
		},
	}
	switch {
	case opts.JevJudge != nil:
		report.Mode = "shadow-live"
	case replayMode:
		report.Mode = "shadow-replay"
	}
	// 审计计数。
	for i, turn := range turns {
		rules := FastlaneRules(turn)
		guard := FastlaneRulesThroughC8(turn)
		if turn.Set == "corpus" {
			report.Audit.CorpusTurns++
			if turn.LabelFast {
				report.Audit.CorpusLabelFast++
			}
			if len(rules.Candidates) > 0 {
				report.Audit.CorpusC1Candidates++
			}
			if guard.Eligible {
				report.Audit.CorpusC8Pass++
			}
			if rules.Eligible {
				report.Audit.CorpusRuleEligible++
				if !turn.LabelFast {
					report.Audit.CorpusRuleFalseFast++
				}
			}
			if len(turn.GoldOps) > 0 && fastlaneGoldTouchesBudget(turn.GoldOps) {
				report.Audit.CorpusBudgetGoldTurns++
			}
		} else {
			report.Audit.SetBTurns++
			if turn.LabelFast {
				report.Audit.SetBLabelFast++
			}
			if len(rules.Candidates) > 0 {
				report.Audit.SetBC1Candidates++
			}
			if guard.Eligible {
				report.Audit.SetBC8Pass++
			}
			if rules.Eligible {
				report.Audit.SetBRuleEligible++
				if !turn.LabelFast {
					report.Audit.SetBRuleFalseFast++
				}
			}
		}
		_ = i
	}
	if report.Audit.CorpusTurns > 0 {
		report.Audit.TheoreticalBypassRatio = float64(report.Audit.CorpusLabelFast) / float64(report.Audit.CorpusTurns)
	}
	// 纯规则路由统计。
	report.Corpus["rules"] = fastlaneRouteStats("corpus", turns, records, func(r FastlaneTurnRecord) bool { return r.RuleFast })
	report.SetB["rules"] = fastlaneRouteStats("setb", turns, records, func(r FastlaneTurnRecord) bool { return r.RuleFast })
	// Jev 统计与阈值选择。
	if opts.JevJudge != nil || replayMode {
		stats := fastlaneJevStats(jevObs, opts, records)
		report.Jev = &stats
		var curve []FastlaneThresholdPoint
		chosen := -1
		for _, t := range FastlaneThresholdGrid {
			point := FastlaneThresholdPoint{Threshold: t}
			point.FastCorrectCal, point.FalseFastCal, point.CoverageCal = fastlaneJevRouteOn(t, turns, records, isCalibration)
			curve = append(curve, point)
			if chosen < 0 && point.FalseFastCal == 0 && point.FastCorrectCal > 0 {
				chosen = len(curve) - 1
			}
		}
		// 没有任何"零误快且有正覆盖"的点时，选 false_fast 最小的点（并列取
		// 最高阈值，即最保守的点），并把规则如实记为降级选择。
		if chosen < 0 {
			chosen = 0
			for i, point := range curve {
				if point.FalseFastCal < curve[chosen].FalseFastCal ||
					(point.FalseFastCal == curve[chosen].FalseFastCal && point.Threshold > curve[chosen].Threshold) {
					chosen = i
				}
			}
		}
		threshold := curve[chosen].Threshold
		report.ThresholdCurve = curve
		rule := "first_false_fast_free_threshold_with_positive_cal_coverage"
		if curve[chosen].FastCorrectCal == 0 {
			rule = "no_cal_fast_correct_at_any_threshold; using most conservative point"
		}
		report.Threshold = &FastlaneThresholdChoice{
			Threshold: threshold, Rule: rule,
			CalFalseFast: curve[chosen].FalseFastCal, CalFastCorrect: curve[chosen].FastCorrectCal,
		}
		t := threshold
		report.Corpus["rules+jev"] = fastlaneRouteStats("corpus", turns, records, func(r FastlaneTurnRecord) bool {
			return fastlanePassesJev(r, t)
		})
		report.SetB["rules+jev"] = fastlaneRouteStats("setb", turns, records, func(r FastlaneTurnRecord) bool {
			return fastlanePassesJev(r, t)
		})
	}
	// Screening 基线与预测。
	if opts.ScreeningBaselineDir != "" {
		stats := FastlaneScreeningBaselineStats{Dir: opts.ScreeningBaselineDir}
		for _, turn := range turns {
			if turn.Set != "corpus" || !turn.LabelFast {
				continue
			}
			stats.LabelFastTurns++
			if frozen, ok := baseline[fastlaneTurnKey(turn.CaseID, turn.TurnIndex, turn.Set)]; ok {
				stats.ScreeningObserved++
				for _, repeat := range frozen.Repeats {
					if len(repeat.BudgetWrites) == 1 && repeat.BudgetWrites[0].Op == "set" && repeat.BudgetWrites[0].Value != nil && turn.LabelValue != nil {
						var v int
						if err := json.Unmarshal(repeat.BudgetWrites[0].Value, &v); err == nil && v == *turn.LabelValue {
							stats.ScreeningCorrectFast++
							break
						}
					}
				}
			}
		}
		if raw, err := os.ReadFile(filepath.Join(opts.ScreeningBaselineDir, "report.json")); err == nil {
			var screeningReport struct {
				Usage struct {
					LatencyP50MS int64 `json:"latency_p50_ms"`
					LatencyP95MS int64 `json:"latency_p95_ms"`
				} `json:"usage"`
			}
			if json.Unmarshal(raw, &screeningReport) == nil {
				stats.LatencyP50MS = screeningReport.Usage.LatencyP50MS
				stats.LatencyP95MS = screeningReport.Usage.LatencyP95MS
			}
		}
		report.ScreeningBaseline = &stats
		route := report.Corpus["rules"]
		if report.Jev != nil {
			if rj, ok := report.Corpus["rules+jev"]; ok && rj.FastCorrect > route.FastCorrect {
				route = rj
			}
		}
		if report.ScreeningBaseline.LatencyP50MS > 0 {
			savingP50 := report.ScreeningBaseline.LatencyP50MS
			savingP95 := report.ScreeningBaseline.LatencyP95MS
			if report.Jev != nil && report.Jev.LatencyP50MS > 0 {
				savingP50 -= report.Jev.LatencyP50MS
				if report.Jev.LatencyP95MS > 0 {
					savingP95 -= report.Jev.LatencyP95MS
				}
			}
			report.Predicted = &FastlanePrediction{
				Note:            "预测：按冻结 Screening 延迟与观测 Jev 延迟推算的每轮节省，不是生产实测；覆盖率以语料为界。",
				FastCorrectTurns: route.FastCorrect,
				SavedScreeningCalls: route.ScreeningCallsSaved,
				PerTurnSavingP50MS: savingP50,
				PerTurnSavingP95MS: savingP95,
				TotalSavingP50MS: savingP50 * int64(route.ScreeningCallsSaved),
				TotalSavingP95MS: savingP95 * int64(route.ScreeningCallsSaved),
			}
		}
	}
	return report
}

func fastlaneGoldTouchesBudget(gold []ReqV2OpGold) bool {
	for _, op := range gold {
		if op.Field == decision.FastlaneField || strings.HasPrefix(op.Field, "budget") {
			return true
		}
	}
	return false
}

// fastlaneRouteStats 汇总某一路由在一个集合（"corpus" | "setb"）上的表现。
func fastlaneRouteStats(set string, turns []FastlaneTurn, records []FastlaneTurnRecord, fast func(FastlaneTurnRecord) bool) FastlaneRouteStats {
	stats := FastlaneRouteStats{}
	for i, record := range records {
		if i >= len(turns) {
			break
		}
		turn := turns[i]
		if record.Set != set || turn.Set != set {
			continue
		}
		if !fast(record) {
			if turn.LabelFast {
				stats.FalseFallback++
			}
			stats.Turns++
			continue
		}
		stats.Turns++
		stats.FastRouted++
		switch {
		case turn.LabelFast && record.ValueMatch != nil && *record.ValueMatch:
			stats.FastCorrect++
			stats.ScreeningCallsSaved++
		case turn.LabelFast:
			// 值错误是关键字段错写，不是回退；只计 wrong_value。
			stats.FastWrongValue++
		default:
			stats.FalseFast++
		}
		if record.OpSemanticDivergence {
			stats.OpSemanticDivergence++
		}
	}
	return stats
}

func fastlaneJevRouteOn(threshold float64, turns []FastlaneTurn, records []FastlaneTurnRecord, filter func(FastlaneTurn) bool) (fastCorrect, falseFast, coverage int) {
	for i, record := range records {
		if i >= len(turns) {
			break
		}
		turn := turns[i]
		if !filter(turn) {
			continue
		}
		if !fastlanePassesJev(record, threshold) {
			continue
		}
		if turn.LabelFast {
			if record.ValueMatch != nil && *record.ValueMatch {
				fastCorrect++
			}
			coverage++
		} else {
			falseFast++
		}
	}
	return fastCorrect, falseFast, coverage
}

func fastlaneJevStats(jevObs []FastlaneJevObservation, opts FastlaneOptions, records []FastlaneTurnRecord) FastlaneJevStats {
	stats := FastlaneJevStats{Requested: len(jevObs), ErrorClasses: map[string]int{}, Verdicts: map[string]int{}}
	var durations []int64
	rulesFast := map[string]bool{}
	for _, record := range records {
		rulesFast[record.Set+"\x00"+record.CaseID+"\x00"+fmt.Sprint(record.TurnIndex)] = record.RuleFast
	}
	for _, obs := range jevObs {
		if obs.CacheHit {
			stats.CacheHits++
		}
		if obs.ErrorClass != "" {
			stats.Failures++
			stats.ErrorClasses[obs.ErrorClass]++
			continue
		}
		stats.Success++
		stats.Verdicts[obs.Verdict]++
		if obs.DurationMS > 0 {
			durations = append(durations, obs.DurationMS)
		}
		stats.InputTokens += obs.InputTokens
		stats.OutputTokens += obs.OutputTokens
		key := fmt.Sprint(obs.Set, "\x00", obs.CaseID, "\x00", obs.TurnIndex)
		verdict := obs.Verdict == string(decision.VerdictDetermine)
		if wasFast, ok := rulesFast[key]; ok {
			if wasFast && !verdict {
				stats.Vetoes++
			}
			if !wasFast && verdict {
				stats.Rescues++
			}
		}
	}
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		stats.LatencyP50MS = durations[len(durations)*50/100]
		stats.LatencyP95MS = durations[len(durations)*95/100]
	}
	if opts.JevInputPricePerMTok > 0 || opts.JevOutputPricePerMTok > 0 {
		cost := float64(stats.InputTokens)/1e6*opts.JevInputPricePerMTok + float64(stats.OutputTokens)/1e6*opts.JevOutputPricePerMTok
		stats.CostCNY = &cost
		stats.CostRateNote = "按显式传入的费率折现；费率未确认时不得引用为实际支出"
	} else {
		stats.CostRateNote = "未提供确认费率，未折现"
	}
	return stats
}

// replayKey 是重放观测的对齐键。
func replayKey(turn FastlaneTurn, candidate int) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", turn.Set, turn.CaseID, turn.TurnIndex, candidate)
}

func loadFastlaneJevObservations(path string) (map[string]FastlaneJevObservation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]FastlaneJevObservation{}
	dec := json.NewDecoder(f)
	for {
		var obs FastlaneJevObservation
		if err := dec.Decode(&obs); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		out[fmt.Sprintf("%s\x00%s\x00%d\x00%d", obs.Set, obs.CaseID, obs.TurnIndex, obs.CandidateValue)] = obs
	}
	return out, nil
}

func writeFastlaneJSONL(path string, rows any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	switch rows := rows.(type) {
	case []FastlaneTurnRecord:
		for _, row := range rows {
			if err := enc.Encode(row); err != nil {
				return err
			}
		}
	case []FastlaneJevObservation:
		for _, row := range rows {
			if err := enc.Encode(row); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("fastlane: 未支持的 jsonl 类型 %T", rows)
	}
	return nil
}

func writeFastlaneJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
