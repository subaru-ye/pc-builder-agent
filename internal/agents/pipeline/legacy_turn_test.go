package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// legacy turn 是临时传输适配:接收当前 Screening 输出的 reply/next_action,
// 但两者必须在进入领域 Reducer 前剥离,且不改变 readiness。
func TestLegacyTurnDecoderStripsReplyAndNextAction(t *testing.T) {
	raw := []byte(`{"reply":"开始为本轮选配。","next_action":"plan","operations":[
		{"op":"set","field":"budget_cny","value":8000,"quote":"预算8000","evidence":"stated"},
		{"op":"set","field":"use_case.type","value":"gaming","quote":"玩游戏","evidence":"stated"}]}`)
	turn, err := DecodeLegacyRequirementTurn(raw)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Reply != "开始为本轮选配。" || turn.NextAction != "plan" {
		t.Fatalf("decoder 应保留传输字段供展示: %+v", turn)
	}
	update := turn.Update()
	if _, err := schemas.DecodeRequirementUpdate(mustJSON(t, update)); err != nil {
		t.Fatalf("剥离后的领域更新不得携带 reply/next_action: %v", err)
	}
	state := schemas.NewRequirementState()
	source := schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "预算8000，玩游戏"}
	next, err := schemas.ApplyRequirementUpdate(state, update, source)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := json.Marshal(next)
	if strings.Contains(string(stored), "reply") || strings.Contains(string(stored), "next_action") {
		t.Fatalf("reply/next_action 写入了持久化需求真值: %s", stored)
	}
	// next_action=plan|confirm 均不改变 readiness 结论。
	readiness, err := schemas.EvaluateRequirementReadiness(next)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.ConfirmationEligible || len(readiness.MissingFields) == 0 {
		t.Fatalf("不完整状态不得因 next_action 变为可确认: %+v", readiness)
	}
}

// next_action 无论取值都不改变同一状态的 readiness(无权威的确定性证明)。
func TestLegacyNextActionHasNoReadinessAuthority(t *testing.T) {
	for _, action := range []string{"", "collect", "confirm", "plan"} {
		state := completeChatState(t)
		turn, err := DecodeLegacyRequirementTurn([]byte(`{"next_action":"` + action + `","operations":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.ApplyRequirementUpdate(state, turn.Update(), schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "好的"}); err != nil {
			t.Fatal(err)
		}
		readiness, err := schemas.EvaluateRequirementReadiness(state)
		if err != nil || !readiness.ConfirmationEligible {
			t.Fatalf("next_action=%q 改变了 readiness: %+v err=%v", action, readiness, err)
		}
	}
}

func TestLegacyTurnDecoderRejectsUnknownFieldsAndBadAction(t *testing.T) {
	if _, err := DecodeLegacyRequirementTurn([]byte(`{"operations":[],"surprise":1}`)); err == nil {
		t.Fatal("未知字段应拒绝")
	}
	if _, err := DecodeLegacyRequirementTurn([]byte(`{"operations":[],"next_action":"build"}`)); err == nil {
		t.Fatal("非法 next_action 应拒绝")
	}
	if _, err := DecodeLegacyRequirementTurn([]byte(`{"observations":[{}]}`)); err == nil {
		t.Fatal("observations 必须为数组且 quote 必填")
	}
}

func completeChatState(t *testing.T) schemas.RequirementState {
	t.Helper()
	source := schemas.RequirementSource{Kind: "chat", MessageID: "seed", Quote: "预算8000，玩2K游戏，全部新买"}
	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`8000`), Quote: "预算8000"},
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Quote: "游戏"},
		{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"2K"`), Quote: "2K"},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Quote: "全部新买"},
	}}, source)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
