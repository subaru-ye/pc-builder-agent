package planningeval

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestHistoricalBaseProductFlow(t *testing.T) {
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
	suite.Cases = []Case{c}
	raw, _ := json.Marshal(suite)
	report, err := Run(context.Background(), dsn, suite, raw, Models{})
	if err != nil || report.Passed != 1 {
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
	if report.ScreeningCalls != 0 || report.ActualModelRequests != 0 {
		t.Fatal("typed fixture must not call Screening or real providers")
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
