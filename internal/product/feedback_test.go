package product

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScreeningEvidenceDoesNotRecordCredentials(t *testing.T) {
	t.Setenv("SCREENING_PROVIDER", "bailian")
	t.Setenv("SCREENING_MODEL", "test-screening")
	t.Setenv("SCREENING_API_KEY", "private-test-key-never-record")
	t.Setenv("SCREENING_BASE_URL", "https://example.test/api?token=private-query")
	t.Setenv("SCREENING_MODEL_CHAIN", "")
	raw, err := json.Marshal(screeningEvidence(ScreenInput{Text: "预算8000", UserSources: []string{"预算8000"}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-") || !strings.Contains(string(raw), "test-screening") || !strings.Contains(string(raw), "not_per_request_identity") {
		t.Fatalf("metadata contract: %s", raw)
	}
}
