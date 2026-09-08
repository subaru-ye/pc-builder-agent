package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
)

func TestModelIdentityExcludesCredentials(t *testing.T) {
	cfg := modelprovider.Config{APIKey: "secret-credential", BaseURL: "https://example.com/path?token=secret-query", Model: "test", ModelChain: []string{"test", "fallback"}}
	data, err := json.Marshal(modelIdentity(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatal("credential included in metadata")
	}
	if !strings.Contains(string(data), "fallback") {
		t.Fatal("missing chain configuration")
	}
}

func TestVersionedReplayUsesSavedExamAndRejectsMissingCopy(t *testing.T) {
	inputs, out := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(inputs, "Q1.json"), []byte(`{"id":"Q1","title":"clarify","stage":"screening","input":"hello","expect":{"kind":"clarify"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "v1.json")
	if err := evalsuite.FreezeSuite(inputs, manifest, "v1", "2026-09-07"); err != nil {
		t.Fatal(err)
	}
	suite, _, err := evalsuite.LoadSuite(manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := suite.Hash()
	meta := evalsuite.ReportMeta{SuiteVersion: "v1", SuiteSHA256: hash, RequestedSeeds: 1, Mode: "run"}
	if err := writeMeta(out, meta); err != nil {
		t.Fatal(err)
	}
	if err := suite.Write(filepath.Join(out, "cases.json")); err != nil {
		t.Fatal(err)
	}
	summary := evalsuite.Summarize(meta, []evalsuite.CaseRecord{{CaseID: "Q1", Stage: evalsuite.StageScreening, Seed: 1, Verdict: evalsuite.Verdict{Passed: true}}})
	if err := summary.WriteJSONL(out); err != nil {
		t.Fatal(err)
	}
	if got := runReplay(out); got != 0 {
		t.Fatalf("replay exit %d", got)
	}
	report, err := os.ReadFile(filepath.Join(out, "report.md"))
	if err != nil || !strings.Contains(string(report), "部分重放") {
		t.Fatal("legacy screening was presented as regraded", err)
	}
	after, err := readMeta(out)
	if err != nil || after.Mode != "run" || after.SuiteSHA256 != hash {
		t.Fatal("replay altered original metadata", err)
	}
	if err := os.Remove(filepath.Join(out, "cases.json")); err != nil {
		t.Fatal(err)
	}
	if got := runReplay(out); got != 2 {
		t.Fatalf("missing versioned exam accepted: %d", got)
	}
}

func TestScreeningReplayRegradesSavedText(t *testing.T) {
	inputs, out := t.TempDir(), t.TempDir()
	raw := []byte(`{"id":"Q1","title":"budget","stage":"screening","input":"装机","expect":{"kind":"clarify","clarify_fields":["budget_cny"]}}`)
	if err := os.WriteFile(filepath.Join(inputs, "Q1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "v1.1.json")
	if err := evalsuite.FreezeSuite(inputs, manifest, "v1.1", "2026-09-07"); err != nil {
		t.Fatal(err)
	}
	suite, cases, err := evalsuite.LoadSuite(manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := suite.Hash()
	meta := evalsuite.ReportMeta{RecordSchemaVersion: 1, SuiteVersion: "v1.1", SuiteSHA256: hash, RequestedSeeds: 1, Mode: "run"}
	if err := writeMeta(out, meta); err != nil {
		t.Fatal(err)
	}
	if err := suite.Write(filepath.Join(out, "cases.json")); err != nil {
		t.Fatal(err)
	}
	// The mutable fixture is no longer usable; replay must only use its saved exam.
	if err := os.WriteFile(filepath.Join(inputs, "Q1.json"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := cases[0]
	correct := "请问您的预算是多少？"
	wrong := "请问分辨率是多少？"
	pass := evalsuite.AssertScreeningCase(c, correct)
	fail := evalsuite.AssertScreeningCase(c, wrong)
	for _, tt := range []struct {
		name    string
		output  *evalsuite.ScreeningOutput
		verdict evalsuite.Verdict
		runErr  string
		want    int
	}{
		{"pass", &evalsuite.ScreeningOutput{Text: correct}, pass, "", 0},
		{"fail reproduced", &evalsuite.ScreeningOutput{Text: wrong}, fail, "", 0},
		{"stale pass rejected", &evalsuite.ScreeningOutput{Text: wrong}, pass, "", 1},
		{"empty not missing", &evalsuite.ScreeningOutput{}, pass, "", 1},
		{"missing new response", nil, pass, "", 2},
		{"different failing assertion", &evalsuite.ScreeningOutput{}, fail, "", 1},
		{"transport error retained", &evalsuite.ScreeningOutput{Text: correct}, evalsuite.Verdict{Failures: []evalsuite.AssertionFailure{{ID: "RUN", Detail: "timeout"}}}, "timeout", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Deliberately wrong record expectation: the saved exam must take precedence.
			r := evalsuite.CaseRecord{CaseID: c.ID, Stage: c.Stage, Seed: 1, Expect: evalsuite.Expect{Kind: "clarify", ClarifyFields: []string{"resolution"}}, Screening: tt.output, Verdict: tt.verdict, RunErr: tt.runErr}
			r.Attribution = evalsuite.Attribute(r.Verdict.Failures)
			if err := evalsuite.Summarize(meta, []evalsuite.CaseRecord{r}).WriteJSONL(out); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(out, "results.jsonl"))
			if got := runReplay(out); got != tt.want {
				t.Fatalf("exit=%d want=%d", got, tt.want)
			}
			after, _ := os.ReadFile(filepath.Join(out, "results.jsonl"))
			if string(before) != string(after) {
				t.Fatal("original results were overwritten")
			}
		})
	}
}

func TestCheckRecordsRejectsIncompleteOrDuplicateTrials(t *testing.T) {
	cases := map[string]evalsuite.Case{"A": {Stage: evalsuite.StageBuild}}
	records := []evalsuite.CaseRecord{{CaseID: "A", Stage: evalsuite.StageBuild, Seed: 1}, {CaseID: "A", Stage: evalsuite.StageBuild, Seed: 2}}
	if err := checkRecords(records, cases, 2); err != nil {
		t.Fatal(err)
	}
	if err := checkRecords(records[:1], cases, 2); err == nil {
		t.Fatal("missing trial accepted")
	}
	records[1].Seed = 1
	if err := checkRecords(records, cases, 2); err == nil {
		t.Fatal("duplicate trial accepted")
	}
}
