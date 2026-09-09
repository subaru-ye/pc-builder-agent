package evalsuite

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestDialogueGradesEveryTurnAndRejectsMissingEvidence(t *testing.T) {
	budget := 8000
	c := Case{Stage: StageScreening, Turns: []ScreeningTurn{
		{Input: "办公电脑", Expect: Expect{Kind: "clarify", ClarifyFields: []string{"budget_cny"}}},
		{Input: "预算8000", Expect: Expect{Kind: "spec", BudgetCNY: &budget, SpecFields: map[string]json.RawMessage{"use_case.type": json.RawMessage(`"general"`)}}},
	}}
	good := ScreeningOutput{Turns: []ScreeningOutput{{Text: "请提供预算金额。"}, {Text: `{"schema_version":1,"budget_cny":8000,"use_case":{"type":"general"}}`}}}
	if v := AssertScreeningOutput(c, good); !v.Passed {
		t.Fatal(v)
	}
	bad := good
	bad.Turns = append([]ScreeningOutput(nil), good.Turns...)
	bad.Turns[0].Text = "你喜欢什么颜色？"
	if v := AssertScreeningOutput(c, bad); v.Passed || len(v.Failures) == 0 {
		t.Fatal("last turn masked earlier error")
	}
	bad = good
	bad.Turns = bad.Turns[1:]
	if AssertScreeningOutput(c, bad).Passed {
		t.Fatal("missing round passed")
	}
	bad = good
	bad.Turns = append([]ScreeningOutput(nil), good.Turns...)
	bad.Turns[1].Text = `{"schema_version":1,"budget_cny":8000,"use_case":{"type":"productivity"}}`
	if AssertScreeningOutput(c, bad).Passed {
		t.Fatal("wrong use case passed")
	}
	for i := 0; i < 10; i++ {
		if !reflect.DeepEqual(AssertScreeningOutput(c, bad), AssertScreeningOutput(c, bad)) {
			t.Fatal("nondeterministic grading")
		}
	}
}

func TestDialogueFixturesAndContract(t *testing.T) {
	for _, raw := range []string{
		`{"id":"x","title":"x","stage":"screening","input":"x","turns":[{"input":"x","expect":{"kind":"spec"}},{"input":"x","expect":{"kind":"spec"}}]}`,
		`{"id":"x","title":"x","stage":"screening","turns":[{"input":"x","expect":{"kind":"spec"}}]}`,
		`{"id":"x","title":"x","stage":"screening","turns":[{"input":"x","expect":{"kind":"spec"}},{"input":"","expect":{"kind":"spec"}}]}`,
		`{"id":"x","title":"x","stage":"screening","input":"x","expect":{"kind":"spec","spec_fields":{"unknown":true}}}`,
	} {
		if _, err := decodeCase([]byte(raw)); err == nil {
			t.Fatalf("invalid fixture accepted: %s", raw)
		}
	}
	cases, err := LoadCases("testdata/cases")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, c := range cases {
		if len(c.Turns) > 0 {
			count++
		}
	}
	if count != 10 {
		t.Fatalf("dialogue cases=%d", count)
	}
}

type fakeDialogue struct{ calls int }

func (f *fakeDialogue) Run(context.Context, string) (string, error) {
	panic("dialogue became single turn")
}
func (f *fakeDialogue) RunDialogue(_ context.Context, inputs []string) ([]ScreeningOutput, error) {
	f.calls++
	return []ScreeningOutput{{Text: "请提供预算。"}, {Text: "请提供预算。"}}, nil
}
func TestDialogueRepeatsWholeConversations(t *testing.T) {
	f := &fakeDialogue{}
	c := Case{ID: "dialogue", Stage: StageScreening, Turns: []ScreeningTurn{{Input: "办公", Expect: Expect{Kind: "clarify", ClarifyFields: []string{"budget_cny"}}}, {Input: "没定", Expect: Expect{Kind: "clarify", ClarifyFields: []string{"budget_cny"}}}}}
	records, err := RunCases(context.Background(), []Case{c}, Deps{Screening: f, Seeds: 3})
	if err != nil || len(records) != 3 || f.calls != 3 {
		t.Fatalf("records=%v err=%v calls=%d", records, err, f.calls)
	}
	for _, r := range records {
		if !r.Verdict.Passed || len(r.Screening.Turns) != 2 {
			t.Fatal(r)
		}
	}
}
