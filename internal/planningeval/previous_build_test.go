package planningeval

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestHistoricalBaseAndBatchProductFlow(t *testing.T) {
	dsn := os.Getenv("PLANNING_EVAL_DSN")
	if dsn == "" {
		t.Skip("isolated planning evaluation database required")
	}
	_, suite := frozen(t)
	c := suite.Cases[4] // Saved real complete proposal; publication + retry + refresh.
	final := c.Steps[1].Builder[len(c.Steps[1].Builder)-1].Parts[0].Text
	var output struct{ Draft json.RawMessage }
	if err := json.Unmarshal([]byte(final), &output); err != nil {
		t.Fatal(err)
	}
	draft, err := schemas.DecodeBuildDraft(output.Draft)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct{ Selection json.RawMessage }
	_ = json.Unmarshal(output.Draft, &wire)
	selection, _ := json.Marshal(map[string]any{"schema_version": 1, "build_ref": draft.BuildRef, "parts": wire.Selection})
	c.PreviousBuild = &PreviousBuildFixture{Selection: selection, Requirement: json.RawMessage(`{"schema_version":1,"budget_cny":6000,"use_case":{"type":"productivity"}}`), Source: "Saved real candidate selection; test historical precondition"}
	var update schemas.RequirementUpdate
	if err := json.Unmarshal(c.Steps[0].Screen, &update); err != nil {
		t.Fatal(err)
	}
	c.Steps[0].Kind, c.Steps[0].Edit, c.Steps[0].Screen = "edit", update.Operations, nil
	for i := range c.Steps {
		c.Steps[i].Expect.Versions++
	}
	// Reuse the saved CPU-upgrade protocol oracle with batched retrieval; keep
	// its selection, grading, refresh and idempotency assertions unchanged.
	batch := suite.Cases[0]
	batch.ID = "batch-cpu-upgrade"
	for i := range batch.Steps {
		step := &batch.Steps[i]
		if len(step.Builder) == 0 {
			continue
		}
		f := step.Builder[0].Parts[0].FunctionCall
		if f == nil || f.Args["action"] != "search_local" {
			t.Fatal("source fixture no longer begins with local lookup")
		}
		f.Args["action"] = "search_local_batch"
		f.Args["payload"] = `{"queries":[{"category":"cpu"},{"category":"motherboard"}]}`
		step.Expect.SearchCandidates = map[string]bool{"cpu-r7-5700x": true}
	}
	suite.Cases = []Case{c, batch}
	raw, _ := json.Marshal(suite)
	report, err := Run(context.Background(), dsn, suite, raw, Models{})
	if err != nil || report.Passed != 2 {
		for _, c := range report.Cases {
			for _, s := range c.Steps {
				for _, check := range s.Checks {
					if !check.Pass {
						t.Log(check.Name, check.Detail)
					}
				}
			}
		}
		t.Fatalf("historical base replay: passed=%d error=%v", report.Passed, err)
	}
	if report.ScreeningCalls != 2 || report.ActualModelRequests != 0 {
		t.Fatal("only the two batch chat messages should use the offline Screening oracle; no real providers")
	}
	for _, step := range report.Cases[0].Steps {
		found := false
		for _, check := range step.Checks {
			found = found || (check.Name == "immutable_version:1" && check.Pass)
		}
		if !found {
			t.Fatal("historical precondition was not checked for immutability")
		}
	}
	for _, step := range report.Cases[1].Steps {
		if step.Kind == "confirm" || step.Kind == "message" && step.Result != nil {
			if step.Result == nil || step.Result.ToolCalls != 3 || step.Result.ModelCalls != 3 {
				t.Fatalf("batch execution cost not persisted: %+v", step.Result)
			}
		}
	}
}

func TestMigratedHistoricalBuilderSuite(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy-builder-v1.5-20260915/suite.json")
	if err != nil {
		t.Fatal(err)
	}
	suite, err := Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(suite.Cases) != 31 || !suite.Live {
		t.Fatal("historical Builder scope lost")
	}
	parts := map[string]bool{}
	for _, p := range suite.Catalog.Candidates {
		parts[p.ID] = true
	}
	for _, c := range suite.Cases {
		for _, step := range c.Steps {
			for _, id := range step.Expect.SelectedParts {
				if !parts[id] {
					t.Fatalf("%s expected SKU not in frozen catalog: %s", c.ID, id)
				}
			}
			for _, ids := range step.Expect.SelectedOptions {
				if len(ids) == 0 {
					t.Fatalf("%s empty acceptable selection set", c.ID)
				}
				for _, id := range ids {
					if !parts[id] {
						t.Fatalf("%s unknown option %s", c.ID, id)
					}
				}
			}
		}
	}
}

func TestRecordedHistoricalSnapshotRequiresMatchingSelection(t *testing.T) {
	raw, err := os.ReadFile("testdata/current-123-20260915-r2/mechanisms/suite.json")
	if err != nil {
		t.Fatal(err)
	}
	suite, err := Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	f := *suite.Cases[1].PreviousBuild
	if f.Snapshot == nil || validatePreviousBuild(f) != nil {
		t.Fatal("recorded historical fixture invalid")
	}
	var selection map[string]any
	_ = json.Unmarshal(f.Selection, &selection)
	selection["parts"].(map[string]any)["cpu"] = "cpu-r7-5700x"
	f.Selection, _ = json.Marshal(selection)
	if validatePreviousBuild(f) == nil {
		t.Fatal("historical snapshot accepted for a different selection")
	}
}
