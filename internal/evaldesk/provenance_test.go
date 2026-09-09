package evaldesk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func frozenFixture(t *testing.T, root, name string, caseNames ...string) (string, []json.RawMessage) {
	t.Helper()
	dir := filepath.Join(root, "artifacts", "eval", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	snapshot := evalsuite.SuiteSnapshot{Manifest: evalsuite.SuiteManifest{SchemaVersion: 1, Version: "frozen-test", CreatedAt: "2026-09-09"}}
	for _, name := range caseNames {
		raw, err := os.ReadFile(filepath.Join("..", "evalsuite", "testdata", "cases", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		hash, err := evalsuite.JSONHash(raw)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Manifest.Cases = append(snapshot.Manifest.Cases, evalsuite.SuiteCase{ID: name, File: name + ".json", SHA256: hash})
		snapshot.Cases = append(snapshot.Cases, raw)
	}
	if err := snapshot.Write(filepath.Join(dir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	hash, _ := snapshot.Hash()
	writeFixtureJSON(t, filepath.Join(dir, "meta.json"), evalsuite.ReportMeta{SuiteVersion: "frozen-test", SuiteSHA256: hash, RequestedSeeds: 3, GeneratedAt: time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC), Status: "running"})
	return runID(filepath.Join("artifacts", "eval", name)), snapshot.Cases
}

func TestProvenanceFrozenInputsIncludeEveryUnexecutedQuestion(t *testing.T) {
	root := t.TempDir()
	id, raw := frozenFixture(t, root, "frozen", "L2-104", "L5-301")
	out, err := openStore(t, root).Provenance(id)
	if err != nil {
		t.Fatal(err)
	}
	if out.Run.Status != "incomplete" || out.FrozenCaseCount == nil || *out.FrozenCaseCount != 2 || len(out.Cases) != 2 {
		t.Fatalf("unexecuted frozen questions omitted: %+v", out)
	}
	build, dialogue := out.Cases[0], out.Cases[1]
	if build.RecordedTrials != 0 || build.PlannedTrials == nil || *build.PlannedTrials != 3 || build.Inputs[0].InputKind != "structured" {
		t.Fatalf("unexecuted status invalid: %+v", build)
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal(raw[0], &original); err != nil {
		t.Fatal(err)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal([]byte(build.Inputs[0].Input), &input); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"requirement", "change", "base_selection", "locked"} {
		if len(original[key]) == 0 {
			t.Fatalf("test fixture lacks required field %s", key)
		}
		if hashJSON(json.RawMessage(input[key])) != hashJSON(json.RawMessage(original[key])) {
			t.Fatalf("frozen build field changed: %s", key)
		}
	}
	if hashJSON(json.RawMessage(build.Inputs[0].Expected)) != hashJSON(original["expect"]) {
		t.Fatal("build expectation changed")
	}
	var turns struct {
		Turns []struct {
			Input  string          `json:"input"`
			Expect json.RawMessage `json:"expect"`
		} `json:"turns"`
	}
	if err := json.Unmarshal(raw[1], &turns); err != nil {
		t.Fatal(err)
	}
	if len(dialogue.Inputs) != len(turns.Turns) {
		t.Fatal("dialogue turns lost")
	}
	for i, turn := range turns.Turns {
		got := dialogue.Inputs[i]
		if got.Turn != i+1 || got.Input != turn.Input || hashJSON(json.RawMessage(got.Expected)) != hashJSON(turn.Expect) {
			t.Fatalf("frozen turn changed: %+v", got)
		}
	}
	if !strings.Contains(strings.Join(dialogue.Inputs[2].ExpectationSummary, " "), "6000 元") {
		t.Fatal("Chinese expectation summary missing")
	}
	encoded, _ := json.Marshal(out)
	for _, private := range []string{"trials", "modelOutputs", "candidates", "result", "run_err", "raw.log"} {
		if strings.Contains(string(encoded), `"`+private+`"`) {
			t.Fatalf("execution trace exposed: %s", private)
		}
	}
}

func TestProvenancePreservesOmittedRequirementFields(t *testing.T) {
	root := t.TempDir()
	id, _ := frozenFixture(t, root, "defaults", "L1-001")
	dir := filepath.Join(root, "artifacts", "eval", "defaults")
	var snapshot evalsuite.SuiteSnapshot
	raw, _ := os.ReadFile(filepath.Join(dir, "cases.json"))
	_ = json.Unmarshal(raw, &snapshot)
	var frozen map[string]any
	_ = json.Unmarshal(snapshot.Cases[0], &frozen)
	requirement := frozen["requirement"].(map[string]any)
	delete(requirement, "budget_flex")
	delete(requirement, "size_pref")
	changed, _ := json.Marshal(frozen)
	snapshot.Cases[0] = changed
	snapshot.Manifest.Cases[0].SHA256, _ = evalsuite.JSONHash(changed)
	if err := snapshot.Write(filepath.Join(dir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	hash, _ := snapshot.Hash()
	writeFixtureJSON(t, filepath.Join(dir, "meta.json"), evalsuite.ReportMeta{SuiteSHA256: hash, RequestedSeeds: 3})
	out, err := openStore(t, root).Provenance(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Cases) != 1 {
		t.Fatal("fixture invalid")
	}
	if strings.Contains(out.Cases[0].Inputs[0].Input, "budget_flex") || strings.Contains(out.Cases[0].Inputs[0].Input, "size_pref") {
		t.Fatal("schema defaults were invented as frozen fields")
	}
}

func TestProvenanceMissingOrInvalidSuiteNeverUsesCurrentQuestions(t *testing.T) {
	root := t.TempDir()
	id := fixture(t, root, "legacy", "v-old", map[string]string{"S-1": "保存的历史输入"}, "", 1)
	dir := filepath.Join(root, "artifacts", "eval", "legacy")
	if err := os.Remove(filepath.Join(dir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, root)
	out, err := store.Provenance(id)
	if err != nil {
		t.Fatal(err)
	}
	if out.FrozenCaseCount != nil || len(out.Cases) != 0 || !strings.Contains(strings.Join(out.Notes, " "), "冻结题库未记录") {
		t.Fatalf("missing suite fabricated: %+v", out)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), []byte(`{"manifest":{},"cases":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = store.Provenance(id)
	if err != nil || len(out.Cases) != 0 || out.FrozenCaseCount != nil || out.Run.Status != "invalid" {
		t.Fatalf("invalid suite fabricated: %+v %v", out, err)
	}
	if _, err := store.Provenance("../../.env"); err == nil {
		t.Fatal("arbitrary path accepted")
	}
}

func TestFrozenProjectionRedactsSecretsAndPathsButKeepsJSON(t *testing.T) {
	value := map[string]any{"notes": "Authorization: Bearer secret-authorization\n路径 C:\\Users\\private\\token.txt 和 /home/private/key.txt", "owned_parts": []any{map[string]any{"api_key": "bare-private-key", "model": "visible-model"}}}
	text := frozenJSON(value)
	if !json.Valid([]byte(text)) {
		t.Fatalf("redaction corrupted JSON: %s", text)
	}
	for _, secret := range []string{"secret-authorization", "C:\\\\Users", "/home/private", "bare-private-key"} {
		if strings.Contains(text, secret) {
			t.Fatalf("private data escaped: %s", text)
		}
	}
	if !strings.Contains(text, "visible-model") || !strings.Contains(text, "本机路径已隐藏") {
		t.Fatalf("projection lost useful data: %s", text)
	}
}

func TestProvenanceNestedExpectationSecretsAndUnknownWireFields(t *testing.T) {
	root := t.TempDir()
	id, _ := frozenFixture(t, root, "nested", "L5-301")
	dir := filepath.Join(root, "artifacts", "eval", "nested")
	var snapshot evalsuite.SuiteSnapshot
	raw, _ := os.ReadFile(filepath.Join(dir, "cases.json"))
	_ = json.Unmarshal(raw, &snapshot)
	var frozen map[string]any
	_ = json.Unmarshal(snapshot.Cases[0], &frozen)
	turns := frozen["turns"].([]any)
	expect := turns[2].(map[string]any)["expect"].(map[string]any)
	expect["spec_fields"].(map[string]any)["owned_parts"] = []any{map[string]any{"category": "gpu", "model": "visible", "api_key": "nested-bare-secret", "nested": map[string]any{"Authorization": "another-bare-secret"}}}
	changed, _ := json.Marshal(frozen)
	snapshot.Cases[0] = changed
	snapshot.Manifest.Cases[0].SHA256, _ = evalsuite.JSONHash(changed)
	if err := snapshot.Write(filepath.Join(dir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	hash, _ := snapshot.Hash()
	writeFixtureJSON(t, filepath.Join(dir, "meta.json"), evalsuite.ReportMeta{SuiteSHA256: hash, RequestedSeeds: 3})
	store := openStore(t, root)
	out, err := store.Provenance(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Cases) != 1 {
		t.Fatal("nested expectation fixture not loaded")
	}
	projected, _ := json.Marshal(out)
	for _, secret := range []string{"nested-bare-secret", "another-bare-secret"} {
		if strings.Contains(string(projected), secret) {
			t.Fatalf("nested expected secret escaped: %s", projected)
		}
	}
	if !json.Valid([]byte(out.Cases[0].Inputs[2].Expected)) {
		t.Fatal("nested expected JSON invalid after redaction")
	}
	// A forged extra top-level trajectory/config field is not a frozen question.
	frozen["raw_metadata"] = map[string]any{"api_key": "never-open"}
	changed, _ = json.Marshal(frozen)
	snapshot.Cases[0] = changed
	snapshot.Manifest.Cases[0].SHA256, _ = evalsuite.JSONHash(changed)
	writeFixtureJSON(t, filepath.Join(dir, "cases.json"), snapshot)
	// Recompute the declared hash without invoking the strict writer.
	hash, _ = snapshot.Hash()
	writeFixtureJSON(t, filepath.Join(dir, "meta.json"), evalsuite.ReportMeta{SuiteSHA256: hash, RequestedSeeds: 3})
	out, err = store.Provenance(id)
	if err != nil || len(out.Cases) != 0 {
		t.Fatal("unknown frozen wire fields accepted")
	}
}

func TestTimelineUsesRecordedTimeAndPlacesUnknownLast(t *testing.T) {
	root := t.TempDir()
	ids := []string{}
	for i, name := range []string{"20990101-name-is-not-date", "early", "late"} {
		id := fixture(t, root, name, "v", map[string]string{"S": "帮我装机"}, "", 1)
		ids = append(ids, id)
		metaPath := filepath.Join(root, "artifacts", "eval", name, "meta.json")
		var meta evalsuite.ReportMeta
		raw, _ := os.ReadFile(metaPath)
		_ = json.Unmarshal(raw, &meta)
		meta.GeneratedAt = time.Time{}
		if i == 1 {
			meta.GeneratedAt, _ = time.Parse(time.RFC3339, "2026-09-09T10:00:00+08:00")
		}
		if i == 2 {
			meta.GeneratedAt, _ = time.Parse(time.RFC3339, "2026-09-09T03:00:00Z")
		}
		writeFixtureJSON(t, metaPath, meta)
	}
	out := openStore(t, root).Timeline()
	if len(out.Items) != 3 || out.Items[0].Run.ID != ids[2] || out.Items[1].Run.ID != ids[1] || out.Items[2].Run.ID != ids[0] || out.Items[2].Run.CreatedAt != nil {
		t.Fatalf("timeline inferred filename date or sorted offsets lexically: %+v", out.Items)
	}
	if empty := openStore(t, t.TempDir()).Timeline(); len(empty.Items) != 0 || len(empty.Warnings) == 0 {
		t.Fatal("missing artifacts were not explicit")
	}
}

func TestTimelinePreservesRecordedFractionalSeconds(t *testing.T) {
	root := t.TempDir()
	timestamps := []string{"2026-09-09T13:44:47.4133304+08:00", "2026-09-09T13:44:47.9133304+08:00"}
	ids := []string{}
	for i, timestamp := range timestamps {
		name := fmt.Sprintf("fraction-%d", i)
		ids = append(ids, fixture(t, root, name, "v", map[string]string{"S": "帮我装机"}, "", 1))
		metaPath := filepath.Join(root, "artifacts", "eval", name, "meta.json")
		var meta evalsuite.ReportMeta
		raw, _ := os.ReadFile(metaPath)
		_ = json.Unmarshal(raw, &meta)
		meta.GeneratedAt, _ = time.Parse(time.RFC3339Nano, timestamp)
		writeFixtureJSON(t, metaPath, meta)
	}
	store := openStore(t, root)
	timeline := store.Timeline()
	if len(timeline.Items) != 2 || timeline.Items[0].Run.ID != ids[1] || timeline.Items[1].Run.ID != ids[0] {
		t.Fatalf("same-second runs incorrectly ordered: %+v", timeline.Items)
	}
	for i, item := range timeline.Items {
		if deref(item.Run.CreatedAt) != timestamps[1-i] {
			t.Fatalf("recorded precision lost: %+v", item.Run.CreatedAt)
		}
	}
	out, err := store.Provenance(ids[0])
	if err != nil || deref(out.Run.CreatedAt) != timestamps[0] {
		t.Fatalf("provenance changed recorded timestamp: %+v %v", out.Run.CreatedAt, err)
	}
}

func TestCommitFileProjectionIsBoundedAndRejectsPrivatePaths(t *testing.T) {
	for _, name := range []string{"../outside.go", "/etc/private.txt", "C:/private.go", "internal/../other.go", "web/.env.local", "artifacts/eval/meta.json", "docs/secrets.md", "internal/x\n.go", "internal/private.key", "web/node_modules/thing.js"} {
		if publicCommitPath(name) {
			t.Fatalf("private path accepted: %q", name)
		}
	}
	raw := []byte("M\x00internal/evalsuite/report.go\x00A\x00docs/中文.md\x00D\x00web/.env\x00")
	files, truncated, err := parseCommitFiles(raw)
	if err != nil || len(files) != 2 || !truncated || files[0].Status != "modified" || files[1].Label != "新增" {
		t.Fatalf("bad file projection: %+v %v %v", files, truncated, err)
	}
	var many bytes.Buffer
	for i := 0; i < maxCommitFiles+1; i++ {
		fmt.Fprintf(&many, "A\x00docs/file-%d.md\x00", i)
	}
	files, truncated, err = parseCommitFiles(many.Bytes())
	if err != nil || len(files) != maxCommitFiles || !truncated {
		t.Fatal("file count limit missing")
	}
	if _, _, err := parseCommitFiles([]byte("M\x00")); err == nil {
		t.Fatal("malformed file list accepted")
	}
}

func TestCommitProvenanceReadsRecordedObjectNotHEADAndCachesTitles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=", "-c", "user.name=Evaldesk Test", "-c", "user.email=evaldesk@example.invalid"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	git("init", "--quiet")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "来源.md"), []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "docs/来源.md")
	git("commit", "--quiet", "-m", "docs: 冻结时的提交")
	first := git("rev-parse", "HEAD")
	when := git("show", "--no-patch", "--format=%cI", first)
	git("commit", "--quiet", "--allow-empty", "-m", "current HEAD is not historical")
	ids := []string{}
	for _, name := range []string{"one", "two"} {
		id := fixture(t, root, name, "v", map[string]string{"S": "帮我装机"}, "", 1)
		ids = append(ids, id)
		metaPath := filepath.Join(root, "artifacts", "eval", name, "meta.json")
		var meta evalsuite.ReportMeta
		raw, _ := os.ReadFile(metaPath)
		_ = json.Unmarshal(raw, &meta)
		dirty := true
		meta.Code.CheckoutCommit = first
		meta.Code.CheckoutDirty = &dirty
		writeFixtureJSON(t, metaPath, meta)
	}
	before := git("status", "--porcelain=v1")
	store := openStore(t, root)
	timeline := store.Timeline()
	if len(timeline.Items) != 2 || len(store.commits) != 1 {
		t.Fatal("commit metadata was not deduplicated")
	}
	for _, entry := range timeline.Items {
		if deref(entry.Commit.Subject) != "docs: 冻结时的提交" || deref(entry.Commit.CommittedAt) != when {
			t.Fatalf("wrong commit evidence: %+v", entry.Commit)
		}
	}
	out, err := store.Provenance(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if !out.Commit.FilesAvailable || len(out.Commit.Files) != 1 || out.Commit.Files[0].Path != "docs/来源.md" || out.Commit.Files[0].Status != "added" {
		t.Fatalf("historical file list incorrect: %+v", out.Commit)
	}
	if !strings.Contains(strings.Join(out.Commit.Notes, " "), "未提交改动") || git("status", "--porcelain=v1") != before {
		t.Fatal("provenance limit missing or repository was modified")
	}
	missing := &savedRun{summary: RunSummary{Versions: Versions{Commit: strptr(strings.Repeat("e", 40))}}}
	if got := store.commitDetails(missing); got.Status != "unavailable" || got.FilesAvailable || got.Subject != nil {
		t.Fatalf("missing commit invented: %+v", got)
	}
}

func TestProvenanceAndTimelineHTTPRemainReadOnly(t *testing.T) {
	root := t.TempDir()
	id, _ := frozenFixture(t, root, "run", "L5-301")
	handler := Handler(openStore(t, root))
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/evaldesk/timeline", 200}, {"GET", "/api/evaldesk/provenance?run=" + id, 200}, {"GET", "/api/evaldesk/provenance", 400}, {"GET", "/api/evaldesk/provenance?run=../../.env", 404}, {"POST", "/api/evaldesk/provenance?run=" + id, 405},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:8086"+tc.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("wrong route behavior %s: %d %s", tc.path, w.Code, w.Body)
		}
	}
}
