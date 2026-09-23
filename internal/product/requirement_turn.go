package product

// Screening v2 的确定性回复组合与短期 presentation action:
// 1 应用 operations(失败不部分保存)→ 2 重算 readiness → 3 answer 守卫
// → 4 展示 answer → 5 incomplete 追加一个问题组 → 6/7/8 presentation action。
// Spec 4 在 requirementPresentationAction 同一入口扩展三轴 Policy。
import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// requirementProposalStore 是支持助手建议协议的存储能力面;不满足的旧诊断
// store 保持原协议:建议不保存、不接受(裸"可以"永远没有可验证对象)。
type requirementProposalStore interface {
	ActiveRequirementProposals(context.Context, string, string) ([]store.RequirementProposalRecord, error)
}

// 模型宣称已开始/正在执行生成,或承诺配置主机外品类时,answer 无法安全保留;
// 用诚实的范围说明整体替换,不改写用户需求事实。
var (
	answerExecutionClaim = regexp.MustCompile(`已开始生成|开始生成|正在生成|已经开始|已为您生成|已启动生成|已开始配置|正在配置|开始选配`)
	answerPeripheralTerm = regexp.MustCompile(`显示器|键盘|鼠标|键鼠`)
	answerPeripheralDeal = regexp.MustCompile(`配|搭配|选购|购买|包含|加上|一起|安排|涵盖`)
)

const (
	answerExecutionHonest = "需求还在整理中，尚未生成任何配置；核定确认之后才会进入生成。"
	answerScopeHonest     = "当前配置范围仅支持主机（tower），显示器、键盘、鼠标暂不在本次范围内。"
	proposalClarifyText   = "没有识别到可以确认的对应建议；请直接说明要采用的字段和值。"
	acceptRejectedMarker  = "没有同字段同值的可验证建议"
)

// guardScreeningAnswer 是确定性工作流/能力范围守卫:聊天轮永不启动 Builder,
// 因此任何执行宣称都不可透传;主机外品类的承诺同样替换为范围说明。
func guardScreeningAnswer(answer string) string {
	if answer == "" {
		return ""
	}
	if answerExecutionClaim.MatchString(answer) {
		return answerExecutionHonest
	}
	if answerPeripheralTerm.MatchString(answer) && answerPeripheralDeal.MatchString(answer) {
		return answerScopeHonest
	}
	return answer
}

// verifyAcceptedProposals 做服务器端核验:只有存在同字段、同规范化值、且
// 只对本轮有效的未解析建议,且本轮用户原话确实表达接受时,accepted_proposal
// 操作才被保留;模型标签本身不构成证据。未通过的操作降级为 observation,
// 不污染需求真值。
func verifyAcceptedProposals(turn *pipeline.RequirementTurnResult, proposals []store.RequirementProposalRecord, source schemas.RequirementSource) (accepted []store.RequirementProposalAccept) {
	kept := make([]schemas.RequirementOperation, 0, len(turn.Operations))
	for _, op := range turn.Operations {
		if op.Evidence != "accepted_proposal" {
			kept = append(kept, op)
			continue
		}
		normalized, normErr := schemas.NormalizeRequirementValue(op.Field, op.Value)
		if reason := rejectProposalOperation(op, proposals, source); reason == "" && normErr == nil && hasMatchingProposal(op.Field, normalized, proposals) {
			kept = append(kept, op)
			accepted = append(accepted, store.RequirementProposalAccept{Field: op.Field, Value: normalized})
			continue
		}
		// accepted_proposal 核验失败的操作一律降级为 observation,不改写为
		// stated:模型标签失败后,任何"原话出现相同数字+肯定词"的推断都不
		// 足以成为用户证据("我看到 7500 元报价"不得变成预算设定)。
		quote := op.Quote
		if !strings.Contains(source.Quote, quote) {
			quote = source.Quote
		}
		if strings.TrimSpace(quote) != "" && len(turn.Observations) < 32 {
			turn.Observations = append(turn.Observations, schemas.RequirementObservationInput{
				Field: op.Field, Quote: quote, Reason: "该确认" + acceptRejectedMarker + "，未作为用户要求采用",
			})
		}
	}
	turn.Operations = kept
	return accepted
}

// rejectProposalOperation 是标签之外的服务器语义核验,返回空表示接受有效。
// 接受意图依据完整用户原话判定,顺序固定:先排除询问与明确拒绝;之后要求
// 正向肯定信号——规范化后完整匹配的肯定短答,或以采纳句式开头且包含该字段
// 具体提案值的采纳句。"未提问、未拒绝"不等于同意,子串命中不算肯定:
// "不可以""行不通""我不想按 7500"一律保守拒绝并保留 observation。
func rejectProposalOperation(op schemas.RequirementOperation, proposals []store.RequirementProposalRecord, source schemas.RequirementSource) string {
	if reason := proposalAcceptanceVerdict(source.Quote); reason != "" {
		return reason
	}
	if !quoteShowsAcceptance(source.Quote, op.Value) {
		return "用户本轮原话没有明确接受建议"
	}
	if len(proposals) > 1 && !quoteMentionsValue(source.Quote, op.Value) {
		return "存在多个待确认建议，无法确定“可以”指向"
	}
	return ""
}

// proposalAcceptanceVerdict 按固定顺序核验完整用户原话:先排除询问
// ("7500 够吗？"),再排除明确拒绝("7500 不行");返回空才可能是接受。
var proposalQuestionMarkers = []string{
	"够吗", "行吗", "可以吗", "好吗", "是吗", "对吗", "够不够", "行不行", "怎么样", "如何", "多少", "贵不贵",
}

func proposalAcceptanceVerdict(quote string) string {
	if quoteAsksQuestion(quote) {
		return "用户本轮在询问，不是接受建议"
	}
	if quoteRejectsProposal(quote) {
		return "用户本轮明确拒绝了建议"
	}
	return ""
}

func quoteAsksQuestion(quote string) bool {
	if strings.Contains(quote, "？") || strings.Contains(quote, "?") {
		return true
	}
	for _, marker := range proposalQuestionMarkers {
		if strings.Contains(quote, marker) {
			return true
		}
	}
	return false
}

// proposalShortAccepts 是规范化后可独立成立的肯定短答:整句完整匹配,
// 不做子串包含——"不可以"不会命中"可以","行不通"不会命中"行"。
var proposalShortAccepts = []string{
	"可以", "行", "好的", "好呀", "嗯", "嗯嗯", "同意", "没问题", "成",
	"就这样", "就按这个", "听你的", "照你说的", "没错", "是的", "对的", "ok",
}

// proposalAdoptionPrefixes 是带提案值的明确采纳句式前缀:规范化整句必须以
// 其一开头且包含该字段的具体提案值("按 7500 来""就用 1080p 吧");
// "我不想按 7500"不以采纳前缀开头,被保守拒绝。
var proposalAdoptionPrefixes = []string{"按", "就按", "就用", "定为", "敲定", "采纳", "确认", "来个"}

// quoteShowsAcceptance 只认两类正向信号,不做整句子串查找:
//  1. 规范化后完整匹配的肯定短答;
//  2. 以采纳前缀开头且包含该字段具体提案值的采纳句。
// 其余表达一律保守拒绝并保留 observation。(ponytail: 白名单漏掉的口语
// 变体代价是再问一次,不会污染 active 需求;完备意图识别留给后续模型级判定。)
func quoteShowsAcceptance(quote string, value json.RawMessage) bool {
	normalized := normalizeUtterance(quote)
	if normalized == "" {
		return false
	}
	for _, short := range proposalShortAccepts {
		if normalized == short {
			return true
		}
	}
	for _, prefix := range proposalAdoptionPrefixes {
		if strings.HasPrefix(normalized, prefix) && quoteMentionsValue(quote, value) {
			return true
		}
	}
	return false
}

// normalizeUtterance 规范化用户原话:去全部空白、统一小写。
func normalizeUtterance(quote string) string {
	return strings.Join(strings.Fields(strings.ToLower(quote)), "")
}

// quoteMentionsValue 判断完整原话是否复述了该字段的具体值。
func quoteMentionsValue(quote string, value json.RawMessage) bool {
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil {
		return quoteContainsNumber(quote, number.String())
	}
	literal, ok := proposalValueLiteral(value)
	if !ok {
		return false
	}
	return strings.Contains(strings.ToLower(quote), strings.ToLower(literal))
}

// hasMatchingProposal 核验存在同字段、同规范化值、只对本轮有效的未解析建议。
func hasMatchingProposal(field string, normalized json.RawMessage, proposals []store.RequirementProposalRecord) bool {
	for _, proposal := range proposals {
		proposalValue, perr := schemas.NormalizeRequirementValue(proposal.Field, proposal.Value)
		if perr == nil && proposal.Field == field && sameRequirementJSON(normalized, proposalValue) {
			return true
		}
	}
	return false
}

// proposalValueLiteral 提取值在用户语言中的可验证字面量:数值→数字串,
// 非空字符串→其内容;数组/对象没有单值字面量。
func proposalValueLiteral(value json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		trimmed := strings.TrimSpace(text)
		return trimmed, trimmed != ""
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil {
		return number.String(), true
	}
	return "", false
}

// quoteRejectsProposal 是保守的明确拒绝表达识别(关键词白名单;ponytail:
// 否定语境的完备语义超出本轮范围,升级路径是模型级意图判定。误拒的代价是
// 保留 observation 请用户重说,不会污染 active 需求)。
var proposalRejectionMarkers = []string{
	"不行", "不要", "先不", "不换", "不采用", "不采纳", "不同意",
	"算了", "拒绝", "再想想", "再考虑", "暂不", "换一个", "换别的", "换其他",
}

func quoteRejectsProposal(quote string) bool {
	for _, marker := range proposalRejectionMarkers {
		if strings.Contains(quote, marker) {
			return true
		}
	}
	return false
}

// quoteNumberPattern 抓取连续数字(允许千分位逗号),保证 token 边界:
// 子串包含会把 "75000" 误当 "7500" 的证据。
var quoteNumberPattern = regexp.MustCompile(`[0-9][0-9,，]*`)

func quoteNumberTokens(quote string) []string {
	matches := quoteNumberPattern.FindAllString(quote, -1)
	tokens := make([]string, 0, len(matches))
	for _, match := range matches {
		normalized := strings.ReplaceAll(strings.ReplaceAll(match, ",", ""), "，", "")
		if normalized != "" {
			tokens = append(tokens, normalized)
		}
	}
	return tokens
}

func quoteContainsNumber(quote, digits string) bool {
	for _, token := range quoteNumberTokens(quote) {
		if token == digits {
			return true
		}
	}
	return false
}

func rejectedAcceptCount(turn pipeline.RequirementTurnResult) int {
	count := 0
	for _, observation := range turn.Observations {
		if strings.Contains(observation.Reason, acceptRejectedMarker) {
			count++
		}
	}
	return count
}

// requirementPresentationAction 是短期 presentation action 的唯一入口:
// 只由确定性 readiness 与本轮 signals 决定,不启动 Builder;Spec 4 在此扩展
// confirmation/build 三轴 Policy,不另建并行决策器。
func requirementPresentationAction(readiness schemas.RequirementReadiness, signals pipeline.RequirementTurnSignals, question *schemas.RequirementQuestion) (string, []string) {
	if readiness.ConfirmationEligible {
		if signals.RequestsReview || signals.RequestsBuild {
			return "open_requirement_review", nil
		}
		return "", nil
	}
	if signals.RequestsReview || signals.RequestsBuild {
		if question != nil && len(question.Fields) > 0 {
			return "focus_missing_requirement", question.Fields
		}
		return "focus_missing_requirement", readiness.MissingFields[:1]
	}
	return "", nil
}

// proposalAcceptanceQuestionTailLen 是"以接受问句收尾"检查的尾部窗口:
// 问句标记必须出现在结尾附近,而不是全文任意位置。
const proposalAcceptanceQuestionTailLen = 8

// proposalAsksAcceptance 检查文本是否以真实的接受问句收尾——先剥掉尾部
// 句号/叹号,要求结尾是问号,或结尾片段以问句词收束。"……可以吗？我已开始
// 生成配置"这类问句后跟陈述的文本不构成可接受提案。
func proposalAsksAcceptance(text string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(text), "。！!～~… \t\n")
	runes := []rune(trimmed)
	if len(runes) == 0 {
		return false
	}
	tail := string(runes[max(0, len(runes)-proposalAcceptanceQuestionTailLen):])
	if strings.HasSuffix(tail, "？") || strings.HasSuffix(tail, "?") {
		return true
	}
	for _, marker := range []string{"吗", "呢", "行不行", "好不好", "如何", "怎么样"} {
		if strings.HasSuffix(tail, marker) {
			return true
		}
	}
	return false
}

// presentableProposal 决定建议能否展示并保存(Spec:最终可见回复必须明确
// 呈现对应字段、具体值和可接受问句)。提案文本也是面向用户的模型回复,
// 必须原样通过工作流与能力范围守卫(V8/V9):声称已开始生成或承诺外设的
// 文本不得展示或保存。文本必须包含该字段的具体值字面量(数值按数字 token
// 等值,防"75000"冒充"7500"),并以接受问句收尾。
func presentableProposal(proposal pipeline.RequirementProposal, normalized json.RawMessage) bool {
	if strings.TrimSpace(proposal.Text) == "" || len([]rune(proposal.Text)) > 500 {
		return false
	}
	if guardScreeningAnswer(proposal.Text) != proposal.Text {
		return false
	}
	if !quoteMentionsValue(proposal.Text, normalized) {
		return false
	}
	return proposalAsksAcceptance(proposal.Text)
}

// composeTurnReply 按固定顺序组合最终回复;每段都由确定性代码控制。
func composeTurnReply(state schemas.RequirementState, readiness schemas.RequirementReadiness,
	turn pipeline.RequirementTurnResult, visibleProposals []pipeline.RequirementProposal,
	confirmation ConfirmationStatus, hasVersions bool) string {
	var segments []string
	if answer := guardScreeningAnswer(turn.Answer); answer != "" {
		segments = append(segments, answer)
	}
	for _, proposal := range visibleProposals {
		segments = append(segments, proposal.Text)
	}
	if rejected := rejectedAcceptCount(turn); rejected > 0 {
		segments = append(segments, proposalClarifyText)
	}
	if readiness.ConfirmationEligible {
		switch {
		case turn.Signals.RequestsReview || turn.Signals.RequestsBuild:
			segments = append(segments, "需求已经齐备，现在可以核定需求；确认后再生成配置。")
		case confirmation == ConfirmationConfirmed:
			segments = append(segments, "当前有效需求保持不变，可继续查看配置或修改需求。")
		case confirmation == ConfirmationModified:
			segments = append(segments, "需求草稿已更新，原配置保持不变。请确认后生成新的配置版本。")
		default:
			segments = append(segments, "需求已经齐备，现在可以核定需求；确认后再生成配置。")
		}
		return strings.Join(segments, "\n")
	}
	if question, err := schemas.RequirementQuestionText(state); err == nil && question != "" {
		prefix := ""
		if len(state.Changes) > 0 || len(state.Observations) > 0 {
			prefix = "本轮可确认的信息和原文已保存。"
		}
		segments = append(segments, prefix+question)
	}
	return strings.Join(segments, "\n")
}

// completeRequirementTurn 是聊天轮与编辑轮共用的收口:确定性 readiness 决定
// phase,组合器生成助手文案,建议保存与接受解析在同一 CompleteRun 事务落库。
// state 必须已经应用 verifyAcceptedProposals 之后的最终操作批;turn 为 nil
// 表示编辑轮:没有模型语义,只有领域状态与追问。
func (s *Service) completeRequirementTurn(ctx context.Context, ownerID string, r store.AgentRun,
	state schemas.RequirementState, source schemas.RequirementSource,
	turn *pipeline.RequirementTurnResult, accepted []store.RequirementProposalAccept, retries int) error {
	pending, readiness, err := schemas.RequirementStateSpec(state)
	if err != nil {
		return err
	}
	phase := store.PhaseRequirementReady
	if !readiness.ConfirmationEligible {
		phase = store.PhaseCollecting
		pending = nil
	}
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, r.SessionID)
	if err != nil {
		return err
	}
	// 本轮草稿(next)尚未落库,不能走会话级三轴派生;确认轴直接与快照比较。
	var confirmationStatus ConfirmationStatus
	if ws.ConfirmationID == "" {
		confirmationStatus = ConfirmationUnconfirmed
	} else {
		confirmation, found, err := s.store.ConfirmationByID(ctx, r.SessionID, ws.ConfirmationID)
		if err != nil {
			return err
		}
		confirmationStatus = ConfirmationModified
		if found {
			draftHash := ""
			if readiness.ConfirmationEligible && len(pending) > 0 {
				if draftHash, err = schemas.CanonicalHash(pending); err != nil {
					return err
				}
			}
			if draftHash != "" && draftHash == confirmation.ReviewHash {
				confirmationStatus = ConfirmationConfirmed
			}
		}
	}
	question, err := schemas.NextRequirementQuestion(state)
	if err != nil {
		return err
	}
	// 已确认且草稿预览未变的会话回到 ready:后续消息走 screening 收集而非
	// 重新确认;phase 是执行恢复用的粗粒度标记,admission 不依赖它。
	if phase == store.PhaseRequirementReady && confirmationStatus == ConfirmationConfirmed {
		phase = store.PhaseReady
	}
	empty := pipeline.RequirementTurnResult{Operations: []schemas.RequirementOperation{}}
	if turn == nil {
		turn = &empty
	}
	action, actionFields := requirementPresentationAction(readiness, turn.Signals, question)
	var (
		visibleProposals []pipeline.RequirementProposal
		saveProposals    []store.RequirementProposalSave
	)
	// 只有确实展示给用户的建议才可保存:文本必须明确呈现具体值并以可被
	// "可以"回答的问句收尾;"这样可以吗?"这类不含值的文本不得暗中绑定提案。
	// store 在同一事务里再校验文本逐字出现后才落库。
	for _, proposal := range turn.Proposals {
		normalized, err := schemas.NormalizeRequirementValue(proposal.Field, proposal.Value)
		if err != nil {
			continue // 非法建议不展示、不保存。
		}
		if !presentableProposal(proposal, normalized) {
			continue // 缺值或非问句的建议不可接受,不展示、不保存。
		}
		visibleProposals = append(visibleProposals, proposal)
		saveProposals = append(saveProposals, store.RequirementProposalSave{Field: proposal.Field, Value: normalized, Text: proposal.Text})
	}
	assistant := composeTurnReply(state, readiness, *turn, visibleProposals, confirmationStatus, ws.VersionCount > 0)
	if assistant == "" {
		assistant = ScreeningReadyMessage
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	msg, err := s.store.CompleteRun(context.WithoutCancel(ctx), store.CompleteRunParams{
		RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(),
		AssistantContent: assistant, Status: store.RunSucceeded, Phase: phase,
		PendingRequirement: pending, SetPending: true, RequirementState: raw, SetRequirementState: true,
		ScreeningModel: s.screeningModelFor(r.Kind), RetryCount: retries,
		SaveProposals: saveProposals, ConsumeMessageID: source.MessageID,
		ResolveProposals: accepted,
	})
	if err != nil {
		return err
	}
	s.publish(ctx, r.ID, "requirement.updated", json.RawMessage(raw))
	if msg != nil {
		s.publish(ctx, r.ID, "assistant.completed", map[string]any{"message": messagePayload(*msg)})
	}
	if action != "" {
		payload := map[string]any{"action": action}
		if len(actionFields) > 0 {
			payload["fields"] = actionFields
		}
		s.publish(ctx, r.ID, "presentation.action", payload)
	}
	if phase == store.PhaseRequirementReady && len(pending) > 0 {
		s.publish(ctx, r.ID, "requirement.ready", pending)
	}
	s.publish(ctx, r.ID, "run.completed", map[string]any{"status": "succeeded"})
	return nil
}
