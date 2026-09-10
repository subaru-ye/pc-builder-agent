package schemas

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRequirementSemanticsSurviveProjectionAndStrengthEdit(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "主要将旅行素材拼成小片子", `[{"op":"set","field":"notes","value":"主要将旅行素材拼成小片子","kind":"fact","evidence":"stated","strength":"must","quote":"主要将旅行素材拼成小片子"}]`)
	confirmed, _ := json.Marshal(state)
	state = updateState(t, state, "预算尽量控制在8000", `[{"op":"set","field":"budget_cny","value":8000,"strength":"prefer","quote":"预算尽量控制在8000"}]`)
	raw, _, err := RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := DecodeRequirementSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if spec.RequirementSemantics["notes"] != "fact" || spec.ConstraintStrengths["notes"] != "must" || spec.ConstraintStrengths["budget_cny"] != "prefer" {
		t.Fatalf("lost independent semantics/strength: %+v", spec)
	}
	var restored RequirementState
	if err := json.Unmarshal(confirmed, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Fields["budget_cny"].Strength != "must" || restored.Fields["notes"].Evidence != "stated" {
		t.Fatal("confirmation/source changed")
	}
	state = updateState(t, state, "必须能接两台显示器", `[{"op":"set","field":"notes","value":"必须能接两台显示器","kind":"constraint","evidence":"stated","strength":"must","quote":"必须能接两台显示器"}]`)
	raw, _, _ = RequirementStateSpec(state)
	spec, _ = DecodeRequirementSpec(raw)
	if spec.RequirementSemantics["notes"] != "constraint" || spec.ConstraintStrengths["notes"] != "must" {
		t.Fatal("real hard condition was downgraded")
	}
}

func TestRequirementObservationsPreserveSourceAndCanBeWithdrawn(t *testing.T) {
	state := completeRequirementState(t)
	source := RequirementSource{Kind: "chat", MessageID: "unstructured", Quote: "方便我背着到处跑"}
	state, err := ApplyRequirementUpdate(state, RequirementUpdate{Operations: []RequirementOperation{}, Observations: []RequirementObservationInput{{Quote: source.Quote, Reason: "保留便携背景，未指定板型"}}}, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Observations) != 1 || state.Observations[0].Field != "notes" || state.Observations[0].Source.MessageID != source.MessageID {
		t.Fatal("observation lacks actionable association/source")
	}
	raw, _, _ := RequirementStateSpec(state)
	spec, err := DecodeRequirementSpec(raw)
	if err != nil || len(spec.RequirementObservations) != 1 || !strings.Contains(string(RequirementStatePromptView(state)), source.Quote) {
		t.Fatal("unstructured context never reached consumers")
	}
	stored, _ := json.Marshal(state)
	if err := json.Unmarshal(stored, &state); err != nil {
		t.Fatal(err)
	}
	state = updateState(t, state, "那条方便携带的说明划掉吧", `[{"op":"remove","field":"notes","quote":"那条方便携带的说明划掉吧"}]`)
	raw, _, _ = RequirementStateSpec(state)
	spec, _ = DecodeRequirementSpec(raw)
	if len(spec.RequirementObservations) != 0 || strings.Contains(string(RequirementStatePromptView(state)), source.Quote) || !state.Observations[0].Resolved {
		t.Fatal("withdrawn unstructured context revived")
	}
}

func TestRequirementStateDoesNotUpgradeLegacyOriginOrUnknownSemantics(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "要能连上学校的设备", `[{"op":"set","field":"notes","value":"要能连上学校的设备","strength":"must","quote":"要能连上学校的设备"}]`)
	raw, _, _ := RequirementStateSpec(state)
	spec, _ := DecodeRequirementSpec(raw)
	if spec.RequirementSemantics["notes"] != "" || state.Fields["notes"].Evidence != "" {
		t.Fatal("legacy origin/classification invented")
	}
	before, _ := json.Marshal(state)
	_, err := ApplyRequirementUpdate(state, RequirementUpdate{Operations: []RequirementOperation{{Op: "set", Field: "size_pref", Value: json.RawMessage(`"itx"`), Evidence: "inferred", Quote: "越小越好"}}}, RequirementSource{Kind: "chat", Quote: "越小越好"})
	if err == nil {
		t.Fatal("inferred enum became an expressed preference")
	}
	after, _ := json.Marshal(state)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected inference changed source state")
	}
}

func TestRequirementChangedTextDoesNotBorrowOldContextClassification(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "帮朋友做些片子", `[{"op":"set","field":"notes","value":"帮朋友做些片子","kind":"fact","strength":"must","quote":"帮朋友做些片子"}]`)
	state = updateState(t, state, "必须有雷电接口", `[{"op":"set","field":"notes","value":"必须有雷电接口","strength":"must","quote":"必须有雷电接口"}]`)
	if state.Fields["notes"].Kind != "" || state.Fields["notes"].Strength != "must" {
		t.Fatal("new unclassified hard text borrowed former fact classification")
	}
}

func TestRequirementOwnedRemovalAlsoResolvesAssociatedObservations(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "已有显卡RTX 4060", `[{"op":"set","field":"existing_parts","value":["gpu"],"quote":"已有显卡RTX 4060"},{"op":"set","field":"owned_parts","value":[{"category":"gpu","model":"RTX 4060","quantity":1}],"quote":"已有显卡RTX 4060"}]`)
	state.Observations = []RequirementObservation{{Field: "owned_parts", Text: "还有个蓝色的老显卡", Reason: "型号待确认", Source: RequirementSource{Kind: "chat", Quote: "还有个蓝色的老显卡"}}}
	state = updateState(t, state, "已有件这回都不用了", `[{"op":"remove","field":"existing_parts","quote":"已有件这回都不用了"}]`)
	raw, _, err := RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := DecodeRequirementSpec(raw)
	if !state.Observations[0].Resolved || len(spec.RequirementObservations) != 0 || strings.Contains(string(RequirementStatePromptView(state)), "蓝色的老显卡") {
		t.Fatal("dependent owned observation revived after removing existing parts")
	}
}

func TestRequirementAppearanceDoesNotRewriteWorkloadNotes(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "剪4K视频，外观尽量白色", `[{"op":"set","field":"notes","value":"剪4K视频","strength":"must","quote":"剪4K视频"},{"op":"set","field":"appearance","value":"白色","kind":"constraint","strength":"prefer","quote":"外观尽量白色"}]`)
	raw, _, err := RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := DecodeRequirementSpec(raw)
	if spec.Notes != "剪4K视频" || string(spec.RequirementDetails["appearance"]) != `"白色"` || spec.ConstraintStrengths["appearance"] != "prefer" {
		t.Fatal("appearance rewrote independently classified workload notes")
	}
}

func TestRequirementOptionalSoftConflictDoesNotBlockKnownRequirements(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "声音普通还是尽量安静都还没想好", `[{"op":"conflict","field":"noise_pref","kind":"constraint","strength":"prefer","evidence":"uncertain","quote":"声音普通还是尽量安静都还没想好"}]`)
	raw, missing, err := RequirementStateSpec(state)
	if err != nil || len(missing) > 0 || RequirementStateQuestions(state) != "" {
		t.Fatalf("optional soft conflict blocked known requirements: %v %v", missing, err)
	}
	spec, _ := DecodeRequirementSpec(raw)
	if spec.NoisePref != NoisePrefAny || len(spec.RequirementObservations) != 1 || spec.RequirementObservations[0].Field != "noise_pref" {
		t.Fatal("old soft preference remained active or ambiguity lost")
	}
	state = updateState(t, state, "噪声标准必须确认后再装", `[{"op":"conflict","field":"noise_pref","kind":"constraint","strength":"must","evidence":"uncertain","quote":"噪声标准必须确认后再装"}]`)
	_, missing, _ = RequirementStateSpec(state)
	if len(missing) != 1 || missing[0] != "noise_pref" {
		t.Fatal("real hard conflict was ignored")
	}
}

func TestRequirementNewObservationAfterRemovalIsAvailableWithoutRestoringOldValue(t *testing.T) {
	state := completeRequirementState(t)
	state = updateState(t, state, "旧补充都撤掉", `[{"op":"remove","field":"notes","quote":"旧补充都撤掉"}]`)
	source := RequirementSource{Kind: "chat", MessageID: "new-observation", Quote: "新想到的便携场景还没整理好"}
	state, err := ApplyRequirementUpdate(state, RequirementUpdate{Operations: []RequirementOperation{}, Observations: []RequirementObservationInput{{Field: "notes", Quote: source.Quote, Reason: "新的未结构化背景"}}}, source)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := DecodeRequirementSpec(raw)
	if state.Fields["notes"].Status != "removed" || spec.Notes != "" || len(spec.RequirementObservations) != 1 || !strings.Contains(string(RequirementStatePromptView(state)), source.Quote) {
		t.Fatal("fresh source was hidden or old field restored")
	}
}

func TestRequirementRemovalDoesNotReappearThroughSameTurnObservation(t *testing.T) {
	state := completeRequirementState(t)
	source := RequirementSource{Kind: "chat", MessageID: "remove-current", Quote: "静音那条不用管了"}
	state, err := ApplyRequirementUpdate(state, RequirementUpdate{Operations: []RequirementOperation{{Op: "remove", Field: "noise_pref", Quote: source.Quote}}, Observations: []RequirementObservationInput{{Field: "noise_pref", Quote: source.Quote, Reason: "本轮原文"}}}, source)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := DecodeRequirementSpec(raw)
	if !state.Observations[0].Resolved || len(spec.RequirementObservations) != 0 {
		t.Fatal("same-turn removed requirement returned through observation")
	}
}
