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

func TestFieldContainsChecksOnlyStringValue(t *testing.T) {
	for _, tc := range []struct {
		value string
		pass  bool
	}{
		{`"同事"`, true}, {`"给同事装机"`, true}, {`"自己"`, false},
		{`{"同事":"自己"}`, false}, {`null`, false},
	} {
		r := StepRecord{State: schemas.RequirementState{Fields: map[string]schemas.RequirementField{"recipient": {
			Status: "active", Value: json.RawMessage(tc.value), Source: &schemas.RequirementSource{Quote: "给同事"},
		}}}}
		Grade(&r, Expect{Fields: map[string]FieldExpect{"recipient": {Status: "active", Contains: []string{"同事"}}}}, nil)
		found := false
		for _, c := range r.Checks {
			if c.Name == "state:recipient" {
				found = true
				if c.Pass != tc.pass {
					t.Fatalf("%s: got %v, want %v", tc.value, c.Pass, tc.pass)
				}
			}
		}
		if !found {
			t.Fatal("missing field check")
		}
	}
}
