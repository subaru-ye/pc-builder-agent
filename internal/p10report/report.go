// Package p10report 定义阶段 1 封板的脱敏验收报告和硬门禁。
package p10report

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const SchemaVersion = 1

type LiveReport struct {
	SchemaVersion  int         `json:"schema_version"`
	GeneratedAt    string      `json:"generated_at"`
	Models         Models      `json:"models"`
	Trials         []LiveTrial `json:"trials"`
	RouteLatencyMS []int64     `json:"route_latency_ms"`
}

type Models struct {
	Screening string `json:"screening"`
	Builder   string `json:"builder"`
	Embedding string `json:"embedding"`
}

type LiveTrial struct {
	Scenario           string          `json:"scenario"`
	Repetition         int             `json:"repetition"`
	ModelCode          string          `json:"model_code"`
	SessionFingerprint string          `json:"session_fingerprint"`
	RunFingerprints    []string        `json:"run_fingerprints"`
	FinalPhase         string          `json:"final_phase"`
	Versions           []int           `json:"versions"`
	FirstProgressMS    int64           `json:"first_progress_ms"`
	TotalMS            int64           `json:"total_ms"`
	MaxSSEGapMS        int64           `json:"max_sse_gap_ms"`
	ScreeningCalls     int             `json:"screening_calls"`
	BuilderCalls       int             `json:"builder_calls"`
	ValidationRounds   int             `json:"validation_rounds"`
	EmbeddingCache     string          `json:"embedding_cache"`
	TotalCNY           string          `json:"total_cny,omitempty"`
	BudgetDeltaCNY     string          `json:"budget_delta_cny,omitempty"`
	OverallStatus      string          `json:"overall_status,omitempty"`
	DiffSummary        map[string]any  `json:"diff_summary,omitempty"`
	Assertions         map[string]bool `json:"assertions"`
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
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("报告末尾存在多余 JSON")
	}
	return nil
}

func ValidateLive(report LiveReport) []error {
	var errs []error
	if report.SchemaVersion != SchemaVersion {
		errs = append(errs, fmt.Errorf("live schema_version=%d, want %d", report.SchemaVersion, SchemaVersion))
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
	return errs
}

func ValidateHuman(report HumanReport) []error {
	var errs []error
	if report.SchemaVersion != SchemaVersion {
		errs = append(errs, fmt.Errorf("human schema_version=%d, want %d", report.SchemaVersion, SchemaVersion))
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

func FormatErrors(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, "- "+err.Error())
	}
	return strings.Join(parts, "\n")
}
