package p10report

import (
	"strings"
	"testing"
)

func TestValidateHumanRequiresThreeDirectBuyScores(t *testing.T) {
	report := HumanReport{SchemaVersion: 1, Reviews: []HumanReview{
		{ReviewerID: "R1", Profile: "novice", Scores: fullScores()},
		{ReviewerID: "R2", Profile: "hardware_familiar", Scores: fullScores()},
		{ReviewerID: "R3", Profile: "builder_experienced", Scores: fullScores()},
	}}
	if errs := ValidateHuman(report); len(errs) != 0 {
		t.Fatalf("合格真人报告被拒绝: %s", FormatErrors(errs))
	}
	report.Reviews[2].Scores.DirectBuy = 1
	if errs := ValidateHuman(report); len(errs) == 0 {
		t.Fatal("3 人中任一人未给 2 分必须失败")
	}
}

func fullScores() HumanScores {
	return HumanScores{RequirementFit: 2, Completeness: 2, Trust: 2, ChangeControl: 2, DirectBuy: 2, ShareClarity: 2}
}

func TestValidateLiveRequiresPassCubed(t *testing.T) {
	report := validV2LiveReport()
	if errs := ValidateLive(report); len(errs) != 0 {
		t.Fatalf("合格 live 报告被拒绝: %s", FormatErrors(errs))
	}
	report.Trials = report.Trials[:17]
	report.Summary = SummarizeLive(report)
	if errs := ValidateLive(report); len(errs) == 0 {
		t.Fatal("缺一次重复必须失败")
	}
}

func validV2LiveReport() LiveReport {
	report := LiveReport{SchemaVersion: LiveSchemaVersion, HarnessMode: "v2",
		Models: Models{Screening: "s", Builder: "b", Embedding: "e"}, RouteLatencyMS: []int64{10}}
	for scenario := 1; scenario <= 6; scenario++ {
		for repetition := 1; repetition <= 3; repetition++ {
			trial := LiveTrial{Scenario: string(rune('L')), Repetition: repetition, ModelCode: "model"}
			trial.Scenario += string(rune('0' + scenario))
			trial.SessionFingerprint = "0123456789ab"
			trial.RunFingerprints = []string{"abcdef012345"}
			trial.FinalPhase = "ready"
			trial.Versions = []int{1}
			trial.FirstProgressMS = 10
			trial.TotalMS = 10_000
			trial.MaxSSEGapMS = 20
			trial.ScreeningCalls = 1
			trial.BuilderCalls = 1
			trial.ValidationRounds = 1
			trial.HarnessMode = "v2"
			trial.HarnessRuns = 1
			trial.Usage = TokenUsage{PromptTokens: 90, CandidateTokens: 10, TotalTokens: 100}
			trial.ModelCallDurationMS = 50
			trial.Assertions = map[string]bool{"ok": true}
			if scenario == 5 {
				trial.FinalPhase = "collecting"
				trial.Versions = nil
				trial.BuilderCalls = 0
				trial.ValidationRounds = 0
				trial.HarnessMode = "not_used"
				trial.HarnessRuns = 0
			}
			report.Trials = append(report.Trials, trial)
		}
	}
	report.Summary = SummarizeLive(report)
	return report
}

func TestValidateLiveV2CostAndHarnessGates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*LiveReport)
		want   string
	}{
		{name: "legacy schema", mutate: func(report *LiveReport) { report.SchemaVersion = 1 }, want: "仅可读取为历史证据"},
		{name: "legacy harness", mutate: func(report *LiveReport) { report.HarnessMode = "legacy" }, want: "want v2"},
		{name: "per build call cap", mutate: func(report *LiveReport) {
			report.Trials[0].BuilderCalls = 4
			report.Summary = SummarizeLive(*report)
		}, want: "必须在 1..3"},
		{name: "total call cap", mutate: func(report *LiveReport) {
			for i := range report.Trials {
				if report.Trials[i].Scenario != "L5" {
					report.Trials[i].BuilderCalls = 3
					report.Trials[i].ValidationRounds = 3
				}
			}
			report.Trials[0].BuilderCalls = 4
			report.Trials[0].ValidationRounds = 4
			report.Summary = SummarizeLive(*report)
		}, want: "超过 45"},
		{name: "token reduction", mutate: func(report *LiveReport) {
			report.Trials[0].Usage.TotalTokens = MaxV2TotalTokens
			report.Summary = SummarizeLive(*report)
		}, want: "30% 降幅"},
		{name: "median", mutate: func(report *LiveReport) {
			for i := range report.Trials {
				report.Trials[i].TotalMS = 90_001
			}
			report.Summary = SummarizeLive(*report)
		}, want: "耗时中位数"},
		{name: "upstream error", mutate: func(report *LiveReport) {
			report.Trials[0].ModelErrorClasses = []string{"quota"}
		}, want: "上游错误分类"},
		{name: "summary mismatch", mutate: func(report *LiveReport) {
			report.Summary.Usage.TotalTokens++
		}, want: "汇总与逐轮指标不一致"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := validV2LiveReport()
			tt.mutate(&report)
			errs := ValidateLive(report)
			if !strings.Contains(FormatErrors(errs), tt.want) {
				t.Fatalf("缺少错误 %q:\n%s", tt.want, FormatErrors(errs))
			}
		})
	}
}

func TestDecodeLiveRejectsSensitiveDataInsideOpenMap(t *testing.T) {
	raw := `{
  "schema_version": 1,
  "generated_at": "2026-08-10T00:00:00Z",
  "models": {"screening":"s","builder":"b","embedding":"e"},
  "trials": [{
    "scenario":"L1","repetition":1,"model_code":"b",
    "session_fingerprint":"0123456789ab","run_fingerprints":["abcdef012345"],
    "final_phase":"ready","versions":[1],"first_progress_ms":10,"total_ms":100,
    "max_sse_gap_ms":20,"screening_calls":1,"builder_calls":1,"validation_rounds":1,
    "embedding_cache":"not_used","assertions":{"ok":true},
    "diff_summary":{"token":"must-not-pass"}
  }],
  "route_latency_ms":[10]
}`
	if _, err := DecodeLive(strings.NewReader(raw)); err == nil || !strings.Contains(err.Error(), "diff_summary.token") {
		t.Fatalf("开放 map 中的敏感键必须被拒绝, err=%v", err)
	}
}

func TestDecodeHumanRejectsRawIdentifiersInNotes(t *testing.T) {
	raw := `{
  "schema_version":1,"reviewed_at":"2026-08-10T00:00:00Z",
  "reviews":[{"reviewer_id":"R1","profile":"novice","scores":{"requirement_fit":2,"completeness":2,"trust":2,"change_control":2,"direct_buy":2,"share_clarity":2},"assisted":false,"blockers":[],"notes":"session 123e4567-e89b-42d3-a456-426614174000"}]
}`
	if _, err := DecodeHuman(strings.NewReader(raw)); err == nil || !strings.Contains(err.Error(), "reviews[0].notes") {
		t.Fatalf("真人备注中的原始 UUID 必须被拒绝, err=%v", err)
	}
}
