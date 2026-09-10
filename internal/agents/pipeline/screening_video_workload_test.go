package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
)

// Saved real deepseek-v4-flash-0731 response from the reported failed suggestion.
func TestScreeningVideoWorkloadReplay(t *testing.T) {
	raw, err := os.ReadFile("testdata/requirement_video_editing.json")
	if err != nil {
		t.Fatal(err)
	}
	var output string
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	m := &stateProtocolModel{output: output}
	state := schemas.NewRequirementState()
	state.Fields["notes"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"需要无线网络"`), Strength: "prefer"}
	source := schemas.RequirementSource{Kind: "chat", MessageID: "video-editing", Quote: "预算 6000，主要剪 4K 视频，尽量安静"}
	ctx := WithRequirementState(context.Background(), state, source)
	var delivered string
	for response, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, &model.LLMRequest{}, false) {
		if err != nil {
			t.Fatal(err)
		}
		delivered = screeningText(response.Content)
	}
	update, err := schemas.DecodeRequirementUpdate([]byte(delivered))
	if err != nil {
		t.Fatal(err)
	}
	next, err := schemas.ApplyRequirementUpdate(state, update, source)
	if err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 || string(next.Fields["budget_cny"].Value) != "6000" || string(next.Fields["use_case.type"].Value) != `"productivity"` || next.Fields["noise_pref"].Strength != "prefer" {
		t.Fatalf("lost valid requirements: %+v", next.Fields)
	}
	if next.Fields["use_case.resolution"].Status == "active" {
		t.Fatal("video resolution became display preference")
	}
	if note := string(next.Fields["notes"].Value); !strings.Contains(note, "无线网络") || len(next.Observations) != 1 || !strings.Contains(next.Observations[0].Text, "4K 视频") {
		t.Fatalf("lost workload or prior note: %s", note)
	}
	next = semanticTurn(t, next, "显示器打算用2K", `{"operations":[{"op":"set","field":"use_case.resolution","value":"2K","kind":"fact","evidence":"stated","quote":"显示器打算用2K"}]}`)
	rawSpec, _, err := schemas.RequirementStateSpec(next)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := schemas.DecodeRequirementSpec(rawSpec)
	if len(spec.RequirementObservations) != 1 || !strings.Contains(spec.RequirementObservations[0].Text, "4K 视频") || spec.UseCase.Resolution != schemas.Resolution2K {
		t.Fatal("display update erased workload source parameters")
	}
}
