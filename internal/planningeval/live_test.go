package planningeval

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type countingLive struct{ calls atomic.Int32 }

func (m *countingLive) Name() string { return "local-provider-double" }
func (m *countingLive) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls.Add(1)
		yield(&model.LLMResponse{Content: genai.NewContentFromText("response", genai.RoleModel), UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 2, CandidatesTokenCount: 1, TotalTokenCount: 3}}, nil)
	}
}

func TestLiveCeilingSharedAcrossRolesAndConcurrentAttempts(t *testing.T) {
	fake := &countingLive{}
	g := &gateway{models: Models{MaxCalls: 3}}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			role := "builder"
			if i%2 == 0 {
				role = "screening"
			}
			m := &tracedModel{g: g, role: role, live: fake}
			for range m.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
			}
		}(i)
	}
	wg.Wait()
	r := g.snapshot()
	admitted := 0
	for _, trace := range r.Trace {
		if trace.ProviderCalled {
			admitted++
			if trace.InputTokens == nil || *trace.InputTokens != 2 || trace.Tokens == nil || *trace.Tokens != 3 {
				t.Fatal("usage lost")
			}
		}
	}
	if fake.calls.Load() != 3 || admitted != 3 || len(r.Trace) != 12 {
		t.Fatalf("limit/denominator lost: %d %d %d", fake.calls.Load(), admitted, len(r.Trace))
	}
}

func TestJournalFailurePreventsProviderCall(t *testing.T) {
	fake := &countingLive{}
	g := &gateway{models: Models{MaxCalls: 1, Journal: func(any) error { return errors.New("disk unavailable") }}}
	m := &tracedModel{g: g, live: fake}
	for range m.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
	}
	if fake.calls.Load() != 0 || g.snapshot().Trace[0].ProviderCalled {
		t.Fatal("spent despite missing evidence journal")
	}
}

func TestLiveSuitesCannotContainOracleAnswers(t *testing.T) {
	raw, _ := frozen(t)
	// Inject live marker into a valid replay fixture; its model answers must reject it.
	raw = append([]byte(`{"live":true,`), raw[1:]...)
	if _, err := Load(raw); err == nil {
		t.Fatal("accepted oracle answers in live suite")
	}
}

func TestBudgetCeilingRejectsUnknownAndOverBudget(t *testing.T) {
	for _, tc := range []struct {
		total   string
		missing int
		pass    bool
	}{{"8000.00", 0, true}, {"8000.01", 0, false}, {"7000", 1, false}, {"NaN", 0, false}} {
		r := StepRecord{Result: &planning.Result{Quote: &validate.Quote{TotalCNY: tc.total, MissingCount: tc.missing}}}
		Grade(&r, Expect{BudgetCeilingCNY: "8000"}, nil)
		for _, c := range r.Checks {
			if c.Name == "budget_ceiling" && c.Pass != tc.pass {
				t.Fatalf("wrong budget grade: %+v", tc)
			}
		}
	}
}

func TestCPUChangeRequiresDifferentSelectionAndPreviousDraft(t *testing.T) {
	raw, err := os.ReadFile("../planning/testdata/budget_accounting_recording.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ Result planning.Result }
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	prior := StepRecord{Result: &saved.Result}
	for _, changed := range []bool{false, true} {
		var draft map[string]any
		if err := json.Unmarshal(saved.Result.Draft, &draft); err != nil {
			t.Fatal(err)
		}
		if changed {
			draft["selection"].(map[string]any)["cpu"] = "cpu-r7-5700x"
		}
		encoded, _ := json.Marshal(draft)
		for _, previous := range []*StepRecord{nil, &prior} {
			r := StepRecord{Result: &planning.Result{Draft: encoded}, State: schemas.RequirementState{NextAction: "collect"}}
			Grade(&r, Expect{CPUChanged: true, NextAction: "plan"}, previous)
			for _, c := range r.Checks {
				if c.Name == "changed_cpu" && c.Pass != (changed && previous != nil) {
					t.Fatalf("CPU change grade: changed=%v previous=%v check=%+v", changed, previous != nil, c)
				}
				if c.Name == "next_action" && c.Pass {
					t.Fatal("continued collection counted as execution")
				}
			}
		}
	}
}

func TestPreserveEssentialPartsAllowsCheapSwapsButNotCapacityCuts(t *testing.T) {
	raw, err := os.ReadFile("../planning/testdata/budget_accounting_recording.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ Result planning.Result }
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		edit  func(parts map[string]any)
		valid bool
	}{
		{"cheaper_same_capacity_psu", func(parts map[string]any) { parts["psu"] = "psu-cheap-650w" }, true},
		{"memory_capacity_upgrade", func(parts map[string]any) { parts["memory"] = "mem-kit-32-3600" }, true},
		{"memory_capacity_cut", func(parts map[string]any) { parts["memory"] = "mem-stick-8-3200" }, false},
		{"gpu_tier_change", func(parts map[string]any) { parts["gpu"] = "gpu-cheap-5060" }, false},
	} {
		var draft map[string]any
		if err := json.Unmarshal(saved.Result.Draft, &draft); err != nil {
			t.Fatal(err)
		}
		tc.edit(draft["selection"].(map[string]any))
		encoded, _ := json.Marshal(draft)
		r := StepRecord{Result: &planning.Result{Draft: encoded}}
		Grade(&r, Expect{PreserveEssentialParts: true}, &StepRecord{Result: &saved.Result})
		for _, c := range r.Checks {
			if c.Name == "preserved_essential_parts" && c.Pass != tc.valid {
				t.Fatalf("wrong essential parts grade for %s: %+v", tc.name, c)
			}
		}
	}
}

func TestPreservedPartsIgnoreNewReferenceButDetectHardwareChanges(t *testing.T) {
	raw, err := os.ReadFile("../planning/testdata/budget_accounting_recording.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ Result planning.Result }
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"cpu_only", "memory", "quantity"} {
		var draft map[string]any
		if err := json.Unmarshal(saved.Result.Draft, &draft); err != nil {
			t.Fatal(err)
		}
		draft["build_ref"] = "new-upgrade-draft"
		parts := draft["selection"].(map[string]any)
		parts["cpu"] = "cpu-r7-5700x"
		switch change {
		case "memory":
			parts["memory"] = "mem-other"
		case "quantity":
			parts["ssd"].([]any)[0].(map[string]any)["quantity"] = 2
		}
		encoded, _ := json.Marshal(draft)
		r := StepRecord{Result: &planning.Result{Draft: encoded}}
		Grade(&r, Expect{PreserveOtherParts: true}, &StepRecord{Result: &saved.Result})
		for _, c := range r.Checks {
			if c.Name == "preserved_other_parts" && c.Pass != (change == "cpu_only") {
				t.Fatalf("wrong part preservation grade for %s: %+v", change, c)
			}
		}
	}
}
