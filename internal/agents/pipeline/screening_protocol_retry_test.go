package pipeline

import (
	"context"
	"iter"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type protocolSequenceModel struct {
	outputs []string
	calls   int
}

func TestTruncatedOuterDraftCannotBecomeAnInnerRequirement(t *testing.T) {
	// 来自单次生成对照 L4-213 的真实格式缺陷：外层缺结束符，内部 use_case 完整。
	truncated := `{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"},"existing_parts":["gpu"],"budget_basis":"new_purchase"`
	input := "我已有一张显卡，想配主机玩游戏，显示器是2K的，新增购买预算8000元。"
	for _, fix := range []bool{true, false} {
		outputs := []string{truncated}
		if fix {
			outputs = append(outputs, truncated+"}")
		}
		m := &protocolSequenceModel{outputs: outputs}
		ctx := WithScreeningBuildState(WithScreeningSources(context.Background(), []string{input}), false)
		var original []string
		var missing []string
		ctx = WithScreeningObserver(ctx, func(raw string, fields []string) { original = append(original, raw); missing = fields })
		var text string
		var gotErr error
		for r, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, &model.LLMRequest{}, false) {
			if err != nil {
				gotErr = err
				continue
			}
			if r != nil {
				text = screeningText(r.Content)
			}
		}
		if m.calls != 2 || len(original) != 2 || original[0] != truncated {
			t.Fatalf("lost retry evidence: calls=%d raw=%v", m.calls, original)
		}
		if fix {
			if gotErr != nil || !reflect.DeepEqual(missing, []string{"owned_parts.gpu.model"}) || strings.Contains(text, "预算") {
				t.Fatalf("nested object caused false questions: %s %v %v", text, missing, gotErr)
			}
		} else if gotErr == nil || text != "" {
			t.Fatalf("still truncated draft escaped as visible question: %s %v", text, gotErr)
		}
	}
	if malformedOuterDraft("请提供预算。") || malformedOuterDraft("需求如下："+truncated+"}") {
		t.Fatal("plain text or complete outer object rejected")
	}
}

func (m *protocolSequenceModel) Name() string { return "protocol-sequence" }
func (m *protocolSequenceModel) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		text := m.outputs[min(m.calls, len(m.outputs)-1)]
		m.calls++
		yield(&model.LLMResponse{Content: genai.NewContentFromText(text, genai.RoleModel)}, nil)
	}
}

func TestDraftProtocolRetryIsBoundedAndPreservesSources(t *testing.T) {
	invalid := `{"schema_version":1,"budget_cny":8000,"use_case":{"type":"general"},"priority":["noise"]}`
	valid := `{"schema_version":1,"budget_cny":8000,"use_case":{"type":"general"},"noise_pref":"silent"}`
	for _, tc := range []struct {
		name        string
		outputs     []string
		hasBuild    bool
		calls       int
		wantMissing string
	}{
		{"repair enum", []string{invalid, valid}, false, 2, ""},
		{"stop after one retry", []string{invalid}, false, 2, ""},
		{"existing change protocol unaffected", []string{invalid}, true, 1, ""},
		{"missing information is a question", []string{`{"schema_version":1,"use_case":{"type":"general"}}`}, false, 1, "budget_cny"},
		{"intent retry retains missing information", []string{`{"schema_version":1,"intent":"new_build","budget_cny":null}`, `{"schema_version":1}`}, false, 2, "budget_cny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &protocolSequenceModel{outputs: tc.outputs}
			ctx := WithScreeningBuildState(context.Background(), tc.hasBuild)
			ctx = WithScreeningSources(ctx, []string{"办公主机，预算8000元，安静"})
			var originals []string
			var missing []string
			ctx = WithScreeningObserver(ctx, func(raw string, fields []string) { originals = append(originals, raw); missing = fields })
			req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("办公主机，预算8000元，安静", genai.RoleUser)}}
			var delivered []string
			for r, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, req, false) {
				if err != nil {
					t.Fatal(err)
				}
				delivered = append(delivered, screeningText(r.Content))
			}
			if m.calls != tc.calls || len(originals) != tc.calls || len(delivered) != 1 {
				t.Fatalf("calls=%d originals=%v delivered=%v", m.calls, originals, delivered)
			}
			if len(req.Contents) != 1 || req.Config != nil {
				t.Fatal("shared request mutated")
			}
			if tc.wantMissing != "" && (len(missing) == 0 || missing[0] != tc.wantMissing) {
				t.Fatalf("missing evidence: %v", missing)
			}
			if tc.name == "repair enum" && delivered[0] != valid {
				t.Fatal("valid retry not delivered")
			}
			if tc.name == "stop after one retry" && delivered[0] != invalid {
				t.Fatal("program silently fabricated valid fields")
			}
		})
	}
}
