package planning

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func completeRecording(t *testing.T) (schemas.PlanningInput, Result, recordedCatalog) {
	t.Helper()
	raw, err := os.ReadFile("testdata/complete_proposal_recording.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Input  schemas.PlanningInput
		Result Result
	}
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	c := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)}}}
	for _, p := range f.Result.Candidates {
		c.Candidates = append(c.Candidates, store.Candidate{SKU: p.ID, Category: p.Category, Brand: p.Brand, Model: p.Model, Specs: p.Specs, PriceCNY: p.Price})
	}
	return f.Input, f.Result, c
}

func TestSavedCompleteProposalAutomaticallyDelivers(t *testing.T) {
	input, record, catalog := completeRecording(t)
	if record.Outcome != "proposal" || len(record.Evidence) != 100 || record.SearchCalls != 0 {
		t.Fatal("recording changed")
	}
	m := &scriptedModel{respond: func(_ int, r *model.LLMRequest) *genai.Content {
		if !strings.Contains(r.Contents[0].Parts[0].Text, `free.workload_resolution`) {
			t.Fatal("state not sent to builder")
		}
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), input)
	if err != nil || got.Outcome != "ready" || got.ModelOutcome != "proposal" || got.Delivery.Status != "eligible" || got.ModelCalls != 1 || got.Quote.TotalCNY != "4579.90" || len(got.Validation.Checks) != 12 || len(got.Issues) != 0 {
		t.Fatalf("not delivered: %+v %v", got, err)
	}
}

func TestDeliveryHonorsProblemsButNotUnstatedPreferences(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*schemas.PlanningInput, *Result, *recordedCatalog)
		want   string
	}{
		{"unrequested-noise", func(_ *schemas.PlanningInput, r *Result, _ *recordedCatalog) {
			r.Assessments = append(r.Assessments, Assessment{Field: "noise_pref", Status: "unknown"})
		}, "ready"},
		{"soft-noise", func(i *schemas.PlanningInput, r *Result, _ *recordedCatalog) {
			i.State.Fields["noise_pref"] = schemas.RequirementField{Status: "active", Kind: "constraint", Strength: "prefer", Value: json.RawMessage(`"silent"`)}
			r.Assessments = append(r.Assessments, Assessment{Field: "noise_pref", Status: "unknown", Explanation: "不保证静音"})
		}, "ready"},
		{"must-noise", func(i *schemas.PlanningInput, _ *Result, _ *recordedCatalog) {
			i.State.Fields["noise_pref"] = schemas.RequirementField{Status: "active", Kind: "constraint", Strength: "must", Value: json.RawMessage(`"silent"`)}
		}, "proposal"},
		{"missing-assessment", func(_ *schemas.PlanningInput, r *Result, _ *recordedCatalog) { r.Assessments = nil }, "proposal"},
		{"missing-price", func(_ *schemas.PlanningInput, _ *Result, c *recordedCatalog) { c.Candidates[0].PriceCNY = nil }, "proposal"},
		{"compatibility-conflict", func(_ *schemas.PlanningInput, _ *Result, c *recordedCatalog) {
			for n := range c.Candidates {
				if c.Candidates[n].Category == schemas.CategoryMotherboard {
					c.Candidates[n].Specs = json.RawMessage(strings.Replace(string(c.Candidates[n].Specs), "AM4", "AM5", 1))
				}
			}
		}, "proposal"},
		{"necessary-question", func(_ *schemas.PlanningInput, r *Result, _ *recordedCatalog) {
			r.Outcome = "clarify"
			r.Reply = "旧硬盘是否需要保留？"
		}, "clarify"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, record, catalog := completeRecording(t)
			tc.change(&input, &record, &catalog)
			if record.Outcome == "proposal" {
				record.Outcome = "ready"
			} // A false ready claim cannot override facts.
			m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
				raw, _ := json.Marshal(record)
				return genai.NewContentFromText(string(raw), genai.RoleModel)
			}}
			got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
			if err != nil || got.Outcome != tc.want || (tc.want == "proposal" && len(got.Issues) == 0) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestEmptyProposalFeedbackCanFillAssessment(t *testing.T) {
	input, record, catalog := completeRecording(t)
	m := &scriptedModel{respond: func(n int, r *model.LLMRequest) *genai.Content {
		copy := record
		if n == 1 {
			copy.Assessments = nil
		} else if !strings.Contains(r.Contents[len(r.Contents)-1].Parts[0].Text, "预算仍待确认") {
			t.Fatal("missing assessment was not returned as feedback")
		}
		raw, _ := json.Marshal(copy)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), input)
	if err != nil || got.Outcome != "ready" || got.ModelCalls != 2 || len(got.Issues) != 0 {
		t.Fatalf("stale issues survived repair: %+v %v", got, err)
	}
}

func TestUnfinishedDeliveryRetainsExternalAlternatives(t *testing.T) {
	input, record, _ := completeRecording(t)
	record.Issues = []string{"需要讨论取舍"}
	x := execution{input: input, result: record, candidates: append([]Candidate{}, record.Candidates...), evidence: record.Evidence}
	alternative := record.Candidates[0]
	alternative.ID = "ext-alternative"
	alternative.External = true
	x.candidates = append(x.candidates, alternative)
	got := x.finish()
	if got.Outcome != "proposal" || len(got.Candidates) != len(record.Candidates)+1 {
		t.Fatal("lost external research progress")
	}
	x.result.Issues = nil
	got = x.finish()
	if got.Outcome != "ready" || len(got.Candidates) != len(record.Candidates) {
		t.Fatal("unselected alternative entered formal build")
	}
}
