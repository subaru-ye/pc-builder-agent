package pipeline

import (
	"context"
	"encoding/json"
	"strings"
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

func TestScreeningStateMissingRemovalSourceCannotBorrowUnrelatedIntent(t *testing.T) {
	for _, input := range []string{"预算改成6000", "取消CPU品牌偏好", "取消显卡", "取消GPU", "不要取消显卡品牌偏好", "如果取消显卡品牌偏好会怎样", "可以取消显卡品牌偏好吗", "我朋友说取消显卡品牌偏好", "取消静音要求"} {
		update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{{Op: "remove", Field: "brand_pref.gpu"}}}
		if bindExplicitRemovalSources(&update, input) {
			t.Fatalf("unrelated, negated or hypothetical intent borrowed: %s", input)
		}
		if _, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), update, schemas.RequirementSource{Kind: "chat", Quote: input}); err == nil {
			t.Fatalf("missing quote accepted: %s", input)
		}
	}
	for _, input := range []string{"取消静音要求", "不再要求安静", "静音不用考虑了"} {
		update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{{Op: "remove", Field: "noise_pref"}}}
		if !bindExplicitRemovalSources(&update, input) || update.Operations[0].Quote != input {
			t.Fatalf("explicit noise removal was not bound: %s", input)
		}
	}
	for _, op := range []string{"set", "alternative", "conflict", "restore"} {
		update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{{Op: op, Field: "brand_pref.gpu", Value: json.RawMessage(`"amd"`)}}}
		if bindExplicitRemovalSources(&update, "取消显卡品牌偏好") || strings.TrimSpace(update.Operations[0].Quote) != "" {
			t.Fatalf("source repair escaped the remove-only boundary: %s", op)
		}
	}
}
