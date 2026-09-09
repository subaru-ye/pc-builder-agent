package evalsuite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPromptEvidenceIsIndependentStableAndImmutable(t *testing.T) {
	components := []PromptComponent{{Role: "screening", Name: "system", Text: "原始提示词\n"}, {Role: "builder", Name: "system", Text: "选择配件"}}
	saved, id, err := NewPromptEvidence(components)
	if err != nil {
		t.Fatal(err)
	}
	components[0], components[1] = components[1], components[0]
	_, reordered, err := NewPromptEvidence(components)
	if err != nil || !reflect.DeepEqual(id, reordered) {
		t.Fatal("component order changed prompt identity", err)
	}
	components[1].Text += "新增约束"
	_, changed, err := NewPromptEvidence(components)
	if err != nil || changed.SHA256 == id.SHA256 || changed.Components["builder/system"] != id.Components["builder/system"] || changed.Components["screening/system"] == id.Components["screening/system"] {
		t.Fatal("prompt edits are not independently traceable", err)
	}
	dir := t.TempDir()
	if err := saved.Write(dir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "prompts.json"))
	if !strings.Contains(string(before), "原始提示词\\n") || strings.Contains(string(before), "新增约束") {
		t.Fatal("original prompt text was normalized or changed")
	}
	if err := saved.Write(dir); err == nil {
		t.Fatal("existing prompt evidence was overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "prompts.json"))
	if string(before) != string(after) {
		t.Fatal("refused overwrite changed original evidence")
	}
}

func TestDataIdentityUsesFrozenSpecsAndPrices(t *testing.T) {
	_, baseline := deliveredFixture(t)
	want, err := NewDataIdentity(baseline.Snapshot)
	if err != nil || len(want.SHA256) != 64 || want.Scope != "catalog_prices" {
		t.Fatalf("missing complete data identity: %+v %v", want, err)
	}
	for _, change := range []string{"price", "specs"} {
		t.Run(change, func(t *testing.T) {
			_, record := deliveredFixture(t)
			if change == "price" {
				price := "1.00"
				record.Snapshot.Catalog.Candidates[0].PriceCNY = &price
			} else {
				record.Snapshot.Catalog.Candidates[0].Specs = json.RawMessage(`{"changed":true}`)
			}
			got, err := NewDataIdentity(record.Snapshot)
			if err != nil || got.SHA256 == want.SHA256 || record.Snapshot.SnapshotDate != baseline.Snapshot.SnapshotDate {
				t.Fatal("same-date content change was not detected", err)
			}
		})
	}
	if _, err := NewDataIdentity(SnapshotView{SnapshotDate: baseline.Snapshot.SnapshotDate}); err == nil {
		t.Fatal("date-only historical evidence was promoted to a catalog fingerprint")
	}
}

func TestVerifyRunEvidenceRejectsTamperingButKeepsHistoryMissing(t *testing.T) {
	dir := t.TempDir()
	if err := VerifyRunEvidence(dir, ReportMeta{}, nil); err != nil {
		t.Fatal("old metadata was forced to invent new evidence", err)
	}
	raw, err := json.Marshal(ReportMeta{})
	if err != nil || strings.Contains(string(raw), "prompts") || strings.Contains(string(raw), `"data"`) || strings.Contains(string(raw), "started_at") {
		t.Fatal("missing history fields were serialized as evidence")
	}
	_, record := deliveredFixture(t)
	snapshot, prompts, err := NewPromptEvidence([]PromptComponent{{Role: "builder", Name: "system", Text: "选择配件"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := NewDataIdentity(record.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	meta := ReportMeta{Prompts: &prompts, Data: &data}
	if err := VerifyRunEvidence(dir, meta, []CaseRecord{record}); err == nil {
		t.Fatal("missing declared prompt snapshot was accepted")
	}
	if err := snapshot.Write(dir); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRunEvidence(dir, meta, []CaseRecord{record}); err != nil {
		t.Fatal(err)
	}
	data.SHA256 = strings.Repeat("0", 64)
	if err := VerifyRunEvidence(dir, meta, []CaseRecord{record}); err == nil {
		t.Fatal("incorrect data fingerprint was accepted")
	}
	meta.Data = nil
	prompts.Components["builder/system"] = strings.Repeat("0", 64)
	if err := VerifyRunEvidence(dir, meta, []CaseRecord{record}); err == nil {
		t.Fatal("incorrect prompt component fingerprint was accepted")
	}
	prompts.SnapshotFile = "../outside.json"
	if err := VerifyRunEvidence(dir, meta, nil); err == nil {
		t.Fatal("untrusted prompt snapshot path was accepted")
	}
}
