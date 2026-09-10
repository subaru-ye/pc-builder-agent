package pipeline

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type stateProtocolModel struct {
	output  string
	calls   int
	request *model.LLMRequest
}

func (m *stateProtocolModel) Name() string { return "offline-state-protocol" }
func (m *stateProtocolModel) GenerateContent(_ context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		m.request = req
		yield(&model.LLMResponse{Content: genai.NewContentFromText(m.output, genai.RoleModel)}, nil)
	}
}

func TestScreeningStateUsesSingleCallAndAuthoritativeStateWithoutOldMessages(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["noise_pref"] = schemas.RequirementField{Status: "removed"}
	state.History = []schemas.RequirementChange{{Source: schemas.RequirementSource{Kind: "chat", Quote: "最早旧要求必须安静"}}}
	m := &stateProtocolModel{output: `{"operations":[{"op":"set","field":"budget_cny","value":9000,"quote":"预算改为9000"}]}`}
	ctx := WithRequirementState(context.Background(), state, schemas.RequirementSource{Kind: "chat", MessageID: "message-new", Quote: "预算改为9000"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("用户：最早旧要求必须安静\n助手：默认可以8000", genai.RoleUser)}}
	var delivered string
	for response, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, req, true) {
		if err != nil {
			t.Fatal(err)
		}
		delivered = screeningText(response.Content)
	}
	if m.calls != 1 || !strings.Contains(delivered, "9000") {
		t.Fatalf("unexpected calls/output: %d %s", m.calls, delivered)
	}
	input := screeningText(m.request.Contents[0])
	if strings.Contains(input, "最早旧要求") || strings.Contains(input, "默认可以") || !strings.Contains(input, `"status":"removed"`) || !strings.Contains(input, "预算改为9000") {
		t.Fatalf("unbounded history/defaults leaked: %s", input)
	}
	if req.Config != nil || len(req.Contents) != 1 || screeningText(req.Contents[0]) == input {
		t.Fatal("mutated caller request")
	}
	update, _ := schemas.DecodeRequirementUpdate([]byte(delivered))
	next, err := schemas.ApplyRequirementUpdate(state, update, schemas.RequirementSource{Kind: "chat", MessageID: "message-new", Quote: "预算改为9000"})
	if err != nil || next.Fields["noise_pref"].Status != "removed" {
		t.Fatalf("removed preference revived: %v", err)
	}
}

func TestScreeningStateOwnedPatchCanKeepOtherGroundedModels(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["owned_parts"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1}]`)}
	update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{{Op: "set", Field: "owned_parts", Value: json.RawMessage(`[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1},{"category":"gpu","model":"RTX 4060","quantity":1}]`), Quote: "还已有RTX 4060显卡"}}}
	if err := guardRequirementUpdateEvidence(state, update); err != nil {
		t.Fatal(err)
	}
	state.Fields["owned_parts"] = schemas.RequirementField{Status: "removed"}
	if err := guardRequirementUpdateEvidence(state, update); err == nil {
		t.Fatal("old model restored without current evidence")
	}
}
