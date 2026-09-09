package evaldesk

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Directory junctions are a Windows-only capability and do not require the
// symbolic-link privilege that is often unavailable on developer machines.
func TestWindowsJunctionCannotEscapeArtifactRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(root, "artifacts", "eval")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	writeFixtureJSON(t, filepath.Join(outside, "meta.json"), map[string]string{"private": "outside-root"})
	junction := filepath.Join(parent, "junction")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create test junction: %v (%s)", err, output)
	}
	store := openStore(t, root)
	if _, err := store.read(junction, "meta.json"); err == nil {
		t.Fatal("junction escaped the artifact root")
	}
	if len(store.Runs().Runs) != 0 {
		t.Fatal("junction directory was indexed")
	}
}
