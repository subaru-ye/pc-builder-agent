package planning

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

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
