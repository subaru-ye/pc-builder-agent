package buildharness

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestPromptEvidenceMatchesActualBuilderRequest(t *testing.T) {
	components, err := PromptComponents()
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeModel{outputs: []string{draftJSON("psu-a", "build-1")}}
	h := newTestHarness(t, m, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
		return passingResult("8000.00"), nil
	})
	if _, err := h.Run(context.Background(), BuildInput{Requirement: fixtureRequirement()}); err != nil {
		t.Fatal(err)
	}
	if len(m.requests) != 1 || m.requests[0].Config.SystemInstruction.Parts[0].Text != components["system"] {
		t.Fatal("frozen builder system prompt differs from actual request")
	}
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		prompt, err := buildPrompt(BuildInput{Requirement: fixtureRequirement()}, testBundle(), nil, nil, validate.Result{}, attempt)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OutputContract json.RawMessage `json:"output_contract"`
		}
		if err := json.Unmarshal([]byte(prompt), &envelope); err != nil {
			t.Fatal(err)
		}
		if string(envelope.OutputContract) != components[fmt.Sprintf("output_contract_attempt_%d", attempt)] {
			t.Fatalf("attempt %d output contract was not frozen exactly", attempt)
		}
	}
}
