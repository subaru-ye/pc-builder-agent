package planningeval

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func frozen(t *testing.T) ([]byte, Suite) {
	t.Helper()
	b, err := os.ReadFile("testdata/proposal-review-20260915/suite.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(b)
	if err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestFrozenSuiteAndDrift(t *testing.T) {
	b, s := frozen(t)
	p, err := os.ReadFile("testdata/proposal-review-20260915/provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyProvenance(b, p); err != nil {
		t.Fatal(err)
	}
	if len(s.Cases) != 12 {
		t.Fatalf("unexpected frozen case count %d", len(s.Cases))
	}
	if VerifyProvenance(append(b, ' '), p) == nil {
		t.Fatal("byte drift accepted")
	}
	if _, err = Load(append(b, []byte("{}")...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	s.Cases = append(s.Cases, s.Cases[0])
	duplicate, _ := json.Marshal(s)
	if _, err = Load(duplicate); err == nil {
		t.Fatal("duplicate case accepted")
	}
	s.Cases = s.Cases[:len(s.Cases)-1]
	s.Cases[0].Steps[0].Kind = "publish"
	unknown, _ := json.Marshal(s)
	if _, err = Load(unknown); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestGraderRejectsFalseSuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record StepRecord
		expect Expect
		failed string
	}{
		{"lost preference", StepRecord{}, Expect{Fields: map[string]FieldExpect{"noise_pref": {Value: json.RawMessage(`"silent"`), Status: "active"}}}, "state:noise_pref"},
		{"claimed lookup without tool", StepRecord{Reply: "已检索"}, Expect{RequireTools: []string{"search_local"}}, "tool_required:search_local"},
		{"prompt mention is not context", StepRecord{Trace: []Trace{{Request: json.RawMessage(`{"config":{"systemInstruction":"CPU-5700X"},"contents":[]}`)}}}, Expect{ModelInputContains: []string{"CPU-5700X"}}, "actual_model_input:CPU-5700X"},
		{"unlinked ready", StepRecord{PlanningInput: &schemas.PlanningInput{}, Result: &planning.Result{Outcome: "ready"}}, Expect{Outcome: "ready"}, "server_delivery"},
		{"empty proposal", StepRecord{PlanningInput: &schemas.PlanningInput{}, Result: &planning.Result{Outcome: "proposal"}}, Expect{Outcome: "proposal"}, "meaningful_planning_progress"},
		{"unreviewed excuse", StepRecord{PlanningInput: &schemas.PlanningInput{}, Result: &planning.Result{Outcome: "proposal", Issues: []string{"无法满足"}, Candidates: []planning.Candidate{{ID: "cpu"}}}}, Expect{Outcome: "proposal"}, "meaningful_planning_progress"},
		{"repeated known question", StepRecord{Reply: "主板型号是什么？"}, Expect{ReplyForbidden: []string{"主板型号"}}, "no_repeated_question:主板型号"},
		{"missing quote", StepRecord{Result: &planning.Result{}}, Expect{MissingPrices: new(int)}, "missing_prices"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Grade(&tc.record, tc.expect, nil)
			found := false
			for _, c := range tc.record.Checks {
				if c.Name == tc.failed && !c.Pass {
					found = true
				}
			}
			if !found || tc.record.Classification != "behavior_failure" {
				t.Fatalf("false success: %+v", tc.record)
			}
		})
	}
}

func TestRefreshIsNotNewDelivery(t *testing.T) {
	r := StepRecord{Kind: "refresh", Versions: 1, Result: &planning.Result{Outcome: "ready", BuildVersion: 1, Delivery: &planning.Delivery{Status: "delivered"}}}
	Grade(&r, Expect{Versions: 1}, nil)
	if r.Classification != "read_or_retry" {
		t.Fatalf("counted historical delivery: %s", r.Classification)
	}
}

func TestBuilderStateComparisonPreservesSemanticsAndMetadata(t *testing.T) {
	field := schemas.RequirementField{Value: json.RawMessage(`["cpu", "motherboard"]`), Status: "active", Kind: "fact", Strength: "must", Source: &schemas.RequirementSource{Kind: "chat", MessageID: "message", Quote: "已有CPU和主板"}}
	for _, tc := range []struct {
		name   string
		change func(*schemas.RequirementField)
		pass   bool
	}{
		{"wire whitespace", func(f *schemas.RequirementField) { f.Value = json.RawMessage(`["cpu","motherboard"]`) }, true},
		{"value lost", func(f *schemas.RequirementField) { f.Value = json.RawMessage(`["cpu"]`) }, false},
		{"strength changed", func(f *schemas.RequirementField) { f.Strength = "prefer" }, false},
		{"source lost", func(f *schemas.RequirementField) { f.Source = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := field
			tc.change(&sent)
			r := StepRecord{State: schemas.RequirementState{Fields: map[string]schemas.RequirementField{"existing_parts": field}},
				PlanningInput: &schemas.PlanningInput{State: schemas.RequirementState{Fields: map[string]schemas.RequirementField{"existing_parts": sent}}}}
			Grade(&r, Expect{Fields: map[string]FieldExpect{"existing_parts": {Status: "active"}}}, nil)
			for _, check := range r.Checks {
				if check.Name == "builder_state:existing_parts" {
					if check.Pass != tc.pass {
						t.Fatalf("unexpected state comparison: %+v", check)
					}
					return
				}
			}
			t.Fatal("builder state check missing")
		})
	}
}

func TestFixtureTransportNeverFallsBack(t *testing.T) {
	client := &http.Client{Transport: fixtureTransport{pages: map[string]string{"https://fixture.invalid/spec": "AM4"}}}
	res, err := client.Get("https://fixture.invalid/spec")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "AM4" {
		t.Fatal("fixture lost")
	}
	if _, err = client.Get("https://unregistered.invalid/secret"); err == nil {
		t.Fatal("unknown destination accepted")
	}
}

func TestExhaustedOracleDoesNotInventResponse(t *testing.T) {
	g := &gateway{}
	m := &tracedModel{g: g, role: "builder"}
	errors := 0
	for response, err := range m.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if response != nil {
			t.Fatal("invented model response")
		}
		if err != nil {
			errors++
		}
	}
	r := g.snapshot()
	if errors != 1 || len(r.Trace) != 1 || r.Trace[0].ProviderCalled || r.Trace[0].Error == "" {
		t.Fatalf("missing failure trace: %+v", r)
	}
}

func TestRejectSharedDatabaseBeforeConnecting(t *testing.T) {
	for _, dsn := range []string{"", "postgres://localhost/pcbuilder", "postgres://remote.example/peval_test", "%broken"} {
		if Prepare(context.Background(), dsn, CatalogFixture{}) == nil {
			t.Fatalf("accepted shared DSN %q", dsn)
		}
	}
}

func TestSearchGraderRequiresActualCorrelatedToolResult(t *testing.T) {
	for _, tc := range []struct {
		name, action, request string
		present, pass         bool
	}{
		{"returned candidate", "search_local", `{"contents":[{"parts":[{"functionResponse":{"id":"lookup","name":"planning_action","response":{"candidates":[{"id":"new-cpu"}]}}}]}]}`, true, true},
		{"empty search proves absent", "search_local", `{"contents":[{"parts":[{"functionResponse":{"id":"lookup","name":"planning_action","response":{"candidates":[]}}}]}]}`, false, true},
		{"initial catalog is not search", "search_local", `{"contents":[{"parts":[{"text":"new-cpu"}]}]}`, true, false},
		{"no response cannot prove absent", "search_local", `{"contents":[]}`, false, false},
		{"search error cannot prove absent", "search_local", `{"contents":[{"parts":[{"functionResponse":{"id":"lookup","name":"planning_action","response":{"error":"unavailable"}}}]}]}`, false, false},
		{"different tool cannot prove search", "register_candidate", `{"contents":[{"parts":[{"functionResponse":{"id":"lookup","name":"planning_action","response":{"candidates":[{"id":"new-cpu"}]}}}]}]}`, true, false},
		{"unmatched response", "search_local", `{"contents":[{"parts":[{"functionResponse":{"id":"other","name":"planning_action","response":{"candidates":[{"id":"new-cpu"}]}}}]}]}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := StepRecord{Trace: []Trace{
				{Role: "builder", Response: &genai.Content{Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "lookup", Name: "planning_action", Args: map[string]any{"action": tc.action}}}}}},
				{Role: "builder", Request: json.RawMessage(tc.request)},
			}}
			Grade(&r, Expect{SearchCandidates: map[string]bool{"new-cpu": tc.present}}, nil)
			for _, check := range r.Checks {
				if check.Name == "search_candidate:new-cpu" {
					if check.Pass != tc.pass {
						t.Fatalf("unexpected grade: %+v", check)
					}
					return
				}
			}
			t.Fatal("search assertion was not evaluated")
		})
	}
}
