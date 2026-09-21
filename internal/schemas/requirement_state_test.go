package schemas

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func updateState(t *testing.T, state RequirementState, message, ops string) RequirementState {
	t.Helper()
	update, err := DecodeRequirementUpdate([]byte(`{"operations":` + ops + `}`))
	if err != nil {
		t.Fatal(err)
	}
	next, err := ApplyRequirementUpdate(state, update, RequirementSource{Kind: "chat", MessageID: message, Quote: message})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func completeRequirementState(t *testing.T) RequirementState {
	return updateState(t, NewRequirementState(), "预算8000，2K游戏，尽量安静，显卡必须英伟达，给朋友装机", `[
		{"op":"set","field":"budget_cny","value":8000,"quote":"预算8000"},
		{"op":"set","field":"use_case.type","value":"gaming","quote":"2K游戏"},
		{"op":"set","field":"use_case.resolution","value":"2K","quote":"2K游戏"},
		{"op":"set","field":"noise_pref","value":"silent","strength":"prefer","quote":"尽量安静"},
		{"op":"set","field":"brand_pref.gpu","value":"nvidia","strength":"must","quote":"显卡必须英伟达"},
		{"op":"set","field":"recipient","value":"朋友","quote":"给朋友装机"}
	]`)
}

func TestRequirementStateBudgetChangeKeepsFactsAndConfirmationImmutable(t *testing.T) {
	confirmed := completeRequirementState(t)
	before, _ := json.Marshal(confirmed)
	next := updateState(t, confirmed, "预算改为9000", `[{"op":"set","field":"budget_cny","value":9000,"quote":"预算改为9000"}]`)
	raw, missing, err := RequirementStateSpec(next)
	if err != nil || len(missing) > 0 {
		t.Fatalf("projection: %s %v %v", raw, missing, err)
	}
	spec, _ := DecodeRequirementSpec(raw)
	if spec.BudgetCNY != 9000 || spec.NoisePref != NoisePrefSilent || spec.BrandPref.GPU != GPUBrandNvidia || spec.UseCase.Resolution != Resolution2K {
		t.Fatalf("lost facts: %+v", spec)
	}
	if spec.ConstraintStrengths["noise_pref"] != "prefer" || spec.ConstraintStrengths["brand_pref.gpu"] != "must" {
		t.Fatalf("lost strengths: %+v", spec.ConstraintStrengths)
	}
	after, _ := json.Marshal(confirmed)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated confirmed snapshot")
	}
	if len(next.Changes) != 1 || len(next.History) != 7 || next.Fields["noise_pref"].Source.MessageID == "预算改为9000" {
		t.Fatalf("invalid provenance/history: %+v", next)
	}
	if question := RequirementStateQuestions(next); question != "" {
		t.Fatalf("repeated known questions: %s", question)
	}
	if string(spec.RequirementDetails["recipient"]) != `"朋友"` || NewRequirementState().Fields["recipient"].Status != "unknown" {
		t.Fatal("recipient leaked across sessions")
	}
}

func TestRequirementStateUnknownAndRemovedNeverBecomeStatedDefaults(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "不用再追求安静", `[{"op":"remove","field":"noise_pref","quote":"不用再追求安静"}]`)
	state = updateState(t, state, "预算8500", `[{"op":"set","field":"budget_cny","value":8500,"quote":"预算8500"}]`)
	raw, _, err := RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := DecodeRequirementSpec(raw)
	if spec.NoisePref != NoisePrefAny || state.Fields["noise_pref"].Status != "removed" || state.Fields["noise_pref"].Value != nil {
		t.Fatal("removed preference revived")
	}
	if state.Fields["size_pref"].Status != "unknown" || state.Fields["budget_flex"].Status != "unknown" || spec.BudgetFlex != 0.1 {
		t.Fatal("program defaults became user facts")
	}
	if _, ok := spec.ConstraintStrengths["noise_pref"]; ok {
		t.Fatal("removed preference still passed to builder")
	}
	state = updateState(t, state, "用途暂时没定", `[{"op":"remove","field":"use_case.type","quote":"用途暂时没定"}]`)
	_, missing, _ := RequirementStateSpec(state)
	if !reflect.DeepEqual(missing, []string{"use_case.type"}) {
		t.Fatalf("unrelated missing fields: %v", missing)
	}
}

func TestRequirementStateAlternativeTemporaryConflictAndRestore(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "如果换AMD会怎样", `[{"op":"alternative","field":"brand_pref.gpu","value":"amd","quote":"如果换AMD会怎样"}]`)
	if string(state.Fields["brand_pref.gpu"].Value) != `"nvidia"` || len(state.Alternatives) != 1 {
		t.Fatal("alternative changed active requirement")
	}
	state = updateState(t, state, "这次先用AMD，尽量即可", `[{"op":"set","field":"brand_pref.gpu","value":"amd","strength":"prefer","scope":"temporary","quote":"这次先用AMD，尽量即可"}]`)
	state = updateState(t, state, "预算改9000", `[{"op":"set","field":"budget_cny","value":9000,"quote":"预算改9000"}]`)
	if state.Fields["brand_pref.gpu"].Scope != "temporary" {
		t.Fatal("temporary exception expired on unrelated turn")
	}
	state = updateState(t, state, "恢复原来的显卡要求", `[{"op":"restore","field":"brand_pref.gpu","quote":"恢复原来的显卡要求"}]`)
	if field := state.Fields["brand_pref.gpu"]; string(field.Value) != `"nvidia"` || field.Strength != "must" || field.Previous != nil {
		t.Fatalf("bad restore: %+v", field)
	}
	state = updateState(t, state, "预算必须8000，也必须9000", `[{"op":"conflict","field":"budget_cny","value":8000,"quote":"预算必须8000，也必须9000"}]`)
	_, missing, err := RequirementStateSpec(state)
	if err != nil || !reflect.DeepEqual(missing, []string{"budget_cny"}) || strings.Contains(RequirementStateQuestions(state), "用途") {
		t.Fatalf("conflict asks unrelated questions: %v %v", missing, err)
	}
}

func TestRequirementStateEditAndChatUseIdenticalReducerAndRefresh(t *testing.T) {
	base := completeRequirementState(t)
	chat := updateState(t, base, "改预算9000", `[{"op":"set","field":"budget_cny","value":9000,"quote":"改预算9000"}]`)
	edit, err := ApplyRequirementUpdate(base, RequirementUpdate{Operations: []RequirementOperation{{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`)}}}, RequirementSource{Kind: "edit", MessageID: "edit-1", Quote: "预算改为9000"})
	if err != nil {
		t.Fatal(err)
	}
	chatSpec, _, _ := RequirementStateSpec(chat)
	editSpec, _, _ := RequirementStateSpec(edit)
	if !bytes.Equal(chatSpec, editSpec) {
		t.Fatalf("edit differs from chat: %s %s", chatSpec, editSpec)
	}
	stored, _ := json.Marshal(edit)
	var restored RequirementState
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatal(err)
	}
	reloaded, _, _ := RequirementStateSpec(restored)
	if !bytes.Equal(editSpec, reloaded) || !reflect.DeepEqual(edit.History, restored.History) {
		t.Fatal("refresh lost state/history")
	}
}

func TestRequirementStateRejectsUngroundedAndInvalidOperationsAtomically(t *testing.T) {
	base := completeRequirementState(t)
	for _, ops := range []string{
		`[{"op":"set","field":"noise_pref","value":"silent","quote":"尽量安静"}]`,
		`[{"op":"set","field":"budget_cny","value":9000,"quote":"新预算"},{"op":"set","field":"noise_pref","value":"invalid","quote":"新预算"}]`,
		`[{"op":"set","field":"private_profile","value":"朋友","quote":"新预算"}]`,
		`[{"op":"set","field":"budget_flex","value":-0.8,"quote":"新预算"}]`,
		`[{"op":"restore","field":"budget_cny","quote":"新预算"}]`,
	} {
		update, err := DecodeRequirementUpdate([]byte(`{"operations":` + ops + `}`))
		if err != nil {
			t.Fatal(err)
		}
		next, err := ApplyRequirementUpdate(base, update, RequirementSource{Kind: "chat", Quote: "新预算"})
		if err == nil {
			t.Fatalf("invalid accepted: %s", ops)
		}
		if !reflect.DeepEqual(base, next) {
			t.Fatal("failed update changed state")
		}
	}
}

func TestRequirementStateOwnedRemovalClearsAssociatedModels(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "已有RTX 4060显卡，预算只算新增", `[
		{"op":"set","field":"existing_parts","value":["gpu"],"quote":"已有RTX 4060显卡"},
		{"op":"set","field":"owned_parts","value":[{"category":"gpu","model":"RTX 4060","quantity":1}],"quote":"已有RTX 4060显卡"},
		{"op":"set","field":"budget_basis","value":"new_purchase","quote":"预算只算新增"}]`)
	state = updateState(t, state, "不再使用已有配件", `[{"op":"remove","field":"existing_parts","quote":"不再使用已有配件"}]`)
	raw, missing, err := RequirementStateSpec(state)
	if err != nil || len(missing) > 0 {
		t.Fatalf("withdrawn owned still questioned: %v %v", missing, err)
	}
	spec, _ := DecodeRequirementSpec(raw)
	if len(spec.ExistingParts) > 0 || len(spec.OwnedParts) > 0 {
		t.Fatal("withdrawn owned passed to builder")
	}
}

// F9:视图超限时按优先级裁剪并显式声明,未展示不等于不存在。
func TestRequirementStatePromptViewIsBoundedAndDeclaresTruncation(t *testing.T) {
	small := completeRequirementState(t)
	view := RequirementStatePromptView(small)
	if strings.Contains(string(view), "view_note") {
		t.Fatal("small state must not declare truncation")
	}
	if !strings.Contains(string(view), `"budget_cny"`) {
		t.Fatal("structured fields missing from small view")
	}

	state := completeRequirementState(t)
	var big strings.Builder
	big.WriteString("这条观察记录非常长，用于撑爆视图上限。")
	for i := 0; i < 60; i++ {
		big.WriteString("历史观察原文内容持续追加，确保超过二十四千字节的上限。")
	}
	for i := 0; i < 8; i++ {
		state.Observations = append(state.Observations, RequirementObservation{
			Field: "notes", Text: big.String(), Source: RequirementSource{Kind: "chat", Quote: big.String()},
		})
	}
	view = RequirementStatePromptView(state)
	if len(view) > promptViewMaxBytes {
		t.Fatalf("view not bounded: %d bytes", len(view))
	}
	if !strings.Contains(string(view), "view_note") || !strings.Contains(string(view), "按未知处理") {
		t.Fatal("truncation not declared")
	}
	var payload struct {
		Fields       map[string]any `json:"fields"`
		Observations []any          `json:"observations"`
	}
	if err := json.Unmarshal(view, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Fields["budget_cny"] == nil {
		t.Fatal("structured must field was trimmed")
	}
	for key := range payload.Fields {
		field := state.Fields[key]
		if key == "budget_cny" {
			continue
		}
		if field.Status == "unknown" && len(field.Value) == 0 {
			t.Fatalf("unknown field %s should have been trimmed first", key)
		}
	}
	if len(payload.Observations) == 0 || len(payload.Observations) >= 8 {
		t.Fatalf("observations not trimmed: %d", len(payload.Observations))
	}
}
