package evalsuite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func deliveredFixture(t *testing.T) (Case, CaseRecord) {
	t.Helper()
	raw, err := os.ReadFile("testdata/grader/delivered.json")
	if err != nil {
		t.Fatal(err)
	}
	var r CaseRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("testdata/cases/L1-001.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := decodeCase(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c, r
}

func TestIndependentGraderRejectsForgedDeliveries(t *testing.T) {
	for _, tt := range []struct {
		name, assertion string
		mutate          func(*CaseRecord)
	}{
		{"real delivery", "", func(*CaseRecord) {}},
		{"catalog membership is insufficient", "A6", func(r *CaseRecord) { r.Candidates.Groups = nil }},
		{"wrong candidate category", "A6", func(r *CaseRecord) {
			for i := range r.Candidates.Groups {
				r.Candidates.Groups[i].Category = schemas.CategorySSD
			}
		}},
		{"saved fail despite succeeded", "A10", func(r *CaseRecord) { r.Result.Result.Report.OverallStatus = schemas.OverallFail }},
		{"forged pass against incompatible specs", "A10", func(r *CaseRecord) {
			for i, p := range r.Snapshot.Catalog.Candidates {
				if p.Category == schemas.CategoryMotherboard {
					var spec map[string]any
					_ = json.Unmarshal(p.Specs, &spec)
					spec["socket"] = "AM5"
					r.Snapshot.Catalog.Candidates[i].Specs, _ = json.Marshal(spec)
				}
			}
		}},
		{"quote from stale price", "A7", func(r *CaseRecord) { price := "1.00"; r.Snapshot.Catalog.Candidates[0].PriceCNY = &price }},
		{"missing evidence", "E1", func(r *CaseRecord) { r.Snapshot.Catalog = nil }},
		{"wrong snapshot", "E1", func(r *CaseRecord) { r.Candidates.SnapshotDate = "2000-01-01" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, r := deliveredFixture(t)
			tt.mutate(&r)
			if !AssertCase(c, *r.Result, r.Snapshot).Passed {
				t.Fatal("反例应展示历史判卷确实漏过的交付")
			}
			v, err := GradeBuild(c, r, CurrentGraderVersion)
			if err != nil {
				t.Fatal(err)
			}
			if tt.assertion == "" {
				if !v.Passed {
					t.Fatalf("real delivery: %+v", v)
				}
				return
			}
			for _, f := range v.Failures {
				if f.ID == tt.assertion {
					return
				}
			}
			t.Fatalf("missing %s: %+v", tt.assertion, v)
		})
	}
}

func TestLegacyGradePreserved(t *testing.T) {
	c, r := deliveredFixture(t)
	r.Candidates = nil
	v, err := GradeBuild(c, r, "")
	if err != nil || !reflect.DeepEqual(v, AssertCase(c, *r.Result, r.Snapshot)) {
		t.Fatalf("legacy changed: %+v %v", v, err)
	}
	if _, err := GradeBuild(c, r, "unknown"); err == nil {
		t.Fatal("unknown grader accepted")
	}
}

func TestStoppedRunKeepsEveryCaseInDenominator(t *testing.T) {
	c, r := deliveredFixture(t)
	// 没有提供任何 Harness：若停止检查失效，会在调用前失败。
	seen := 0
	rows, err := RunCases(context.Background(), []Case{c}, Deps{GraderVersion: CurrentGraderVersion, Snapshot: r.Snapshot, Seeds: 3, StopReason: func() error { return fmt.Errorf("调用上限已耗尽，本题未执行") }, OnRecord: func(CaseRecord) { seen++ }})
	if err != nil || len(rows) != 3 || seen != 3 {
		t.Fatalf("lost stopped cases: %d %d %v", len(rows), seen, err)
	}
	s := Summarize(ReportMeta{RequestedSeeds: 3}, rows)
	if s.AllGreen() || s.Passed != 0 || s.Failed != 3 {
		t.Fatalf("stopped became passed: %+v", s)
	}
	for _, row := range rows {
		if row.RunErr == "" || len(row.Requirement) == 0 || row.Usage == nil || row.Usage.ModelCalls != 0 {
			t.Fatalf("missing evidence: %+v", row)
		}
	}
}
