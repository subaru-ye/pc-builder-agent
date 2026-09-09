package evaldesk

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func fixture(t *testing.T, root, name, version string, inputs map[string]string, fail string, calls int64) string {
	t.Helper()
	dir := filepath.Join(root, "artifacts", "eval", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	snap := evalsuite.SuiteSnapshot{Manifest: evalsuite.SuiteManifest{SchemaVersion: 1, Version: version, CreatedAt: "2026-09-09"}}
	for id, input := range inputs {
		raw, err := json.Marshal(map[string]any{"id": id, "title": "冻结测试题 " + id, "stage": "screening", "input": input, "expect": map[string]any{"kind": "clarify", "clarify_fields": []string{"budget_cny"}}})
		if err != nil {
			t.Fatal(err)
		}
		hash, err := evalsuite.JSONHash(raw)
		if err != nil {
			t.Fatal(err)
		}
		snap.Manifest.Cases = append(snap.Manifest.Cases, evalsuite.SuiteCase{ID: id, File: id + ".json", SHA256: hash})
		snap.Cases = append(snap.Cases, raw)
	}
	if err := snap.Write(filepath.Join(dir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	hash, err := snap.Hash()
	if err != nil {
		t.Fatal(err)
	}
	meta := evalsuite.ReportMeta{SuiteVersion: version, SuiteSHA256: hash, RequestedSeeds: 3, RecordSchemaVersion: 1, SnapshotDate: "2026-09-08", GraderVersion: evalsuite.CurrentGraderVersion, GeneratedAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), Code: &evalsuite.CodeIdentity{BinarySHA256: name}, HarnessProfile: &evalsuite.HarnessProfile{AttemptLimit: 3, Semantic: true}, Models: map[string]map[string]any{}}
	for _, role := range []string{"builder", "screening", "embedding"} {
		meta.Models[role] = map[string]any{"model": "fixed", "provider": "test", "base_host": "example.invalid", "model_chain": []string{}, "api_key": "never-expose-this-credential"}
	}
	writeFixtureJSON(t, filepath.Join(dir, "meta.json"), meta)
	cases, err := snap.DecodeCases()
	if err != nil {
		t.Fatal(err)
	}
	records := []evalsuite.CaseRecord{}
	for _, c := range cases {
		for seed := 1; seed <= 3; seed++ {
			output := evalsuite.ScreeningOutput{Text: "请问你的预算是多少？", ModelText: "请问你的预算是多少？"}
			if c.ID == fail && seed == 2 {
				output.Text = "可以开始装机。"
				output.ModelText = "可以开始装机。"
			}
			v := evalsuite.AssertScreeningOutput(c, output)
			records = append(records, evalsuite.CaseRecord{CaseID: c.ID, Title: c.Title, Stage: c.Stage, Seed: seed, Expect: c.Expect, Snapshot: evalsuite.SnapshotView{SnapshotDate: meta.SnapshotDate}, Screening: &output, Verdict: v, Attribution: evalsuite.Attribute(v.Failures), DurationMS: 10, Usage: &evalsuite.Usage{ModelCalls: calls, UsageResponses: calls, TotalTokens: 100 * calls}})
		}
	}
	if err := (evalsuite.Summary{Records: records}).WriteJSONL(dir); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(root, dir)
	return runID(rel)
}

func writeFixtureJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func openStore(t *testing.T, root string) *Store {
	t.Helper()
	s, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStrictComparisonAndReadOnlyProjection(t *testing.T) {
	root := t.TempDir()
	a := fixture(t, root, "a", "v1", map[string]string{"S-1": "帮我装机"}, "", 1)
	b := fixture(t, root, "b", "v1", map[string]string{"S-1": "帮我装机"}, "S-1", 2)
	path := filepath.Join(root, "artifacts", "eval", "b", "results.jsonl")
	before, _ := os.ReadFile(path)
	store := openStore(t, root)
	result, err := store.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "strict" || result.Counts["regressed"] != 1 || result.Cases[0].OriginalA.Passed != 3 || result.Cases[0].CurrentB.Passed != 2 {
		t.Fatalf("unexpected comparison: %+v", result)
	}
	if *result.Metrics.Delta.ModelCalls != 3 || *result.Metrics.Delta.EmbeddingCalls != 0 || *result.Metrics.Delta.TotalTokens != 300 || *result.Cases[0].CallsDelta != 3 {
		t.Fatalf("wrong independent deltas: %+v", result.Metrics.Delta)
	}
	detail, err := store.Cases(a, b, "S-1")
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Candidate.InputFrozen || detail.Candidate.Inputs[0].Input != "帮我装机" || len(detail.Candidate.Trials[1].Current.Failures) == 0 {
		t.Fatalf("missing evidence: %+v", detail)
	}
	raw, _ := json.Marshal(result)
	for _, secret := range []string{"never-expose-this-credential", "base_host", root, "results.jsonl"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private field leaked: %s", secret)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("read operation changed historical records")
	}
}

func TestCrossSuiteOnlyUnchangedContentAndExpectationsJoin(t *testing.T) {
	root := t.TempDir()
	a := fixture(t, root, "a", "v1", map[string]string{"same": "共同题", "modified": "旧输入", "removed": "删除题"}, "", 1)
	b := fixture(t, root, "b", "v2", map[string]string{"same": "共同题", "modified": "新输入", "added": "新增题"}, "", 2)
	result, err := openStore(t, root).Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "observational" || result.Counts["common"] != 1 || result.Counts["modified"] != 1 || result.Counts["added"] != 1 || result.Counts["removed"] != 1 {
		t.Fatalf("wrong cohort: %+v", result.Counts)
	}
	if result.Metrics.Baseline.CaseCount != 1 || result.Metrics.Baseline.Recorded != 3 || *result.Metrics.Delta.ModelCalls != 3 {
		t.Fatalf("full-suite totals leaked into common cohort: %+v", result.Metrics)
	}
	for _, c := range result.Cases {
		if c.Content != "common" && c.CallsDelta != nil {
			t.Fatal("non-common case received a cost delta")
		}
	}
}

func TestExpectationOnlyChangeIsNotACommonQuestion(t *testing.T) {
	root := t.TempDir()
	a := fixture(t, root, "a", "v1", map[string]string{"S-1": "相同输入"}, "", 1)
	b := fixture(t, root, "b", "v2", map[string]string{"S-1": "相同输入"}, "", 1)
	dir := filepath.Join(root, "artifacts", "eval", "b")
	var snapshot evalsuite.SuiteSnapshot
	raw, _ := os.ReadFile(filepath.Join(dir, "cases.json"))
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	var question map[string]any
	if err := json.Unmarshal(snapshot.Cases[0], &question); err != nil {
		t.Fatal(err)
	}
	question["expect"] = map[string]any{"kind": "clarify", "clarify_fields": []string{"resolution"}}
	snapshot.Cases[0], _ = json.Marshal(question)
	snapshot.Manifest.Cases[0].SHA256, _ = evalsuite.JSONHash(snapshot.Cases[0])
	if err := snapshot.Write(filepath.Join(dir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	var meta evalsuite.ReportMeta
	raw, _ = os.ReadFile(filepath.Join(dir, "meta.json"))
	_ = json.Unmarshal(raw, &meta)
	meta.SuiteSHA256, _ = snapshot.Hash()
	writeFixtureJSON(t, filepath.Join(dir, "meta.json"), meta)
	cases, err := snapshot.DecodeCases()
	if err != nil {
		t.Fatal(err)
	}
	records, _ := readPartialRecords(filepath.Join(dir, "results.jsonl"))
	for i := range records {
		records[i].Expect = cases[0].Expect
		records[i].Verdict = evalsuite.AssertScreeningOutput(cases[0], *records[i].Screening)
		records[i].Attribution = evalsuite.Attribute(records[i].Verdict.Failures)
	}
	if err := (evalsuite.Summary{Records: records}).WriteJSONL(dir); err != nil {
		t.Fatal(err)
	}
	result, err := openStore(t, root).Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Counts["common"] != 0 || result.Counts["modified"] != 1 || result.Counts["regressed"] != 0 || result.Metrics.Baseline != nil || result.Metrics.Delta.ModelCalls != nil {
		t.Fatalf("expectation change was compared as regression: %+v", result)
	}
}

func TestMissingExecutionAndLegacyNeverBecomeAllPassed(t *testing.T) {
	root := t.TempDir()
	id := fixture(t, root, "partial", "v1", map[string]string{"S-1": "输入"}, "", 1)
	path := filepath.Join(root, "artifacts", "eval", "partial", "results.jsonl")
	records, _ := readPartialRecords(path)
	if err := (evalsuite.Summary{Records: records[:2]}).WriteJSONL(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, root)
	run, err := store.lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	if run.summary.Status != "incomplete" || *run.summary.Original.Expected != 3 || run.summary.Original.AllPassedCases != 0 || run.summary.Current != nil || run.summary.Original.Usage.Complete {
		t.Fatalf("missing trial treated as pass: %+v", run.summary)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	run, err = store.lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	if run.summary.Original.Recorded != 0 || *run.summary.Original.Expected != 3 || run.summary.Original.Usage != nil {
		t.Fatal("missing file must retain planned denominator and unknown usage")
	}
	legacy := fixture(t, root, "legacy", "v1", map[string]string{"S-1": "旧题"}, "", 1)
	legacyDir := filepath.Join(root, "artifacts", "eval", "legacy")
	if err := os.Remove(filepath.Join(legacyDir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	writeFixtureJSON(t, filepath.Join(legacyDir, "meta.json"), evalsuite.ReportMeta{SnapshotDate: "2026-09-08"})
	run, err = store.lookup(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if run.summary.Status != "legacy" || run.summary.Original.Repeats != nil || run.summary.Original.AllPassedRate != nil || run.summary.Versions.PromptVersion != nil || run.summary.Versions.DataFingerprint != nil {
		t.Fatalf("historical metadata inferred: %+v", run.summary)
	}
}

func TestDuplicateIdentityStaysInvalidDespiteRunningMarker(t *testing.T) {
	root := t.TempDir()
	id := fixture(t, root, "duplicate", "v1", map[string]string{"S-1": "输入"}, "", 1)
	dir := filepath.Join(root, "artifacts", "eval", "duplicate")
	records, _ := readPartialRecords(filepath.Join(dir, "results.jsonl"))
	records[1] = records[0]
	if err := (evalsuite.Summary{Records: records}).WriteJSONL(dir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	var meta evalsuite.ReportMeta
	_ = json.Unmarshal(raw, &meta)
	meta.Status = "running"
	writeFixtureJSON(t, filepath.Join(dir, "meta.json"), meta)
	run, err := openStore(t, root).lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	if run.summary.Status != "invalid" || run.summary.Original.ExecutionRate != nil || run.summary.Current != nil {
		t.Fatal("invalid run gained trustworthy metrics")
	}
}

func TestUnknownUsageAndRepeatMismatchDoNotInventDeltas(t *testing.T) {
	root := t.TempDir()
	a := fixture(t, root, "a", "v1", map[string]string{"S-1": "输入"}, "", 1)
	b := fixture(t, root, "b", "v1", map[string]string{"S-1": "输入"}, "", 1)
	dir := filepath.Join(root, "artifacts", "eval", "b")
	records, _ := readPartialRecords(filepath.Join(dir, "results.jsonl"))
	records[1].Usage = nil
	if err := (evalsuite.Summary{Records: records}).WriteJSONL(dir); err != nil {
		t.Fatal(err)
	}
	result, err := openStore(t, root).Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics.Delta.ModelCalls != nil || result.Cases[0].CallsDelta != nil || result.Metrics.Candidate.Usage.Complete {
		t.Fatal("unknown usage converted to zero")
	}
}

func TestCallsWithoutUsageResponsesKeepTokensUnknown(t *testing.T) {
	root := t.TempDir()
	a := fixture(t, root, "a", "v1", map[string]string{"S-1": "输入"}, "", 1)
	b := fixture(t, root, "b", "v1", map[string]string{"S-1": "输入"}, "", 2)
	dir := filepath.Join(root, "artifacts", "eval", "b")
	records, _ := readPartialRecords(filepath.Join(dir, "results.jsonl"))
	for i := range records {
		records[i].Usage.UsageResponses = 0
		records[i].Usage.TotalTokens = 0
	}
	if err := (evalsuite.Summary{Records: records}).WriteJSONL(dir); err != nil {
		t.Fatal(err)
	}
	result, err := openStore(t, root).Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics.Delta.ModelCalls == nil || *result.Metrics.Delta.ModelCalls != 3 || result.Metrics.Delta.TotalTokens != nil || result.Cases[0].TokensDelta != nil {
		t.Fatal("absent token responses were treated as zero tokens")
	}
}

func TestHTTPOnlyLocalReadOnlyOpaqueIdentifiers(t *testing.T) {
	root := t.TempDir()
	id := fixture(t, root, "a", "v1", map[string]string{"S-1": "输入"}, "", 1)
	handler := Handler(openStore(t, root))
	for _, test := range []struct {
		path, method, host, origin string
		code                       int
	}{
		{"/api/evaldesk/runs", "GET", "127.0.0.1:8086", "", 200},
		{"/api/evaldesk/runs", "POST", "127.0.0.1:8086", "", 405},
		{"/api/evaldesk/runs", "GET", "evil.example", "", 403},
		{"/api/evaldesk/runs", "GET", "localhost:8086", "https://evil.example", 403},
		{"/api/evaldesk/compare?baseline=../../.env&candidate=" + id, "GET", "localhost:8086", "", 404},
		{"/artifacts/eval/a/results.jsonl", "GET", "localhost:8086", "", 404},
		{"/api/evaldesk/cases?candidate=" + id + "&case=S-1", "GET", "localhost:8086", "http://localhost:3000", 200},
	} {
		req := httptest.NewRequest(test.method, test.path, nil)
		req.Host = test.host
		req.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != test.code {
			t.Errorf("%s got %d want %d", test.path, response.Code, test.code)
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS must not grant arbitrary origins")
		}
	}
	for _, address := range []string{"0.0.0.0:8086", ":8086", "192.168.1.1:8086", "evil.example:8086"} {
		if ListenAddress(address) {
			t.Fatal("remote bind accepted")
		}
	}
}

func TestCredentialTextRedaction(t *testing.T) {
	for _, test := range []struct{ input, secret string }{
		{"Authorization: Basic dXNlcjpwYXNz", "dXNlcjpwYXNz"},
		{"Cookie: session=first; refresh=second", "second"},
		{`"api_key": "two words"`, "words"},
		{`{"Authorization":"Basic dXNlcjpwYXNz"}`, "dXNlcjpwYXNz"},
		{`{"Cookie":"session=first; refresh=second"}`, "second"},
		{`{"password":"first\"second"}`, "second"},
		{"https://api.invalid/?token=query-secret", "query-secret"},
		{"https://api.invalid/?key=query-secret", "query-secret"},
		{"Bearer abc123.signature", "abc123"},
		{"https://user:pass@host/private", "pass@"},
		{"sk-123456789abcdef", "123456789abcdef"},
	} {
		if strings.Contains(clean(test.input), test.secret) {
			t.Errorf("credential leaked from %q as %q", test.input, clean(test.input))
		}
	}
	if clean("已知 total_tokens=42；用户说用2K分辨率。") != "已知 total_tokens=42；用户说用2K分辨率。" {
		t.Fatal("redaction damaged ordinary evidence")
	}
}

func TestSymlinkArtifactsCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "artifacts", "eval"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFixtureJSON(t, filepath.Join(outside, "meta.json"), map[string]string{"secret": "do-not-read"})
	if err := os.Symlink(outside, filepath.Join(root, "artifacts", "eval", "escape")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	store := openStore(t, root)
	if len(store.Runs().Runs) != 0 {
		t.Fatal("symlink directory was indexed")
	}
	dir := filepath.Join(root, "artifacts", "eval", "ordinary")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "meta.json"), filepath.Join(dir, "meta.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(dir, "meta.json"); err == nil {
		t.Fatal("symlink file was read")
	}
	if _, err := store.read(outside, "meta.json"); err == nil {
		t.Fatal("out-of-root file was read")
	}
}

func TestSafeFileRejectsOutsideRootAndTraversalWithoutSymlinkPrivileges(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeFixtureJSON(t, filepath.Join(outside, "meta.json"), map[string]string{"private": "outside-root"})
	store := openStore(t, root)
	if _, err := store.read(outside, "meta.json"); err == nil {
		t.Fatal("out-of-root file was read")
	}
	if _, err := store.read(root, "../meta.json"); err == nil {
		t.Fatal("traversal filename was accepted")
	}
}

func TestDeclaredPromptSymlinkIsRejectedBeforeAudit(t *testing.T) {
	root := t.TempDir()
	id := fixture(t, root, "a", "v1", map[string]string{"S-1": "输入"}, "", 1)
	dir := filepath.Join(root, "artifacts", "eval", "a")
	outside := t.TempDir()
	snapshot, identity, err := evalsuite.NewPromptEvidence([]evalsuite.PromptComponent{{Role: "screening", Name: "system", Text: "private prompt"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Write(outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "prompts.json"), filepath.Join(dir, "prompts.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	var meta evalsuite.ReportMeta
	_ = json.Unmarshal(raw, &meta)
	meta.Prompts = &identity
	writeFixtureJSON(t, filepath.Join(dir, "meta.json"), meta)
	run, err := openStore(t, root).lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	if run.summary.Status != "invalid" || run.summary.Verified || run.summary.Current != nil || run.summary.Versions.PromptSnapshot {
		t.Fatal("unsafe prompt snapshot was audited")
	}
}

func TestRunErrorsAndUnknownStagesNeverExposeRawValues(t *testing.T) {
	rawSecret := "private error without a recognisable credential prefix"
	run := &savedRun{summary: RunSummary{ID: "safe"}, cases: map[string]evalsuite.Case{}, records: []evalsuite.CaseRecord{{CaseID: "legacy", Stage: evalsuite.Stage(rawSecret), Seed: 1, RunErr: rawSecret, Verdict: evalsuite.Verdict{Failures: []evalsuite.AssertionFailure{{ID: "RUN", Detail: rawSecret}}}}}}
	detail := caseSide(run, "legacy")
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), rawSecret) || detail.Stage != "unknown" {
		t.Fatal("raw execution error or unknown stage leaked")
	}
}

func TestNonDeliveryDecisionMessageIsVisibleEvidence(t *testing.T) {
	run := &savedRun{summary: RunSummary{ID: "safe"}, cases: map[string]evalsuite.Case{}, records: []evalsuite.CaseRecord{{CaseID: "non-delivery", Stage: evalsuite.StageBuild, Seed: 1, Result: &buildharness.BuildResult{Decision: &buildharness.Decision{Kind: "catalog_infeasible", Reason: "platform_budget_lower_bound", Scope: "current_catalog", SnapshotDate: "2026-09-08", LowerBoundCNY: "3500", Message: "当前冻结目录下，完整办公主机最低需要 3500 元。"}}}}}
	detail := caseSide(run, "non-delivery")
	if detail.Trials[0].Selection == nil || !strings.Contains(*detail.Trials[0].Selection, "最低需要 3500 元") || !strings.Contains(*detail.Trials[0].Selection, "platform_budget_lower_bound") {
		t.Fatal("non-delivery reason and user-visible message were omitted")
	}
}
