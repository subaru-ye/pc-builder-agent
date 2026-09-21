package planningeval

import (
	"encoding/json"
	"os"
	"testing"
)

// RegradeFixture recomputes checks of a saved live report against a suite's
// current expects. Enabled only via REGRADE_REPORT / REGRADE_SUITE /
// REGRADE_OUT so it never runs in CI.
func TestRegradeFixture(t *testing.T) {
	report, suite, out := os.Getenv("REGRADE_REPORT"), os.Getenv("REGRADE_SUITE"), os.Getenv("REGRADE_OUT")
	if report == "" || suite == "" || out == "" {
		t.Skip("manual regrade helper: set REGRADE_REPORT, REGRADE_SUITE, REGRADE_OUT")
	}
	var doc struct {
		Cases []struct {
			ID    string       `json:"id"`
			Steps []StepRecord `json:"steps"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var fsuite struct {
		Cases []struct {
			ID    string `json:"id"`
			Steps []Step `json:"steps"`
		} `json:"cases"`
	}
	sraw, err := os.ReadFile(suite)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sraw, &fsuite); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != len(fsuite.Cases) {
		t.Fatalf("case count mismatch: %d vs %d", len(doc.Cases), len(fsuite.Cases))
	}
	for ci := range doc.Cases {
		if doc.Cases[ci].ID != fsuite.Cases[ci].ID {
			t.Fatalf("case id mismatch: %s vs %s", doc.Cases[ci].ID, fsuite.Cases[ci].ID)
		}
		steps, expects := doc.Cases[ci].Steps, fsuite.Cases[ci].Steps
		if len(steps) != len(expects) {
			t.Fatalf("step count mismatch in %s", doc.Cases[ci].ID)
		}
		for si := range steps {
			steps[si].Checks = nil
			var prev *StepRecord
			if si > 0 {
				prev = &steps[si-1]
			}
			Grade(&steps[si], expects[si].Expect, prev)
		}
	}
	encoded, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("regraded report written to %s", out)
}
