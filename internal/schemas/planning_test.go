package schemas

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFreeRequirementsUseTheSameRevisionAndRemovalFlow(t *testing.T) {
	state := NewRequirementState()
	source := RequirementSource{Kind: "chat", MessageID: "message1", Quote: "预算六千，必须有两个网口，尽量安静，最好有提手"}
	update := RequirementUpdate{Operations: []RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`6000`), Quote: "预算六千", Strength: "must"},
		{Op: "set", Field: "free.network", Value: json.RawMessage(`"必须有两个网口"`), Quote: "必须有两个网口", Kind: "constraint", Strength: "must"},
		{Op: "set", Field: "free.handle", Value: json.RawMessage(`"最好有提手"`), Quote: "最好有提手", Kind: "constraint", Strength: "prefer"},
		{Op: "set", Field: "noise_pref", Value: json.RawMessage(`"silent"`), Quote: "尽量安静", Kind: "constraint", Strength: "prefer"},
	}}
	next, e := ApplyRequirementUpdate(state, update, source)
	if e != nil {
		t.Fatal(e)
	}
	next, e = ApplyRequirementUpdate(next, RequirementUpdate{Operations: []RequirementOperation{{Op: "remove", Field: "free.network"}, {Op: "set", Field: "budget_cny", Value: json.RawMessage(`7000`)}}}, RequirementSource{Kind: "edit", MessageID: "message2", Quote: "撤销双网口，预算七千"})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := PlanningRequirement(next)
	if e != nil {
		t.Fatal(e)
	}
	if next.Fields["free.handle"].Status != "active" || next.Fields["noise_pref"].Strength != "prefer" || next.Fields["free.network"].Status != "removed" || next.Revision != 2 || !strings.Contains(string(raw), `"schema_version":2`) {
		t.Fatalf("lost state: %s", raw)
	}
}
