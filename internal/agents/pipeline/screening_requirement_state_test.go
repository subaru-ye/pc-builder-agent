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

func TestOwnedSourceGuardUsesQuantityDefault(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		pass                bool
	}{
		{"add GPU without repeating known CPU", `[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1}]`, `[{"category":"cpu","model":"AMD Ryzen 5 7600"},{"category":"gpu","model":"RTX 4060"}]`, true},
		{"prior omitted quantity", `[{"category":"cpu","model":"AMD Ryzen 5 7600"}]`, `[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1},{"category":"gpu","model":"RTX 4060","quantity":1}]`, true},
		{"invented CPU still rejected", `[{"category":"cpu","model":"AMD Ryzen 5 7600"}]`, `[{"category":"cpu","model":"AMD Ryzen 9 7900"},{"category":"gpu","model":"RTX 4060"}]`, false},
		{"changed SSD quantity needs evidence", `[{"category":"ssd","model":"Samsung 990 PRO","quantity":1}]`, `[{"category":"ssd","model":"Samsung 990 PRO","quantity":2},{"category":"gpu","model":"RTX 4060"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := schemas.NewRequirementState()
			state.Fields["owned_parts"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(tc.before)}
			update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{{Op: "set", Field: "owned_parts", Value: json.RawMessage(tc.after), Quote: "还已有RTX 4060显卡"}}}
			if err := guardRequirementUpdateEvidence(state, update); (err == nil) != tc.pass {
				t.Fatalf("guard error = %v, want pass %v", err, tc.pass)
			}
		})
	}
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

type sequenceModel struct {
	outputs []string
	calls   int
	lastReq *model.LLMRequest
}

func (m *sequenceModel) Name() string { return "offline-sequence" }
func (m *sequenceModel) GenerateContent(_ context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		i := m.calls
		if i >= len(m.outputs) {
			i = len(m.outputs) - 1
		}
		m.calls++
		m.lastReq = req
		yield(&model.LLMResponse{Content: genai.NewContentFromText(m.outputs[i], genai.RoleModel)}, nil)
	}
}

func planReadyStateFixture() schemas.RequirementState {
	state := schemas.NewRequirementState()
	state.Fields["budget_cny"] = schemas.RequirementField{Status: "active", Value: json.RawMessage("8000")}
	state.Fields["use_case.type"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"gaming"`)}
	return state
}

func runScreeningState(t *testing.T, state schemas.RequirementState, m *sequenceModel) string {
	t.Helper()
	ctx := WithRequirementState(context.Background(), state, schemas.RequirementSource{Kind: "chat", MessageID: "message-new", Quote: "预算8000，直接开始配"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("用户：预算8000，直接开始配", genai.RoleUser)}}
	var delivered string
	for response, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, req, true) {
		if err != nil {
			t.Fatal(err)
		}
		delivered = screeningText(response.Content)
	}
	return delivered
}

func TestScreeningCollectFallbackRetriesOnceWhenStateReady(t *testing.T) {
	state := planReadyStateFixture()
	m := &sequenceModel{outputs: []string{
		`{"operations":[],"next_action":"collect","reply":"好的，本轮信息已记录。"}`,
		`{"operations":[],"next_action":"plan","reply":"开始为本轮选配。"}`,
	}}
	delivered := runScreeningState(t, state, m)
	if m.calls != 2 {
		t.Fatalf("collect fallback must retry exactly once: %d calls", m.calls)
	}
	if !strings.Contains(delivered, `"next_action":"plan"`) {
		t.Fatalf("retry result not adopted: %s", delivered)
	}
	// 纠正提示跟随在首轮模型输出之后。
	var corrective bool
	for _, c := range m.lastReq.Contents {
		if strings.Contains(screeningText(c), "纠偏") {
			corrective = true
		}
	}
	if !corrective {
		t.Fatal("missing corrective instruction in retry request")
	}
	// 用途未知但预算已明确时，继续比较类请求同样应回环为 plan。
	state = schemas.NewRequirementState()
	state.Fields["budget_cny"] = schemas.RequirementField{Status: "active", Value: json.RawMessage("7000")}
	m = &sequenceModel{outputs: []string{
		`{"operations":[],"next_action":"collect","reply":"好的，我先用现有资料继续比较，稍后同步结果。"}`,
		`{"operations":[],"next_action":"plan","reply":"继续本轮比较。"}`,
	}}
	if delivered = runScreeningState(t, state, m); m.calls != 2 || !strings.Contains(delivered, `"next_action":"plan"`) {
		t.Fatalf("budget-only state must retry: %d calls %s", m.calls, delivered)
	}
}

func TestScreeningCollectFallbackStaysOutWhenNotApplicable(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		state        schemas.RequirementState
	}{
		{"state-not-ready", `{"operations":[],"next_action":"collect","reply":"预算定了告诉我。"}`, schemas.NewRequirementState()},
		{"reply-asks-question", `{"operations":[],"next_action":"collect","reply":"需要独显吗？"}`, planReadyStateFixture()},
		{"plan-not-collect", `{"operations":[],"next_action":"plan","reply":"开始为本轮选配。"}`, planReadyStateFixture()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &sequenceModel{outputs: []string{tc.output}}
			delivered := runScreeningState(t, tc.state, m)
			if m.calls != 1 || !strings.Contains(delivered, "next_action") {
				t.Fatalf("unexpected retry: %d calls %s", m.calls, delivered)
			}
		})
	}
}
