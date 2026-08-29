package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/p10report"
)

func TestRunSupportsStagedAndFinalValidation(t *testing.T) {
	livePath := writeReport(t, "live.json", validLiveReport())
	humanPath := writeReport(t, "human.json", validHumanReport())
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantText string
	}{
		{name: "live only", args: []string{"-live", livePath}, wantText: "机器门禁通过"},
		{name: "human only", args: []string{"-human", humanPath}, wantText: "真人门禁通过"},
		{name: "compatible combined", args: []string{"-live", livePath, "-human", humanPath}, wantText: "封板门禁通过"},
		{name: "explicit final", args: []string{"-final", "-live", livePath, "-human", humanPath}, wantText: "封板门禁通过"},
		{name: "final missing human", args: []string{"-final", "-live", livePath}, wantCode: 2, wantText: "必须同时提供"},
		{name: "missing reports", args: nil, wantCode: 2, wantText: "至少提供"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("exit=%d,want %d; stdout=%q stderr=%q", code, tt.wantCode, stdout.String(), stderr.String())
			}
			if combined := stdout.String() + stderr.String(); !strings.Contains(combined, tt.wantText) {
				t.Fatalf("输出缺少 %q: %q", tt.wantText, combined)
			}
		})
	}
}

func TestRunRejectsLegacyLiveAsCurrentClosure(t *testing.T) {
	report := validLiveReport()
	report.SchemaVersion = p10report.LegacyLiveSchemaVersion
	path := writeReport(t, "legacy-live.json", report)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-live", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("legacy exit=%d,want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "仅可读取为历史证据") {
		t.Fatalf("缺少 legacy 说明: %q", stderr.String())
	}
}

func writeReport(t *testing.T, name string, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validLiveReport() p10report.LiveReport {
	report := p10report.LiveReport{SchemaVersion: p10report.LiveSchemaVersion, HarnessMode: "v2",
		Models: p10report.Models{Screening: "screening", Builder: "builder", Embedding: "embedding"}, RouteLatencyMS: []int64{10}}
	for scenario := 1; scenario <= 6; scenario++ {
		for repetition := 1; repetition <= 3; repetition++ {
			trial := p10report.LiveTrial{
				Scenario: "L" + string(rune('0'+scenario)), Repetition: repetition, ModelCode: "model",
				SessionFingerprint: "0123456789ab", RunFingerprints: []string{"abcdef012345"},
				FinalPhase: "ready", Versions: []int{1}, FirstProgressMS: 10, TotalMS: 10_000,
				MaxSSEGapMS: 20, ScreeningCalls: 1, BuilderCalls: 1, ValidationRounds: 1,
				HarnessMode: "v2", HarnessRuns: 1,
				Usage:               p10report.TokenUsage{PromptTokens: 90, CandidateTokens: 10, TotalTokens: 100},
				ModelCallDurationMS: 50, Assertions: map[string]bool{"ok": true},
			}
			if scenario == 5 {
				trial.FinalPhase, trial.Versions = "collecting", nil
				trial.BuilderCalls, trial.ValidationRounds = 0, 0
				trial.HarnessMode, trial.HarnessRuns = "not_used", 0
			}
			report.Trials = append(report.Trials, trial)
		}
	}
	report.Summary = p10report.SummarizeLive(report)
	return report
}

func validHumanReport() p10report.HumanReport {
	scores := p10report.HumanScores{RequirementFit: 2, Completeness: 2, Trust: 2, ChangeControl: 2, DirectBuy: 2, ShareClarity: 2}
	return p10report.HumanReport{SchemaVersion: p10report.HumanSchemaVersion, Reviews: []p10report.HumanReview{
		{ReviewerID: "R1", Profile: "novice", Scores: scores},
		{ReviewerID: "R2", Profile: "hardware_familiar", Scores: scores},
		{ReviewerID: "R3", Profile: "builder_experienced", Scores: scores},
	}}
}
