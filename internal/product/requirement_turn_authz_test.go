package product

// Spec 3 返工:accepted-proposal 授权边界的服务器端语义核验。
// 反例:拒绝语句、多提案歧义、缺值/非问句提案、短数字 panic、数字子串误匹配。
// 正例:单提案裸"可以"、明确复述值的多提案指向。
import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func acceptOp(field string, value string, quote string) schemas.RequirementOperation {
	return schemas.RequirementOperation{Op: "set", Field: field, Value: json.RawMessage(value), Evidence: "accepted_proposal", Quote: quote}
}

// 修复①反例:用户明确拒绝("不行")时,模型标签+字段/值匹配也不得写入 active。
func TestRejectedUtteranceNeverAdoptsProposal(t *testing.T) {
	for _, quote := range []string{"不行", "先不要，我再想想", "算了，不要按这个来", "不换，保持现状"} {
		source := schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: quote}
		turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
			acceptOp("budget_cny", "7500", quote),
		}}
		proposals := []store.RequirementProposalRecord{{Field: "budget_cny", Value: json.RawMessage("7500")}}
		accepted := verifyAcceptedProposals(turn, proposals, source)
		if len(accepted) != 0 || len(turn.Operations) != 0 {
			t.Fatalf("拒绝语句 %q 不得采纳建议: accepted=%v ops=%d", quote, accepted, len(turn.Operations))
		}
		if len(turn.Observations) == 0 {
			t.Fatalf("拒绝语句 %q 应保留 observation", quote)
		}
	}
}

// 修复①反例:裸"可以"对应多个提案时指向不明,不自动采用。
func TestAmbiguousAcceptWithMultipleProposalsIsNotAdopted(t *testing.T) {
	source := schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}
	proposals := []store.RequirementProposalRecord{
		{Field: "budget_cny", Value: json.RawMessage("7500")},
		{Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`)},
	}
	// 模型只输出其中一个,服务器也不知道"可以"指向哪个。
	turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("budget_cny", "7500", "可以"),
	}}
	accepted := verifyAcceptedProposals(turn, proposals, source)
	if len(accepted) != 0 || len(turn.Operations) != 0 {
		t.Fatalf("多提案+裸可以不得自动采用: accepted=%v ops=%d", accepted, len(turn.Operations))
	}
	// 模型一次输出两个也全部降级。
	turn2 := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("budget_cny", "7500", "可以"),
		acceptOp("use_case.resolution", `"1080p"`, "可以"),
	}}
	if accepted := verifyAcceptedProposals(turn2, proposals, source); len(accepted) != 0 || len(turn2.Operations) != 0 {
		t.Fatalf("多提案双接受不得自动采用: %v", accepted)
	}
	if rejectedAcceptCount(*turn2) != 2 {
		t.Fatalf("两个降级都应提示指明字段和值: %+v", turn2.Observations)
	}
}

// 修复①正例:单提案 + 裸"可以"仍被采纳(回归);多提案 + 明确复述值也采纳。
func TestExplicitAndSingleProposalAcceptStillWorks(t *testing.T) {
	single := []store.RequirementProposalRecord{{Field: "budget_cny", Value: json.RawMessage("7500")}}
	source := schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}
	turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("budget_cny", "7500", "可以"),
	}}
	if accepted := verifyAcceptedProposals(turn, single, source); len(accepted) != 1 {
		t.Fatalf("单提案+裸可以应被采纳: %v", accepted)
	}
	multi := []store.RequirementProposalRecord{
		{Field: "budget_cny", Value: json.RawMessage("7500")},
		{Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`)},
	}
	turn2 := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("use_case.resolution", `"1080p"`, "就用 1080p 吧"),
	}}
	if accepted := verifyAcceptedProposals(turn2, multi, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "就用 1080p 吧"}); len(accepted) != 1 || accepted[0].Field != "use_case.resolution" {
		t.Fatalf("明确指向应采纳被指明项: %v", accepted)
	}
}

// 修复②:提案文本必须呈现具体值并以问句收尾才可展示/保存;
// "可以吗?"不能暗中绑定预算值。
func TestPresentableProposalRequiresValueAndQuestion(t *testing.T) {
	budget := json.RawMessage("7500")
	cases := []struct {
		name, text string
		value      json.RawMessage
		want       bool
	}{
		{"完整问句", "按 7500 元的预算继续可以吗？", budget, true},
		{"不带问号但含吗", "按 7500 元的预算继续可以吗", budget, true},
		{"缺具体值", "这样可以吗？", budget, false},
		{"值被更大数字包含", "75000 这样可以吗？", budget, false},
		{"不是问句", "按 7500 元的预算继续。", budget, false},
		{"文本枚举值呈现", "选 1080p 的分辨率可以吗？", json.RawMessage(`"1080p"`), true},
		{"枚举缺值", "用这个分辨率可以吗？", json.RawMessage(`"1080p"`), false},
		{"数组值不可呈现", "这样配可以吗？", json.RawMessage(`["gpu"]`), false},
		{"短数字问句", "60 帧够用吗？", json.RawMessage("60"), true},
	}
	for _, tc := range cases {
		proposal := pipeline.RequirementProposal{Field: "budget_cny", Value: tc.value, Text: tc.text}
		if got := presentableProposal(proposal, tc.value); got != tc.want {
			t.Fatalf("%s: presentable=%v want %v (%s)", tc.name, got, tc.want, tc.text)
		}
	}
}

// 数字 token 等值语义(供采纳句式与提案值呈现使用):短数字不 panic,
// "75000"不是"7500"的证据,千分位归一仍匹配。
func TestQuoteContainsNumberBoundaries(t *testing.T) {
	if !quoteContainsNumber("60 帧够用吗", "60") {
		t.Fatal("60 应有依据")
	}
	if quoteContainsNumber("160 帧", "60") {
		t.Fatal("160 不是 60 的证据")
	}
	if quoteContainsNumber("预算75000", "7500") {
		t.Fatal("75000 不是 7500 的证据")
	}
	if !quoteContainsNumber("预算 7,500 元", "7500") {
		t.Fatal("千分位应归一匹配")
	}
	if !quoteContainsNumber("预算7500吧", "7500") {
		t.Fatal("连续数字 token 应匹配")
	}
	if quoteContainsNumber("这个价位 500 元", "7500") {
		t.Fatal("无关数字不是证据")
	}
}

// 最后一处定向修复:肯定判定只认完整匹配短答或带值的采纳句式,
// 否定复合词与间接表达一律保守拒绝。
func TestNegativeCompoundsNeverShowAcceptance(t *testing.T) {
	single := []store.RequirementProposalRecord{{Field: "budget_cny", Value: json.RawMessage("7500")}}
	for _, quote := range []string{"不可以", "行不通", "我不想按 7500", "不按 7500 来"} {
		turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
			acceptOp("budget_cny", "7500", quote),
		}}
		accepted := verifyAcceptedProposals(turn, single, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: quote})
		if len(accepted) != 0 || len(turn.Operations) != 0 {
			t.Fatalf("否定表达 %q 不得写入 active: accepted=%v ops=%d", quote, accepted, len(turn.Operations))
		}
		if quoteShowsAcceptance(quote, json.RawMessage("7500")) {
			t.Fatalf("否定表达 %q 不得命中肯定判定", quote)
		}
		if len(turn.Observations) == 0 {
			t.Fatalf("否定表达 %q 应保留 observation", quote)
		}
	}
	// 正例:单提案"可以"与"就按 7500 来"保持采纳。
	for _, quote := range []string{"可以", "就按 7500 来"} {
		turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
			acceptOp("budget_cny", "7500", quote),
		}}
		accepted := verifyAcceptedProposals(turn, single, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: quote})
		if len(accepted) != 1 {
			t.Fatalf("正例 %q 应被采纳: %v", quote, accepted)
		}
	}
	// 多提案歧义规则保持:裸"可以"不自动指向。
	multi := []store.RequirementProposalRecord{
		{Field: "budget_cny", Value: json.RawMessage("7500")},
		{Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`)},
	}
	turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("budget_cny", "7500", "可以"),
	}}
	if accepted := verifyAcceptedProposals(turn, multi, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}); len(accepted) != 0 {
		t.Fatalf("多提案+裸可以不得自动采用: %v", accepted)
	}
}

// 修复①(返工):接受意图必须依据完整原话,先排除询问再排除拒绝;
// "7500 不行""7500 够吗？"都不得写入 active,数字相同也不得降级 stated。
func TestAcceptanceIntentChecksFullUtteranceFirst(t *testing.T) {
	proposals := []store.RequirementProposalRecord{{Field: "budget_cny", Value: json.RawMessage("7500")}}
	for _, quote := range []string{"7500 不行", "7500 够吗？", "7500 可以吗？先问问", "7500怎么样"} {
		turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
			acceptOp("budget_cny", "7500", quote),
		}}
		accepted := verifyAcceptedProposals(turn, proposals, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: quote})
		if len(accepted) != 0 || len(turn.Operations) != 0 {
			t.Fatalf("原话 %q 不得写入 active: accepted=%v ops=%d", quote, accepted, len(turn.Operations))
		}
	}
	// 无提案时,相同数字也不得把失败的提案操作降级为 stated。
	for _, quote := range []string{"7500 够吗？", "7500 不行"} {
		turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
			acceptOp("budget_cny", "7500", quote),
		}}
		verifyAcceptedProposals(turn, nil, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: quote})
		if len(turn.Operations) != 0 {
			t.Fatalf("原话 %q 不得降级 stated 写入: %v", quote, turn.Operations)
		}
	}
	// 正例:单提案裸"可以"与多提案下复述值("按 7500 来")仍正常采纳。
	turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("budget_cny", "7500", "可以"),
	}}
	if accepted := verifyAcceptedProposals(turn, proposals, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}); len(accepted) != 1 {
		t.Fatalf("单提案裸可以应采纳: %v", accepted)
	}
	multi := []store.RequirementProposalRecord{
		{Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`)},
		{Field: "budget_cny", Value: json.RawMessage("7500")},
	}
	turn2 := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("budget_cny", "7500", "就按 7500 来吧"),
	}}
	if accepted := verifyAcceptedProposals(turn2, multi, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "就按 7500 来吧"}); len(accepted) != 1 || accepted[0].Field != "budget_cny" {
		t.Fatalf("按7500来应被采纳: %v", accepted)
	}
}

// 修复②(返工):提案文本也是模型生成的最终回复,必须通过 V8/V9 守卫;
// 问句校验要求以接受问句收尾,而非全文任意位置出现问句词。
func TestPresentableProposalGuardsWorkflowAndTailQuestion(t *testing.T) {
	budget := json.RawMessage("7500")
	cases := []struct {
		name, text string
		want       bool
	}{
		{"守卫拦截执行宣称", "按 7500 继续可以吗？我已开始生成配置", false},
		{"守卫拦截外设承诺", "预算按 7500 来可以吗？显示器也一起配上", false},
		{"问句后跟陈述", "按 7500 继续可以吗？就这么定了。", false},
		{"问句词不在尾部", "按 7500 来,这件吗事不用再提。", false},
		{"标准接受问句", "按 7500 元的预算继续可以吗？", true},
		{"尾部无问号的吗", "按 7500 元的预算继续可以吗", true},
		{"60帧问句", "60 帧够用吗？", true},
	}
	for _, tc := range cases {
		value := budget
		if tc.name == "60帧问句" {
			value = json.RawMessage("60")
		}
		proposal := pipeline.RequirementProposal{Field: "budget_cny", Value: value, Text: tc.text}
		if got := presentableProposal(proposal, value); got != tc.want {
			t.Fatalf("%s: presentable=%v want %v (%q)", tc.name, got, tc.want, tc.text)
		}
	}
	// 问句词不在尾部窗口:不得通过。
	if presentableProposal(pipeline.RequirementProposal{Field: "budget_cny", Value: budget, Text: "按 7500 来,这吗事不用再提。"}, budget) {
		t.Fatal("问句词不在尾部不得通过")
	}
}

// 返工:非肯定语句不得默认视为同意;"我看到 7500 元报价"仅凭数字
// 不得采纳,也不得降级 stated。
func TestNeutralUtteranceIsNotConsent(t *testing.T) {
	single := []store.RequirementProposalRecord{{Field: "budget_cny", Value: json.RawMessage("7500")}}
	for _, quote := range []string{"我先去吃饭", "等我想想", "我看到 7500 元报价", "今天天气不错"} {
		turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
			acceptOp("budget_cny", "7500", quote),
		}}
		accepted := verifyAcceptedProposals(turn, single, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: quote})
		if len(accepted) != 0 || len(turn.Operations) != 0 {
			t.Fatalf("非肯定原话 %q 不得采纳建议: accepted=%v ops=%d", quote, accepted, len(turn.Operations))
		}
		// 单提案存在也不得降级 stated 写入。
		if turn.Operations != nil && len(turn.Operations) > 0 {
			t.Fatalf("非肯定原话 %q 不得降级 stated: %v", quote, turn.Operations)
		}
	}
	// 正例保持:单提案"可以"与明确"按 7500 来"仍采纳。
	turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("budget_cny", "7500", "可以"),
	}}
	if accepted := verifyAcceptedProposals(turn, single, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}); len(accepted) != 1 {
		t.Fatalf("单提案裸可以应采纳: %v", accepted)
	}
	turn2 := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		acceptOp("budget_cny", "7500", "按 7500 来"),
	}}
	if accepted := verifyAcceptedProposals(turn2, single, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "按 7500 来"}); len(accepted) != 1 {
		t.Fatalf("按 7500 来应采纳: %v", accepted)
	}
}

// 服务级反例:用户在问行情,模型却贴 accepted_proposal 标签——完整服务路径
// 不得写入 active,也不得降级 stated。
func TestServiceQuestionTurnNeverAdoptsProposal(t *testing.T) {
	st := newPlanningFakeStore()
	state := seededGamingState(t)
	state.Fields["budget_cny"] = schemas.RequirementField{Status: "unknown"}
	st.session.RequirementState = marshalState(t, state)
	sink := newFakeSink()
	agent := &fakeAgent{store: st.fakeProductStore, contextAvailable: true}
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	agent.screen = turnWith(
		&schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}}, "", pipeline.RequirementTurnSignals{},
		pipeline.RequirementProposal{Field: "budget_cny", Value: json.RawMessage("7500"), Text: "按 7500 元的预算继续可以吗？"},
	)
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000211", "先看预算")
	sink.wait(t)
	agent.screen = ScreenResult{Turn: &pipeline.RequirementTurnResult{
		Operations: []schemas.RequirementOperation{acceptOp("budget_cny", "7500", "7500 够吗？")},
		Signals:    pipeline.RequirementTurnSignals{AsksQuestion: true},
	}}
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000212", "7500 够吗？")
	sink.wait(t)
	if field := decodeState(t, st).Fields["budget_cny"]; field.Status == "active" {
		t.Fatalf("问句轮不得写入 active: %+v", field)
	}
	st.mu.Lock()
	resolved := len(st.proposals) > 0 && st.proposals[0].resolved
	st.mu.Unlock()
	if resolved {
		t.Fatal("询问轮不得把建议标记 resolved")
	}
}

// 服务级反例:提案文本带执行宣称时,该文本不得展示、不得保存。
func TestServiceDropsProposalWithWorkflowClaim(t *testing.T) {
	st := newPlanningFakeStore()
	st.session.RequirementState = marshalState(t, seededGamingState(t))
	sink := newFakeSink()
	agent := &fakeAgent{store: st.fakeProductStore, contextAvailable: true}
	svc, _ := NewService(context.Background(), st, agent, sink)
	agent.screen = turnWith(
		&schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}}, "", pipeline.RequirementTurnSignals{},
		pipeline.RequirementProposal{Field: "budget_cny", Value: json.RawMessage("7500"), Text: "按 7500 继续可以吗？我已开始生成配置"},
	)
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000213", "预算多少合适")
	sink.wait(t)
	reply := lastAssistant(t, st).Content
	if strings.Contains(reply, "我已开始生成配置") {
		t.Fatalf("提案文本的虚假宣称不得透传: %s", reply)
	}
	st.mu.Lock()
	saved := len(st.proposals)
	st.mu.Unlock()
	if saved != 0 {
		t.Fatalf("带工作流宣称的提案不得保存: %d", saved)
	}
}

// 服务级反例:拒绝语句经完整服务路径不得写入 active,且提案保持未解析。
func TestServiceRejectsProposalAcceptanceEndToEnd(t *testing.T) {
	st := newPlanningFakeStore()
	state := seededGamingState(t)
	state.Fields["budget_cny"] = schemas.RequirementField{Status: "unknown"}
	st.session.RequirementState = marshalState(t, state)
	sink := newFakeSink()
	agent := &fakeAgent{store: st.fakeProductStore, contextAvailable: true}
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}

	// 轮1:助手建议 7500(合格问句),保存提案。
	agent.screen = turnWith(
		&schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}}, "", pipeline.RequirementTurnSignals{},
		pipeline.RequirementProposal{Field: "budget_cny", Value: json.RawMessage("7500"), Text: "按 7500 元的预算继续可以吗？"},
	)
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000201", "先看预算")
	sink.wait(t)
	st.mu.Lock()
	saved := len(st.proposals)
	st.mu.Unlock()
	if saved != 1 {
		t.Fatalf("合格提案应保存: %d", saved)
	}

	// 轮2:用户拒绝,模型仍贴 accepted_proposal 标签。
	agent.screen = ScreenResult{Turn: &pipeline.RequirementTurnResult{
		Operations: []schemas.RequirementOperation{acceptOp("budget_cny", "7500", "不行")},
	}}
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000202", "不行，再看看")
	sink.wait(t)
	if field := decodeState(t, st).Fields["budget_cny"]; field.Status == "active" {
		t.Fatal("拒绝语句不得把建议写入 active")
	}
	st.mu.Lock()
	resolved := st.proposals[0].resolved
	st.mu.Unlock()
	if resolved {
		t.Fatal("被拒绝的建议不得标记 resolved(用户可能改口)")
	}
	if reply := lastAssistant(t, st).Content; !strings.Contains(reply, "请直接说明要采用的字段和值") {
		t.Fatalf("拒绝后应提示指明字段和值: %s", reply)
	}

	// 轮3(反例②补链):助手只给"这样可以吗?"——缺具体值,不得保存提案。
	agent.screen = turnWith(
		&schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}}, "", pipeline.RequirementTurnSignals{},
		pipeline.RequirementProposal{Field: "budget_cny", Value: json.RawMessage("8000"), Text: "这样可以吗？"},
	)
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000203", "那预算多少合适")
	sink.wait(t)
	reply := lastAssistant(t, st).Content
	if strings.Contains(reply, "这样可以吗？") {
		t.Fatalf("缺值的建议不得展示为可接受提案: %s", reply)
	}
	st.mu.Lock()
	saved = len(st.proposals)
	st.mu.Unlock()
	if saved != 1 {
		t.Fatalf("缺值建议不得保存: %d", saved)
	}
}
