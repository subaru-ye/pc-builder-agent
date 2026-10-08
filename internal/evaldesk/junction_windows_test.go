package evaldesk

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Directory junctions are a Windows-only capability and do not require the
// symbolic-link privilege that is often unavailable on developer machines.
func TestWindowsJunctionCannotEscapeArtifactRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(root, "artifacts", "reqv2")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	plan := map[string]any{"mode": "deterministic", "grader_version": "reqv2-grader-v4"}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "plan.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "report.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(parent, "junction")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create test junction: %v (%s)", err, output)
	}
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(junction, "plan.json"); err == nil {
		t.Fatal("junction escaped the artifact root")
	}
	if runs := store.RunsReqV2().Runs; len(runs) != 0 {
		t.Fatalf("junction directory was indexed: %d runs", len(runs))
	}
}
