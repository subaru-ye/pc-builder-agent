package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
)

// 录自固定 deepseek-v4-flash-0731 的真实失败输出（2026-09-10），无额外模型调用。
func TestScreeningStateSavedLiveRemovalBindsExplicitCurrentSource(t *testing.T) {
	input := "取消显卡品牌偏好，不再要求N卡，其他条件保留。"
	source := schemas.RequirementSource{Kind: "chat", MessageID: "live-removal-regression", Quote: input}
	state := schemas.NewRequirementState()
	state.Fields["brand_pref.gpu"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"nvidia"`), Strength: "prefer", Scope: "session"}
	state.Fields["noise_pref"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"silent"`), Strength: "prefer", Scope: "session"}
	m := &stateProtocolModel{output: `{"operations":[{"op":"remove","field":"brand_pref.gpu"}]}`}
	var delivered string
	for response, err := range (screeningGuard{LLM: m}).GenerateContent(WithRequirementState(context.Background(), state, source), &model.LLMRequest{}, false) {
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
	if err != nil || next.Fields["brand_pref.gpu"].Status != "removed" || next.Fields["noise_pref"].Strength != "prefer" || next.Fields["noise_pref"].Status != "active" {
		t.Fatalf("explicit removal failed or changed unrelated preference: %+v err=%v", next, err)
	}
	if m.calls != 1 || next.Fields["brand_pref.gpu"].Source.MessageID != source.MessageID || next.Fields["brand_pref.gpu"].Source.Quote != input {
		t.Fatal("removal source was not bound to this user message or an extra call was added")
	}
}
