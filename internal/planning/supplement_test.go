package planning

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestLocalSupplementRevalidatesAndSurvivesContinuation(t *testing.T) {
	input, record, catalog := completeRecording(t)
	var patch Candidate
	for i, c := range catalog.Candidates {
		if c.Category == schemas.CategoryMotherboard {
			var specs map[string]json.RawMessage
			_ = json.Unmarshal(c.Specs, &specs)
			patch = Candidate{ID: c.SKU, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: json.RawMessage(`{"memory_speed_max_mts":3200}`), Evidence: []string{"test-page"}, FieldEvidence: map[string]string{"model": "test-page", "specs.memory_speed_max_mts": "test-page"}, FieldQuotes: map[string]string{"specs.memory_speed_max_mts": "Memory speed 3200 MT/s"}}
			specs["memory_speed_max_mts"] = json.RawMessage(`null`)
			catalog.Candidates[i].Specs, _ = json.Marshal(specs)
		}
	}
	// Synthetic page fact checks the tool mechanism only, not manufacturer truth.
	page := Evidence{ID: "test-page", URL: "https://example.org/test-only", Kind: "page", Text: patch.Model + " Memory speed 3200 MT/s", CapturedAt: "2026-09-15"}
	before, _ := json.Marshal(catalog)
	stateBefore, _ := json.Marshal(input)
	modelFor := func(supplement bool) *scriptedModel {
		return &scriptedModel{respond: func(n int, req *model.LLMRequest) *genai.Content {
			if n == 1 && supplement {
				raw, _ := json.Marshal(patch)
				return &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "planning_action", Args: map[string]any{"action": "register_candidate", "payload": string(raw)}}}}}
			}
			raw, _ := json.Marshal(record)
			return genai.NewContentFromText(string(raw), genai.RoleModel)
		}}
	}
	missing, err := (Runner{Model: modelFor(false), Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil || missing.Outcome != "proposal" || missing.Validation.OverallStatus == schemas.OverallPass {
		t.Fatalf("missing specification passed: %+v %v", missing, err)
	}
	prior, _ := json.Marshal(Result{Evidence: []Evidence{page}})
	input.PreviousProposal = prior
	got, err := (Runner{Model: modelFor(true), Catalog: catalog, MaxTurns: 2}).Run(context.Background(), input)
	if err != nil || got.Outcome != "ready" || got.Quote.TotalCNY != record.Quote.TotalCNY {
		t.Fatalf("supplement not used: %+v %v", got, err)
	}
	var saved Candidate
	for _, c := range got.Candidates {
		if c.ID == patch.ID {
			saved = c
		}
	}
	if saved.External || saved.Price == nil || saved.FieldEvidence["memory_speed_max_mts"] != "supplement:"+patch.ID+":memory_speed_max_mts" || saved.FieldQuotes["memory_speed_max_mts"] == "" {
		t.Fatalf("local quote/provenance lost: %+v", saved)
	}
	found := false
	for _, e := range got.Evidence {
		found = found || (e.ID == saved.FieldEvidence["memory_speed_max_mts"] && e.CandidateID == patch.ID && e.Field == "memory_speed_max_mts" && e.Kind == "page" && e.URL == page.URL && e.Text == saved.FieldQuotes[e.Field])
	}
	if !found {
		t.Fatal("field-specific page provenance missing")
	}
	input.PreviousProposal, _ = json.Marshal(got)
	continued, err := (Runner{Model: modelFor(false), Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil || continued.Outcome != "ready" || !reflect.DeepEqual(continued.Validation, got.Validation) {
		t.Fatalf("continuation lost supplement: %+v %v", continued, err)
	}
	after, _ := json.Marshal(catalog)
	input.PreviousProposal = nil
	stateAfter, _ := json.Marshal(input)
	if string(before) != string(after) || string(stateBefore) != string(stateAfter) {
		t.Fatal("catalog or confirmed requirements mutated")
	}
	// A newly published conflicting fact must win over an older session patch.
	for i := range catalog.Candidates {
		if catalog.Candidates[i].SKU == patch.ID {
			catalog.Candidates[i].Specs = json.RawMessage(strings.Replace(string(catalog.Candidates[i].Specs), `"memory_speed_max_mts":null`, `"memory_speed_max_mts":3000`, 1))
		}
	}
	input.PreviousProposal, _ = json.Marshal(got)
	conflict, err := (Runner{Model: modelFor(false), Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil || conflict.Outcome != "proposal" {
		t.Fatalf("stale supplement overrode current facts: %+v %v", conflict, err)
	}
}

func TestLocalSupplementRejectsUntrustedOrConflictingFacts(t *testing.T) {
	for _, mode := range []string{"search", "local_snapshot", "wrong_model", "conflict", "missing_quote", "unsupported_number", "wrong_type", "unknown_id"} {
		t.Run(mode, func(t *testing.T) {
			price := "599.00"
			old := Candidate{ID: "cpu-test", Category: schemas.CategoryCPU, Brand: "Test", Model: "Test CPU", Specs: json.RawMessage(`{"socket":"AM4","tdp_w":null}`), Price: &price}
			c := Candidate{ID: old.ID, Category: old.Category, Brand: old.Brand, Model: old.Model, Specs: json.RawMessage(`{"tdp_w":65}`), Evidence: []string{"page"}, FieldEvidence: map[string]string{"model": "page", "tdp_w": "page"}, FieldQuotes: map[string]string{"tdp_w": "TDP 65 W"}}
			page := Evidence{ID: "page", Kind: "page", Text: "Test CPU socket AM5 TDP 65 W"}
			switch mode {
			case "search", "local_snapshot":
				page.Kind = mode
			case "wrong_model":
				c.Model = "Other CPU"
			case "conflict":
				c.Specs = json.RawMessage(`{"socket":"AM5","tdp_w":65}`)
				c.FieldEvidence["socket"] = "page"
				c.FieldQuotes["socket"] = "socket AM5"
			case "missing_quote":
				c.FieldQuotes = nil
			case "unsupported_number":
				c.Specs = json.RawMessage(`{"tdp_w":999}`)
			case "wrong_type":
				c.Specs = json.RawMessage(`{"tdp_w":"65"}`)
			case "unknown_id":
				c.ID = "cpu-absent"
			}
			x := execution{candidates: []Candidate{old}, evidence: []Evidence{page}}
			err := x.register(c)
			if mode != "missing_quote" && mode != "unsupported_number" && err == nil {
				t.Fatal("invalid supplement accepted")
			}
			var specs map[string]any
			_ = json.Unmarshal(x.candidates[0].Specs, &specs)
			if specs["tdp_w"] != nil || specs["socket"] != "AM4" || *x.candidates[0].Price != price {
				t.Fatal("unverified facts or quote changed")
			}
		})
	}
}

func TestUnverifiedDraftCannotClaimDeliveryThroughClarification(t *testing.T) {
	input, record, catalog := completeRecording(t)
	catalog.Candidates[0].Specs = json.RawMessage(`{}`)
	for _, outcome := range []string{"proposal", "ready", "clarify", "collect"} {
		record.Outcome, record.Reply = outcome, "全部兼容，性能保证，已经正式交付"
		m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
			raw, _ := json.Marshal(record)
			return genai.NewContentFromText(string(raw), genai.RoleModel)
		}}
		got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
		if err != nil || got.Outcome == "ready" || strings.Contains(got.Reply, "性能保证") || len(got.Issues) == 0 || got.Delivery.Status != "unresolved" {
			t.Fatalf("unsafe result: %+v %v", got, err)
		}
	}
}

func TestUnknownThermalCapacityRemovesUnsupportedAssurances(t *testing.T) {
	input, record, catalog := completeRecording(t)
	var draft map[string]any
	if err := json.Unmarshal(record.Draft, &draft); err != nil {
		t.Fatal(err)
	}
	draft["rationale"].(map[string]any)["cooler"] = "360 水冷压制这颗处理器绰绰有余"
	record.Draft, _ = json.Marshal(draft)
	record.Issues = []string{"散热能力未知，但这款水冷完全足够"}
	record.Assumptions = append(record.Assumptions, "散热数据缺失不影响实际安全性")
	selected := draft["selection"].(map[string]any)["cooler"].(string)
	for i := range catalog.Candidates {
		if catalog.Candidates[i].SKU == selected {
			var specs map[string]any
			_ = json.Unmarshal(catalog.Candidates[i].Specs, &specs)
			specs["cooling_capacity_w"] = nil
			catalog.Candidates[i].Specs, _ = json.Marshal(specs)
		}
	}
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil || got.Outcome != "proposal" || got.Validation.OverallStatus != schemas.OverallReview {
		t.Fatalf("unexpected result: %+v %v", got, err)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"绰绰有余", "完全足够", "不影响实际安全性"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("unsupported assurance survived: %s", forbidden)
		}
	}
	if !strings.Contains(got.Reply, "散热能力字段缺失") || !strings.Contains(string(got.Draft), "当前不能确认温控表现") {
		t.Fatalf("deterministic unresolved explanation missing: %+v", got)
	}
}
