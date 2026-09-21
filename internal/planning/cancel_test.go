package planning

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// F6:ShouldCancel 在每轮开头生效,返回 context.Canceled 供上游按取消处理。
func TestRunnerShouldCancelStopsToolLoopBeforeNextTurn(t *testing.T) {
	input, _, catalog := completeRecording(t)
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		return function("search_local", `{"category":"cpu"}`)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, ShouldCancel: func() bool { return m.calls >= 1 }}).Run(context.Background(), input)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel not propagated: %v", err)
	}
	if m.calls != 1 {
		t.Fatalf("cancel checked too late: %d model calls", m.calls)
	}
	if got.Outcome != "" && got.Outcome == "ready" {
		t.Fatal("cancelled run must not produce a delivery outcome")
	}
}
