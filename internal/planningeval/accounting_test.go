package planningeval

import (
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"testing"
)

func TestUsageIncludesFailedAttemptAndExcludesUnsentProviderRequest(t *testing.T) {
	tokens := int32(9)
	r := Report{Mode: "live_models_offline_tools", Classifications: map[string]int{}, Cases: []CaseRecord{{Steps: []StepRecord{{
		PlanningInput:   &schemas.PlanningInput{},
		PlanningAttempt: &planning.Result{ToolCalls: 9, SearchCalls: 1, PageCalls: 2},
		Trace:           []Trace{{Role: "builder", ProviderCalled: true, Tokens: &tokens}, {Role: "builder", Error: "evaluation model-call limit reached"}},
		Error:           "interrupted", Classification: "technical_fault",
	}}}}}
	countUsage(&r)
	if r.ActualModelRequests != 1 || r.BuilderCalls != 2 || r.ToolCalls != 9 || r.SearchCalls != 1 || r.PageCalls != 2 || r.Tokens == nil || *r.Tokens != 9 {
		t.Fatalf("incomplete attempt lost usage: %+v", r)
	}
	if r.Cases[0].Steps[0].Result != nil {
		t.Fatal("diagnostic attempt became a saved proposal")
	}
}

func TestSentRequestWithoutUsageRemainsUnknown(t *testing.T) {
	for _, mode := range []string{"live_models_offline_tools", "offline_oracle"} {
		r := Report{Mode: mode, Classifications: map[string]int{}, Cases: []CaseRecord{{Steps: []StepRecord{{Trace: []Trace{{Role: "builder", ProviderCalled: mode == "live_models_offline_tools"}}}}}}}
		countUsage(&r)
		if r.Tokens != nil {
			t.Fatal("missing usage reported as a billed zero")
		}
	}
}
