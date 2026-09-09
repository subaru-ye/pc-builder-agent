package evaldesk

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func modelRun(config map[string]any) *savedRun {
	return &savedRun{meta: evalsuite.ReportMeta{Models: map[string]map[string]any{"builder": config}}}
}

func compareModelSummary(a, b *savedRun) ChangeSummary {
	return modelChanges(a, b, []Condition{condition("modelParameters", "模型完整配置摘要", configHash(a), configHash(b))})
}

func TestModelChangesReadableAndWhitelisted(t *testing.T) {
	a := modelRun(map[string]any{"model": "qwen3.8-max-0902", "provider": "bailian", "timeout": "3m0s", "max_retries": float64(0), "session_cache": true, "model_chain": []any{}, "reasoning_effort": "none", "base_host": "private.example", "api_key": "secret-original"})
	b := modelRun(map[string]any{"model": "deepseek-v4-flash-0731", "provider": "bailian", "timeout": "1m0s", "max_retries": float64(2), "session_cache": false, "model_chain": []any{"backup"}, "reasoning_effort": "high", "base_host": "different.private.example", "api_key": "secret-candidate"})
	out := compareModelSummary(a, b)
	raw, _ := json.Marshal(out)
	text := string(raw)
	for _, expected := range []string{"选配模型", "qwen3.8-max-0902", "deepseek-v4-flash-0731", "180 秒", "60 秒", "请求重试上限", "会话缓存", "自动切换模型", "推理强度", "未对前端开放"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing readable diff %q: %s", expected, text)
		}
	}
	for _, private := range []string{"base_host", "api_key", "private.example", "secret-original", "secret-candidate"} {
		if strings.Contains(text, private) {
			t.Fatalf("private config leaked: %s", text)
		}
	}
	if out.State != "changed" {
		t.Fatalf("wrong state: %+v", out)
	}
}

func TestPrivateOnlyAndMalformedModelChangesRemainVisible(t *testing.T) {
	for _, field := range []string{"base_host", "api_key", "unknown_setting", "model"} {
		t.Run(field, func(t *testing.T) {
			a := modelRun(map[string]any{"model": "fixed", field: map[string]any{"api_key": "private-a"}})
			b := modelRun(map[string]any{"model": "fixed", field: map[string]any{"api_key": "private-b"}})
			out := compareModelSummary(a, b)
			raw, _ := json.Marshal(out)
			if out.State != "changed" || !strings.Contains(string(raw), "未对前端开放") || strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "api_key") {
				t.Fatalf("hidden change lost or leaked: %s", raw)
			}
		})
	}
	if modelValue(map[string]any{"model_chain": []any{map[string]any{"api_key": "secret"}}}, "model_chain", "chain") != nil {
		t.Fatal("object in chain was exposed")
	}
}

func TestMissingModelValuesAndEquivalentRepresentationAreNotInvented(t *testing.T) {
	a, b := modelRun(map[string]any{"model": "fixed"}), modelRun(map[string]any{"model": "fixed", "session_cache": false})
	out := compareModelSummary(a, b)
	if len(out.Details) != 1 || out.Details[0].Baseline != nil || deref(out.Details[0].Candidate) != "关闭" || !strings.Contains(strings.Join(out.Notes, ""), "未记录不表示未设置") {
		t.Fatalf("missing was treated as false: %+v", out)
	}
	a, b = modelRun(map[string]any{"timeout": "60s"}), modelRun(map[string]any{"timeout": "1m"})
	out = compareModelSummary(a, b)
	if out.State != "changed" || !strings.Contains(out.Summary, "可读值相同") {
		t.Fatalf("serialization change was mislabeled: %+v", out)
	}
}

func testSource(t *testing.T, s *Store, name string) (*savedRun, savedSource) {
	t.Helper()
	dir := filepath.Join(s.root, "artifacts", "eval", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	r := &savedRun{dir: dir, summary: RunSummary{Versions: Versions{Binary: strptr(digest)}}}
	m := savedSource{Version: 1, Scope: "repository Go sources and go.mod/go.sum; no go:embed", BinarySHA256: digest, Files: map[string]string{"go.mod": digest, "go.sum": digest, "cmd/eval/main.go": digest}}
	return r, m
}

func saveSource(t *testing.T, r *savedRun, m savedSource) {
	t.Helper()
	raw, _ := json.Marshal(m.Files)
	m.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	writeFixtureJSON(t, filepath.Join(r.dir, "source.json"), m)
}

func TestSourceChangesRequireBoundAndValidatedManifests(t *testing.T) {
	s := openStore(t, t.TempDir())
	a, am := testSource(t, s, "a")
	b, bm := testSource(t, s, "b")
	bm.Files["cmd/eval/main.go"] = strings.Repeat("b", 64)
	bm.Files["internal/evalsuite/report.go"] = strings.Repeat("c", 64)
	saveSource(t, a, am)
	saveSource(t, b, bm)
	out := s.codeChanges(a, b, nil)
	if !strings.Contains(out.Summary, "2 个文件变化") {
		t.Fatalf("actual source diff missing: %+v", out)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*savedSource)
	}{
		{"unrelated_binary", func(m *savedSource) { m.BinarySHA256 = strings.Repeat("d", 64) }},
		{"traversal", func(m *savedSource) { m.Files["internal/../../private.go"] = strings.Repeat("a", 64) }},
		{"absolute", func(m *savedSource) { m.Files["C:/private.go"] = strings.Repeat("a", 64) }},
		{"wrong_scope", func(m *savedSource) { m.Scope = "partial" }},
		{"missing_build_input", func(m *savedSource) { delete(m.Files, "go.mod") }},
		{"invalid_file_digest", func(m *savedSource) { m.Files["cmd/eval/main.go"] = "not-a-digest" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, m := testSource(t, s, tc.name)
			tc.mutate(&m)
			saveSource(t, r, m)
			files, _ := s.sourceFiles(r)
			if files != nil {
				t.Fatal("untrusted source was compared")
			}
			out := s.codeChanges(a, r, nil)
			if !strings.Contains(strings.Join(out.Notes, ""), "候选源码清单") || strings.Contains(out.Summary, "个文件变化") {
				t.Fatalf("untrusted source difference claimed: %+v", out)
			}
		})
	}
	var tampered savedSource
	raw, _ := os.ReadFile(filepath.Join(b.dir, "source.json"))
	_ = json.Unmarshal(raw, &tampered)
	tampered.Files["go.mod"] = strings.Repeat("e", 64)
	writeFixtureJSON(t, filepath.Join(b.dir, "source.json"), tampered)
	if files, _ := s.sourceFiles(b); files != nil {
		t.Fatal("tampered manifest digest accepted")
	}
}

func TestCommitMissingInvalidAndDirtyAreExplicit(t *testing.T) {
	s := openStore(t, t.TempDir())
	for _, hash := range []string{"--help", "HEAD", strings.Repeat("f", 40)} {
		text := deref(s.commitDescription(strptr(hash)))
		if text == "" || strings.Contains(text, s.root) {
			t.Fatalf("missing commit was invented or leaked path: %q", text)
		}
	}
	a, _ := testSource(t, s, "a")
	b, _ := testSource(t, s, "b")
	dirty := true
	a.summary.Versions.Dirty = &dirty
	b.summary.Versions.Commit = strptr(strings.Repeat("f", 40))
	out := s.codeChanges(a, b, nil)
	text := strings.Join(out.Notes, " ")
	for _, expected := range []string{"启动时", "未提交改动", "基线源码清单未记录", "候选源码清单未记录", "不代表实际执行程序"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing provenance limitation %q: %+v", expected, out)
		}
	}
}

func TestLocalCommitTitleIsReadWithoutChangingRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=", "-c", "user.name=Evaldesk Test", "-c", "user.email=evaldesk@example.invalid"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	git("init", "--quiet")
	git("commit", "--quiet", "--allow-empty", "-m", "docs(eval): 只读提交说明")
	hash := git("rev-parse", "HEAD")
	before := git("status", "--porcelain=v1")
	text := deref(openStore(t, root).commitDescription(strptr(hash)))
	if !strings.Contains(text, "docs(eval): 只读提交说明") || strings.Contains(text, hash) || git("status", "--porcelain=v1") != before {
		t.Fatalf("Git title missing or repository modified: %q", text)
	}
}
