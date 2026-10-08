package pipeline

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type promptEvidenceModel struct{ requests []*model.LLMRequest }

func (*promptEvidenceModel) Name() string { return "offline-prompt-evidence" }
func (m *promptEvidenceModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.requests = append(m.requests, req)
		raw := `{"schema_version": 2, "configuration_scope": ["tower"],"budget_cny":8000,"use_case":{"type":"general"},"priority":["noise"]}`
		if len(m.requests) > 1 {
			raw = `{"schema_version": 2, "configuration_scope": ["tower"],"budget_cny":8000,"use_case":{"type":"general"}}`
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText(raw, genai.RoleModel)}, nil)
	}
}

func TestRequirementPromptEvidenceMatchesActualSystemAndRetry(t *testing.T) {
	m := &promptEvidenceModel{}
	ctx := WithRequirementState(context.Background(), schemas.NewRequirementState(), schemas.RequirementSource{Kind: "chat", MessageID: "turn-1", Quote: "预算8000"})
	// fake 的旧格式输出触发增量协议纠偏，两次请求均由内存模型捕获。
	for range (screeningGuard{LLM: m}).GenerateContent(ctx, &model.LLMRequest{}, false) {
	}
	if len(m.requests) != 2 {
		t.Fatalf("expected format retry, got %d requests", len(m.requests))
	}
	evidence := RequirementPromptComponents()
	for _, req := range m.requests {
		if req.Config == nil || screeningText(req.Config.SystemInstruction) != evidence["system"] {
			t.Fatal("current requirement prompt differs from actual request")
		}
	}
	retry := m.requests[1].Contents
	if screeningText(retry[len(retry)-1]) != evidence["format_retry"] {
		t.Fatal("current requirement retry differs from actual request")
	}
}

func TestNewBuildPromptEvidenceMatchesActualSystemAndRetry(t *testing.T) {
	m := &promptEvidenceModel{}
	ctx := WithScreeningBuildState(context.Background(), false)
	for _, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("办公主机预算8000元", genai.RoleUser)}}, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(m.requests) != 2 {
		t.Fatalf("expected format retry, got %d requests", len(m.requests))
	}
	evidence := NewBuildPromptComponents()
	for _, req := range m.requests {
		if req.Config == nil || screeningText(req.Config.SystemInstruction) != evidence["new_build_system"] {
			t.Fatal("frozen system prompt differs from actual request")
		}
	}
	retry := m.requests[1].Contents
	if screeningText(retry[len(retry)-1]) != evidence["format_retry"] {
		t.Fatal("frozen retry prompt differs from actual request")
	}
}
