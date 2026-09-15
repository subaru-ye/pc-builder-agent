package planningeval

import (
	"encoding/json"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestReferenceRetentionUsesSavedContent(t *testing.T) {
	const quote = "看过显卡报价3000元，还没买"
	value := json.RawMessage(`"看过显卡报价3000元，还没买"`)
	for _, tc := range []struct {
		name   string
		state  schemas.RequirementState
		passed bool
	}{
		{"reply only", schemas.RequirementState{Reply: quote}, false},
		{"active background", schemas.RequirementState{Fields: map[string]schemas.RequirementField{"free.gpu_reference": {Status: "active", Kind: "context", Value: value}}}, true},
		{"removed background", schemas.RequirementState{Fields: map[string]schemas.RequirementField{"free.gpu_reference": {Status: "removed", Kind: "context", Value: value}}}, false},
		{"unrelated field source", schemas.RequirementState{Fields: map[string]schemas.RequirementField{"use_case.type": {Status: "active", Value: json.RawMessage(`"gaming"`), Source: &schemas.RequirementSource{Quote: quote}}}}, false},
		{"invented ownership", schemas.RequirementState{Fields: map[string]schemas.RequirementField{"owned_parts": {Status: "active", Value: value}}}, false},
		{"alternative", schemas.RequirementState{Alternatives: []schemas.RequirementAlternative{{Value: value}}}, true},
		{"observation", schemas.RequirementState{Observations: []schemas.RequirementObservation{{Text: quote}}}, true},
		{"resolved observation", schemas.RequirementState{Observations: []schemas.RequirementObservation{{Text: quote, Resolved: true}}}, false},
		{"split unrelated entries", schemas.RequirementState{Observations: []schemas.RequirementObservation{{Text: "显卡还没买"}, {Text: "处理器报价3000"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := StepRecord{State: tc.state, Reply: quote}
			Grade(&r, Expect{RetainedReferences: [][]string{{"显卡", "3000"}}}, nil)
			for _, c := range r.Checks {
				if c.Name == "retained_reference:1" {
					if c.Pass != tc.passed {
						t.Fatalf("retention = %v, want %v", c.Pass, tc.passed)
					}
					return
				}
			}
			t.Fatal("missing retention check")
		})
	}
	if retainedReference(schemas.RequirementState{Observations: []schemas.RequirementObservation{{Text: quote}}}, nil) || retainedReference(schemas.RequirementState{Observations: []schemas.RequirementObservation{{Text: quote}}}, []string{""}) {
		t.Fatal("empty assertion accepted")
	}
}
