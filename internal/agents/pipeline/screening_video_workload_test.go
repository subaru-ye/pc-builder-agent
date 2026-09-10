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
	state.Fields["notes"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"需要无线网络"`)}
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
	if note := string(next.Fields["notes"].Value); !strings.Contains(note, "4K 视频") || !strings.Contains(note, "无线网络") {
		t.Fatalf("lost workload or prior note: %s", note)
	}
}

func TestVideoEditingEvidenceBoundaries(t *testing.T) {
	for _, quote := range []string{"主要剪 4K 视频", "剪片子", "编辑高清视频"} {
		if !groundedStatePreference("use_case.type", "productivity", quote) {
			t.Errorf("rejected %s", quote)
		}
	}
	for _, quote := range []string{"看看4K视频", "不用剪视频", "只是日常办公"} {
		if groundedStatePreference("use_case.type", "productivity", quote) {
			t.Errorf("invented editing for %s", quote)
		}
	}
	op := schemas.RequirementOperation{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"4K"`), Quote: "剪4K视频，显示器用4K"}
	update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}
	if normalizeVideoWorkload(&update, schemas.NewRequirementState(), op.Quote) {
		t.Fatal("explicit display resolution was removed")
	}
}

func TestVideoWorkloadKeepsExistingNotesWhenAlreadyRecorded(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["notes"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"需要无线网络；剪4K视频"`)}
	update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"4K"`), Quote: "剪4K视频"}}}
	if !normalizeVideoWorkload(&update, state, "剪4K视频") || string(update.Operations[0].Value) != string(state.Fields["notes"].Value) {
		t.Fatal("existing notes lost when the workload was already recorded")
	}
}
