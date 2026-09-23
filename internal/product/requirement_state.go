package product

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// RequirementEdit 使用与 Screening 相同的字段操作；修订号由存储事务核验。
type RequirementEdit struct {
	ExpectedRevision int                            `json:"expected_revision"`
	Operations       []schemas.RequirementOperation `json:"operations"`
}

func decodeSessionRequirements(raw json.RawMessage) (schemas.RequirementState, error) {
	// v1 及损坏状态以稳定错误拒绝,不静默重建空 v2 状态;
	// 开发数据通过显式运维步骤重建(见 docs/ops/本地运行与部署.md)。
	return schemas.DecodeRequirementState(raw)
}

// EditRequirement 用独立原子事务保存侧栏草稿编辑:复用同一 Reducer、权限与
// 幂等约束,不创建 AgentRun、不改 phase;Builder 运行期间同样可用,运行中的
// 载荷与确认快照保持不变。revision 校验发生在存储事务内。
func (s *Service) EditRequirement(ctx context.Context, ownerID, sessionID, requestID string, edit RequirementEdit) (SessionDetail, error) {
	if edit.ExpectedRevision < 0 || len(edit.Operations) == 0 || len(edit.Operations) > 32 {
		return SessionDetail{}, NewProblem("invalid_request", "需求修改无效", 422, "请提交1–32项修改及有效修订号。", requestID)
	}
	text := requirementEditText(edit.Operations)
	request, err := json.Marshal(edit)
	if err != nil {
		return SessionDetail{}, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(request))
	// 重放预检先于 Reducer:重复请求可能包含无法重复应用的操作(如 restore),
	// 相同指纹直接返回当前会话,不同指纹是稳定冲突。
	if old, found, err := s.store.RequirementEditFingerprint(ctx, sessionID, requestID); err != nil {
		return SessionDetail{}, err
	} else if found {
		if old != fingerprint {
			return SessionDetail{}, store.ErrIdempotencyConflict
		}
		return s.GetSession(ctx, ownerID, sessionID)
	}
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	if len(ws.RequirementState) == 0 {
		return SessionDetail{}, NewProblem("invalid_session_phase", "旧会话没有可溯源的增量需求", 409,
			"请继续使用此会话的原需求确认表单，或新建会话记录动态需求；原需求和配置未修改。", requestID)
	}
	state, err := decodeSessionRequirements(ws.RequirementState)
	if err != nil {
		return SessionDetail{}, err
	}
	source := schemas.RequirementSource{Kind: "edit", Quote: text}
	next, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: edit.Operations}, source)
	if err != nil {
		return SessionDetail{}, NewProblem("schema_validation_failed", "需求修改未保存", 422, err.Error(), requestID)
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return SessionDetail{}, err
	}
	pending, _, err := schemas.RequirementStateSpec(next)
	if err != nil {
		return SessionDetail{}, err
	}
	if _, err := s.store.EditRequirementDraft(ctx, store.EditRequirementDraftParams{
		OwnerID: ownerID, SessionID: sessionID, RequestID: requestID, Fingerprint: fingerprint,
		ExpectedRevision: edit.ExpectedRevision, Next: raw, Pending: pending,
	}); err != nil {
		return SessionDetail{}, err
	}
	return s.GetSession(ctx, ownerID, sessionID)
}

func requirementEditText(operations []schemas.RequirementOperation) string {
	labels := map[string]string{"budget_cny": "预算", "budget_flex": "预算弹性", "budget_basis": "预算口径", "use_case.type": "用途", "use_case.titles": "游戏或软件", "use_case.resolution": "分辨率", "use_case.fps_target": "目标帧率", "brand_pref.cpu": "CPU品牌", "brand_pref.gpu": "显卡品牌", "noise_pref": "静音", "size_pref": "尺寸", "appearance": "外观", "recipient": "装机对象", "notes": "补充说明", "existing_parts": "已有配件", "owned_parts": "已有配件型号", "priority": "优先配件"}
	lines := make([]string, 0, len(operations))
	for _, op := range operations {
		label := labels[op.Field]
		if label == "" {
			label = op.Field
			if schemas.FreeField(op.Field) {
				label = "补充要求"
			}
		}
		switch op.Op {
		case "remove":
			lines = append(lines, "撤销"+label)
		case "restore":
			lines = append(lines, "恢复"+label+"的原要求")
		default:
			var qualifiers []string
			switch op.Kind {
			case "fact":
				qualifiers = append(qualifiers, "用途或已有事实")
			case "context":
				qualifiers = append(qualifiers, "补充说明")
			case "constraint":
				qualifiers = append(qualifiers, "配置条件")
			}
			if op.Strength == "must" && op.Kind != "fact" && op.Kind != "context" {
				qualifiers = append(qualifiers, "必须满足")
			}
			if op.Strength == "prefer" && op.Kind != "fact" && op.Kind != "context" {
				qualifiers = append(qualifiers, "尽量满足")
			}
			if op.Scope == "temporary" {
				qualifiers = append(qualifiers, "本次临时例外")
			}
			suffix := ""
			if len(qualifiers) > 0 {
				suffix = "（" + strings.Join(qualifiers, "，") + "）"
			}
			verb := "改为"
			if op.Op == "alternative" {
				verb = "新增讨论备选"
			}
			if op.Op == "conflict" {
				verb = "出现待确认冲突"
			}
			lines = append(lines, label+verb+string(op.Value)+suffix)
		}
	}
	return "通过需求面板修改：" + strings.Join(lines, "；")
}

func sameRequirementJSON(a, b json.RawMessage) bool {
	var left, right any
	return len(a) > 0 && len(b) > 0 && json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}

// 兼容旧确认表单：只把真正改变的字段转成操作，未改动的程序默认值不成为用户事实。
func requirementReplacementOperations(before, after json.RawMessage) ([]schemas.RequirementOperation, error) {
	decoded, err := schemas.DecodeRequirementSpec(after)
	if err != nil {
		return nil, err
	}
	after, err = schemas.EncodeRequirementSpec(decoded)
	if err != nil {
		return nil, err
	}
	var oldValues, newValues map[string]json.RawMessage
	if err := json.Unmarshal(before, &oldValues); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(after, &newValues); err != nil {
		return nil, err
	}
	read := func(values map[string]json.RawMessage, key string) json.RawMessage {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) == 1 {
			return values[key]
		}
		var child map[string]json.RawMessage
		_ = json.Unmarshal(values[parts[0]], &child)
		return child[parts[1]]
	}
	operations := []schemas.RequirementOperation{}
	for _, key := range schemas.RequirementFieldKeys {
		if key == "appearance" || key == "recipient" {
			continue
		}
		oldValue, newValue := read(oldValues, key), read(newValues, key)
		if (len(oldValue) == 0 && len(newValue) == 0) || sameRequirementJSON(oldValue, newValue) {
			continue
		}
		op := schemas.RequirementOperation{Field: key, Op: "set", Value: newValue}
		if len(newValue) == 0 || string(newValue) == `""` {
			op.Op = "remove"
			op.Value = nil
		}
		operations = append(operations, op)
	}
	return operations, nil
}

func (s *Service) attachRequirementState(ctx context.Context, ownerID string, r store.AgentRun, input *ScreenInput) error {
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, r.SessionID)
	if err != nil {
		return err
	}
	var state schemas.RequirementState
	if len(ws.RequirementState) == 0 {
		// v1 一次性切换:没有增量状态的旧会话不再从旧需求单静默重建,
		// 以稳定错误拒绝,由用户显式新建会话或走运维重建步骤。
		if len(ws.PendingRequirement) > 0 || ws.ConfirmationID != "" {
			return schemas.ErrRequirementStateUnsupported
		}
		if _, supportsPlanning := s.store.(proposalStore); !supportsPlanning {
			return nil // Legacy diagnostic stores keep their original protocol.
		}
		state = schemas.NewRequirementState()
	} else if state, err = decodeSessionRequirements(ws.RequirementState); err != nil {
		return err
	}
	input.RequirementState = &state
	input.HasBuild = ws.VersionCount > 0
	// The first message of a session always hands back an explicit confirmation;
	// later messages (and confirmed requirements) may continue into planning
	// directly — the user's own follow-up is the authorization.
	input.Conversation.CanPlan = ws.ConfirmationID != "" || state.Revision > 0
	if st, ok := s.store.(proposalStore); ok {
		if ws.VersionCount > 0 {
			base, err := st.BuildByVersion(ctx, r.SessionID, ws.VersionCount)
			if err != nil {
				return err
			}
			input.Conversation.BuildVersion = base.Version
			input.Conversation.BaseDraft, input.Conversation.Quote = base.Draft, base.Quote
			var snapshot struct {
				Candidates []struct {
					ID       string `json:"id"`
					Category string `json:"category"`
					Brand    string `json:"brand"`
					Model    string `json:"model"`
				} `json:"candidates"`
			}
			if json.Unmarshal(base.CandidateSnapshot, &snapshot) == nil {
				input.Conversation.Parts, _ = json.Marshal(snapshot.Candidates)
			}
		}
		previous, err := st.LatestProposal(ctx, r.SessionID)
		if err != nil {
			return err
		}
		// Only continuation details: hundreds of sources belong in Builder tools.
		var proposal struct {
			Result struct {
				Outcome string          `json:"outcome"`
				Draft   json.RawMessage `json:"draft,omitempty"`
				Issues  []string        `json:"issues"`
				Reply   string          `json:"reply"`
			} `json:"result"`
		}
		if len(previous) > 0 && json.Unmarshal(previous, &proposal) == nil {
			input.Conversation.Proposal, _ = json.Marshal(proposal.Result)
		}
	}
	messages, err := s.store.WebMessages(ctx, r.SessionID)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if message.Role == "assistant" {
			text := []rune(message.Content)
			if len(text) > 3000 {
				text = text[:3000]
			}
			input.Conversation.LastAssistant = string(text)
		}
		if message.Role == "user" && message.RunID != nil && *message.RunID == r.ID {
			input.RequirementSource = schemas.RequirementSource{MessageID: message.ID, Kind: "chat", Quote: message.Content}
			break
		}
	}
	s.attachTurnContext(ctx, r, input)
	return nil
}

// attachTurnContext 组装 v2 输入构造的有界参考:确定性 readiness 摘要、
// 领域下一步追问、上一条助手回复绑定的未解决建议。它们是参考,不赋予
// 模型覆盖权;TurnProposals 是服务器侧核验记录,不发给模型。
func (s *Service) attachTurnContext(ctx context.Context, r store.AgentRun, input *ScreenInput) {
	if input.RequirementState == nil {
		return
	}
	readiness, err := schemas.EvaluateRequirementReadiness(*input.RequirementState)
	if err != nil {
		return
	}
	question, err := schemas.NextRequirementQuestion(*input.RequirementState)
	if err != nil {
		return
	}
	readinessJSON, err := json.Marshal(readiness)
	if err != nil {
		return
	}
	promptContext := pipeline.RequirementTurnPromptContext{
		Readiness: json.RawMessage(readinessJSON), NextQuestion: question,
		HasBuild: input.HasBuild,
	}
	if st, ok := s.store.(requirementProposalStore); ok && input.RequirementSource.MessageID != "" {
		if proposals, err := st.ActiveRequirementProposals(ctx, r.SessionID, input.RequirementSource.MessageID); err == nil {
			input.TurnProposals = proposals
			for _, proposal := range proposals {
				promptContext.Proposals = append(promptContext.Proposals, pipeline.RequirementProposalView{
					Field: proposal.Field, Value: proposal.Value, Text: proposal.Text,
				})
			}
		}
	}
	input.TurnContext = &promptContext
}
