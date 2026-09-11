package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
)

func semanticTurn(t *testing.T, state schemas.RequirementState, input, output string) schemas.RequirementState {
	t.Helper()
	source := schemas.RequirementSource{Kind: "chat", MessageID: "current-message", Quote: input}
	m := &stateProtocolModel{output: output}
	var delivered string
	for response, err := range (screeningGuard{LLM: m}).GenerateContent(WithRequirementState(context.Background(), state, source), &model.LLMRequest{}, false) {
		if err != nil {
			t.Fatal(err)
		}
		delivered = screeningText(response.Content)
	}
	if m.calls != 1 {
		t.Fatalf("unexpected model calls: %d", m.calls)
	}
	update, err := schemas.DecodeRequirementUpdate([]byte(delivered))
	if err != nil {
		t.Fatal(err)
	}
	next, err := schemas.ApplyRequirementUpdate(state, update, source)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

// 语义答案是人工 oracle，测试开放表达不会被后端词表拦截，不冒充模型准确率。
func TestScreeningOpenLanguageAndPartialUncertainty(t *testing.T) {
	input := "兜里六千来块，给朋友拿去把旅行拍的一堆片子拼起来，别像吹风机；箱子能搬来搬去就行，帧数说不准"
	next := semanticTurn(t, schemas.NewRequirementState(), input, `{"operations":[
		{"op":"set","field":"budget_cny","value":6000,"kind":"constraint","evidence":"stated","quote":"兜里六千来块"},
		{"op":"set","field":"use_case.type","value":"productivity","kind":"fact","evidence":"stated","quote":"把旅行拍的一堆片子拼起来"},
		{"op":"set","field":"recipient","value":"朋友","kind":"fact","evidence":"stated","quote":"给朋友"},
		{"op":"set","field":"noise_pref","value":"silent","kind":"constraint","evidence":"stated","strength":"prefer","quote":"别像吹风机"},
		{"op":"set","field":"use_case.fps_target","value":"不知道","evidence":"uncertain","strength":"prefer","quote":"帧数说不准"},
		{"op":"set","field":"size_pref","value":"itx","kind":"constraint","evidence":"inferred","quote":"箱子能搬来搬去就行"}
	],"observations":[{"field":"notes","quote":"把旅行拍的一堆片子拼起来","reason":"工作负载原文"}]}`)
	if string(next.Fields["budget_cny"].Value) != "6000" || next.Fields["noise_pref"].Strength != "prefer" || next.Fields["size_pref"].Status != "unknown" || next.Fields["use_case.fps_target"].Status != "unknown" {
		t.Fatalf("lost reliable fields or adopted guesses: %+v", next)
	}
	if schemas.RequirementStateQuestions(next) != "" {
		t.Fatal("optional uncertainty blocked complete requirements")
	}
	raw, missing, err := schemas.RequirementStateSpec(next)
	if err != nil || len(missing) != 0 {
		t.Fatalf("projection failed: %v %v", missing, err)
	}
	spec, _ := schemas.DecodeRequirementSpec(raw)
	if spec.RequirementSemantics["use_case.type"] != "fact" || len(spec.RequirementObservations) != 3 || next.Fields["budget_flex"].Status != "unknown" {
		t.Fatalf("lost semantics/source or stated defaults: %+v", spec)
	}
	if schemas.NewRequirementState().Fields["recipient"].Status != "unknown" {
		t.Fatal("friend leaked to another session")
	}
}

func TestScreeningInvalidFieldDoesNotRejectOtherFields(t *testing.T) {
	next := semanticTurn(t, schemas.NewRequirementState(), "手头9000，做点文档，弹性还没想好", `{"operations":[
		{"op":"set","field":"budget_cny","value":9000,"evidence":"stated","quote":"手头9000"},
		{"op":"set","field":"use_case.type","value":"general","evidence":"stated","quote":"做点文档"},
		{"op":"set","field":"budget_flex","value":-0.9,"evidence":"stated","quote":"弹性还没想好"},
		{"op":"set","field":"noise_pref","value":"silent","evidence":"stated","quote":"旧消息中要求静音"}
	]}`)
	if next.Fields["budget_cny"].Status != "active" || next.Fields["use_case.type"].Status != "active" || next.Fields["budget_flex"].Status != "conflict" || next.Fields["noise_pref"].Status != "unknown" {
		t.Fatal("invalid field blocked turn or ungrounded field adopted")
	}
	if len(next.Observations) != 2 || next.Observations[1].Source.Quote != "手头9000，做点文档，弹性还没想好" {
		t.Fatalf("fabricated old quote retained: %+v", next.Observations)
	}
}

func TestScreeningNecessaryAmbiguityKeepsReliableFields(t *testing.T) {
	next := semanticTurn(t, schemas.NewRequirementState(), "八千还是九千得再商量，主要做三维资产，尽量安静", `{"operations":[
		{"op":"conflict","field":"budget_cny","kind":"constraint","evidence":"uncertain","quote":"八千还是九千得再商量"},
		{"op":"set","field":"use_case.type","value":"productivity","kind":"fact","evidence":"stated","quote":"主要做三维资产"},
		{"op":"set","field":"noise_pref","value":"silent","strength":"prefer","kind":"constraint","evidence":"stated","quote":"尽量安静"}
	]}`)
	questions := schemas.RequirementStateQuestions(next)
	if !strings.Contains(questions, "预算") || strings.Contains(questions, "用途") || next.Fields["noise_pref"].Status != "active" {
		t.Fatalf("unrelated questions or lost fields: %s %+v", questions, next)
	}
}

func TestScreeningAlternativeRemovalAndNoHistoricalResurrection(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["noise_pref"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"silent"`), Strength: "prefer"}
	state = semanticTurn(t, state, "前面说怕吵那条就划掉吧，换A卡能便宜多少只是问问", `{"operations":[
		{"op":"remove","field":"noise_pref","evidence":"stated","quote":"前面说怕吵那条就划掉吧"},
		{"op":"alternative","field":"brand_pref.gpu","value":"amd","kind":"constraint","evidence":"stated","quote":"换A卡能便宜多少只是问问"}
	]}`)
	state = semanticTurn(t, state, "再加一千到9000", `{"operations":[
		{"op":"set","field":"budget_cny","value":9000,"evidence":"stated","quote":"再加一千到9000"},
		{"op":"set","field":"noise_pref","value":"silent","evidence":"stated","quote":"前面说怕吵"}
	]}`)
	if state.Fields["noise_pref"].Status != "removed" || state.Fields["brand_pref.gpu"].Status != "unknown" || len(state.Alternatives) != 1 {
		t.Fatal("removed/alternative became active")
	}
}

func TestScreeningMalformedOutputStillFailsWithoutRetry(t *testing.T) {
	m := &stateProtocolModel{output: `{"operations":[{"op":"set","field":"budget_cny","value":8000`}
	ctx := WithRequirementState(context.Background(), schemas.NewRequirementState(), schemas.RequirementSource{Kind: "chat", Quote: "预算8000"})
	var got error
	for response, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, &model.LLMRequest{}, false) {
		got = err
		if response != nil {
			t.Fatal("malformed output escaped")
		}
	}
	if !errors.Is(got, ErrRequirementUpdate) || m.calls != 1 {
		t.Fatalf("unexpected failure/calls: %v %d", got, m.calls)
	}
}

func TestScreeningInvalidBudgetCorrectionDoesNotReuseOldBudget(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["budget_cny"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`8000`), Strength: "must"}
	state = semanticTurn(t, state, "预算改成八九千之间，办公室用就行", `{"operations":[
		{"op":"set","field":"budget_cny","value":"8000-9000","evidence":"stated","quote":"预算改成八九千之间"},
		{"op":"set","field":"use_case.type","value":"general","kind":"fact","evidence":"stated","quote":"办公室用就行"}
	]}`)
	if state.Fields["budget_cny"].Status != "conflict" || state.Fields["use_case.type"].Status != "active" {
		t.Fatal("invalid correction silently reused old budget or lost reliable purpose")
	}
	if question := schemas.RequirementStateQuestions(state); !strings.Contains(question, "预算") || strings.Contains(question, "用途") {
		t.Fatalf("wrong followup: %s", question)
	}
}

func TestScreeningNoChangePreservesConfirmedProjection(t *testing.T) {
	state := semanticTurn(t, schemas.NewRequirementState(), "预算六千，文档表格用", `{"operations":[
		{"op":"set","field":"budget_cny","value":6000,"kind":"constraint","evidence":"stated","quote":"预算六千"},
		{"op":"set","field":"use_case.type","value":"general","kind":"fact","evidence":"stated","quote":"文档表格用"}
	]}`)
	before, _, err := schemas.RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	state = semanticTurn(t, state, "好的，谢谢", `{"operations":[]}`)
	after, _, err := schemas.RequirementStateSpec(state)
	if err != nil || string(before) != string(after) || len(state.Observations) != 0 || schemas.RequirementStateQuestions(state) != "" {
		t.Fatal("no-op changed confirmed requirement or repeated questions")
	}
}

func TestMalformedNewHardConditionDoesNotInheritOldSoftStrength(t *testing.T) {
	state := semanticTurn(t, schemas.NewRequirementState(), "预算6000，办公，尽量安静", `{"operations":[
		{"op":"set","field":"budget_cny","value":6000,"quote":"预算6000"},
		{"op":"set","field":"use_case.type","value":"general","quote":"办公"},
		{"op":"set","field":"noise_pref","value":"silent","strength":"prefer","kind":"constraint","quote":"尽量安静"}
	]}`)
	state = semanticTurn(t, state, "现在必须绝对安静，给朋友用", `{"operations":[
		{"op":"set","field":"noise_pref","value":"inaudible","strength":"must","kind":"constraint","evidence":"stated","quote":"现在必须绝对安静"},
		{"op":"set","field":"recipient","value":"朋友","kind":"fact","evidence":"stated","quote":"给朋友用"}
	]}`)
	if state.Fields["noise_pref"].Status != "conflict" || state.Fields["noise_pref"].Strength != "must" || state.Fields["recipient"].Status != "active" {
		t.Fatal("malformed hard condition became a soft observation or blocked reliable context")
	}
	if question := schemas.RequirementStateQuestions(state); !strings.Contains(question, "静音") || strings.Contains(question, "预算") || strings.Contains(question, "用途") {
		t.Fatalf("wrong targeted confirmation: %s", question)
	}
}

func TestScreeningEvidenceFailureDoesNotReuseOldHardFields(t *testing.T) {
	for _, tc := range []struct{ field, value, quote, output string }{
		{"budget_flex", "0.1", "预算绝对不能超", `{"operations":[{"op":"set","field":"budget_flex","value":0,"strength":"must","quote":"预算绝对不能超"}]}`},
		{"owned_parts", `[{"category":"gpu","model":"RTX 4060","quantity":1}]`, "显卡其实是五零系列，具体后两位忘了", `{"operations":[{"op":"set","field":"owned_parts","value":[{"category":"gpu","model":"RTX 5090","quantity":1}],"strength":"must","evidence":"stated","quote":"显卡其实是五零系列，具体后两位忘了"}]}`},
	} {
		state := schemas.NewRequirementState()
		state.Fields[tc.field] = schemas.RequirementField{Status: "active", Value: json.RawMessage(tc.value), Strength: "must"}
		state = semanticTurn(t, state, tc.quote, tc.output)
		if state.Fields[tc.field].Status != "conflict" || len(state.Observations) != 1 {
			t.Fatalf("%s silently reused an outdated hard field: %+v", tc.field, state.Fields[tc.field])
		}
	}
}

func TestScreeningUncertainUpdateHonorsInheritedHardStrength(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["size_pref"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"itx"`), Strength: "must", Kind: "constraint"}
	state = semanticTurn(t, state, "板型可能要改，大一点还是小一点得量过桌面再说", `{"operations":[{"op":"set","field":"size_pref","value":"matx","evidence":"uncertain","quote":"板型可能要改，大一点还是小一点得量过桌面再说"}]}`)
	if state.Fields["size_pref"].Status != "conflict" || state.Fields["size_pref"].Previous == nil || string(state.Fields["size_pref"].Previous.Value) != `"itx"` {
		t.Fatal("uncertain hard update silently retained old active choice")
	}
}
