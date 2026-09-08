package evalsuite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestClarificationTargetsMissingInformation(t *testing.T) {
	for _, tt := range []struct {
		name, field, text, failure string
	}{
		{"empty", "budget_cny", " \n", "S1"},
		{"unrelated", "budget_cny", "好的，我会为你推荐。", "S3"},
		{"wrong question", "budget_cny", "您的分辨率是多少？", "S3"},
		{"statement plus unrelated question", "budget_cny", "预算按8000元计算，请问玩什么游戏？", "S3"},
		{"negative", "budget_cny", "不需要告诉我预算是多少。", "S3"},
		{"refusal", "budget_cny", "抱歉，我无法确认预算是多少。", "S3"},
		{"budget question", "budget_cny", "请问您的预算大概是多少？", ""},
		{"budget request", "budget_cny", "请提供预算范围。", ""},
		{"budget synonym", "budget_cny", "您打算花多少钱装机？", ""},
		{"resolution question", "resolution", "显示器分辨率是多少？", ""},
		{"resolution choices", "resolution", "您使用的是1080P、2K还是4K？", ""},
		{"screen size insufficient", "resolution", "您显示器是多少英寸？", "S3"},
		{"wrong resolution question", "resolution", "预算多少？", "S3"},
		{"malformed JSON", "budget_cny", "预算多少？ {\"budget_cny\":", "S1"},
		{"unexpected JSON", "budget_cny", "预算多少？ {}", "S1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := Case{Stage: StageScreening, Expect: Expect{Kind: "clarify", ClarifyFields: []string{tt.field}}}
			v := AssertScreeningCase(c, tt.text)
			if tt.failure == "" {
				if !v.Passed {
					t.Fatalf("expected pass: %+v", v)
				}
			} else if v.Passed || len(v.Failures) == 0 || v.Failures[0].ID != tt.failure {
				t.Fatalf("expected %s: %+v", tt.failure, v)
			}
		})
	}
	c := Case{Stage: StageScreening, Expect: Expect{Kind: "clarify", ClarifyFields: []string{"budget_cny", "resolution"}}}
	if AssertScreeningCase(c, "请问预算多少？").Passed {
		t.Fatal("all requested fields must be asked")
	}
	if !AssertScreeningCase(c, "预算多少？\n显示器分辨率是多少？").Passed {
		t.Fatal("both questions should pass")
	}
}

func TestClarifyFieldContract(t *testing.T) {
	for _, raw := range []string{
		`{"id":"Q","title":"t","stage":"screening","input":"hi","expect":{"kind":"clarify","clarify_fields":["unknown"]}}`,
		`{"id":"Q","title":"t","stage":"screening","input":"hi","expect":{"kind":"spec","clarify_fields":["budget_cny"]}}`,
		`{"id":"Q","title":"t","stage":"screening","input":"hi","expect":{"kind":"clarify","clarify_fields":["budget_cny","budget_cny"]}}`,
	} {
		if _, err := decodeCase([]byte(raw)); err == nil {
			t.Fatal("accepted invalid field contract")
		}
	}
}

type screeningStub struct {
	text string
	err  error
}

func (s screeningStub) Run(context.Context, string) (string, error) { return s.text, s.err }

func TestScreeningRecordsPreserveTextAndErrors(t *testing.T) {
	c := Case{ID: "Q", Stage: StageScreening, Expect: Expect{Kind: "clarify", ClarifyFields: []string{"budget_cny"}}}
	for _, stub := range []screeningStub{{text: "  您的预算是多少？\n"}, {}, {text: "预算多少？", err: errors.New("transport failed")}} {
		records, err := RunCases(context.Background(), []Case{c}, Deps{Screening: stub, Seeds: 2})
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := Summarize(ReportMeta{}, records).WriteJSONL(dir); err != nil {
			t.Fatal(err)
		}
		saved, err := ReadRecords(filepath.Join(dir, "results.jsonl"))
		if err != nil || !reflect.DeepEqual(records, saved) {
			t.Fatalf("record round trip: %v", err)
		}
		for _, r := range saved {
			if r.Screening == nil || r.Screening.Text != stub.text {
				t.Fatal("missing original text, including empty response")
			}
			if stub.err != nil {
				if r.Verdict.Passed || r.RunErr == "" || r.Verdict.Failures[0].ID != "RUN" {
					t.Fatal("execution error passed")
				}
			} else if !reflect.DeepEqual(r.Verdict, AssertScreeningCase(c, r.Screening.Text)) {
				t.Fatal("regrade differs")
			}
		}
	}
}
