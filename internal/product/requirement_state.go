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
	var state schemas.RequirementState
	if len(raw) == 0 {
		return state, nil
	}
	err := json.Unmarshal(raw, &state)
	return state, err
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
		if err := s.completeRequirementState(ctx, ownerID, r, next); err != nil {
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

func (s *Service) completeRequirementState(ctx context.Context, ownerID string, r store.AgentRun, state schemas.RequirementState) error {
	pending, missing, err := planningProjection(state)
	if err != nil {
		return err
	}
	phase := store.PhaseRequirementReady
	assistant := ScreeningReadyMessage
	if state.Reply != "" {
		assistant = state.Reply
	}
	if state.NextAction == "collect" {
		phase = store.PhaseCollecting
	}
	if len(missing) > 0 {
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
	if state.Reply == "" && len(missing) == 0 && ws.VersionCount > 0 && sameRequirementJSON(pending, ws.ConfirmedRequirement) {
		phase = store.PhaseReady
		assistant = "当前有效需求保持不变，可继续查看配置或修改需求。"
	} else if state.Reply == "" && len(missing) == 0 && len(ws.ConfirmedRequirement) > 0 {
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

func requirementLifecycle(ws store.WebSession) (string, []string) {
	if ws.Phase == store.PhaseCollecting && len(ws.PendingRequirement) == 0 {
		return "collecting", []string{}
	}
	state, err := decodeSessionRequirements(ws.RequirementState)
	if err != nil || len(ws.RequirementState) == 0 {
		return "collecting", []string{}
	}
	pending, missing, err := planningProjection(state)
	if missing == nil {
		missing = []string{}
	}
	if len(ws.ConfirmedRequirement) > 0 {
		if err == nil && sameRequirementJSON(pending, ws.ConfirmedRequirement) {
			return "confirmed", missing
		}
		return "modified", missing
	}
	if err != nil || len(missing) > 0 {
		return "collecting", missing
	}
	if state.NextAction == "collect" {
		return "collecting", missing
	}
	return "ready_to_confirm", missing
}

func (s *Service) attachRequirementState(ctx context.Context, ownerID string, r store.AgentRun, input *ScreenInput) error {
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, r.SessionID)
	if err != nil {
		return err
	}
	state, err := decodeSessionRequirements(ws.RequirementState)
	if err != nil {
		return err
	}
	if len(ws.RequirementState) == 0 {
		raw := ws.PendingRequirement
		if _, supportsPlanning := s.store.(proposalStore); !supportsPlanning {
			return nil // Legacy diagnostic stores keep their original protocol.
		}
		if len(raw) == 0 {
			raw = ws.ConfirmedRequirement
		}
		state = schemas.LegacyPlanningState(raw)
	}
	input.RequirementState = &state
	input.HasBuild = ws.VersionCount > 0
	// The first message of a session always hands back an explicit confirmation;
	// later messages (and confirmed requirements) may continue into planning
	// directly — the user's own follow-up is the authorization.
	input.Conversation.CanPlan = len(ws.ConfirmedRequirement) > 0 || state.Revision > 0 || len(ws.PendingRequirement) > 0
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

func planningProjection(state schemas.RequirementState) (json.RawMessage, []string, error) {
	raw, err := schemas.PlanningRequirement(state)
	return raw, []string{}, err
}
