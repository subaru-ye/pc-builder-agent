package evalsuite

import "testing"

func TestRepeatedQuestionDistinguishesRequestsFromStatements(t *testing.T) {
	for _, tt := range []struct {
		name, field, text string
		want              bool
	}{
		{"statement", "budget_basis", "预算按新增购买6000元记录。请提供CPU型号？", false},
		{"unrelated", "budget_cny", "预算6000元已记录，请提供CPU型号？", false},
		{"negated", "budget_basis", "无需再次确认新增购买预算。请提供CPU型号？", false},
		{"tag question", "budget_basis", "这是新增购买费用，对吗？", true},
		{"implicit request", "budget_basis", "还需要确认预算口径。", true},
		{"list", "budget_basis", "请补充以下信息：\n1. CPU完整型号\n2. 预算口径", true},
		{"known basis", "budget_basis", "您已说的6000元是新增购买其他配件的费用，对吗？", true},
		{"basis not amount", "budget_cny", "预算6000元是仅指新增购买费用，还是整机参考总价？", false},
		{"amount", "budget_cny", "新增购买预算是多少？", true},
		{"known resolution", "resolution", "您说显示器是2K的，我再确认一下分辨率？", true},
		{"resolution statement", "resolution", "2K分辨率已经明确。请提供CPU型号？", false},
		{"known use", "use_case", "是否主要用于纯游戏还是兼顾办公？", true},
		{"owned request", "owned_parts", "请分别提供CPU完整型号。", true},
		{"known owned statement", "owned_parts", "CPU型号已记录。请问预算是多少？", false},
		{"no cross paragraph", "budget_basis", "这是新增购买费用。还有其他问题吗？", false},
		{"purpose of owned question", "budget_basis", "以便准确判断是否需要购买其他配件：请问你已有的CPU和内存具体是什么型号？", false},
		{"basis choice across comma", "budget_cny", "但请确认：这笔6000元预算是指除了这颗CPU之外，新增购买其他配件的费用，还是包含这颗CPU价值在内的整机参考总价？", false},
		{"known basis while asking amount", "budget_basis", "这次新增购买其他配件的总预算是多少元？", false},
		{"known quantity", "owned_parts", "您已有CPU的数量是几颗？", true},
		{"amount imperative", "budget_basis", "请提供新增购买配件的预算金额。", false},
		{"amount imperative is amount", "budget_cny", "请提供新增购买配件的预算金额。", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := repeatedQuestion(tt.text, tt.field); (got != "") != tt.want {
				t.Fatalf("question=%q want=%v", got, tt.want)
			}
		})
	}
}

func TestScreeningKnownFieldsContract(t *testing.T) {
	c := Case{Stage: StageScreening, Expect: Expect{Kind: "clarify", ClarifyFields: []string{"owned_parts"}, ForbiddenClarifyFields: []string{"budget_basis", "budget_cny", "resolution", "use_case"}}}
	good := "日常办公、新增购买预算6000元已记录。请提供CPU和内存的完整型号？"
	if v := AssertScreeningCase(c, good); !v.Passed {
		t.Fatalf("statement should pass: %+v", v)
	}
	// 来自 v1.2 的真实尾问：问到缺失型号不能抵消重复确认已知预算。
	bad := "请提供CPU和内存的完整型号？另外，确认一下：您已说的6000元是新增购买其他配件的费用，对吗？"
	if v := AssertScreeningCase(c, bad); v.Passed {
		t.Fatal("repeated confirmation passed")
	} else {
		hasFailure(t, v, "S4")
	}
	c.Expect.ForbiddenClarifyFields = nil
	if v := AssertScreeningCase(c, bad); !v.Passed {
		t.Fatalf("old fixture must retain old expectations: %+v", v)
	}
	for _, raw := range []string{
		`{"id":"Q","title":"t","stage":"screening","input":"hi","expect":{"kind":"clarify","forbidden_clarify_fields":["unknown"]}}`,
		`{"id":"Q","title":"t","stage":"screening","input":"hi","expect":{"kind":"spec","forbidden_clarify_fields":["budget_cny"]}}`,
		`{"id":"Q","title":"t","stage":"screening","input":"hi","expect":{"kind":"clarify","clarify_fields":["budget_cny"],"forbidden_clarify_fields":["budget_cny"]}}`,
		`{"id":"Q","title":"t","stage":"screening","input":"hi","expect":{"kind":"clarify","forbidden_clarify_fields":["budget_cny","budget_cny"]}}`,
	} {
		if _, err := decodeCase([]byte(raw)); err == nil {
			t.Fatal("accepted contradictory or unsupported contract")
		}
	}
}
