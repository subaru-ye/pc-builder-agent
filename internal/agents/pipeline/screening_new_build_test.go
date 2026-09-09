package pipeline

import (
	"reflect"
	"strings"
	"testing"
)

func TestNewBuildDraftOnlyAsksRequiredMissingFields(t *testing.T) {
	for _, tc := range []struct {
		name, draft string
		missing     []string
	}{
		{"complete gaming without optional titles", `{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"},"noise_pref":"silent"}`, nil},
		{"general without monitor or owned parts", `{"schema_version":1,"budget_cny":4500,"use_case":{"type":"general"}}`, nil},
		{"productivity without resolution", `{"schema_version":1,"budget_cny":12000,"use_case":{"type":"productivity"}}`, nil},
		{"missing budget", `{"schema_version":1,"use_case":{"type":"gaming","resolution":"2K"}}`, []string{"budget_cny"}},
		{"missing resolution", `{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming"}}`, []string{"resolution"}},
		{"missing use", `{"schema_version":1,"budget_cny":8000}`, []string{"use_case"}},
		{"missing budget and use", `{"schema_version":1}`, []string{"budget_cny", "use_case"}},
		{"change stays change", `{"schema_version":1,"intent":"adjust_budget","budget_delta_cny":1000}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, missing := guardOwnedScreening(tc.draft, []string{"预算8000元、4500元、12000元，分辨率2K"})
			if !reflect.DeepEqual(missing, tc.missing) {
				t.Fatalf("missing %v want %v", missing, tc.missing)
			}
			if len(missing) == 0 && text != tc.draft {
				t.Fatalf("complete draft rewritten: %s", text)
			}
			if len(missing) > 0 && (extractJSONObject(text) != nil || strings.Contains(text, "已有") || strings.Contains(text, "游戏名")) {
				t.Fatalf("unrelated question or incomplete JSON: %s", text)
			}
		})
	}
}

func TestBudgetMustComeFromUser(t *testing.T) {
	for _, text := range []string{"预算8500", "预算为8,500元", "预算0.85万", "预算8.5k", "八千五百元", "8500", "新增购买预算 8500 元"} {
		if !groundedBudget(8500, []string{text}) {
			t.Errorf("valid amount rejected: %s", text)
		}
	}
	for _, text := range []string{"已有 Intel Core i5-8500 CPU，预算还没告诉你", "预算8000元", "显示器是2K"} {
		if groundedBudget(8500, []string{text}) {
			t.Errorf("unsupported amount accepted: %s", text)
		}
	}
	_, missing := guardOwnedScreening(`{"budget_cny":8500,"use_case":{"type":"general"}}`, []string{"办公主机，金额还没告诉你"})
	if !reflect.DeepEqual(missing, []string{"budget_cny"}) {
		t.Fatalf("invented budget escaped: %v", missing)
	}
}
