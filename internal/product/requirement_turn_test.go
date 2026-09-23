package product

// Screening v2 确定性测试:assistant proposal 可见性/紧邻/失效、answer 守卫、
// presentation action、unsupported 撤销与裸"可以"边界。模型输出由 fakeAgent
// 提供替身,断言只针对服务器确定性逻辑。
import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func seededGamingState(t *testing.T) schemas.RequirementState {
	t.Helper()
	state := schemas.NewRequirementState()
	user := "打游戏用，1080p，配件全新买"
	source := schemas.RequirementSource{Kind: "chat", MessageID: "seed", Quote: user}
	update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "打游戏"},
		{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"1080p"`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "1080p"},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Kind: "fact", Strength: "must", Scope: "session", Evidence: "stated", Quote: "全新买"},
	}}
	next, err := schemas.ApplyRequirementUpdate(state, update, source)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func turnWith(update *schemas.RequirementUpdate, answer string, signals pipeline.RequirementTurnSignals, proposals ...pipeline.RequirementProposal) ScreenResult {
	turn := pipeline.RequirementTurnResult{Operations: update.Operations, Observations: update.Observations, Answer: answer, Signals: signals, Proposals: proposals}
	return ScreenResult{Turn: &turn, RequirementUpdate: update}
}

func lastAssistant(t *testing.T, st *planningFakeStore) store.WebMessage {
	t.Helper()
	messages, _ := st.WebMessages(context.Background(), "session-1")
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return messages[i]
		}
	}
	t.Fatal("no assistant message")
	return store.WebMessage{}
}

func sendTurn(t *testing.T, svc *Service, requestID, text string) {
	t.Helper()
	if _, err := svc.StartMessage(context.Background(), "owner-1", "session-1", requestID, text); err != nil {
		t.Fatal(err)
	}
}

// 验收场景 2/3/8:助手建议保存前必须展示;紧邻轮"可以"经服务器验证才采用。
func TestProposalVisibleAdjacentAcceptProtocol(t *testing.T) {
	st := newPlanningFakeStore()
	st.session.RequirementState = marshalState(t, seededGamingState(t))
	sink := newFakeSink()
	agent := &fakeAgent{store: st.fakeProductStore, contextAvailable: true}
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}

	// 轮1:用户问"7500够吗?",模型回答并建议 budget_cny=7500。
	agent.screen = turnWith(
		&schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}},
		"7500 在 1080p 游戏主机上是够用的预算档。", pipeline.RequirementTurnSignals{AsksQuestion: true},
		pipeline.RequirementProposal{Field: "budget_cny", Value: json.RawMessage("7500"), Text: "按 7500 元的预算继续可以吗？"},
	)
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000101", "7500够吗？")
	sink.wait(t)

	state := decodeState(t, st)
	if state.Fields["budget_cny"].Status == "active" {
		t.Fatal("咨询行情不得把建议值写成 active 需求")
	}
	reply := lastAssistant(t, st).Content
	if !strings.Contains(reply, "按 7500 元的预算继续可以吗？") {
		t.Fatalf("建议问句必须展示给用户后才能保存: %s", reply)
	}
	st.mu.Lock()
	saved := append([]fakeRequirementProposal(nil), st.proposals...)
	st.mu.Unlock()
	if len(saved) != 1 || saved[0].field != "budget_cny" || saved[0].consumedBy != "" {
		t.Fatalf("建议应与助手消息同轮原子保存且尚未消费: %+v", saved)
	}
	// 建议只对紧邻的下一条用户消息有效:轮1自身不可见。
	if proposals, _ := st.ActiveRequirementProposals(context.Background(), "session-1", currentUserMessageID(t, st, 1)); len(proposals) != 0 {
		t.Fatalf("建议不得对产生它的轮次生效: %+v", proposals)
	}

	// 轮2:"可以" — 模型标签 accepted_proposal,由服务器核验同字段同值。
	agent.screen = ScreenResult{Turn: &pipeline.RequirementTurnResult{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_cny", Value: json.RawMessage("7500"), Kind: "constraint", Strength: "must", Scope: "session", Evidence: "accepted_proposal", Quote: "可以"},
		},
		Signals: pipeline.RequirementTurnSignals{},
	}}
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000102", "可以")
	sink.wait(t)

	state = decodeState(t, st)
	if field := state.Fields["budget_cny"]; field.Status != "active" || string(field.Value) != "7500" || field.Evidence != "accepted_proposal" {
		t.Fatalf("匹配建议应被采用并保留证据: %+v", field)
	}
	if readiness, _ := schemas.EvaluateRequirementReadiness(state); !readiness.ConfirmationEligible {
		t.Fatalf("接受建议后应就绪: %+v", readiness)
	}

	// 轮3:再次"可以" — 建议已消费,标签不再可信,不得复用。
	agent.screen = ScreenResult{Turn: &pipeline.RequirementTurnResult{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_flex", Value: json.RawMessage("0.2"), Evidence: "accepted_proposal", Quote: "可以"},
		},
	}}
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000103", "可以")
	sink.wait(t)
	state = decodeState(t, st)
	if state.Fields["budget_flex"].Status == "active" {
		t.Fatal("非紧邻轮或已失效建议不得通过 accepted_proposal 标签采用")
	}
	if reply := lastAssistant(t, st).Content; !strings.Contains(reply, "请直接说明要采用的字段和值") {
		t.Fatalf("未验证接受应提示指明字段和值: %s", reply)
	}
}

// 验收场景 7:不存在可验证建议时,"可以"不能凭上下文编造字段。
func TestBareAcceptWithoutProposalCreatesNoField(t *testing.T) {
	st := newPlanningFakeStore()
	st.session.RequirementState = marshalState(t, seededGamingState(t))
	sink := newFakeSink()
	agent := &fakeAgent{store: st.fakeProductStore, contextAvailable: true}
	agent.screen = ScreenResult{Turn: &pipeline.RequirementTurnResult{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_cny", Value: json.RawMessage("8000"), Evidence: "accepted_proposal", Quote: "可以"},
		},
		Signals: pipeline.RequirementTurnSignals{Ambiguous: true},
	}}
	svc, _ := NewService(context.Background(), st, agent, sink)
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000110", "可以")
	sink.wait(t)
	state := decodeState(t, st)
	if state.Fields["budget_cny"].Status == "active" {
		t.Fatal("无可验证建议时不得编造字段")
	}
	if field := state.Fields["budget_cny"]; field.Status != "unknown" && field.Status != "conflict" {
		t.Fatalf("裸可以不得污染预算字段: %+v", field)
	}
	if reply := lastAssistant(t, st).Content; !strings.Contains(reply, "请直接说明要采用的字段和值") {
		t.Fatalf("应要求用户指明字段和值: %s", reply)
	}
}

// 未展示的建议(值非法)不得保存,也不得被后续"可以"接受。
func TestUnshownProposalIsDroppedAndUnacceptable(t *testing.T) {
	st := newPlanningFakeStore()
	st.session.RequirementState = marshalState(t, seededGamingState(t))
	sink := newFakeSink()
	agent := &fakeAgent{store: st.fakeProductStore, contextAvailable: true, screen: turnWith(
		&schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}}, "", pipeline.RequirementTurnSignals{},
		pipeline.RequirementProposal{Field: "budget_cny", Value: json.RawMessage(`"abc"`), Text: "按 abc 元的预算继续可以吗？"},
	)}
	svc, _ := NewService(context.Background(), st, agent, sink)
	sendTurn(t, svc, "00000000-0000-4000-8000-000000000120", "预算多少合适")
	sink.wait(t)
	if reply := lastAssistant(t, st).Content; strings.Contains(reply, "abc") {
		t.Fatalf("非法建议值不得展示: %s", reply)
	}
	if proposals, _ := st.ActiveRequirementProposals(context.Background(), "session-1", currentUserMessageID(t, st, 1)); len(proposals) != 0 {
		t.Fatalf("未展示的建议不得保存: %+v", proposals)
	}
}

func TestGuardScreeningAnswer(t *testing.T) {
	if got := guardScreeningAnswer("已开始生成配置，请稍候。"); got != answerExecutionHonest {
		t.Fatalf("虚假执行宣称必须替换: %q", got)
	}
	if got := guardScreeningAnswer("好的，显示器和主机一起给你配。"); got != answerScopeHonest {
		t.Fatalf("主机外品类承诺必须替换: %q", got)
	}
	if got := guardScreeningAnswer("1080p 分辨率下这套配置可以流畅运行。"); got != "1080p 分辨率下这套配置可以流畅运行。" {
		t.Fatalf("正常回答不得改写: %q", got)
	}
	if got := guardScreeningAnswer(""); got != "" {
		t.Fatalf("空回答保持为空: %q", got)
	}
	// 我有一台 1080p 显示器(背景陈述回答)不属于品类承诺。
	if got := guardScreeningAnswer("1080p 显示器下这个预算的帧率表现足够。"); got == answerScopeHonest {
		t.Fatal("提及外设但无承诺动词的回答不得误替换")
	}
}

func TestRequirementPresentationAction(t *testing.T) {
	question := func(reason string, fields ...string) *schemas.RequirementQuestion {
		return &schemas.RequirementQuestion{ReasonCode: reason, Fields: fields}
	}
	ready := schemas.RequirementReadiness{Status: "ready", ConfirmationEligible: true}
	incomplete := schemas.RequirementReadiness{Status: "incomplete", MissingFields: []string{"budget_cny"}}
	build := pipeline.RequirementTurnSignals{RequestsBuild: true}
	if action, fields := requirementPresentationAction(ready, build, nil); action != "open_requirement_review" || fields != nil {
		t.Fatalf("ready+build 应打开核定: %s %v", action, fields)
	}
	if action, fields := requirementPresentationAction(ready, pipeline.RequirementTurnSignals{}, nil); action != "" || fields != nil {
		t.Fatalf("ready 无请求不得自动打开面板: %s %v", action, fields)
	}
	action, fields := requirementPresentationAction(incomplete, build, question("requirement_conflict", "budget_cny"))
	if action != "focus_missing_requirement" || len(fields) != 1 || fields[0] != "budget_cny" {
		t.Fatalf("incomplete+build 应聚焦首个阻塞项: %s %v", action, fields)
	}
	if action, _ := requirementPresentationAction(incomplete, pipeline.RequirementTurnSignals{}, nil); action != "" {
		t.Fatalf("incomplete 无请求不产生 action: %s", action)
	}
	// unsupported 优先于普通缺项。
	action, fields = requirementPresentationAction(incomplete, build, question("unsupported_capability", "monitor"))
	if action != "focus_missing_requirement" || fields[0] != "monitor" {
		t.Fatalf("unsupported 是首个阻塞项: %s %v", action, fields)
	}
}

// 仅 conflict 或仅 unsupported 阻塞时,追问必须指向该阻塞而不是普通缺项。
func TestQuestionTargetsConflictAndUnsupportedFirst(t *testing.T) {
	conflict := seededGamingState(t)
	conflict.Fields["budget_cny"] = schemas.RequirementField{Status: "conflict", Strength: "must", Evidence: "uncertain"}
	reply := composeTurnReply(conflict, mustReadiness(t, conflict), pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{}},
		nil, ConfirmationUnconfirmed, false)
	if !strings.Contains(reply, "预算应采用哪个要求") {
		t.Fatalf("冲突应先于缺项追问: %s", reply)
	}

	unsupported := seededGamingState(t)
	unsupported.Observations = []schemas.RequirementObservation{{Text: "要带显示器", Reason: "unsupported_capability:monitor", Source: schemas.RequirementSource{Kind: "chat", Quote: "要带显示器"}}}
	reply = composeTurnReply(unsupported, mustReadiness(t, unsupported), pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{}},
		nil, ConfirmationUnconfirmed, false)
	if !strings.Contains(reply, "仅支持主机") || !strings.Contains(reply, "显示器") {
		t.Fatalf("unsupported 追问应说明 tower 边界: %s", reply)
	}
}

func TestVerifyAcceptedProposalsValueMustMatch(t *testing.T) {
	source := schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}
	turn := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage("9000"), Evidence: "accepted_proposal", Quote: "可以"},
	}}
	proposals := []store.RequirementProposalRecord{
		{Field: "budget_cny", Value: json.RawMessage("7500"), Text: "按 7500 元继续可以吗？"},
	}
	if accepted := verifyAcceptedProposals(turn, proposals, source); len(accepted) != 0 {
		t.Fatal("值不同不得采用")
	}
	if len(turn.Operations) != 0 {
		t.Fatal("未验证操作必须移除")
	}
	if len(turn.Observations) != 1 {
		t.Fatalf("未验证接受应保留 observation: %+v", turn.Observations)
	}
	// 多个未解析建议 + 裸"可以"指向不明:Spec 要求不自动采用。
	turn2 := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage("7500"), Evidence: "accepted_proposal", Quote: "可以"},
		{Op: "set", Field: "budget_flex", Value: json.RawMessage("0.1"), Evidence: "accepted_proposal", Quote: "可以"},
	}}
	proposals2 := []store.RequirementProposalRecord{
		{Field: "budget_cny", Value: json.RawMessage("7500")},
		{Field: "budget_flex", Value: json.RawMessage("0.1")},
	}
	accepted := verifyAcceptedProposals(turn2, proposals2, source)
	if len(accepted) != 0 || len(turn2.Operations) != 0 {
		t.Fatalf("多提案+模糊可以不得自动采用: accepted=%v ops=%d", accepted, len(turn2.Operations))
	}
	// 用户明确复述数值指向其中一个:多提案也可接受被指明的那一项。
	turn3 := &pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage("7500"), Evidence: "accepted_proposal", Quote: "就按7500来吧"},
	}}
	accepted = verifyAcceptedProposals(turn3, proposals2, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "就按7500来吧"})
	if len(accepted) != 1 || accepted[0].Field != "budget_cny" {
		t.Fatalf("明确指向应被采纳: %+v", accepted)
	}
}

// 验收场景 10/11:unsupported 撤销只认能力专属 remove;背景提及不阻塞。
func TestUnsupportedWithdrawUnblocksOnlyViaCapabilityRemove(t *testing.T) {
	state := seededGamingState(t)
	state.Fields["budget_cny"] = schemas.RequirementField{Status: "active", Value: json.RawMessage("8000"), Evidence: "stated"}
	state.Observations = []schemas.RequirementObservation{{Text: "还要配显示器", Reason: "unsupported_capability:monitor", Source: schemas.RequirementSource{Kind: "chat", Quote: "还要配显示器"}}}
	if readiness, _ := schemas.EvaluateRequirementReadiness(state); readiness.ConfirmationEligible {
		t.Fatal("未解决 unsupported 必须阻塞确认")
	}
	withdrawn, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "remove", Field: "unsupported.monitor", Quote: "显示器先不要了"},
	}}, schemas.RequirementSource{Kind: "chat", MessageID: "m2", Quote: "显示器先不要了"})
	if err != nil {
		t.Fatal(err)
	}
	if readiness, _ := schemas.EvaluateRequirementReadiness(withdrawn); !readiness.ConfirmationEligible {
		t.Fatalf("明确放弃后应解除阻塞: %+v", readiness)
	}
	// notes 更新不解除阻塞。
	notesOnly, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "notes", Value: json.RawMessage(`"更新了说明"`), Kind: "context", Evidence: "stated", Quote: "更新了说明"},
	}}, schemas.RequirementSource{Kind: "chat", MessageID: "m3", Quote: "更新了说明"})
	if err != nil {
		t.Fatal(err)
	}
	if readiness, _ := schemas.EvaluateRequirementReadiness(notesOnly); readiness.ConfirmationEligible || len(readiness.UnsupportedCapabilities) != 1 {
		t.Fatalf("无关字段更新不得解除 unsupported 阻塞: %+v", readiness)
	}
}

func TestAcceptedProposalEvidenceContract(t *testing.T) {
	base := seededGamingState(t)
	op := schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: json.RawMessage("7500"), Evidence: "accepted_proposal", Quote: "可以"}
	if _, err := schemas.ApplyRequirementUpdate(base, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}},
		schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}); err != nil {
		t.Fatalf("chat 轮 set + 本轮 quote 应接受: %v", err)
	}
	nonSet := op
	nonSet.Op = "remove"
	if _, err := schemas.ApplyRequirementUpdate(base, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{nonSet}},
		schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}); err == nil {
		t.Fatal("accepted_proposal 仅支持 set")
	}
	if _, err := schemas.ApplyRequirementUpdate(base, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}},
		schemas.RequirementSource{Kind: "edit", MessageID: "m", Quote: "可以"}); err == nil {
		t.Fatal("edit 来源不得使用 accepted_proposal")
	}
	noQuote := op
	noQuote.Quote = ""
	if _, err := schemas.ApplyRequirementUpdate(base, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{noQuote}},
		schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: "可以"}); err == nil {
		t.Fatal("接受表达必须绑定本轮原文")
	}
}

func marshalState(t *testing.T, state schemas.RequirementState) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decodeState(t *testing.T, st *planningFakeStore) schemas.RequirementState {
	t.Helper()
	st.mu.Lock()
	raw := st.session.RequirementState
	st.mu.Unlock()
	var state schemas.RequirementState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

// currentUserMessageID 返回第 n 条(1 起)用户消息的 id,用于定位本轮建议。
func currentUserMessageID(t *testing.T, st *planningFakeStore, n int) string {
	t.Helper()
	messages, _ := st.WebMessages(context.Background(), "session-1")
	seen := 0
	for _, message := range messages {
		if message.Role == "user" {
			seen++
			if seen == n {
				return message.ID
			}
		}
	}
	t.Fatalf("user message %d not found", n)
	return ""
}

func mustReadiness(t *testing.T, state schemas.RequirementState) schemas.RequirementReadiness {
	t.Helper()
	readiness, err := schemas.EvaluateRequirementReadiness(state)
	if err != nil {
		t.Fatal(err)
	}
	return readiness
}
