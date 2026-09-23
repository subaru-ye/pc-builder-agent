package pipeline

import (
	"encoding/json"
	"strings"
	"testing"
)

// DecodeRequirementTurn 的严格合同:拒绝 next_action/reply、未知字段、
// 非法 proposal 值与超长输出;省略 turn_signals 合法(多标签全 false)。
func TestDecodeRequirementTurnContract(t *testing.T) {
	valid := `{"operations":[{"op":"set","field":"budget_cny","value":7500,"evidence":"stated","quote":"7500"}],"observations":[],"turn_signals":{"asks_question":true,"requests_review":false,"requests_build":false,"ambiguous":false},"proposals":[{"field":"budget_cny","value":7500,"text":"按 7500 元的预算继续可以吗？"}],"answer":"参考预算档如下。"}`
	turn, err := DecodeRequirementTurn([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if !turn.Signals.AsksQuestion || turn.Signals.RequestsBuild {
		t.Fatalf("signals decoded wrong: %+v", turn.Signals)
	}
	if len(turn.Proposals) != 1 || turn.Proposals[0].Field != "budget_cny" || string(turn.Proposals[0].Value) != "7500" {
		t.Fatalf("proposal decoded wrong: %+v", turn.Proposals)
	}
	if !turn.HasRequirementUpdate() {
		t.Fatal("operations non-empty means update")
	}

	minimal := `{"operations":[]}`
	if turn, err = DecodeRequirementTurn([]byte(minimal)); err != nil || turn.HasRequirementUpdate() || turn.Answer != "" {
		t.Fatalf("minimal turn must decode with no update: %+v err=%v", turn, err)
	}

	for name, raw := range map[string]string{
		"next_action":         `{"operations":[],"next_action":"collect"}`,
		"reply":               `{"operations":[],"reply":"已记录。"}`,
		"unknown field":       `{"operations":[],"surprise":1}`,
		"missing operations":  `{"observations":[]}`,
		"null operations":     `{"operations":null}`,
		"model-supplied id":   `{"operations":[],"proposals":[{"field":"budget_cny","value":1,"text":"t","id":"m1"}]}`,
		"proposal bad value":  `{"operations":[],"proposals":[{"field":"budget_cny","value":"abc","text":"t"}]}`,
		"proposal no text":    `{"operations":[],"proposals":[{"field":"budget_cny","value":1}]}`,
		"proposal bad field":  `{"operations":[],"proposals":[{"field":"nope","value":1,"text":"t"}]}`,
		"notes not proposable": `{"operations":[],"proposals":[{"field":"notes","value":"x","text":"t"}]}`,
	} {
		if _, err := DecodeRequirementTurn([]byte(raw)); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}

	// accepted_proposal 操作由产品层验证,解码层不拒绝(evidence 合法值)。
	accept := `{"operations":[{"op":"set","field":"budget_cny","value":7500,"evidence":"accepted_proposal","quote":"可以"}]}`
	if _, err := DecodeRequirementTurn([]byte(accept)); err != nil {
		t.Fatalf("accepted_proposal op decodes at transport: %v", err)
	}

	// 超长 answer 拒绝。
	long := `{"operations":[],"answer":"` + strings.Repeat("很", 4001) + `"}`
	if _, err := DecodeRequirementTurn([]byte(long)); err == nil {
		t.Fatal("oversized answer must be rejected")
	}
}

// DecodeLegacyTurnForReplay 机械升格:reply→answer,next_action 丢弃,
// 不生成用户事实;产品路径的严格解码继续拒绝 legacy 字段。
func TestDecodeLegacyTurnForReplayIsMechanical(t *testing.T) {
	turn, err := DecodeLegacyTurnForReplay([]byte(`{"reply":"已记录。","next_action":"plan","operations":[{"op":"set","field":"budget_cny","value":8000,"evidence":"stated","quote":"预算8000"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if turn.Answer != "已记录。" || len(turn.Operations) != 1 || turn.Signals.RequestsBuild {
		t.Fatalf("upgrade changed semantics: %+v", turn)
	}
	raw, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "next_action") || strings.Contains(string(raw), `"reply"`) {
		t.Fatalf("upgraded wire must not carry legacy fields: %s", raw)
	}
	if _, err := DecodeRequirementTurn(raw); err != nil {
		t.Fatalf("upgraded wire must pass strict decode: %v", err)
	}
}
