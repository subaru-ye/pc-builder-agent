package pipeline

// Screening v2 一轮合同:operations/observations/turn_signals/proposals/answer。
// 回复与动作不进入 RequirementState;has_requirement_update 由 operations/
// observations 是否为空派生,禁止恢复 next_action 单选标签。严格解码拒绝
// 未知字段、非法值与超长输出;空 answer 合法。
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// RequirementTurnSignals 是多标签请求事实,不是授权:同一句可以同时更新
// 字段并请求生成。只保存在当前 run/turn 的结果或事件中。
type RequirementTurnSignals struct {
	AsksQuestion   bool `json:"asks_question"`
	RequestsReview bool `json:"requests_review"`
	RequestsBuild  bool `json:"requests_build"`
	Ambiguous      bool `json:"ambiguous"`
}

// RequirementProposal 是助手准备向用户建议的字段值,不是 active requirement。
// 模型不提供 assistant message ID;绑定由产品层在持久化边界完成。
type RequirementProposal struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
	Text  string          `json:"text"`
}

// RequirementTurnResult 是 Screening 每轮的短生命周期结构化输出。
type RequirementTurnResult struct {
	Operations   []schemas.RequirementOperation        `json:"operations"`
	Observations []schemas.RequirementObservationInput `json:"observations"`
	Signals      RequirementTurnSignals                `json:"turn_signals"`
	Proposals    []RequirementProposal                 `json:"proposals,omitempty"`
	Answer       string                                `json:"answer,omitempty"`
}

const (
	requirementTurnMaxOperations  = 32
	requirementTurnMaxProposals   = 4
	requirementTurnMaxAnswerRunes = 4000
	requirementProposalMaxRunes   = 500
)

// HasRequirementUpdate 不需要模型输出:operations/observations 任一非空即真。
func (t RequirementTurnResult) HasRequirementUpdate() bool {
	return len(t.Operations) > 0 || len(t.Observations) > 0
}

// Update 返回进入领域 Reducer 的更新批;chat 回复由组合器另行组装。
func (t RequirementTurnResult) Update() schemas.RequirementUpdate {
	return schemas.RequirementUpdate{Operations: t.Operations, Observations: t.Observations}
}

// DecodeRequirementTurn 严格解码 v2 一轮输出:拒绝未知字段(含模型自报的
// message id/next_action)、空 operations 数组、非法 proposal 值与超长文本。
func DecodeRequirementTurn(raw []byte) (RequirementTurnResult, error) {
	var turn RequirementTurnResult
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&turn); err != nil {
		return turn, fmt.Errorf("requirement turn: %w", err)
	}
	if turn.Operations == nil || len(turn.Operations) > requirementTurnMaxOperations || len(turn.Observations) > requirementTurnMaxOperations {
		return turn, fmt.Errorf("requirement turn: operations 必须为数组且最多 %d 项", requirementTurnMaxOperations)
	}
	if len(turn.Proposals) > requirementTurnMaxProposals {
		return turn, fmt.Errorf("requirement turn: proposals 最多 %d 项", requirementTurnMaxProposals)
	}
	if len([]rune(turn.Answer)) > requirementTurnMaxAnswerRunes {
		return turn, fmt.Errorf("requirement turn: answer 过长")
	}
	for i, proposal := range turn.Proposals {
		if !knownStateField(proposal.Field) || proposal.Field == "notes" {
			return turn, fmt.Errorf("requirement turn: proposals[%d] 非法字段 %q", i, proposal.Field)
		}
		if strings.TrimSpace(proposal.Text) == "" || len([]rune(proposal.Text)) > requirementProposalMaxRunes {
			return turn, fmt.Errorf("requirement turn: proposals[%d] 缺少建议问句或过长", i)
		}
		if _, err := schemas.NormalizeRequirementValue(proposal.Field, proposal.Value); err != nil {
			return turn, fmt.Errorf("requirement turn: proposals[%d] %w", i, err)
		}
	}
	return turn, nil
}

// DecodeLegacyTurnForReplay 机械升格冻结的 v1 轨迹(可能含 reply/next_action)
// 为 v2 一轮形状:reply 变 answer,next_action 丢弃;不生成任何用户事实。
// 仅供评估/测试回放冻结录制,产品运行时路径不得调用——v2 合同在解码边界
// 拒绝 next_action/reply,本函数不构成第二套需求合同。
func DecodeLegacyTurnForReplay(raw []byte) (RequirementTurnResult, error) {
	var legacy struct {
		Reply        string                                `json:"reply"`
		NextAction   string                                `json:"next_action"`
		Operations   []schemas.RequirementOperation        `json:"operations"`
		Observations []schemas.RequirementObservationInput `json:"observations"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return RequirementTurnResult{}, fmt.Errorf("legacy turn replay: %w", err)
	}
	turn := RequirementTurnResult{Operations: legacy.Operations, Observations: legacy.Observations, Answer: legacy.Reply}
	if turn.Operations == nil {
		turn.Operations = []schemas.RequirementOperation{}
	}
	return turn, nil
}

// RequirementProposalView 是产品层传入的"未解决建议"输入视图:值已由服务器
// 规范化,绑定信息(assistant message id、紧邻轮次)不发给模型。
type RequirementProposalView struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
	Text  string          `json:"text"`
}

// RequirementTurnPromptContext 是 v2 输入构造的有界参考:确定性 readiness
// 摘要、领域下一步追问与上一轮未解决建议。它们是参考,不赋予模型覆盖权。
type RequirementTurnPromptContext struct {
	Readiness    json.RawMessage              `json:"readiness,omitempty"`
	NextQuestion *schemas.RequirementQuestion `json:"next_question,omitempty"`
	Proposals    []RequirementProposalView    `json:"pending_proposals,omitempty"`
	HasBuild     bool                         `json:"has_build"`
}

type requirementTurnPromptContextKey struct{}

// WithRequirementTurnPromptContext 附着本轮确定性参考;未附着的旧评估入口
// 按无参考渲染,不阻塞 Screening 独立运行。
func WithRequirementTurnPromptContext(ctx context.Context, promptContext RequirementTurnPromptContext) context.Context {
	return context.WithValue(ctx, requirementTurnPromptContextKey{}, promptContext)
}

func requirementTurnPromptContextFrom(ctx context.Context) RequirementTurnPromptContext {
	if promptContext, ok := ctx.Value(requirementTurnPromptContextKey{}).(RequirementTurnPromptContext); ok {
		return promptContext
	}
	return RequirementTurnPromptContext{}
}
