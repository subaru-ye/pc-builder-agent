package evaldesk

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func TestCurrentPromptsWithoutArtifacts(t *testing.T) {
	store := newReqV2Store(t, t.TempDir())
	recorder := httptest.NewRecorder()
	Handler(store).ServeHTTP(recorder, httptest.NewRequest("GET", "http://127.0.0.1/api/evaldesk/requirement-v2/prompts", nil))
	if recorder.Code != 200 {
		t.Fatalf("prompts status: %d", recorder.Code)
	}
	var got ReqV2CurrentPrompts
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	builder, err := buildharness.PromptComponents()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{"screening": pipeline.RequirementPromptComponents(), "builder": builder}
	if got.Source != "evaldesk_binary" || len(got.Components) != len(want["screening"])+len(builder) {
		t.Fatal("missing prompt components or incorrect source")
	}
	var raw []evalsuite.PromptComponent
	for _, component := range got.Components {
		if component.Text != want[component.Role][component.Name] || component.Text == "" {
			t.Fatalf("original text changed: %s/%s", component.Role, component.Name)
		}
		hash := sha256.Sum256([]byte(component.Text))
		if component.SHA256 != fmt.Sprintf("%x", hash) {
			t.Fatal("component fingerprint mismatch")
		}
		raw = append(raw, component.PromptComponent)
	}
	_, identity, err := evalsuite.NewPromptEvidence(raw)
	if err != nil || identity.SHA256 != got.SHA256 {
		t.Fatal("combined fingerprint mismatch")
	}
}

func TestPromptGitHistoryReadsLiteralVersionsWithoutExecutingCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
	}
	run("init")
	path := filepath.Join(root, filepath.FromSlash(promptHistoryFiles[0].path))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for _, version := range []struct{ prompt, code string }{{"旧版\n逐字原文", "old"}, {"旧版\n逐字原文", "unrelated"}, {"新版\n保留换行", "new"}} {
		raw := "package pipeline\nconst requirementStateInstruction = " + strconv.Quote(version.prompt) + "\nconst formatFallbackInstruction = `格式纠偏`\nfunc init(){ panic(" + strconv.Quote(version.code) + ") }\n"
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		run("add", "--", promptHistoryFiles[0].path)
		run("-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "core.hooksPath=", "commit", "-m", version.code)
	}
	history := newReqV2Store(t, root).PromptVersionsReqV2()
	if len(history.Versions) != 2 {
		t.Fatalf("expected two content versions: %+v", history)
	}
	for _, version := range history.Versions {
		if version.Source != "git" || version.Role != "screening" || len(version.Components) != 2 {
			t.Fatal("incorrect source or components")
		}
		if version.Subject == "unrelated" {
			t.Fatal("unrelated code commit presented as prompt version")
		}
		if !strings.Contains(version.Components[1].Text, "\n") {
			t.Fatal("original line break lost")
		}
	}
	// 只按完整提交与角色绑定审阅，不把新提交或 Builder 说明挂到 Screening。
	var review ReqV2PromptReview
	if err := json.Unmarshal([]byte(`{"title":"修复需求规则","reason":"来自该提交的故障证据","changes":"具体规则调整","verification":{"kind":"tests_added","summary":"只确认新增了测试","limitation":"不代表模型效果"}}`), &review); err != nil {
		t.Fatal(err)
	}
	for _, version := range history.Versions {
		if version.Subject == "new" {
			review.Commit, review.Role = version.Commit, "screening"
		}
	}
	docDir := filepath.Join(root, "docs", "eval")
	if err := os.MkdirAll(docDir, 0755); err != nil {
		t.Fatal(err)
	}
	rawReview, _ := json.Marshal(map[string]any{"schema_version": 1, "reviews": []ReqV2PromptReview{review}})
	if err := os.WriteFile(filepath.Join(docDir, "prompt-iterations.json"), rawReview, 0644); err != nil {
		t.Fatal(err)
	}
	history = newReqV2Store(t, root).PromptVersionsReqV2()
	for _, version := range history.Versions {
		if (version.Review != nil) != (version.Commit == review.Commit && version.Role == review.Role) {
			t.Fatal("review associated by wrong identity")
		}
	}
	review.Role = "builder"
	rawReview, _ = json.Marshal(map[string]any{"schema_version": 1, "reviews": []ReqV2PromptReview{review}})
	if err := os.WriteFile(filepath.Join(docDir, "prompt-iterations.json"), rawReview, 0644); err != nil {
		t.Fatal(err)
	}
	for _, version := range newReqV2Store(t, root).PromptVersionsReqV2().Versions {
		if version.Review != nil {
			t.Fatal("Builder review leaked into Screening")
		}
	}
}

func TestInvalidPromptReviewsDoNotHideOriginalHistory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "eval")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{`, `{"schema_version":2}`, `{"schema_version":1,"reviews":[{"commit":"not-a-hash","role":"screening","title":"x","reason":"y"}]}`, `{"schema_version":1,"reviews":[{"commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","role":"screening","title":"x","reason":"y","verification":{"kind":"passed"}}]}`} {
		if err := os.WriteFile(filepath.Join(dir, "prompt-iterations.json"), []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		index, note := newReqV2Store(t, root).promptReviews()
		if len(index) != 0 || note == "" {
			t.Fatal("invalid metadata accepted or failure hidden")
		}
	}
}

func TestSavedPromptVersionsValidateAndGroupRuns(t *testing.T) {
	root := t.TempDir()
	snapshot, identity, err := evalsuite.NewPromptEvidence([]evalsuite.PromptComponent{{Role: "screening", Name: "system", Text: "冻结的实际原文"}})
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, name := range []string{"a", "b"} {
		fixture := writeRun(t, root, name, func(plan, report, manifest, gates map[string]any) { plan["prompts"] = identity })
		if err := snapshot.Write(fixture.Dir); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, fixture.Dir)
	}
	store := newReqV2Store(t, root)
	versions := store.PromptVersionsReqV2()
	if len(versions.Versions) != 1 || len(versions.Versions[0].Runs) != 2 || versions.Versions[0].Source != "run_snapshot" {
		t.Fatalf("same text not grouped: %+v", versions)
	}
	for _, run := range store.RunsReqV2().Runs {
		if run.PromptSHA == "" || run.Evidence.Status == "invalid" {
			t.Fatal("verified prompt declaration lost")
		}
	}
	for _, dir := range dirs {
		if err := os.WriteFile(filepath.Join(dir, "prompts.json"), []byte(`{"schema_version":1,"components":[{"role":"screening","name":"system","text":"被篡改"}]}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	versions = store.PromptVersionsReqV2()
	if len(versions.Versions) != 0 || len(versions.Notes) == 0 {
		t.Fatal("tampered prompt accepted")
	}
	for _, run := range store.RunsReqV2().Runs {
		if run.Evidence.Status != "invalid" {
			t.Fatal("changed prompt evidence did not invalidate cached summary")
		}
	}
}

func TestPromptSnapshotRejectsUndeclaredPathAndSchema(t *testing.T) {
	snapshot, identity, err := evalsuite.NewPromptEvidence([]evalsuite.PromptComponent{{Role: "screening", Name: "system", Text: "原文"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snapshot)
	identity.SnapshotFile = "../outside.json"
	if _, err := verifyPromptSnapshot(raw, &identity); err == nil {
		t.Fatal("untrusted snapshot file accepted")
	}
	identity.SnapshotFile = "prompts.json"
	snapshot.SchemaVersion = 2
	raw, _ = json.Marshal(snapshot)
	if _, err := verifyPromptSnapshot(raw, &identity); err == nil {
		t.Fatal("unsupported snapshot schema accepted")
	}
}
