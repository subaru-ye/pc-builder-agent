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
	if m.calls != 1 || delivered != m.output {
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

func TestScreeningStateRejectsFabricationAndMalformedOutputWithoutExtraCalls(t *testing.T) {
	for _, tc := range []struct{ message, output string }{
		{"预算还没定", `{"operations":[{"op":"set","field":"budget_cny","value":8000,"quote":"预算还没定"}]}`},
		{"预算8000", `{"operations":[{"op":"set","field":"budget_flex","value":0.1,"quote":"预算8000"}]}`},
		{"预算8000", `{"operations":[{"op":"set","field":"noise_pref","value":"silent","quote":"旧消息说安静"}]}`},
		{"办公主机", `{"operations":[{"op":"set","field":"budget_cny","value":8000`},
		{"已有显卡", `{"operations":[{"op":"set","field":"owned_parts","value":[{"category":"gpu","model":"RTX 4060"}],"quote":"已有显卡"}]}`},
	} {
		m := &stateProtocolModel{output: tc.output}
		ctx := WithRequirementState(context.Background(), schemas.NewRequirementState(), schemas.RequirementSource{Kind: "chat", Quote: tc.message})
		var rejected bool
		for response, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, &model.LLMRequest{}, false) {
			if err != nil {
				rejected = true
			}
			if response != nil {
				t.Fatalf("invalid update escaped: %s", tc.output)
			}
		}
		if !rejected || m.calls != 1 {
			t.Fatalf("expected one failed call: %d %v", m.calls, rejected)
		}
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

func TestScreeningStateDoesNotTurnQuotesIntoUnstatedPreferences(t *testing.T) {
	for _, tc := range []struct{ field, value, quote string }{
		{"budget_flex", `0.3`, "预算严格不超过8000元"},
		{"budget_flex", `0.3`, "预算可以浮动10%"},
		{"budget_flex", `0.1`, "预算8000元左右"},
		{"brand_pref.gpu", `"amd"`, "预算8000，想要安静的电脑"},
		{"brand_pref.gpu", `"amd"`, "CPU选AMD，显卡不限品牌"},
		{"brand_pref.gpu", `"amd"`, "显卡不要AMD"},
		{"brand_pref.cpu", `"amd"`, "已有AMD Ryzen 5 7600处理器"},
		{"brand_pref.cpu", `"any"`, "噪音不限"},
		{"noise_pref", `"silent"`, "静音不用考虑了"},
		{"noise_pref", `"silent"`, "预算8000"},
		{"size_pref", `"itx"`, "电脑越小越好"},
		{"size_pref", `"atx"`, "用matx"},
		{"size_pref", `"itx"`, "不要itx"},
		{"use_case.type", `"productivity"`, "只是日常办公"},
	} {
		op := schemas.RequirementOperation{Op: "set", Field: tc.field, Value: json.RawMessage(tc.value), Quote: tc.quote}
		if err := guardRequirementUpdateEvidence(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}); err == nil {
			t.Errorf("ungrounded %s=%s accepted from %s", tc.field, tc.value, tc.quote)
		}
	}
	for _, tc := range []struct{ field, value, quote string }{
		{"budget_flex", `0`, "预算严格不超过8000元"},
		{"budget_flex", `0.1`, "预算可以浮动10%"},
		{"budget_flex", `0.15`, "预算浮动百分之十五"},
		{"budget_flex", `0.2`, "预算弹性为0.2"},
		{"brand_pref.gpu", `"nvidia"`, "显卡优先英伟达"},
		{"brand_pref.cpu", `"amd"`, "CPU选AMD"},
		{"noise_pref", `"silent"`, "尽量安静"},
		{"size_pref", `"matx"`, "尺寸希望m-atx"},
		{"use_case.type", `"general"`, "日常办公"},
	} {
		op := schemas.RequirementOperation{Op: "set", Field: tc.field, Value: json.RawMessage(tc.value), Quote: tc.quote}
		if err := guardRequirementUpdateEvidence(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}); err != nil {
			t.Errorf("explicit %s rejected: %v", tc.quote, err)
		}
	}
}

func TestScreeningStateExplicitAlternativeCannotSetCurrentRequirement(t *testing.T) {
	op := schemas.RequirementOperation{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"4K"`), Quote: "换成4K"}
	update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}
	if err := guardRequirementUpdateEvidence(schemas.NewRequirementState(), update, "如果换成4K会怎样？先不改"); err == nil {
		t.Fatal("alternative updated active resolution")
	}
	op.Op = "alternative"
	if err := guardRequirementUpdateEvidence(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}, "如果换成4K会怎样？先不改"); err != nil {
		t.Fatal(err)
	}
	op = schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`), Quote: "预算改成9000"}
	if err := guardRequirementUpdateEvidence(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}, "预算改成9000。如果换成4K会怎样？先不改分辨率"); err != nil {
		t.Fatalf("independent budget update blocked: %v", err)
	}
}
