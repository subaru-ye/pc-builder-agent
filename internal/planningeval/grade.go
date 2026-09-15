package planningeval

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
func toolActions(r StepRecord) []string {
	var actions []string
	for _, t := range r.Trace {
		if t.Response == nil {
			continue
		}
		for _, p := range t.Response.Parts {
			if p != nil && p.FunctionCall != nil {
				a, _ := p.FunctionCall.Args["action"].(string)
				actions = append(actions, a)
			}
		}
	}
	return actions
}
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func Grade(r *StepRecord, e Expect, previous *StepRecord) {
	check := func(name string, pass bool, detail any) {
		r.Checks = append(r.Checks, Check{name, pass, fmt.Sprint(detail)})
	}
	check("run_completed", r.Error == "", r.Error)
	check("version_count", r.Versions == e.Versions, r.Versions)
	if e.NextAction != "" {
		check("next_action", r.State.NextAction == e.NextAction, r.State.NextAction)
	}
	for name, want := range e.Fields {
		got, ok := r.State.Fields[name]
		if want.Status == "absent" {
			check("state:"+name, !ok, got)
			continue
		}
		match := ok && (want.Status == "" || got.Status == want.Status) && (want.Strength == "" || got.Strength == want.Strength) && (want.Kind == "" || got.Kind == want.Kind) && (len(want.Value) == 0 || jsonEqual(got.Value, want.Value))
		check("state:"+name, match, got)
		if ok && got.Status == "active" {
			check("source:"+name, got.Source != nil && got.Source.MessageID != "", got.Source)
		}
		if r.PlanningInput != nil {
			sent := r.PlanningInput.State.Fields[name]
			// PostgreSQL JSONB and the wire codec may format arrays/objects
			// differently. Compare all field metadata and values semantically.
			gotJSON, _ := json.Marshal(got)
			sentJSON, _ := json.Marshal(sent)
			check("builder_state:"+name, jsonEqual(gotJSON, sentJSON), sent)
		}
	}
	actions := toolActions(*r)
	for _, a := range e.RequireTools {
		executed := false
		if r.Result != nil && r.PlanningInput != nil {
			_, executed = r.Result.StageMS[a]
		}
		check("tool_required:"+a, contains(actions, a) && executed, actions)
	}
	for _, a := range e.ForbidTools {
		check("tool_forbidden:"+a, !contains(actions, a), actions)
	}
	n := 0
	for _, t := range r.Trace {
		if t.Role == "builder" {
			n++
		}
	}
	if e.BuilderCalls != nil {
		check("builder_calls", n == *e.BuilderCalls, n)
	}
	for _, s := range e.ReplyForbidden {
		check("no_repeated_question:"+s, !strings.Contains(r.Reply, s), r.Reply)
	}
	for _, s := range e.ModelInputContains {
		found := false
		for _, t := range r.Trace {
			// Only actual conversation/tool input counts. A mention in the
			// system instructions cannot prove the data reached the model.
			var sent struct{ Contents json.RawMessage }
			_ = json.Unmarshal(t.Request, &sent)
			if strings.Contains(string(sent.Contents), s) {
				found = true
			}
		}
		check("actual_model_input:"+s, found, s)
	}
	// Only returned search candidates count, not the initial catalog or model prose.
	searched := map[string]bool{}
	searchIDs := map[string]bool{}
	for _, trace := range r.Trace {
		if trace.Role != "builder" || trace.Response == nil {
			continue
		}
		for _, part := range trace.Response.Parts {
			if part != nil && part.FunctionCall != nil && part.FunctionCall.Name == "planning_action" &&
				part.FunctionCall.Args["action"] == "search_local" && part.FunctionCall.ID != "" {
				searchIDs[part.FunctionCall.ID] = true
			}
		}
	}
	searchResponse := false
	for _, trace := range r.Trace {
		var request struct {
			Contents []struct {
				Parts []struct {
					FunctionResponse *struct {
						Name, ID string
						Response struct{ Candidates json.RawMessage }
					}
				}
			}
		}
		if trace.Role != "builder" || json.Unmarshal(trace.Request, &request) != nil {
			continue
		}
		for _, content := range request.Contents {
			for _, part := range content.Parts {
				response := part.FunctionResponse
				if response == nil || response.Name != "planning_action" || !searchIDs[response.ID] {
					continue
				}
				var candidates []struct{ ID string }
				if len(response.Response.Candidates) == 0 || json.Unmarshal(response.Response.Candidates, &candidates) != nil {
					continue
				}
				searchResponse = true
				for _, candidate := range candidates {
					searched[candidate.ID] = true
				}
			}
		}
	}
	for id, present := range e.SearchCandidates {
		check("search_candidate:"+id, searchResponse && searched[id] == present, present)
	}
	if e.Alternatives != nil {
		check("alternatives", len(r.State.Alternatives) == *e.Alternatives, len(r.State.Alternatives))
	}
	if e.Outcome != "" {
		check("final_outcome", r.Result != nil && r.Result.Outcome == e.Outcome, r.Result)
	}
	if e.Validation != "" {
		check("validation", r.Result != nil && r.Result.Validation != nil && string(r.Result.Validation.OverallStatus) == e.Validation, e.Validation)
	}
	if e.MissingPrices != nil {
		check("missing_prices", r.Result != nil && r.Result.Quote != nil && r.Result.Quote.MissingCount == *e.MissingPrices, r.Result)
	}
	if e.BudgetCeilingCNY != "" {
		within := false
		ceiling, valid := new(big.Rat).SetString(e.BudgetCeilingCNY)
		if valid && ceiling.Sign() > 0 && r.Result != nil && r.Result.Quote != nil && r.Result.Quote.MissingCount == 0 {
			total, ok := new(big.Rat).SetString(r.Result.Quote.TotalCNY)
			within = ok && total.Sign() > 0 && total.Cmp(ceiling) <= 0
		}
		check("budget_ceiling", within, e.BudgetCeilingCNY)
	}
	for _, s := range e.IssuesContain {
		found := false
		if r.Result != nil {
			for _, i := range r.Result.Issues {
				found = found || strings.Contains(i, s)
			}
		}
		check("specific_issue:"+s, found, s)
	}
	for id, specs := range e.CandidateSpecs {
		var actual map[string]json.RawMessage
		if r.Result != nil {
			for _, c := range r.Result.Candidates {
				if c.ID == id {
					_ = json.Unmarshal(c.Specs, &actual)
				}
			}
		}
		for k, v := range specs {
			check("candidate_fact:"+id+":"+k, jsonEqual(actual[k], v), string(actual[k]))
		}
	}
	if e.CPU != "" {
		ok := false
		if r.Result != nil {
			d, err := schemas.DecodeBuildDraft(r.Result.Draft)
			ok = err == nil && d.Selection.CPU == e.CPU
		}
		check("selected_cpu", ok, e.CPU)
	}
	if e.PreserveOtherParts {
		ok := false
		if r.Result != nil && previous != nil && previous.Result != nil {
			a, ea := schemas.DecodeBuildDraft(r.Result.Draft)
			b, eb := schemas.DecodeBuildDraft(previous.Result.Draft)
			a.Selection.CPU = b.Selection.CPU
			// Each new draft may have its own reference; it is not a part.
			a.Selection.BuildRef = b.Selection.BuildRef
			ok = ea == nil && eb == nil && reflect.DeepEqual(a.Selection, b.Selection)
		}
		check("preserved_other_parts", ok, "")
	}
	if e.CPUChanged {
		ok := false
		if r.Result != nil && previous != nil && previous.Result != nil {
			a, ea := schemas.DecodeBuildDraft(r.Result.Draft)
			b, eb := schemas.DecodeBuildDraft(previous.Result.Draft)
			ok = ea == nil && eb == nil && a.Selection.CPU != "" && a.Selection.CPU != b.Selection.CPU
		}
		// Identity change proves execution, not a performance improvement.
		check("changed_cpu", ok, "performance benefit needs separate evidence review")
	}
	// A delivered result must be server linked, not merely model 'ready'.
	if r.Result != nil && r.Result.Outcome == "ready" {
		check("server_delivery", r.Result.Delivery != nil && r.Result.Delivery.Status == "delivered" && r.Result.BuildVersion > 0, r.Result.Delivery)
	}
	r.Classification = "continued_collection"
	if r.Error != "" {
		r.Classification = "technical_fault"
	} else if r.Kind == "refresh" || r.Kind == "retry" {
		r.Classification = "read_or_retry"
	} else if r.Kind == "edit" {
		r.Classification = "requirement_edit"
	} else if r.PlanningInput != nil && r.Result != nil {
		switch r.Result.Outcome {
		case "ready":
			r.Classification = "delivered"
		case "clarify":
			r.Classification = "clarification"
		case "proposal":
			r.Classification = "stalled"
			if len(r.Result.Issues) > 0 && len(r.Result.Candidates) > 0 && len(e.IssuesContain) > 0 {
				r.Classification = "pending_with_evidence"
			}
		}
	}
	if r.Classification == "stalled" {
		check("meaningful_planning_progress", false, "proposal needs concrete candidates and independently expected unresolved issues")
	}
	for _, c := range r.Checks {
		if !c.Pass && r.Error == "" {
			r.Classification = "behavior_failure"
			break
		}
	}
}
