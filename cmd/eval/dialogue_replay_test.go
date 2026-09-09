package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestDialogueReplayUsesAllFrozenTurns(t *testing.T) {
	inputs, out := t.TempDir(), t.TempDir()
	raw := []byte(`{"id":"Q1","title":"dialogue","stage":"screening","turns":[{"input":"办公电脑","expect":{"kind":"clarify","clarify_fields":["budget_cny"]}},{"input":"还没定","expect":{"kind":"clarify","clarify_fields":["budget_cny"]}}]}`)
	if err := os.WriteFile(filepath.Join(inputs, "Q1.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "suite.json")
	if err := evalsuite.FreezeSuite(inputs, manifest, "dialogue", "2026-09-09"); err != nil {
		t.Fatal(err)
	}
	suite, cases, err := evalsuite.LoadSuite(manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := suite.Hash()
	meta := evalsuite.ReportMeta{RecordSchemaVersion: 1, SuiteVersion: "dialogue", SuiteSHA256: hash, RequestedSeeds: 1, Mode: "run"}
	if err := writeMeta(out, meta); err != nil {
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
	record := evalsuite.CaseRecord{CaseID: "Q1", Stage: evalsuite.StageScreening, Seed: 1, Screening: &output, Verdict: evalsuite.AssertScreeningOutput(cases[0], output)}
	write := func() {
		t.Helper()
		if err := evalsuite.Summarize(meta, []evalsuite.CaseRecord{record}).WriteJSONL(out); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if got := runReplay(out); got != 0 {
		t.Fatalf("valid dialogue exit=%d", got)
	}
	output.Turns[0].Text = "请提供分辨率。"
	// 同步后续真实上下文，故意保留旧的通过 Verdict；重放应识别第一轮答错。
	input := product.BuildScreenInput([]store.WebMessage{{Role: "user", Content: "办公电脑"}, {Role: "assistant", Content: output.Turns[0].Text}}, "还没定")
	output.Turns[1].Context = input.Context
	write()
	if got := runReplay(out); got != 1 {
		t.Fatalf("stale first-turn verdict accepted: %d", got)
	}
	output.Turns = output.Turns[:1]
	write()
	if got := runReplay(out); got != 2 {
		t.Fatalf("missing turn accepted: %d", got)
	}
}
