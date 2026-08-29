package pipeline

import (
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestHarnessVerdictCarriesAtomicSaveInputs(t *testing.T) {
	draft, err := schemas.DecodeBuildDraft([]byte(draftJSON))
	if err != nil {
		t.Fatal(err)
	}
	result := buildharness.BuildResult{
		Succeeded: true,
		Draft:     draft,
		Result: validate.Result{
			Report: schemas.ValidationReport{BuildRef: draft.BuildRef, OverallStatus: schemas.OverallPass},
			Quote:  validate.Quote{SnapshotDate: "2026-08-27", TotalCNY: "8000.00"},
		},
	}
	v, err := harnessVerdict(result)
	if err != nil {
		t.Fatal(err)
	}
	if !v.deliver || len(draftWireJSON(v.draft)) == 0 || v.reportJSON == "" || !strings.Contains(v.reportJSON, `"overall_status":"pass"`) {
		t.Fatalf("v2 保存桥接缺少原子输入: %+v", v)
	}
}
