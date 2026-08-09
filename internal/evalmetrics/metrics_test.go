package evalmetrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordDisabledAndRedacted(t *testing.T) {
	t.Setenv("P10_METRICS_DIR", "")
	Record("api", "disabled", map[string]any{"value": 1})

	dir := t.TempDir()
	t.Setenv("P10_METRICS_DIR", dir)
	Record("api", "run", map[string]any{"session_fingerprint": Fingerprint("secret-session")})
	raw, err := os.ReadFile(filepath.Join(dir, "api.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-session") {
		t.Fatal("metrics 不得写入原 session ID")
	}
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("metrics 应为单行 JSON: %v", err)
	}
	if event.SchemaVersion != 1 || event.Component != "api" || event.Name != "run" {
		t.Fatalf("指标字段不符: %+v", event)
	}
}

func TestFingerprintStableAndSeparated(t *testing.T) {
	first := Fingerprint("a")
	second := Fingerprint("a")
	other := Fingerprint("b")
	if first != second || first == other {
		t.Fatal("指纹应稳定且隔离")
	}
}
