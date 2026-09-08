package evalsuite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestV13MigrationPreservesInputsAndHistoricalContracts(t *testing.T) {
	_, oldCases, err := LoadSuite("testdata/suites/v1.2.json", "testdata/cases")
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := LoadSuite("testdata/suites/v1.3.json", "testdata/cases")
	if err != nil {
		t.Fatal(err)
	}
	if len(oldCases) != 38 || len(current) != 40 {
		t.Fatal("unexpected suite size")
	}
	old, next := map[string]Case{}, map[string]Case{}
	for _, c := range oldCases {
		old[c.ID] = c
	}
	for _, c := range current {
		next[c.ID] = c
	}
	for _, pair := range [][2]string{{"L1-010", "L4-209"}, {"L1-011", "L4-210"}} {
		a, b := old[pair[0]], next[pair[1]]
		if !reflect.DeepEqual(a.Requirement, b.Requirement) || a.Expect.Outcome != "pass" || b.Expect.Outcome != "clarify" || b.Expect.Reason != "missing_owned_information" {
			t.Fatalf("unreviewed input/contract migration: %v", pair)
		}
		if _, ok := next[pair[0]]; ok {
			t.Fatal("legacy success contract still in current suite")
		}
	}
	for _, pair := range [][2]string{{"L4-207", "L4-211"}, {"L4-208", "L4-212"}} {
		a, b := old[pair[0]], next[pair[1]]
		if a.Input != b.Input || !reflect.DeepEqual(a.Expect.ClarifyFields, b.Expect.ClarifyFields) || len(a.Expect.ForbiddenClarifyFields) != 0 || len(b.Expect.ForbiddenClarifyFields) == 0 {
			t.Fatalf("screening input or original expectation changed: %v", pair)
		}
	}
	if !reflect.DeepEqual(old["L1-004"], next["L1-004"]) {
		t.Fatal("low-budget failure was hidden")
	}
}

func TestSuiteFreezeSnapshotAndTampering(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "Q1.json")
	raw := []byte(`{"id":"Q1","title":"追问预算 <test>","stage":"screening","input":"配电脑","expect":{"kind":"clarify"}}`)
	if err := os.WriteFile(input, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "v1.json")
	if err := FreezeSuite(dir, manifest, "v1", "2026-09-07"); err != nil {
		t.Fatal(err)
	}
	if err := FreezeSuite(dir, manifest, "v1", "2026-09-07"); err == nil {
		t.Fatal("overwrote frozen manifest")
	}
	suite, cases, err := LoadSuite(manifest, dir)
	if err != nil || len(cases) != 1 {
		t.Fatalf("load: %v", err)
	}
	hash, _ := suite.Hash()
	path := filepath.Join(t.TempDir(), "cases.json")
	if err := suite.Write(path); err != nil {
		t.Fatal(err)
	}
	// Round trip canonical hashes survive pretty-printing and escaped HTML/Chinese.
	_, saved, err := ReadSuiteSnapshot(path, hash)
	if err != nil || saved[0].Input != "配电脑" {
		t.Fatalf("round trip: %v", err)
	}
	if err := os.WriteFile(input, []byte(`{"id":"Q1","title":"changed","stage":"screening","input":"changed","expect":{"kind":"spec"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadSuite(manifest, dir); err == nil {
		t.Fatal("accepted changed expectation")
	}
	if _, _, err := ReadSuiteSnapshot(path, hash); err != nil {
		t.Fatal("snapshot depends on mutable workspace", err)
	}
	suite.Cases[0] = json.RawMessage(`{"input":"tampered"}`)
	if _, err := suite.DecodeCases(); err == nil {
		t.Fatal("accepted tampered snapshot")
	}
}

func TestSuiteIdentityValidation(t *testing.T) {
	suite, cases, err := LoadSuite("testdata/suites/v1.1.json", "testdata/cases")
	if err != nil || len(cases) != 30 {
		t.Fatalf("v1.1: %v, count %d", err, len(cases))
	}
	path := filepath.Join(t.TempDir(), "cases.json")
	if err := suite.Write(path); err != nil {
		t.Fatal(err)
	}
	hash, _ := suite.Hash()
	_, saved, err := ReadSuiteSnapshot(path, hash)
	if err != nil {
		t.Fatal(err)
	}
	// Embedded RawMessage values may be indented differently in the snapshot.
	// Compare all decoded values canonically, including change/base/locked/expect.
	caseHash := func(value any) string {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := JSONHash(encoded)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if caseHash(cases) != caseHash(saved) {
		t.Fatal("full exam including change/base/locked/expect did not survive")
	}
	for _, kind := range []string{"duplicate", "path", "missing", "id"} {
		t.Run(kind, func(t *testing.T) {
			copySuite := suite
			copySuite.Manifest.Cases = append([]SuiteCase{}, suite.Manifest.Cases...)
			switch kind {
			case "duplicate":
				copySuite.Manifest.Cases[1] = copySuite.Manifest.Cases[0]
			case "path":
				copySuite.Manifest.Cases[0].File = "../case.json"
			case "missing":
				copySuite.Cases = nil
			case "id":
				copySuite.Manifest.Cases[0].ID = "OTHER"
				copySuite.Manifest.Cases[0].File = "OTHER.json"
			}
			if _, err := copySuite.DecodeCases(); err == nil {
				t.Fatal("accepted invalid suite")
			}
		})
	}
}
