package planning

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type archiveCatalog struct {
	recordedCatalog
	artifacts map[string]json.RawMessage
}

func (c archiveCatalog) PlanningArtifact(_ context.Context, runID string) (json.RawMessage, error) {
	return c.artifacts[runID], nil
}

func TestTransportDegradeTruncatesLongTextAndQuotesOnly(t *testing.T) {
	long := strings.Repeat("正文", 200)
	short := "短引文"
	got := TransportDegrade(Result{
		Evidence:   []Evidence{{ID: "e1", Text: long}, {ID: "e2", Text: short}},
		Candidates: []Candidate{{ID: "c1", FieldQuotes: map[string]string{"tdp_w": long, "socket": short}}},
	})
	if got.Evidence[1].Text != short {
		t.Fatal("short evidence text must stay unchanged")
	}
	if got.Candidates[0].FieldQuotes["socket"] != short {
		t.Fatal("short field quote must stay unchanged")
	}
	for _, bad := range []struct{ name, value string }{
		{"evidence text", got.Evidence[0].Text},
		{"field quote", got.Candidates[0].FieldQuotes["tdp_w"]},
	} {
		if len([]rune(bad.value)) >= 300 || !strings.Contains(bad.value, "未完整展示") {
			t.Fatalf("%s not degraded: %q", bad.name, bad.value)
		}
	}
}

func TestArchiveAndDegradeKeepsFullPayloadAndReturnsPreview(t *testing.T) {
	full := strings.Repeat("厂商规格原文。", 500)
	st := &memoryArtifacts{artifacts: map[string]json.RawMessage{}}
	result := Result{Evidence: []Evidence{{ID: "page-1", Kind: "page", Text: full}}}
	got, err := ArchiveAndDegrade(context.Background(), st, "run-1", "session-1", result)
	if err != nil {
		t.Fatal(err)
	}
	var archived Result
	if err := json.Unmarshal(st.artifacts["run-1"], &archived); err != nil {
		t.Fatal(err)
	}
	if archived.Evidence[0].Text != full {
		t.Fatal("archived payload must keep full evidence text")
	}
	if len([]rune(got.Evidence[0].Text)) >= len([]rune(full))/10 {
		t.Fatalf("transport copy not degraded: %d runes", len([]rune(got.Evidence[0].Text)))
	}
}

type memoryArtifacts struct {
	artifacts map[string]json.RawMessage
}

func (m *memoryArtifacts) SavePlanningArtifact(_ context.Context, p store.SavePlanningArtifactParams) error {
	m.artifacts[p.RunID] = append(json.RawMessage(nil), p.Payload...)
	return nil
}

func TestPreviousEvidenceRefilledFromArchiveForReadEvidence(t *testing.T) {
	full := strings.Repeat("厂商规格原文。", 500)
	page := Evidence{ID: "page-1", URL: "https://example.org/spec", Kind: "page", Text: full, CapturedAt: "2026-09-21"}
	artifact, _ := json.Marshal(Result{SchemaVersion: 1, Evidence: []Evidence{page}})
	degraded, _ := json.Marshal(TransportDegrade(Result{SchemaVersion: 1, Evidence: []Evidence{page}}))
	catalog := archiveCatalog{artifacts: map[string]json.RawMessage{"run-1": artifact}}
	input := schemas.PlanningInput{SchemaVersion: 2, PreviousProposal: degraded, PreviousRunID: "run-1"}
	m := &scriptedModel{respond: func(n int, req *model.LLMRequest) *genai.Content {
		if n == 1 {
			return function("read_evidence", `{"id":"page-1"}`)
		}
		response := lastFunctionResponse(req)
		if !strings.Contains(response, strings.Repeat("厂商规格原文。", 300)) || !strings.Contains(response, `"total_chars":3500`) {
			t.Fatalf("read_evidence did not return refilled full text: %d bytes", len(response))
		}
		raw, _ := json.Marshal(map[string]any{"outcome": "proposal", "reply": "已读取上一轮证据正文。", "issues": []string{}, "assessments": []Assessment{}, "assumptions": []string{}})
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 2}).Run(context.Background(), input)
	if err != nil || got.Outcome != "proposal" {
		t.Fatalf("run failed: %+v %v", got, err)
	}
}

func TestPreviousEvidenceStaysDegradedWithoutArchive(t *testing.T) {
	full := strings.Repeat("厂商规格原文。", 500)
	page := Evidence{ID: "page-1", Kind: "page", Text: full, CapturedAt: "2026-09-21"}
	degraded, _ := json.Marshal(TransportDegrade(Result{SchemaVersion: 1, Evidence: []Evidence{page}}))
	catalog := archiveCatalog{artifacts: map[string]json.RawMessage{}}
	input := schemas.PlanningInput{SchemaVersion: 2, PreviousProposal: degraded, PreviousRunID: "run-missing"}
	m := &scriptedModel{respond: func(n int, req *model.LLMRequest) *genai.Content {
		if n == 1 {
			return function("read_evidence", `{"id":"page-1"}`)
		}
		response := lastFunctionResponse(req)
		if !strings.Contains(response, "未完整展示") {
			t.Fatal("degraded previous must not gain full text without archive")
		}
		raw, _ := json.Marshal(map[string]any{"outcome": "proposal", "reply": "已处理。", "issues": []string{}, "assessments": []Assessment{}, "assumptions": []string{}})
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 2}).Run(context.Background(), input)
	if err != nil || got.Outcome != "proposal" {
		t.Fatalf("run failed: %+v %v", got, err)
	}
}

func lastFunctionResponse(req *model.LLMRequest) string {
	response := ""
	for _, content := range req.Contents {
		for _, part := range content.Parts {
			if part.FunctionResponse != nil {
				raw, _ := json.Marshal(part.FunctionResponse.Response)
				response = string(raw)
			}
		}
	}
	return response
}
