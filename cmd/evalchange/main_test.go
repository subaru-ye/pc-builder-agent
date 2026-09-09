package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func TestSourceManifestRejectsMismatchedBinaryOrDigest(t *testing.T) {
	m := sourceManifest{Version: 1, Files: map[string]string{"cmd/eval/main.go": "hash"}, BinarySHA256: "binary"}
	raw, _ := json.Marshal(m.Files)
	m.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	if !sourceMatches(m, "binary") {
		t.Fatal("valid source rejected")
	}
	if sourceMatches(m, "other") {
		t.Fatal("unrelated binary accepted")
	}
	m.Files["cmd/eval/main.go"] = "changed"
	if sourceMatches(m, "binary") {
		t.Fatal("tampered source digest accepted")
	}
}

func savedRun(t *testing.T, binary string) string {
	t.Helper()
	raw, err := os.ReadFile("../../internal/evalsuite/testdata/cases/L1-001.json")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := evalsuite.JSONHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	s := evalsuite.SuiteSnapshot{Manifest: evalsuite.SuiteManifest{SchemaVersion: 1, Version: "test", CreatedAt: "2026-09-09", Cases: []evalsuite.SuiteCase{{ID: "L1-001", File: "L1-001.json", SHA256: hash}}}, Cases: []json.RawMessage{raw}}
	dir := t.TempDir()
	if err := s.Write(filepath.Join(dir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	hash, err = s.Hash()
	if err != nil {
		t.Fatal(err)
	}
	m := evalsuite.ReportMeta{SuiteSHA256: hash, SuiteVersion: "test", RequestedSeeds: 3, SnapshotDate: "2026-09-08", Code: &evalsuite.CodeIdentity{BinarySHA256: binary}, HarnessProfile: &evalsuite.HarnessProfile{AttemptLimit: 3, Semantic: true}, Models: map[string]map[string]any{}}
	for _, role := range []string{"builder", "screening", "embedding"} {
		m.Models[role] = map[string]any{"model": "fixed", "model_chain": []string{}, "provider": "test", "base_host": "example.invalid"}
	}
	if err := writeJSON(filepath.Join(dir, "meta.json"), m); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../internal/evalsuite/testdata/grader/delivered.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []evalsuite.CaseRecord
	for seed := 1; seed <= 3; seed++ {
		var r evalsuite.CaseRecord
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		r.Seed = seed
		rows = append(rows, r)
	}
	if err := evalsuite.Summarize(m, rows).WriteJSONL(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestOfflineCompareAndTamperedRun(t *testing.T) {
	a, b := savedRun(t, "before"), savedRun(t, "after")
	root := t.TempDir()
	if code := run([]string{"-mode", "compare", "-baseline", a, "-candidate", b, "-out", root}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	paths, err := filepath.Glob(filepath.Join(root, "*", "comparison.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("missing report %v %v", paths, err)
	}
	raw, err := os.ReadFile(filepath.Join(b, "results.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// 复制首条导致第四次执行，必须拒绝，不能靠分数为绿绕过完整性。
	end := 0
	for end < len(raw) && raw[end] != '\n' {
		end++
	}
	raw = append(raw, raw[:end+1]...)
	if err := os.WriteFile(filepath.Join(b, "results.jsonl"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-mode", "compare", "-baseline", a, "-candidate", b, "-out", root}); code != 2 {
		t.Fatalf("tampering accepted: %d", code)
	}
}
