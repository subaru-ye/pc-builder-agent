package pipeline

import (
	"encoding/json"

	"github.com/a2aproject/a2a-go/v2/a2a"
	adka2a "google.golang.org/adk/v2/server/adka2a/v2"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
)

// BuildDecisionPart keeps a business outcome separate from free-form model text.
// A2A data parts survive the remote transport without displaying internal JSON.
func BuildDecisionPart(decision *buildharness.Decision) (*genai.Part, error) {
	raw, err := json.Marshal(decision)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	parts, err := adka2a.ToGenAIParts([]*a2a.Part{a2a.NewDataPart(map[string]any{
		"type": "pc_builder.build_decision", "schema_version": 1, "decision": data,
	})})
	if err != nil {
		return nil, err
	}
	return parts[0], nil
}

// ReadBuildDecisionPart accepts only the versioned business-result envelope.
// The product layer still projects known reasons; Message is evidence, not UI copy.
func ReadBuildDecisionPart(part *genai.Part) *buildharness.Decision {
	if part == nil || part.Thought || part.InlineData == nil || len(part.InlineData.Data) > 32*1024 {
		return nil
	}
	converted, err := adka2a.ToA2APart(part, nil)
	if err != nil || converted.Data() == nil {
		return nil
	}
	raw, err := json.Marshal(converted.Data())
	if err != nil {
		return nil
	}
	var envelope struct {
		Type          string                 `json:"type"`
		SchemaVersion int                    `json:"schema_version"`
		Decision      *buildharness.Decision `json:"decision"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Type != "pc_builder.build_decision" || envelope.SchemaVersion != 1 {
		return nil
	}
	return envelope.Decision
}
