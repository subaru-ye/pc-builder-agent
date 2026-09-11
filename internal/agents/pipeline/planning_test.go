package pipeline

import (
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"testing"
)

func TestPlanningA2APreservesOutcomeAndProgress(t *testing.T) {
	for _, outcome := range []string{"collect", "clarify", "proposal", "ready", "technical_fault"} {
		p, err := PlanningResultPart(planning.Result{SchemaVersion: 1, Outcome: outcome, ModelOutcome: "proposal", Delivery: &planning.Delivery{Status: "eligible", Issues: []string{}}, Reply: "可以继续讨论", Issues: []string{"等待规格"}, ModelCalls: 3, ToolCalls: 2, Evidence: []planning.Evidence{{CandidateID: "cpu-example", Field: "socket", Kind: "catalog"}}})
		if err != nil {
			t.Fatal(err)
		}
		got := ReadPlanningResult(p)
		if got == nil || got.Outcome != outcome || got.ModelCalls != 3 || len(got.Issues) != 1 || got.ModelOutcome != "proposal" || got.Delivery.Status != "eligible" || got.Evidence[0].Field != "socket" {
			t.Fatalf("A2A lost result: %+v", got)
		}
	}
}
