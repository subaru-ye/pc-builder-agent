package planning

import (
	"context"
	"encoding/json"
	"iter"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// usageModel 给脚本响应附加 token 计量,驱动 F7 预算逻辑。
type usageModel struct {
	inner  *scriptedModel
	tokens int32
}

func (*usageModel) Name() string { return "offline-usage-model" }
func (m *usageModel) GenerateContent(ctx context.Context, req *model.LLMRequest, b bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for resp, err := range m.inner.GenerateContent(ctx, req, b) {
			if resp != nil && resp.UsageMetadata == nil {
				resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{TotalTokenCount: m.tokens}
			}
			yield(resp, err)
		}
	}
}

// F7:token 超限立即进入整理轮(摘除工具),未解决问题保留为 proposal。
func TestRunnerTokenBudgetEntersWindDownAndKeepsResult(t *testing.T) {
	input, record, catalog := completeRecording(t)
	sawNoTools := false
	m := &usageModel{inner: &scriptedModel{respond: func(n int, req *model.LLMRequest) *genai.Content {
		if n == 1 {
			return function("search_local", `{"category":"cpu"}`)
		}
		if req.Config != nil && req.Config.Tools == nil {
			sawNoTools = true
		}
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}, tokens: 10}
	got, err := (Runner{Model: m, Catalog: catalog, TokenBudget: 5, MaxTurns: 8}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !sawNoTools {
		t.Fatal("wind-down turn did not remove tools")
	}
	if got.Outcome != string(schemas.OverallPass) && got.Outcome != "ready" && got.Outcome != "proposal" {
		t.Fatalf("unexpected outcome after wind-down: %s", got.Outcome)
	}
	if got.Tokens < 5 {
		t.Fatalf("budget accounting broken: %d", got.Tokens)
	}
}
