package planningeval

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/genai"
)

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func requirementValueEqual(field string, a, b json.RawMessage) bool {
	if field != "owned_parts" {
		return jsonEqual(a, b)
	}
	// Normalize only the documented quantity default. Keep model spelling,
	// category, extra keys and actual quantities in the comparison.
	normalize := func(raw json.RawMessage) ([]map[string]any, error) {
		var parts []map[string]any
		if err := json.Unmarshal(raw, &parts); err != nil {
			return nil, err
		}
		for _, part := range parts {
			if part == nil {
				continue
			}
			if q, exists := part["quantity"]; !exists || q == float64(0) {
				part["quantity"] = float64(1)
			}
		}
		return parts, nil
	}
	x, ex := normalize(a)
	y, ey := normalize(b)
	return ex == nil && ey == nil && reflect.DeepEqual(x, y)
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

// Each fragment group must occur in one saved reference. Reply, history and
// field source quotes cannot substitute for actually retained reference content.
// This is a literal retention audit, not a claim of semantic equivalence.
func retainedReference(state schemas.RequirementState, fragments []string) bool {
	if len(fragments) == 0 {
		return false
	}
	matches := func(text string) bool {
		for _, fragment := range fragments {
			if fragment == "" || !strings.Contains(text, fragment) {
				return false
			}
		}
		return true
	}
	for name, field := range state.Fields {
		if (name == "notes" || strings.HasPrefix(name, "free.")) && field.Status == "active" && field.Kind == "context" && matches(string(field.Value)) {
			return true
		}
	}
	for _, alternative := range state.Alternatives {
		if matches(string(alternative.Value)) {
			return true
		}
	}
	for _, observation := range state.Observations {
		if !observation.Resolved && matches(observation.Text) {
			return true
		}
	}
	return false
}

// Only correlated tool results prove retrieval. A batch request, initial
// samples, pending queries and errors do not prove that any query ran.
func localSearchResults(r StepRecord) (map[string]bool, bool, bool) {
	ids := map[string]string{}
	for _, trace := range r.Trace {
		if trace.Role != "builder" || trace.Response == nil {
			continue
		}
		for _, part := range trace.Response.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			f := part.FunctionCall
			action, _ := f.Args["action"].(string)
			if f.Name == "planning_action" && f.ID != "" && (action == "search_local" || action == "search_local_batch") {
				ids[f.ID] = action
			}
		}
	}
	searched := map[string]bool{}
	responseSeen, batchExecuted := false, false
	for _, trace := range r.Trace {
		var request struct{ Contents []*genai.Content }
		if trace.Role != "builder" || json.Unmarshal(trace.Request, &request) != nil {
			continue
		}
		for _, content := range request.Contents {
			if content == nil {
				continue
			}
			for _, part := range content.Parts {
				if part == nil || part.FunctionResponse == nil {
					continue
				}
				f := part.FunctionResponse
				action := ids[f.ID]
				if f.Name != "planning_action" || action == "" {
					continue
				}
				type candidatesResult struct {
					Candidates json.RawMessage
					Error      string
				}
				var response struct {
					candidatesResult
					Results []struct{ Result candidatesResult }
				}
				raw, _ := json.Marshal(f.Response)
				if json.Unmarshal(raw, &response) != nil || response.Error != "" {
					continue
				}
				results := []candidatesResult{response.candidatesResult}
				if action == "search_local_batch" {
					results = nil
					for _, item := range response.Results {
						results = append(results, item.Result)
					}
				}
				for _, result := range results {
					var candidates []struct{ ID string }
					// Historical single-query empty results were encoded as null.
					// Batches use [] and must not treat a missing/null item as executed.
					if result.Error != "" || json.Unmarshal(result.Candidates, &candidates) != nil || action == "search_local_batch" && candidates == nil {
						continue
					}
					responseSeen = true
					batchExecuted = batchExecuted || action == "search_local_batch"
					for _, candidate := range candidates {
						searched[candidate.ID] = true
					}
				}
			}
		}
	}
	return searched, responseSeen, batchExecuted
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
		match := ok && (want.Status == "" || got.Status == want.Status) && (want.Strength == "" || got.Strength == want.Strength) && (want.Kind == "" || got.Kind == want.Kind) && (len(want.Value) == 0 || requirementValueEqual(name, got.Value, want.Value))
		if len(want.Contains) > 0 {
			var value string
			match = match && json.Unmarshal(got.Value, &value) == nil
			for _, fragment := range want.Contains {
				match = match && fragment != "" && strings.Contains(value, fragment)
			}
		}
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
	searched, searchResponse, batchExecuted := localSearchResults(*r)
	for _, a := range e.RequireTools {
		executed := false
		if r.Result != nil && r.PlanningInput != nil {
			_, executed = r.Result.StageMS[a]
		}
		requested := contains(actions, a) || a == "search_local" && batchExecuted
		check("tool_required:"+a, requested && executed, actions)
	}
	for _, a := range e.ForbidTools {
		attempted := contains(actions, a) || a == "search_local" && contains(actions, "search_local_batch")
		check("tool_forbidden:"+a, !attempted, actions)
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
	for id, present := range e.SearchCandidates {
		check("search_candidate:"+id, searchResponse && searched[id] == present, present)
	}
	if e.Alternatives != nil {
		check("alternatives", len(r.State.Alternatives) == *e.Alternatives, len(r.State.Alternatives))
	}
	for i, fragments := range e.RetainedReferences {
		check(fmt.Sprintf("retained_reference:%d", i+1), retainedReference(r.State, fragments), fragments)
	}
	if e.Outcome != "" {
		check("final_outcome", r.Result != nil && r.Result.Outcome == e.Outcome, r.Result)
	}
	if len(e.OutcomeOneOf) > 0 {
		check("allowed_outcome", r.Result != nil && contains(e.OutcomeOneOf, r.Result.Outcome), e.OutcomeOneOf)
	}
	if len(e.IssuesAny) > 0 {
		found := false
		if r.Result != nil {
			for _, issue := range r.Result.Issues {
				for _, expected := range e.IssuesAny {
					found = found || strings.Contains(issue, expected)
				}
			}
		}
		check("specific_issue_any", found, e.IssuesAny)
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
		if valid && ceiling.Sign() > 0 && r.Result != nil && r.Result.Quote != nil {
			quote := r.Result.Quote
			amount, missing := quote.TotalCNY, quote.MissingCount
			if e.PurchaseBudget && quote.PurchaseTotalCNY != nil {
				amount, missing = *quote.PurchaseTotalCNY, quote.PurchaseMissingCount
			}
			total, ok := new(big.Rat).SetString(amount)
			within = missing == 0 && ok && total.Sign() > 0 && total.Cmp(ceiling) <= 0
			if e.PurchaseBudget && quote.PurchaseTotalCNY == nil {
				within = false
			}
		}
		check("budget_ceiling", within, e.BudgetCeilingCNY)
	}
	// Selection facts are read from the chosen candidate, not its rationale or
	// the model's self-assessment. Locked parts also compare exact IDs.
	selected := map[string]planning.Candidate{}
	if r.Result != nil {
		if draft, err := schemas.DecodeBuildDraft(r.Result.Draft); err == nil {
			for _, id := range draft.Selection.SKUs() {
				for _, candidate := range r.Result.Candidates {
					if candidate.ID == id {
						selected[string(candidate.Category)] = candidate
					}
				}
			}
		}
	}
	for category, id := range e.SelectedParts {
		check("selected_part:"+category, selected[category].ID == id, selected[category].ID)
	}
	for category, ids := range e.SelectedOptions {
		check("selected_option:"+category, contains(ids, selected[category].ID), selected[category].ID)
	}
	for category, brand := range e.SelectedBrands {
		check("selected_brand:"+category, strings.EqualFold(selected[category].Brand, brand), selected[category].Brand)
	}
	for category, specs := range e.SelectedSpecs {
		var actual map[string]json.RawMessage
		_ = json.Unmarshal(selected[category].Specs, &actual)
		for key, value := range specs {
			check("selected_spec:"+category+":"+key, jsonEqual(actual[key], value), string(actual[key]))
		}
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
			if len(r.Result.Issues) > 0 && len(r.Result.Candidates) > 0 && (len(e.IssuesContain) > 0 || len(e.IssuesAny) > 0) {
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
