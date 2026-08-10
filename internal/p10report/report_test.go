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
	report := LiveReport{SchemaVersion: 1, Models: Models{Screening: "s", Builder: "b", Embedding: "e"}, RouteLatencyMS: []int64{10}}
	for scenario := 1; scenario <= 6; scenario++ {
		for repetition := 1; repetition <= 3; repetition++ {
			trial := LiveTrial{Scenario: string(rune('L')), Repetition: repetition, ModelCode: "model"}
			trial.Scenario += string(rune('0' + scenario))
			trial.SessionFingerprint = "0123456789ab"
			trial.RunFingerprints = []string{"abcdef012345"}
			trial.FinalPhase = "ready"
			trial.Versions = []int{1}
			trial.FirstProgressMS = 10
			trial.TotalMS = 100
			trial.MaxSSEGapMS = 20
			trial.Assertions = map[string]bool{"ok": true}
			if scenario == 5 {
				trial.FinalPhase = "collecting"
				trial.Versions = nil
				trial.BuilderCalls = 0
				trial.ValidationRounds = 0
			}
			report.Trials = append(report.Trials, trial)
		}
	}
	if errs := ValidateLive(report); len(errs) != 0 {
		t.Fatalf("合格 live 报告被拒绝: %s", FormatErrors(errs))
	}
	report.Trials = report.Trials[:17]
	if errs := ValidateLive(report); len(errs) == 0 {
		t.Fatal("缺一次重复必须失败")
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
