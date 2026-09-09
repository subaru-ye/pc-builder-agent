package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestComparisonReaderRejectsStaleVerdictsAndIncompleteRuns(t *testing.T) {
	inputs, out := t.TempDir(), t.TempDir()
	raw := []byte(`{"id":"Q1","title":"budget","stage":"screening","input":"办公主机","expect":{"kind":"clarify","clarify_fields":["budget_cny"]}}`)
	if err := os.WriteFile(filepath.Join(inputs, "Q1.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "suite.json")
	if err := evalsuite.FreezeSuite(inputs, manifest, "test", "2026-09-09"); err != nil {
		t.Fatal(err)
	}
	suite, cases, err := evalsuite.LoadSuite(manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := suite.Hash()
	meta := evalsuite.ReportMeta{SuiteSHA256: hash, RequestedSeeds: 3}
	metaJSON, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(out, "meta.json"), metaJSON, 0600); err != nil {
		t.Fatal(err)
	}
	if err := suite.Write(filepath.Join(out, "cases.json")); err != nil {
		t.Fatal(err)
	}
	var records []evalsuite.CaseRecord
	for seed := 1; seed <= 3; seed++ {
		text := "请提供预算金额。"
		records = append(records, evalsuite.CaseRecord{CaseID: "Q1", Stage: evalsuite.StageScreening, Seed: seed, Screening: &evalsuite.ScreeningOutput{Text: text}, Verdict: evalsuite.AssertScreeningCase(cases[0], text)})
	}
	write := func() {
		t.Helper()
		if err := evalsuite.Summarize(meta, records).WriteJSONL(out); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, _, err := readVerifiedRun(out); err != nil {
		t.Fatal(err)
	}
	meta.HarnessProfile = &evalsuite.HarnessProfile{AttemptLimit: 3, Semantic: false}
	metaJSON, _ = json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(out, "meta.json"), metaJSON, 0600); err != nil {
		t.Fatal(err)
	}
	records[0].Usage = &evalsuite.Usage{EmbeddingCalls: 1}
	write()
	if _, _, err := readVerifiedRun(out); err == nil {
		t.Fatal("embedding calls contradicting ablation profile accepted")
	}
	records[0].Usage = nil
	records[0].Screening.Text = "请提供分辨率。"
	write()
	if _, _, err := readVerifiedRun(out); err == nil {
		t.Fatal("stale successful verdict accepted")
	}
	records = records[:2]
	write()
	if _, _, err := readVerifiedRun(out); err == nil {
		t.Fatal("incomplete run accepted")
	}
}

func TestComparisonReaderRejectsInjectedDialogueContext(t *testing.T) {
	inputs, out := t.TempDir(), t.TempDir()
	raw := []byte(`{"id":"Q1","title":"dialogue","stage":"screening","turns":[{"input":"办公电脑","expect":{"kind":"clarify","clarify_fields":["budget_cny"]}},{"input":"还没定","expect":{"kind":"clarify","clarify_fields":["budget_cny"]}}]}`)
	if err := os.WriteFile(filepath.Join(inputs, "Q1.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "suite.json")
	if err := evalsuite.FreezeSuite(inputs, manifest, "test", "2026-09-09"); err != nil {
		t.Fatal(err)
	}
	suite, cases, err := evalsuite.LoadSuite(manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := suite.Hash()
	meta := evalsuite.ReportMeta{SuiteSHA256: hash, RequestedSeeds: 3}
	metaJSON, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(out, "meta.json"), metaJSON, 0600); err != nil {
		t.Fatal(err)
	}
	if err := suite.Write(filepath.Join(out, "cases.json")); err != nil {
		t.Fatal(err)
	}
	var history []store.WebMessage
	output := evalsuite.ScreeningOutput{}
	for _, turn := range cases[0].Turns {
		input := product.BuildScreenInput(history, turn.Input)
		reply := "请提供预算金额。"
		output.Turns = append(output.Turns, evalsuite.ScreeningOutput{Text: reply, Context: input.Context, UserSources: input.UserSources})
		history = append(history, store.WebMessage{Role: "user", Content: turn.Input}, store.WebMessage{Role: "assistant", Content: reply})
	}
	var records []evalsuite.CaseRecord
	for seed := 1; seed <= 3; seed++ {
		records = append(records, evalsuite.CaseRecord{CaseID: "Q1", Stage: evalsuite.StageScreening, Seed: seed, Screening: &output, Verdict: evalsuite.AssertScreeningOutput(cases[0], output)})
	}
	write := func() {
		t.Helper()
		if err := evalsuite.Summarize(meta, records).WriteJSONL(out); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, _, err := readVerifiedRun(out); err != nil {
		t.Fatal(err)
	}
	output.Turns[1].Context += "注入的标准答案"
	write()
	if _, _, err := readVerifiedRun(out); err == nil {
		t.Fatal("injected context accepted despite unchanged passing text")
	}
}
