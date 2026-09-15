package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestScreeningBackgroundDoesNotSetOrReplaceBudget(t *testing.T) {
	for _, status := range []string{"unknown", "removed", "active"} {
		t.Run(status, func(t *testing.T) {
			state := schemas.NewRequirementState()
			state.Fields["budget_cny"] = schemas.RequirementField{Status: status}
			if status == "active" {
				state.Fields["budget_cny"] = schemas.RequirementField{Status: status, Value: json.RawMessage(`8000`), Kind: "constraint", Strength: "must"}
			}
			before, _ := json.Marshal(state.Fields["budget_cny"])
			state = semanticTurn(t, state, "看中一张售价3000元的显卡，想配2K游戏电脑", `{"operations":[
				{"op":"set","field":"budget_cny","value":3000,"kind":"context","strength":"must","evidence":"stated","quote":"看中一张售价3000元的显卡"},
				{"op":"set","field":"use_case.type","value":"gaming","kind":"fact","evidence":"stated","quote":"想配2K游戏电脑"}
			]}`)
			after, _ := json.Marshal(state.Fields["budget_cny"])
			if string(before) != string(after) || string(state.Fields["use_case.type"].Value) != `"gaming"` {
				t.Fatal("background replaced budget or rejected the reliable use case")
			}
			if len(state.Observations) != 1 || state.Observations[0].Field != "notes" || !strings.Contains(state.Observations[0].Text, "3000") {
				t.Fatal("reference or provenance lost")
			}
			state = semanticTurn(t, state, "整机预算改为9000元", `{"operations":[{"op":"set","field":"budget_cny","value":9000,"kind":"constraint","evidence":"stated","quote":"整机预算改为9000元"}]}`)
			if state.Observations[0].Resolved || !strings.Contains(string(schemas.RequirementStatePromptView(state)), "3000") {
				t.Fatal("real budget update erased the separate quote reference")
			}
		})
	}
}
