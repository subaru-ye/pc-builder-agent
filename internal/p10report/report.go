// Package p10report 定义产品发布验收的脱敏报告和硬门禁。
package p10report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const (
	LegacyLiveSchemaVersion = 1
	LiveSchemaVersion       = 2
	HumanSchemaVersion      = 1

	HistoricalTotalTokens = int64(1_006_062)
	MaxV2TotalTokens      = int64(704_243)
	MaxV2BuilderCalls     = 45
	MaxV2MedianMS         = float64(90_000)
)

type LiveReport struct {
	SchemaVersion  int         `json:"schema_version"`
	GeneratedAt    string      `json:"generated_at"`
	HarnessMode    string      `json:"harness_mode,omitempty"`
	Models         Models      `json:"models"`
	Trials         []LiveTrial `json:"trials"`
	RouteLatencyMS []int64     `json:"route_latency_ms"`
	Summary        LiveSummary `json:"summary,omitempty"`
}

type Models struct {
	Screening string `json:"screening"`
	Builder   string `json:"builder"`
	Embedding string `json:"embedding"`
}

type LiveTrial struct {
	Scenario            string          `json:"scenario"`
	Repetition          int             `json:"repetition"`
	ModelCode           string          `json:"model_code"`
	SessionFingerprint  string          `json:"session_fingerprint"`
	RunFingerprints     []string        `json:"run_fingerprints"`
	FinalPhase          string          `json:"final_phase"`
	Versions            []int           `json:"versions"`
	FirstProgressMS     int64           `json:"first_progress_ms"`
	TotalMS             int64           `json:"total_ms"`
	MaxSSEGapMS         int64           `json:"max_sse_gap_ms"`
	ScreeningCalls      int             `json:"screening_calls"`
	BuilderCalls        int             `json:"builder_calls"`
	ValidationRounds    int             `json:"validation_rounds"`
	EmbeddingCache      string          `json:"embedding_cache"`
	HarnessMode         string          `json:"harness_mode,omitempty"`
	HarnessRuns         int             `json:"harness_runs,omitempty"`
	Usage               TokenUsage      `json:"usage,omitempty"`
	ModelCallDurationMS int64           `json:"model_call_duration_ms,omitempty"`
	ModelErrorClasses   []string        `json:"model_error_classes,omitempty"`
	TotalCNY            string          `json:"total_cny,omitempty"`
	BudgetDeltaCNY      string          `json:"budget_delta_cny,omitempty"`
	OverallStatus       string          `json:"overall_status,omitempty"`
	DiffSummary         map[string]any  `json:"diff_summary,omitempty"`
	Assertions          map[string]bool `json:"assertions"`
}

type TokenUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CandidateTokens  int64 `json:"candidate_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
	ToolPromptTokens int64 `json:"tool_prompt_tokens"`
}

type LiveSummary struct {
	TrialCount            int        `json:"trial_count"`
	ScreeningCalls        int        `json:"screening_calls"`
	BuilderCalls          int        `json:"builder_calls"`
	ValidationRounds      int        `json:"validation_rounds"`
	Usage                 TokenUsage `json:"usage"`
	HistoricalTotalTokens int64      `json:"historical_total_tokens"`
	TokenReductionPercent float64    `json:"token_reduction_percent"`
	ModelCallDurationMS   int64      `json:"model_call_duration_ms"`
	TrialTotalMedianMS    float64    `json:"trial_total_median_ms"`
	TrialTotalMaxMS       int64      `json:"trial_total_max_ms"`
	NonModelRouteP95MS    int64      `json:"non_model_route_p95_ms"`
}

type HumanReport struct {
	SchemaVersion int           `json:"schema_version"`
	ReviewedAt    string        `json:"reviewed_at"`
	Reviews       []HumanReview `json:"reviews"`
}

type HumanReview struct {
	ReviewerID string      `json:"reviewer_id"`
	Profile    string      `json:"profile"`
	Scores     HumanScores `json:"scores"`
	Assisted   bool        `json:"assisted"`
	Blockers   []string    `json:"blockers"`
	Notes      string      `json:"notes"`
}

type HumanScores struct {
	RequirementFit int `json:"requirement_fit"`
	Completeness   int `json:"completeness"`
	Trust          int `json:"trust"`
	ChangeControl  int `json:"change_control"`
	DirectBuy      int `json:"direct_buy"`
	ShareClarity   int `json:"share_clarity"`
}

var fingerprintRE = regexp.MustCompile(`^[0-9a-f]{12}$`)

const maxReportBytes = 10 << 20

var (
	forbiddenReportKeys = map[string]struct{}{
		"api_key": {}, "apikey": {}, "authorization": {}, "cookie": {}, "owner_id": {},
		"session_id": {}, "run_id": {}, "share_token": {}, "token": {}, "prompt": {},
		"model_response": {}, "request_body": {}, "response_body": {},
	}
	secretValueRE = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{10,}|bearer\s+[A-Za-z0-9._~-]{10,}|pcb_anonymous_id=)`)
	rawUUIDRE     = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	opaqueTokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

func DecodeLive(r io.Reader) (LiveReport, error) {
	var report LiveReport
	if err := decodeStrict(r, &report); err != nil {
		return LiveReport{}, err
	}
	return report, nil
}

func DecodeHuman(r io.Reader) (HumanReport, error) {
	var report HumanReport
	if err := decodeStrict(r, &report); err != nil {
		return HumanReport{}, err
	}
	return report, nil
}

func decodeStrict(r io.Reader, dst any) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxReportBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxReportBytes {
		return fmt.Errorf("报告超过 %d 字节上限", maxReportBytes)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return err
	}
	if path, found := sensitiveReportValue(generic, "$"); found {
		return fmt.Errorf("报告包含禁止的敏感字段或原始标识: %s", path)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("报告末尾存在多余 JSON")
	}
	return nil
}

func sensitiveReportValue(value any, path string) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if _, forbidden := forbiddenReportKeys[normalized]; forbidden {
				return path + "." + key, true
			}
			if childPath, found := sensitiveReportValue(child, path+"."+key); found {
				return childPath, true
			}
		}
	case []any:
		for index, child := range typed {
			if childPath, found := sensitiveReportValue(child, fmt.Sprintf("%s[%d]", path, index)); found {
				return childPath, true
			}
		}
	case string:
		if secretValueRE.MatchString(typed) || rawUUIDRE.MatchString(typed) || opaqueTokenRE.MatchString(typed) {
			return path, true
		}
	}
	return "", false
}

func ValidateLive(report LiveReport) []error {
	var errs []error
	if report.SchemaVersion != LiveSchemaVersion {
		if report.SchemaVersion == LegacyLiveSchemaVersion {
			errs = append(errs, fmt.Errorf("live schema_version=1 仅可读取为历史证据,不能满足 Harness v2 封口;want %d", LiveSchemaVersion))
		} else {
			errs = append(errs, fmt.Errorf("live schema_version=%d, want %d", report.SchemaVersion, LiveSchemaVersion))
		}
	}
	if report.Models.Screening == "" || report.Models.Builder == "" || report.Models.Embedding == "" {
		errs = append(errs, fmt.Errorf("三类模型 Code 必须记录完整"))
	}
	want := make(map[string]bool, 18)
	for scenario := 1; scenario <= 6; scenario++ {
		for repetition := 1; repetition <= 3; repetition++ {
			want[fmt.Sprintf("L%d/%d", scenario, repetition)] = false
		}
	}
	for _, trial := range report.Trials {
		key := fmt.Sprintf("%s/%d", trial.Scenario, trial.Repetition)
		seen, exists := want[key]
		if !exists {
			errs = append(errs, fmt.Errorf("未知或非法轮次 %s", key))
			continue
		}
		if seen {
			errs = append(errs, fmt.Errorf("重复轮次 %s", key))
		}
		want[key] = true
		if trial.ModelCode == "" {
			errs = append(errs, fmt.Errorf("%s 缺少模型 Code", key))
		}
		if !fingerprintRE.MatchString(trial.SessionFingerprint) {
			errs = append(errs, fmt.Errorf("%s session 指纹非法", key))
		}
		for _, fp := range trial.RunFingerprints {
			if !fingerprintRE.MatchString(fp) {
				errs = append(errs, fmt.Errorf("%s run 指纹非法", key))
			}
		}
		if trial.FirstProgressMS < 0 || trial.FirstProgressMS > 1000 {
			errs = append(errs, fmt.Errorf("%s 首个进度 %dms 超过 1s", key, trial.FirstProgressMS))
		}
		if trial.TotalMS <= 0 || trial.TotalMS > 600_000 {
			errs = append(errs, fmt.Errorf("%s 总耗时 %dms 非法或超过 10min", key, trial.TotalMS))
		}
		if trial.MaxSSEGapMS < 0 || trial.MaxSSEGapMS > 30_000 {
			errs = append(errs, fmt.Errorf("%s SSE 最大空窗 %dms 超过 30s", key, trial.MaxSSEGapMS))
		}
		if len(trial.Assertions) == 0 {
			errs = append(errs, fmt.Errorf("%s 没有断言", key))
		}
		for name, ok := range trial.Assertions {
			if !ok {
				errs = append(errs, fmt.Errorf("%s 断言失败: %s", key, name))
			}
		}
		if trial.Scenario == "L5" {
			if trial.BuilderCalls != 0 || trial.ValidationRounds != 0 || len(trial.Versions) != 0 || trial.FinalPhase != "collecting" {
				errs = append(errs, fmt.Errorf("%s 必须只追问,不得进入 buildsvc", key))
			}
		} else if trial.FinalPhase != "ready" || len(trial.Versions) == 0 {
			errs = append(errs, fmt.Errorf("%s 未交付 ready 配置", key))
		}
		if report.SchemaVersion == LiveSchemaVersion {
			errs = append(errs, validateV2Trial(key, trial)...)
		}
	}
	for key, seen := range want {
		if !seen {
			errs = append(errs, fmt.Errorf("缺少轮次 %s", key))
		}
	}
	if len(report.RouteLatencyMS) == 0 {
		errs = append(errs, fmt.Errorf("缺少非模型 API 耗时样本"))
	} else if p95(report.RouteLatencyMS) > 500 {
		errs = append(errs, fmt.Errorf("非模型 API p95=%dms 超过 500ms", p95(report.RouteLatencyMS)))
	}
	if report.SchemaVersion == LiveSchemaVersion {
		errs = append(errs, validateV2Summary(report)...)
	}
	return errs
}

func validateV2Trial(key string, trial LiveTrial) []error {
	var errs []error
	if trial.Usage.TotalTokens <= 0 {
		errs = append(errs, fmt.Errorf("%s 缺少有效模型 Token 指标", key))
	}
	if trial.ModelCallDurationMS <= 0 {
		errs = append(errs, fmt.Errorf("%s 缺少有效模型调用耗时", key))
	}
	if len(trial.ModelErrorClasses) > 0 {
		errs = append(errs, fmt.Errorf("%s 存在上游错误分类: %s", key, strings.Join(trial.ModelErrorClasses, ",")))
	}
	if trial.Scenario == "L5" {
		if trial.HarnessMode != "not_used" || trial.HarnessRuns != 0 {
			errs = append(errs, fmt.Errorf("%s 不得启动构建 Harness", key))
		}
		return errs
	}
	if trial.HarnessMode != "v2" || trial.HarnessRuns != 1 {
		errs = append(errs, fmt.Errorf("%s 必须且只能运行一次 Harness v2,mode=%q runs=%d", key, trial.HarnessMode, trial.HarnessRuns))
	}
	if trial.BuilderCalls < 1 || trial.BuilderCalls > 3 {
		errs = append(errs, fmt.Errorf("%s builder 调用=%d,必须在 1..3", key, trial.BuilderCalls))
	}
	if trial.ValidationRounds < 1 || trial.ValidationRounds > trial.BuilderCalls {
		errs = append(errs, fmt.Errorf("%s 确定性校验轮数=%d 与 builder 调用=%d 不一致", key, trial.ValidationRounds, trial.BuilderCalls))
	}
	return errs
}

func validateV2Summary(report LiveReport) []error {
	var errs []error
	if report.HarnessMode != "v2" {
		errs = append(errs, fmt.Errorf("完整矩阵 harness_mode=%q,want v2", report.HarnessMode))
	}
	want := SummarizeLive(report)
	if report.Summary != want {
		errs = append(errs, fmt.Errorf("live 汇总与逐轮指标不一致: got %+v, want %+v", report.Summary, want))
	}
	if want.HistoricalTotalTokens != HistoricalTotalTokens {
		errs = append(errs, fmt.Errorf("历史 Token 基线=%d,want %d", want.HistoricalTotalTokens, HistoricalTotalTokens))
	}
	if want.Usage.TotalTokens > MaxV2TotalTokens {
		errs = append(errs, fmt.Errorf("矩阵 total_tokens=%d 超过 30%% 降幅上限 %d", want.Usage.TotalTokens, MaxV2TotalTokens))
	}
	if want.BuilderCalls > MaxV2BuilderCalls {
		errs = append(errs, fmt.Errorf("矩阵 builder 调用=%d 超过 %d", want.BuilderCalls, MaxV2BuilderCalls))
	}
	if want.TrialTotalMedianMS > MaxV2MedianMS {
		errs = append(errs, fmt.Errorf("矩阵耗时中位数=%.1fms 超过 %.0fms", want.TrialTotalMedianMS, MaxV2MedianMS))
	}
	return errs
}

// SummarizeLive 从逐轮脱敏指标确定性计算矩阵汇总,避免报告生成端自行声明通过。
func SummarizeLive(report LiveReport) LiveSummary {
	summary := LiveSummary{TrialCount: len(report.Trials), HistoricalTotalTokens: HistoricalTotalTokens}
	durations := make([]int64, 0, len(report.Trials))
	for _, trial := range report.Trials {
		summary.ScreeningCalls += trial.ScreeningCalls
		summary.BuilderCalls += trial.BuilderCalls
		summary.ValidationRounds += trial.ValidationRounds
		summary.Usage.add(trial.Usage)
		summary.ModelCallDurationMS += trial.ModelCallDurationMS
		durations = append(durations, trial.TotalMS)
		if trial.TotalMS > summary.TrialTotalMaxMS {
			summary.TrialTotalMaxMS = trial.TotalMS
		}
	}
	summary.TrialTotalMedianMS = median(durations)
	if len(report.RouteLatencyMS) > 0 {
		summary.NonModelRouteP95MS = p95(report.RouteLatencyMS)
	}
	summary.TokenReductionPercent = math.Round((1-float64(summary.Usage.TotalTokens)/float64(HistoricalTotalTokens))*10_000) / 100
	return summary
}

func (u *TokenUsage) add(other TokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CandidateTokens += other.CandidateTokens
	u.TotalTokens += other.TotalTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.CachedTokens += other.CachedTokens
	u.ToolPromptTokens += other.ToolPromptTokens
}

func ValidateHuman(report HumanReport) []error {
	var errs []error
	if report.SchemaVersion != HumanSchemaVersion {
		errs = append(errs, fmt.Errorf("human schema_version=%d, want %d", report.SchemaVersion, HumanSchemaVersion))
	}
	if len(report.Reviews) != 3 {
		errs = append(errs, fmt.Errorf("真人样本=%d, want 3", len(report.Reviews)))
	}
	wantProfiles := []string{"novice", "hardware_familiar", "builder_experienced"}
	seenIDs := map[string]bool{}
	seenProfiles := map[string]bool{}
	for _, review := range report.Reviews {
		if review.ReviewerID != "R1" && review.ReviewerID != "R2" && review.ReviewerID != "R3" {
			errs = append(errs, fmt.Errorf("非法 reviewer_id=%q", review.ReviewerID))
		}
		if seenIDs[review.ReviewerID] {
			errs = append(errs, fmt.Errorf("重复 reviewer_id=%q", review.ReviewerID))
		}
		seenIDs[review.ReviewerID] = true
		if !slices.Contains(wantProfiles, review.Profile) {
			errs = append(errs, fmt.Errorf("%s profile=%q 非法", review.ReviewerID, review.Profile))
		}
		seenProfiles[review.Profile] = true
		values := []int{review.Scores.RequirementFit, review.Scores.Completeness, review.Scores.Trust,
			review.Scores.ChangeControl, review.Scores.DirectBuy, review.Scores.ShareClarity}
		for _, value := range values {
			if value < 0 || value > 2 {
				errs = append(errs, fmt.Errorf("%s 评分 %d 超出 0..2", review.ReviewerID, value))
			}
		}
		if review.Scores.DirectBuy != 2 {
			errs = append(errs, fmt.Errorf("%s 可直接照买=%d;3 人样本需要 3/3 得 2", review.ReviewerID, review.Scores.DirectBuy))
		}
	}
	for _, profile := range wantProfiles {
		if !seenProfiles[profile] {
			errs = append(errs, fmt.Errorf("缺少受试者类型 %s", profile))
		}
	}
	return errs
}

func p95(values []int64) int64 {
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	index := int(math.Ceil(float64(len(copyValues))*0.95)) - 1
	if index < 0 {
		index = 0
	}
	return copyValues[index]
}

func median(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return float64(copyValues[middle])
	}
	return float64(copyValues[middle-1]+copyValues[middle]) / 2
}

func FormatErrors(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, "- "+err.Error())
	}
	return strings.Join(parts, "\n")
}
