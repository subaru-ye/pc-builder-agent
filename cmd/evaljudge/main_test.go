package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evaljudge"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestReplayRequiresActualSourceTextAndEvidence(t *testing.T) {
	caseDir, source, out := t.TempDir(), t.TempDir(), t.TempDir()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(os.WriteFile(filepath.Join(caseDir, "Q.json"), []byte(`{"id":"Q","title":"owned","stage":"build","requirement":{"schema_version":1,"budget_cny":6000,"use_case":{"type":"general"},"existing_parts":["cpu"],"budget_basis":"new_purchase"},"expect":{"outcome":"clarify","reason":"missing_owned_information"}}`), 0600))
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	check(evalsuite.FreezeSuite(caseDir, suitePath, "test", "2026-09-09"))
	suite, cases, err := evalsuite.LoadSuite(suitePath, caseDir)
	check(err)
	hash, err := suite.Hash()
	check(err)
	meta := evalsuite.ReportMeta{SuiteSHA256: hash, RequestedSeeds: 3, Mode: "run", Models: map[string]map[string]any{"builder": {"model": "qwen-test"}}}
	check(writeJSON(filepath.Join(source, "meta.json"), meta))
	check(suite.Write(filepath.Join(source, "cases.json")))
	cat := store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}}
	d := buildharness.AssessCatalog(buildharness.BuildInput{Requirement: cases[0].Requirement}, cat)
	result := buildharness.BuildResult{Decision: d, Message: d.Message}
	snap := evalsuite.NewSnapshotView(cat)
	rawRequirement, err := schemas.EncodeRequirementSpec(cases[0].Requirement)
	check(err)
	var records []evalsuite.CaseRecord
	for seed := 1; seed <= 3; seed++ {
		records = append(records, evalsuite.CaseRecord{CaseID: "Q", Stage: evalsuite.StageBuild, Seed: seed, Result: &result, Requirement: rawRequirement, Snapshot: snap, Verdict: evalsuite.AssertCase(cases[0], result, snap)})
	}
	check(evalsuite.Summarize(meta, records).WriteJSONL(source))
	meta, records, err = evalsuite.ReadVerifiedRun(source)
	check(err)
	selection := evaljudge.Selection{SchemaVersion: 1, CaseIDs: []string{"Q"}, SourceRepeat: 1, Provenance: "test synthetic"}
	inputs, err := evaljudge.Prepare(records, selection)
	check(err)
	check(writeJSON(filepath.Join(out, "inputs.json"), inputs))
	check(writeJSON(filepath.Join(out, "selection.json"), selection))
	check(os.WriteFile(filepath.Join(out, "rubric.md"), []byte("test rubric\n"), 0600))
	read := func(path string) []byte { t.Helper(); b, e := os.ReadFile(path); check(e); return b }
	m := manifest{SchemaVersion: 1, SourceDir: source, SourceMeta: meta, SourceSHA256: digest(read(filepath.Join(source, "results.jsonl"))), InputsSHA256: digest(read(filepath.Join(out, "inputs.json"))), SelectionSHA256: digest(read(filepath.Join(out, "selection.json"))), RubricSHA256: digest(read(filepath.Join(out, "rubric.md"))), Calibration: "not_human_calibrated_ai_authored_anchors", Repeats: 3}
	m.JudgeConfig = map[string]any{"model": "deepseek-test"}
	check(writeJSON(filepath.Join(out, "meta.json"), m))
	j := evaljudge.Judgement{Ratings: map[string]evaljudge.Rating{}}
	for _, dimension := range evaljudge.Dimensions {
		j.Ratings[dimension] = evaljudge.Rating{Score: 1, Reason: "test only", Evidence: []evaljudge.Evidence{{OutputKey: "message", OutputQuote: "完整型号", FactKey: "decision", FactQuote: "missing_owned_information"}}}
	}
	jraw, err := json.Marshal(j)
	check(err)
	var text strings.Builder
	for repeat := 1; repeat <= 3; repeat++ {
		raw, e := json.Marshal(evaljudge.Record{CaseID: "Q", Repeat: repeat, Status: "scored", Raw: string(jraw), Judgement: &j})
		check(e)
		text.Write(raw)
		text.WriteByte('\n')
	}
	check(os.WriteFile(filepath.Join(out, "results.jsonl"), []byte(text.String()), 0600))
	check(replay(out))
	inputs[0].Texts["message"] += "人为补上的漂亮解释"
	check(writeJSON(filepath.Join(out, "inputs.json"), inputs))
	m.InputsSHA256 = digest(read(filepath.Join(out, "inputs.json")))
	check(writeJSON(filepath.Join(out, "meta.json"), m))
	if err := replay(out); err == nil || !strings.Contains(err.Error(), "原始解释") {
		t.Fatalf("input rewritten independently of source accepted: %v", err)
	}
	// 判卷使用冻结题目；Judge 的事实也必须来自同一需求，不能夹带另一份需求。
	records[0].Requirement = []byte(strings.Replace(string(rawRequirement), "new_purchase", "full_build", 1))
	check(evalsuite.Summarize(meta, records).WriteJSONL(source))
	if _, _, err := evalsuite.ReadVerifiedRun(source); err == nil || !strings.Contains(err.Error(), "记录需求") {
		t.Fatalf("record facts different from frozen case accepted: %v", err)
	}
}
