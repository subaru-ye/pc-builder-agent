package planning

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func recorded5900X(t *testing.T) (Candidate, Evidence) {
	t.Helper()
	raw, err := os.ReadFile("testdata/5900x_registration_recording.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Candidate Candidate
		Page      Evidence
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	// Reconstructed request, not an unrecorded claim about original tool args.
	// These three values occur in the saved body; B550 lacks a supporting quote.
	saved.Candidate.Specs = json.RawMessage(`{"socket":"AM4","tdp_w":105,"has_igpu":false,"supported_chipsets":["B550"]}`)
	saved.Candidate.Unknown = nil
	return saved.Candidate, saved.Page
}

func TestRecordedExternalSpecPathsRetainEvidenceAndReportMissingFields(t *testing.T) {
	c, page := recorded5900X(t)
	x := execution{evidence: []Evidence{page}}
	raw, _ := json.Marshal(c)
	response := x.call(context.Background(), map[string]any{"action": "register_candidate", "payload": string(raw)})
	if response["registered"] != c.ID {
		t.Fatalf("registration failed: %+v", response)
	}
	saved, ok := response["candidate"].(Candidate)
	if !ok {
		t.Fatal("model did not receive the normalized candidate")
	}
	var specs map[string]json.RawMessage
	_ = json.Unmarshal(saved.Specs, &specs)
	if string(specs["socket"]) != `"AM4"` || string(specs["tdp_w"]) != "105" || string(specs["has_igpu"]) != "false" {
		t.Fatalf("discarded real evidence: %+v", saved)
	}
	if _, ok = specs["supported_chipsets"]; ok {
		t.Fatal("accepted chipset without evidence")
	}
	if len(saved.Unknown) != 1 || !strings.Contains(saved.Unknown[0], "supported_chipsets") {
		t.Fatalf("missing actionable registration feedback: %+v", response)
	}
	if saved.FieldEvidence["socket"] != "source-173" || saved.FieldQuotes["socket"] != "AM4接口" || c.FieldEvidence["specs.socket"] != "source-173" {
		t.Fatal("provenance lost or caller mutated")
	}
}

func TestExternalSpecAliasCannotBypassEvidenceChecks(t *testing.T) {
	for _, tc := range []string{"conflicting_refs", "nonexistent_quote", "unsupported_value"} {
		t.Run(tc, func(t *testing.T) {
			c, page := recorded5900X(t)
			x := execution{evidence: []Evidence{page}}
			switch tc {
			case "conflicting_refs":
				c.FieldEvidence["socket"] = "another-page"
			case "nonexistent_quote":
				c.FieldQuotes["specs.socket"] = "AM5插槽"
			case "unsupported_value":
				c.Specs = json.RawMessage(`{"tdp_w":999}`)
			}
			err := x.register(c)
			if tc == "conflicting_refs" {
				if err == nil || len(x.candidates) != 0 {
					t.Fatal("conflicting evidence accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var specs map[string]json.RawMessage
			_ = json.Unmarshal(x.candidates[0].Specs, &specs)
			field := "socket"
			if tc == "unsupported_value" {
				field = "tdp_w"
			}
			if _, ok := specs[field]; ok {
				t.Fatalf("unverified %s was accepted", field)
			}
		})
	}
}
