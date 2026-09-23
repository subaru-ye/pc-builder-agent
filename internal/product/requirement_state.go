package product

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/uuid"
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

// EditRequirement 在服务端保存用户可读的来源消息，再原子发布合并后的草稿。
// 不调用模型，既不保存个人画像，也不修改已确认快照或历史配置。
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
	if _, found, err := s.store.MessageRunByRequest(ctx, ownerID, sessionID, requestID, text, fingerprint); err != nil {
		return SessionDetail{}, err
	} else if found {
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
	if state.Revision != edit.ExpectedRevision {
		return SessionDetail{}, store.ErrRequirementRevision
	}
	messageID := uuid.NewString()
	source := schemas.RequirementSource{MessageID: messageID, Kind: "edit", Quote: text}
	update := schemas.RequirementUpdate{Operations: edit.Operations}
	next, err := schemas.ApplyRequirementUpdate(state, update, source)
	if err != nil {
		return SessionDetail{}, NewProblem("schema_validation_failed", "需求修改未保存", 422, err.Error(), requestID)
	}
	r, duplicate, err := s.store.StartMessageRun(ctx, store.StartMessageRunParams{
		OwnerID: ownerID, SessionID: sessionID, RequestID: requestID, RunID: uuid.NewString(),
		MessageID: messageID, Text: text, Title: "我的装机需求", ForceScreening: true,
		ExpectedRevision:   &edit.ExpectedRevision,
		RequestFingerprint: fingerprint,
	})
	if err != nil {
		return SessionDetail{}, err
	}
	if !duplicate {
		s.publish(ctx, r.ID, "run.started", map[string]any{"kind": r.Kind})
		if err := s.completeRequirementState(ctx, ownerID, r, next, "", 0); err != nil {
			s.failInternal(ctx, r, store.PhaseCollecting, "")
			return SessionDetail{}, err
		}
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

// completeRequirementState 只按确定性 readiness 收口:incomplete 保持收集并
// 用领域追问文案;ready 才发布投影后的 RequirementSpec v2 待确认草稿。
// reply 是 legacy turn 传输适配剥离出的助手文案,仅作展示。
func (s *Service) completeRequirementState(ctx context.Context, ownerID string, r store.AgentRun, state schemas.RequirementState, reply string, retries int) error {
	pending, readiness, err := schemas.RequirementStateSpec(state)
	if err != nil {
		return err
	}
	phase := store.PhaseRequirementReady
	assistant := ScreeningReadyMessage
	if reply != "" {
		assistant = reply
	}
	// 唯一就绪判据是领域 ConfirmationEligible:缺失、阻塞冲突(must 或
	// 条件必填)与未解决 unsupported 都保持收集态,不发布待确认草稿,
	// 也不发 requirement.ready。
	if !readiness.ConfirmationEligible {
		phase = store.PhaseCollecting
		pending = nil
		assistant = schemas.RequirementStateQuestions(state)
		if len(state.Changes) > 0 || len(state.Observations) > 0 {
			assistant = "本轮可确认的信息和原文已保存。" + assistant
		}
	}
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, r.SessionID)
	if err != nil {
		return err
	}
	confirmed := confirmedRequirementProjection(ws.ConfirmedRequirementState)
	if reply == "" && readiness.ConfirmationEligible && ws.VersionCount > 0 && sameRequirementJSON(pending, confirmed) {
		phase = store.PhaseReady
		assistant = "当前有效需求保持不变，可继续查看配置或修改需求。"
	} else if reply == "" && readiness.ConfirmationEligible && len(confirmed) > 0 {
		assistant = "需求草稿已更新，原配置保持不变。请确认后生成新的配置版本。"
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
	})
	if err != nil {
		return err
	}
	s.publish(ctx, r.ID, "requirement.updated", json.RawMessage(raw))
	if msg != nil {
		s.publish(ctx, r.ID, "assistant.completed", map[string]any{"message": messagePayload(*msg)})
	}
	if phase == store.PhaseRequirementReady && len(pending) > 0 {
		s.publish(ctx, r.ID, "requirement.ready", pending)
	}
	s.publish(ctx, r.ID, "run.completed", map[string]any{"status": "succeeded"})
	return nil
}

// confirmedRequirementProjection 返回已确认状态的当前投影;无确认快照或
// 状态不可投影时返回 nil。
func confirmedRequirementProjection(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	state, err := decodeSessionRequirements(raw)
	if err != nil {
		return nil
	}
	spec, readiness, err := schemas.RequirementStateSpec(state)
	if err != nil || !readiness.ConfirmationEligible {
		return nil
	}
	return spec
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

// requirementLifecycle 从确定性 readiness 派生旧形状 DTO(临时适配,Builder
// gate change 中删除):不读模型 next_action,判定只来自领域 readiness 与
// 投影比对。v1/损坏状态返回稳定错误,由调用方转为问题响应。
func requirementLifecycle(ws store.WebSession) (string, []string, error) {
	if len(ws.RequirementState) == 0 {
		if ws.Phase == store.PhaseCollecting && len(ws.PendingRequirement) == 0 {
			return "collecting", []string{}, nil
		}
		// 有 pending 却没有增量状态:这是 v1 一次性切换遗留的旧会话。
		return "", nil, schemas.ErrRequirementStateUnsupported
	}
	state, err := decodeSessionRequirements(ws.RequirementState)
	if err != nil {
		return "", nil, err
	}
	readiness, err := schemas.EvaluateRequirementReadiness(state)
	if err != nil {
		return "", nil, err
	}
	missing := readiness.MissingFields
	if missing == nil {
		missing = []string{}
	}
	if !readiness.ConfirmationEligible {
		return "collecting", missing, nil
	}
	pending, _, err := schemas.RequirementStateSpec(state)
	if err != nil {
		return "", nil, err
	}
	if len(ws.ConfirmedRequirement) > 0 {
		// confirmed_requirement 存 Builder 输入快照;确认/修改判定比较
		// 确认状态与当前状态的投影,不依赖快照存储形状。
		if sameRequirementJSON(pending, confirmedRequirementProjection(ws.ConfirmedRequirementState)) {
			return "confirmed", missing, nil
		}
		return "modified", missing, nil
	}
	return "ready_to_confirm", missing, nil
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
		if len(ws.PendingRequirement) > 0 || len(ws.ConfirmedRequirement) > 0 {
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
	input.Conversation.CanPlan = len(ws.ConfirmedRequirement) > 0 || state.Revision > 0
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
	return nil
}
