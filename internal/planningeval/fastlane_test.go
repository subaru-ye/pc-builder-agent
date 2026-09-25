package planningeval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/decision"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// fastlaneTestTurn 构造最小被评轮次。
func fastlaneTestTurn(quote string, budgetActive bool) FastlaneTurn {
	state := schemas.NewRequirementState()
	if budgetActive {
		state, _ = replayFastlaneState([]schemas.RequirementOperation{{
			Op: "set", Field: decision.FastlaneField,
			Value: []byte(`7000`), Evidence: "stated",
		}}, "预算7000")
	}
	return FastlaneTurn{Set: "corpus", CaseID: "t", Quote: quote, PriorState: state}
}

func TestFastlaneRulesPositiveCandidates(t *testing.T) {
	// 语料中的严格单字段快走候选（除同轮自我纠正因双值被 C1 拦截）。
	// accept-explicit 的 fixture 先验没有预算；raise/correction 已确立预算。
	inactive := []string{"预算提到9000吧", "那就7500吧", "可以，就按7500来"}
	active := []string{"预算改成6000吧", "预算提到9000吧"}
	for _, quote := range inactive {
		res := FastlaneRules(fastlaneTestTurn(quote, false))
		if !res.Eligible {
			t.Errorf("%q: 期望规则快走，实际 %s", quote, res.Stage)
		}
	}
	for _, quote := range active {
		res := FastlaneRules(fastlaneTestTurn(quote, true))
		if !res.Eligible {
			t.Errorf("%q: 期望规则快走，实际 %s", quote, res.Stage)
		}
	}
}

func TestFastlaneRulesFallbacks(t *testing.T) {
	cases := []struct {
		quote     string
		active    bool
		wantStage string
	}{
		{"7500够吗？", false, "c3_question"},              // C3（纯规则语义词表）
		{"看到7500的报价", false, "c5_reference"},            // C5
		{"7500不行，太贵了", false, "c4_reject"},              // C4
		{"预算不要超过7500", false, "c7_budget_semantics"},    // C7 封顶语义（金标还写 budget_flex）
		{"预算改成9000吧，机箱尽量小一点", false, "c8_compound_field"}, // C8 复合
		{"预算9000，开始配吧", false, "c6_execute_request"},    // C6 执行请求
		{"预算6000吧，哦不对，7000", false, "c1_multi_value"},   // C1 双值
		{"帧数至少144", false, "c2_out_of_range"},           // C2 哨兵
		{"配置单发一下", false, "c2_out_of_range"},            // C2 无在区间候选
		{"1080p就行", true, "c8_compound_field"},          // 分辨率误提取
		{"还是按原来的7500来", true, "c7_budget_semantics"},   // restore 语义：范围守卫拦下
		{"i5-8500 是我的CPU", false, "c8_compound_field"},  // 型号数字+品类词
	}
	for _, tc := range cases {
		res := FastlaneRules(fastlaneTestTurn(tc.quote, tc.active))
		if res.Eligible || res.Stage != tc.wantStage {
			t.Errorf("%q: 期望回退 %s，实际 eligible=%v stage=%s", tc.quote, tc.wantStage, res.Eligible, res.Stage)
		}
	}
}

func TestFastlaneRulesThroughC8DefersSemanticsToJev(t *testing.T) {
	// R+J 路由：范围守卫放行语义词表轮次，交给 Jev 判定。
	for _, quote := range []string{"7500够吗？", "看到7500的报价", "7500不行，太贵了"} {
		guard := FastlaneRulesThroughC8(fastlaneTestTurn(quote, false))
		if !guard.Eligible {
			t.Errorf("%q: 期望守卫放行（Jev 判定），实际 %s", quote, guard.Stage)
		}
	}
	// 复合/范围守卫轮次永远不问 Jev。
	for _, quote := range []string{"预算7500吧，机箱要小的", "预算改9000，然后开始吧", "7500或者8000都行", "预算不要超过7500", "还是按原来的7500来"} {
		guard := FastlaneRulesThroughC8(fastlaneTestTurn(quote, false))
		if guard.Eligible {
			t.Errorf("%q: 期望守卫拦截，实际放行", quote)
		}
	}
}

func TestFastlaneApplyOpLandsThroughReducer(t *testing.T) {
	turn := fastlaneTestTurn("预算提到9000吧", true)
	next, err := FastlaneApplyOp(turn.PriorState, turn.Quote, 9000)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	field := next.Fields[decision.FastlaneField]
	if field.Status != "active" {
		t.Fatalf("budget_cny status = %s", field.Status)
	}
	var value int
	if err := json.Unmarshal(field.Value, &value); err != nil || value != 9000 {
		t.Fatalf("budget_cny value = %s (%v)", field.Value, err)
	}
	if _, _, err := schemas.RequirementStateSpec(next); err != nil {
		t.Fatalf("readiness: %v", err)
	}
}

func TestBuildFastlaneCorpusExcludesHoldout(t *testing.T) {
	dataset, err := LoadRequirementV2("testdata/requirement-v2")
	if err != nil {
		t.Skipf("frozen dataset unavailable: %v", err)
	}
	turns, err := BuildFastlaneCorpus(dataset)
	if err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("corpus is empty")
	}
	labelFast := 0
	for _, turn := range turns {
		if turn.Split == "holdout" || strings.Contains(turn.CaseID, "holdout") {
			t.Fatalf("holdout leak: %s", turn.CaseID)
		}
		if turn.LabelFast {
			labelFast++
		}
	}
	if labelFast != 5 {
		t.Errorf("label_fast = %d, want 5 (accept-explicit, raise, correction-same-turn, cv-fps#2, cv-budget-correction#1)", labelFast)
	}
}

func TestLoadFastlaneSetB(t *testing.T) {
	setb, err := LoadFastlaneSetB("testdata/requirement-v2")
	if err != nil {
		t.Fatalf("load set-b: %v", err)
	}
	if len(setb) < 20 {
		t.Fatalf("set-b rows = %d, want >= 20", len(setb))
	}
	cal, report := 0, 0
	for _, c := range setb {
		if c.Partition == "cal" {
			cal++
		} else {
			report++
		}
	}
	if cal == 0 || report == 0 {
		t.Fatalf("partitions cal=%d report=%d, both must be non-empty", cal, report)
	}
}
