package planning

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestRecordedLocalReferenceIsReadableWithoutFillingMissingSpecifications(t *testing.T) {
	input, _, responses, catalog := thermalRecording(t, "testdata/local_reference_failure_20260915.json")
	seen := false
	m := &scriptedModel{respond: func(n int, request *model.LLMRequest) *genai.Content {
		for _, content := range request.Contents {
			for _, part := range content.Parts {
				if part.FunctionResponse == nil {
					continue
				}
				response := part.FunctionResponse.Response
				if response["kind"] == "local_snapshot" && response["id"] == "local:mb-gb-b650-aorus-elite-ax" {
					seen = true
					candidate, ok := response["candidate"].(Candidate)
					var specs map[string]any
					if !ok || json.Unmarshal(candidate.Specs, &specs) != nil || specs["memory_speed_max_mts"] != nil {
						t.Fatal("invented missing speed specification")
					}
				}
			}
		}
		if n > len(responses) {
			t.Fatal("unexpected model call")
		}
		return responses[n-1]
	}}
	got, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), input)
	if err != nil || !seen || got.ModelCalls != 8 || got.Outcome != "proposal" || got.Quote.TotalCNY != "23431.08" {
		t.Fatalf("read=%v result=%+v err=%v", seen, got, err)
	}
	unknown := 0
	for _, check := range got.Validation.Checks {
		if check.Outcome == schemas.OutcomeUnknown {
			unknown++
		}
	}
	if unknown != 2 {
		t.Fatalf("unknown facts were lost: %+v", got.Validation)
	}
}

func TestLocalEvidenceReferenceReturnsSnapshotAndReadableFieldIndex(t *testing.T) {
	row := store.CandidateEvidence{ID: "socket-source", SKU: "cpu-test", Field: "socket", URL: "https://example.org/spec", Text: "Socket AM4", CapturedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	c := Candidate{ID: row.SKU, Category: schemas.CategoryCPU, Model: "Test CPU", Specs: json.RawMessage(`{"socket":"AM4"}`), Evidence: []string{"local:cpu-test"}}
	x := execution{runner: Runner{Catalog: excerptCatalog{rows: []store.CandidateEvidence{row}}}, candidates: []Candidate{c}, date: "2026-09-15"}
	read := func(id string) map[string]any {
		raw, _ := json.Marshal(map[string]string{"id": id})
		return x.call(context.Background(), map[string]any{"action": "read_evidence", "payload": string(raw)})
	}
	for range 2 {
		got := read(c.Evidence[0])
		candidate, ok := got["candidate"].(Candidate)
		if !ok || got["kind"] != "local_snapshot" || string(candidate.Specs) != string(c.Specs) || got["source"] != nil {
			t.Fatal("snapshot reference returned a fabricated page or changed specs", got)
		}
		sources := got["sources"].([]map[string]string)
		if len(sources) != 1 || sources[0]["id"] != "catalog-socket-source" || sources[0]["field"] != "socket" || len(x.evidence) != 1 {
			t.Fatal("missing/duplicated field source", got)
		}
	}
	page := read("catalog-socket-source")["source"].(Evidence)
	if page.Kind != "catalog" || page.Text != row.Text || page.URL != row.URL || page.CapturedAt != "2026-09-01T00:00:00Z" {
		t.Fatal("original evidence changed", page)
	}
	if read("local:missing")["error"] == nil {
		t.Fatal("invented missing candidate")
	}
	x.candidates[0].External = true
	if read("local:cpu-test")["error"] == nil {
		t.Fatal("external candidate posed as local evidence")
	}
}

type excerptCatalog struct {
	recordedCatalog
	rows []store.CandidateEvidence
}

func (c excerptCatalog) CandidateEvidence(context.Context, []string) ([]store.CandidateEvidence, error) {
	return c.rows, nil
}

func TestCatalogSearchIndexKeepsCompleteReadableEvidence(t *testing.T) {
	full := strings.Repeat("厂商规格原文。", 1000) + "接口事实：AM4"
	row := store.CandidateEvidence{ID: "long-spec", SKU: "cpu-test", Field: "socket", URL: "https://example.org/spec", Text: full, CapturedAt: time.Now()}
	catalog := excerptCatalog{rows: []store.CandidateEvidence{row}}
	x := execution{runner: Runner{Catalog: catalog}, candidates: []Candidate{{ID: row.SKU, Category: schemas.CategoryCPU}}, seen: map[string]bool{}}
	result := x.call(context.Background(), map[string]any{"action": "search_local", "payload": `{"category":"cpu"}`})
	sources := result["sources"].(map[string][]map[string]string)
	if len(sources) != 1 || len(sources[row.SKU]) != 1 || sources[row.SKU][0]["id"] != "catalog-long-spec" || sources[row.SKU][0]["field"] != row.Field {
		t.Fatalf("search index lost field association: %+v", sources)
	}
	if len(x.evidence) != 1 || x.evidence[0].Text != full {
		t.Fatal("full source was truncated in persistent evidence")
	}
	read := x.call(context.Background(), map[string]any{"action": "read_evidence", "payload": `{"id":"catalog-long-spec","query":"接口事实","limit":240}`})
	if e, ok := read["source"].(Evidence); !ok || !strings.Contains(e.Text, "接口事实：AM4") {
		t.Fatalf("tail facts became inaccessible: %+v", read)
	}
	short := Evidence{Text: "AM4"}
	if evidencePreview(short) != short {
		t.Fatal("short excerpts should remain unchanged")
	}
}
