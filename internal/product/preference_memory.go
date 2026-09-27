package product

// 用户显式保存的跨会话偏好:唯一写入路径是"从当前会话选择字段并保存"。
// 值、强度与来源一律取自服务端需求状态,请求体只允许 field 与 subject;
// 服务端核验会话归属、原始消息(role=user、quote 是原话子串)、字段与 scope,
// 拒绝 temporary、uncertain/inferred 与伪造来源。自动提取、模型召回与
// Builder 注入待 jev-v2/Builder 分支合并后另定。设计见 docs/tech/偏好记忆设计草案.md。
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const (
	PreferenceActionCreated   = "created"
	PreferenceActionSupersede = "superseded"
	PreferenceActionUnchanged = "unchanged"

	PreferenceSuggestionSuggest  = "suggest"
	PreferenceSuggestionConflict = "conflict"
)

// preferenceVolatileWindow 是召回侧对易失事实的保守新鲜度窗口:
// 超窗的易失记录不作为建议展示。显式保存目前不产生易失记录,
// 该窗口只防御历史/未来写入路径;产品默认值可调,不属于模型合同。
const preferenceVolatileWindow = 7 * 24 * time.Hour

// PreferenceSave 是保存请求的领域输入:只有字段与归属对象。
// 本人(self)与代配对象由用户显式选择,不从 recipient 推断。
type PreferenceSave struct {
	Field   string `json:"field"`
	Subject string `json:"subject"`
}

type PreferenceSaveResult struct {
	Preference schemas.PreferenceMemory `json:"preference"`
	Action     string                   `json:"action"`
}

// preferenceMemoryStore 是偏好记忆的窄存储依赖,随 *Store 能力断言启用;
// 测试假存储可实现该接口替换真库。
type preferenceMemoryStore interface {
	WebMessageByID(ctx context.Context, sessionID, messageID string) (store.WebMessage, error)
	CreatePreferenceMemory(ctx context.Context, m schemas.PreferenceMemory) (schemas.PreferenceMemory, error)
	SupersedePreferenceMemory(ctx context.Context, ownerID, prevID string, next schemas.PreferenceMemory) (schemas.PreferenceMemory, error)
	ActivePreferenceMemoryByIdentity(ctx context.Context, ownerID, subject, field string) (schemas.PreferenceMemory, error)
	ActivePreferenceMemoriesByOwner(ctx context.Context, ownerID string) ([]schemas.PreferenceMemory, error)
	ActivePreferenceMemoryForOwners(ctx context.Context, owners []string, id string) (schemas.PreferenceMemory, error)
	DeletePreferenceMemory(ctx context.Context, ownerID, id string) error
	RequirementEditFingerprint(ctx context.Context, sessionID, requestID string) (string, bool, error)
}

func preferenceNotSavable(detail string) error {
	return NewProblem("preference_not_savable", "该项不能保存为长期偏好", 422, detail, "")
}

// SaveSessionPreference 核验并保存当前会话中的一个需求字段。
// 幂等:同字段同值重复保存返回 unchanged;值变化构成改主意,走 supersede 链。
func (s *Service) SaveSessionPreference(ctx context.Context, ownerID, sessionID string, save PreferenceSave) (PreferenceSaveResult, error) {
	st, ok := s.store.(preferenceMemoryStore)
	if !ok {
		return PreferenceSaveResult{}, NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", "")
	}
	save.Field, save.Subject = strings.TrimSpace(save.Field), strings.TrimSpace(save.Subject)
	if save.Subject == "" || len(save.Subject) > 40 {
		return PreferenceSaveResult{}, NewProblem("invalid_request", "保存请求无效", 400,
			"subject 必填且不超过 40 字符：self 表示本人，其余为代配对象标签。", "")
	}
	if !schemas.ValidPreferenceField(save.Field) {
		return PreferenceSaveResult{}, NewProblem("invalid_request", "保存请求无效", 400,
			fmt.Sprintf("field 必须是已知需求字段或 free.* 条目：%q", save.Field), "")
	}
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, sessionID)
	if err != nil {
		return PreferenceSaveResult{}, err
	}
	state, err := schemas.DecodeRequirementState(ws.RequirementState)
	if err != nil {
		return PreferenceSaveResult{}, preferenceNotSavable("该会话没有可溯源的增量需求状态。")
	}
	memory, err := savablePreferenceFromState(state, save.Field, sessionID)
	if err != nil {
		return PreferenceSaveResult{}, err
	}
	memory.OwnerID = ws.OwnerID
	memory.Subject = save.Subject
	// 来源核验:消息必须真实属于本会话、来自用户,且字段 quote 是其原文子串。
	message, err := st.WebMessageByID(ctx, sessionID, memory.Source.MessageID)
	if err != nil {
		if errors.Is(err, store.ErrWebMessageNotFound) {
			return PreferenceSaveResult{}, preferenceNotSavable("来源消息不存在，无法核验原始来源。")
		}
		return PreferenceSaveResult{}, err
	}
	if message.Role != "user" || !strings.Contains(message.Content, memory.Source.Quote) {
		return PreferenceSaveResult{}, preferenceNotSavable("来源引用与原始消息不符。")
	}
	return upsertPreferenceMemory(ctx, st, memory)
}

// savablePreferenceFromState 从会话需求状态提取可保存字段;任一边界不满足
// 都以稳定问题拒绝,不做默认值补齐。值/强度/证据/来源全部来自服务端状态。
func savablePreferenceFromState(state schemas.RequirementState, field, sessionID string) (schemas.PreferenceMemory, error) {
	fr, ok := state.Fields[field]
	if !ok || !schemas.ValidPreferenceField(field) {
		return schemas.PreferenceMemory{}, preferenceNotSavable(fmt.Sprintf("未知字段：%q。", field))
	}
	if fr.Status != "active" {
		return schemas.PreferenceMemory{}, preferenceNotSavable("该字段当前没有生效值，先在会话或面板中确认后再保存。")
	}
	if fr.Evidence != schemas.PreferenceEvidenceStated && fr.Evidence != schemas.PreferenceEvidenceAccept {
		return schemas.PreferenceMemory{}, preferenceNotSavable("证据不是用户明确表达或明确接受（不确定、推断或缺失的证据不能保存为长期偏好）。")
	}
	// 正向要求 session 范围:临时例外(temporary)与异常的空 scope 都不保存。
	if fr.Scope != "session" {
		return schemas.PreferenceMemory{}, preferenceNotSavable("只有本会话的长期要求（scope=session）可以保存；临时例外只在本次会话生效。")
	}
	if !schemas.IsStablePreferenceField(field) {
		return schemas.PreferenceMemory{}, preferenceNotSavable("预算、已有件与一次性要求不属于稳定偏好，只有品牌、静音、尺寸、外观等可跨会话保存。")
	}
	if fr.Source == nil || fr.Source.Kind != "chat" || fr.Source.MessageID == "" || fr.Source.Quote == "" {
		return schemas.PreferenceMemory{}, preferenceNotSavable("该值来自面板编辑或系统，缺少可核验的对话来源；请先在会话中说明这条偏好再保存。")
	}
	strength := schemas.PreferenceStrengthPrefer
	if fr.Strength == schemas.PreferenceStrengthMust {
		strength = schemas.PreferenceStrengthMust
	}
	return schemas.PreferenceMemory{
		Field:    field,
		Value:    fr.Value,
		Strength: strength,
		Evidence: fr.Evidence,
		Source: schemas.PreferenceSource{
			Kind: "chat", SessionID: sessionID,
			MessageID: fr.Source.MessageID, Quote: fr.Source.Quote,
		},
	}, nil
}

// upsertPreferenceMemory 落库:无记录则创建;同值幂等;异值 supersede。
// 并发写入命中部分唯一索引时重读身份重试一次,顺序重复请求天然幂等。
func upsertPreferenceMemory(ctx context.Context, st preferenceMemoryStore, memory schemas.PreferenceMemory) (PreferenceSaveResult, error) {
	for attempt := 0; ; attempt++ {
		existing, err := st.ActivePreferenceMemoryByIdentity(ctx, memory.OwnerID, memory.Subject, memory.Field)
		switch {
		case errors.Is(err, store.ErrPreferenceMemoryNotFound):
			created, err := st.CreatePreferenceMemory(ctx, memory)
			if errors.Is(err, store.ErrPreferenceIdentityConflict) && attempt == 0 {
				continue
			}
			if err != nil {
				return PreferenceSaveResult{}, err
			}
			return PreferenceSaveResult{Preference: created, Action: PreferenceActionCreated}, nil
		case err != nil:
			return PreferenceSaveResult{}, err
		case schemas.EqualPreferenceValue(existing.Value, memory.Value):
			return PreferenceSaveResult{Preference: existing, Action: PreferenceActionUnchanged}, nil
		default:
			created, err := st.SupersedePreferenceMemory(ctx, memory.OwnerID, existing.ID, memory)
			if errors.Is(err, store.ErrPreferenceUnchanged) {
				return PreferenceSaveResult{Preference: existing, Action: PreferenceActionUnchanged}, nil
			}
			if errors.Is(err, store.ErrPreferenceIdentityConflict) && attempt == 0 {
				continue
			}
			if errors.Is(err, store.ErrPreferenceMemoryNotActive) && attempt == 0 {
				continue // 身份刚被并发 supersede/删除:重读后按最新状态决策。
			}
			if err != nil {
				return PreferenceSaveResult{}, err
			}
			return PreferenceSaveResult{Preference: created, Action: PreferenceActionSupersede}, nil
		}
	}
}

// ListPreferences 汇总登录用户名下全部 owner(匿名→认领后多 owner)的 active
// 偏好,供管理视图查看。本人(self)排在代配对象之前。
func (s *Service) ListPreferences(ctx context.Context, owners []string) ([]schemas.PreferenceMemory, error) {
	st, ok := s.store.(preferenceMemoryStore)
	if !ok {
		return nil, NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", "")
	}
	var memories []schemas.PreferenceMemory
	for _, owner := range owners {
		owned, err := st.ActivePreferenceMemoriesByOwner(ctx, owner)
		if err != nil {
			return nil, err
		}
		memories = append(memories, owned...)
	}
	sort.Slice(memories, func(i, j int) bool {
		if memories[i].Subject != memories[j].Subject {
			if (memories[i].Subject == schemas.PreferenceSubjectSelf) != (memories[j].Subject == schemas.PreferenceSubjectSelf) {
				return memories[i].Subject == schemas.PreferenceSubjectSelf
			}
			return memories[i].Subject < memories[j].Subject
		}
		return memories[i].Field < memories[j].Field
	})
	return memories, nil
}

// DeletePreference 在 owners 中逐一尝试物理删除(含 supersede 链墓碑):
// 用户要求遗忘即真删除;全部未命中返回稳定 not found,不泄露存在性。
func (s *Service) DeletePreference(ctx context.Context, owners []string, preferenceID string) error {
	st, ok := s.store.(preferenceMemoryStore)
	if !ok {
		return NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", "")
	}
	for _, owner := range owners {
		err := st.DeletePreferenceMemory(ctx, owner, preferenceID)
		if errors.Is(err, store.ErrPreferenceMemoryNotFound) {
			continue
		}
		return err
	}
	return store.ErrPreferenceMemoryNotFound
}

// PreferenceSuggestion 是召回确认视图里的一条:同字段跨 owner 同值为单一建议,
// 不同值为冲突(conflict),由用户挑选或放弃,服务端不代选。
type PreferenceSuggestion struct {
	Field   string                     `json:"field"`
	Status  string                     `json:"status"`
	Choices []schemas.PreferenceMemory `json:"choices"`
}

// PreferenceConfirmInput 携带需求修订号与用户逐项挑选的记忆 ID。
// 确认写入走既有 EditRequirement 入口(revision 校验 + 指纹幂等)。
type PreferenceConfirmInput struct {
	ExpectedRevision int
	Subject          string
	MemoryIDs        []string
}

// 确认跳过的稳定原因码;UI 可据其解释,不翻译成误导性成功。
const (
	PreferenceSkipNotFound     = "not_found"
	PreferenceSkipSubject      = "subject_mismatch"
	PreferenceSkipField        = "field_not_storable"
	PreferenceSkipAlreadySet   = "field_already_set"
	PreferenceSkipStale        = "stale"
	PreferenceSkipDuplicated   = "duplicated"
	PreferenceSkipRevisionZero = "empty_request"
)

type PreferenceConfirmSkip struct {
	MemoryID string `json:"memory_id"`
	Reason   string `json:"reason"`
}

type PreferenceConfirmResult struct {
	Applied []string                `json:"applied"`
	Skipped []PreferenceConfirmSkip `json:"skipped"`
	Detail  SessionDetail           `json:"-"`
}

// sessionForOwners 在身份可访问的 owner 中解析会话;全部未命中按稳定 404 处理。
func (s *Service) sessionForOwners(ctx context.Context, owners []string, sessionID string) (store.WebSession, error) {
	for _, owner := range owners {
		ws, err := s.store.WebSessionByOwner(ctx, owner, sessionID)
		if err == nil {
			return ws, nil
		}
		if !errors.Is(err, store.ErrWebSessionNotFound) {
			return store.WebSession{}, err
		}
	}
	return store.WebSession{}, store.ErrWebSessionNotFound
}

// PreferenceSuggestions 召回指定身份可访问、指定 subject 的有效偏好,
// 供用户在新会话中逐项确认。只读白名单字段、非易失过期记录;
// 当前会话已明确的字段不出现在建议里——当前需求永远优先,历史不得覆盖。
// 同字段不同 owner 值冲突时标记 conflict 并列全部选项,不静默代选。
func (s *Service) PreferenceSuggestions(ctx context.Context, owners []string, sessionID, subject string) ([]PreferenceSuggestion, error) {
	st, ok := s.store.(preferenceMemoryStore)
	if !ok {
		return nil, NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", "")
	}
	if subject == "" {
		return nil, NewProblem("invalid_request", "缺少归属对象", 400, "召回偏好必须显式选择本人(self)或具名代配对象。", "")
	}
	ws, err := s.sessionForOwners(ctx, owners, sessionID)
	if err != nil {
		return nil, err
	}
	state, err := schemas.DecodeRequirementState(ws.RequirementState)
	if err != nil {
		return nil, preferenceNotSavable("该会话没有可用的增量需求状态。")
	}
	currentlyActive := map[string]bool{}
	for field, fr := range state.Fields {
		if fr.Status == "active" {
			currentlyActive[field] = true
		}
	}
	groups := map[string][]schemas.PreferenceMemory{}
	today := time.Now().UTC()
	for _, owner := range owners {
		memories, err := st.ActivePreferenceMemoriesByOwner(ctx, owner)
		if err != nil {
			return nil, err
		}
		for _, m := range memories {
			switch {
			case m.Subject != subject, currentlyActive[m.Field],
				!schemas.IsStablePreferenceField(m.Field), schemas.PreferenceStale(m, today, preferenceVolatileWindow):
				continue
			}
			groups[m.Field] = append(groups[m.Field], m)
		}
	}
	suggestions := make([]PreferenceSuggestion, 0, len(groups))
	for field, group := range groups {
		sort.Slice(group, func(i, j int) bool { return group[i].UpdatedAt.After(group[j].UpdatedAt) })
		choices := group[:1]
		for _, m := range group[1:] {
			distinct := true
			for _, kept := range choices {
				if schemas.EqualPreferenceValue(kept.Value, m.Value) {
					distinct = false
					break
				}
			}
			if distinct {
				choices = append(choices, m)
			}
		}
		status := PreferenceSuggestionSuggest
		if len(choices) > 1 {
			status = PreferenceSuggestionConflict
		}
		suggestions = append(suggestions, PreferenceSuggestion{Field: field, Status: status, Choices: choices})
	}
	sort.Slice(suggestions, func(i, j int) bool { return suggestions[i].Field < suggestions[j].Field })
	return suggestions, nil
}

// ConfirmPreferences 把用户逐项确认的记忆写入当前 RequirementState:
// 复用 EditRequirement 的 revision 校验与指纹幂等;每条记忆服务端重读核验,
// 白名单外、已生效、过期与 subject 不符的一律跳过而非覆盖。
// 写入以面板编辑(kind=edit)口径落账,历史原话保留在记忆来源中,
// 不伪装成本轮用户消息。
// 幂等键语义与需求修订一致:fingerprint 只记录成功写入——
//   - 同键同请求重放:不重复写入,逐项按当前状态给出跳过原因
//     (已生效项 field_already_set;原已应用但此后被撤销的项 duplicated),applied 为空;
//   - 同键不同请求(首次已成功写入):指纹不匹配,返回稳定冲突(ErrIdempotencyConflict);
//   - 首次失败(如 revision 冲突)不落指纹,同键重试正常重试。
func (s *Service) ConfirmPreferences(ctx context.Context, owners []string, sessionID, requestID string, input PreferenceConfirmInput) (PreferenceConfirmResult, error) {
	st, ok := s.store.(preferenceMemoryStore)
	if !ok {
		return PreferenceConfirmResult{}, NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", "")
	}
	if input.Subject == "" {
		return PreferenceConfirmResult{}, NewProblem("invalid_request", "缺少归属对象", 400, "确认偏好必须显式选择本人(self)或具名代配对象。", "")
	}
	if len(input.MemoryIDs) == 0 || len(input.MemoryIDs) > 32 {
		return PreferenceConfirmResult{}, NewProblem("invalid_request", "确认请求无效", 400, "请选择 1–32 条要确认的偏好。", requestID)
	}
	ws, err := s.sessionForOwners(ctx, owners, sessionID)
	if err != nil {
		return PreferenceConfirmResult{}, err
	}
	// 幂等键语义与需求修订入口完全对齐:指纹按"本次将写入的编辑内容"计算,
	// 与 EditRequirement 落库的指纹同构——
	//   同键重放(首次已成功写入,无论字段此后是否仍生效):命中同指纹,
	//   不重复写入,待应用项按 duplicated 跳过,applied 为空;
	//   同键不同请求(内容不同,含记忆已被改写):指纹不匹配,稳定冲突拒绝;
	//   首次失败(如 revision 冲突)不落指纹,同键重试正常重试。
	state, err := schemas.DecodeRequirementState(ws.RequirementState)
	if err != nil {
		return PreferenceConfirmResult{}, preferenceNotSavable("该会话没有可用的增量需求状态。")
	}
	result := PreferenceConfirmResult{Skipped: []PreferenceConfirmSkip{}}
	type pendingApply struct {
		id string
		op schemas.RequirementOperation
	}
	var pending []pendingApply
	seen := map[string]bool{}
	today := time.Now().UTC()
	for _, id := range input.MemoryIDs {
		memory, err := st.ActivePreferenceMemoryForOwners(ctx, owners, id)
		if errors.Is(err, store.ErrPreferenceMemoryNotFound) {
			result.Skipped = append(result.Skipped, PreferenceConfirmSkip{MemoryID: id, Reason: PreferenceSkipNotFound})
			continue
		}
		if err != nil {
			return PreferenceConfirmResult{}, err
		}
		switch {
		case memory.Subject != input.Subject:
			result.Skipped = append(result.Skipped, PreferenceConfirmSkip{MemoryID: id, Reason: PreferenceSkipSubject})
			continue
		case !schemas.IsStablePreferenceField(memory.Field):
			result.Skipped = append(result.Skipped, PreferenceConfirmSkip{MemoryID: id, Reason: PreferenceSkipField})
			continue
		case state.Fields[memory.Field].Status == "active":
			// 当前会话已明确的值优先:历史偏好只补空,不覆盖。
			result.Skipped = append(result.Skipped, PreferenceConfirmSkip{MemoryID: id, Reason: PreferenceSkipAlreadySet})
			continue
		case schemas.PreferenceStale(memory, today, preferenceVolatileWindow):
			result.Skipped = append(result.Skipped, PreferenceConfirmSkip{MemoryID: id, Reason: PreferenceSkipStale})
			continue
		case seen[memory.Field]:
			result.Skipped = append(result.Skipped, PreferenceConfirmSkip{MemoryID: id, Reason: PreferenceSkipDuplicated})
			continue
		}
		seen[memory.Field] = true
		pending = append(pending, pendingApply{id: memory.ID, op: schemas.RequirementOperation{
			Op: "set", Field: memory.Field, Value: memory.Value,
			Strength: memory.Strength, Scope: "session",
		}})
	}
	if len(pending) == 0 {
		detail, err := s.GetSession(ctx, ws.OwnerID, sessionID)
		if err != nil {
			return PreferenceConfirmResult{}, err
		}
		result.Detail = detail
		return result, nil
	}
	operations := make([]schemas.RequirementOperation, 0, len(pending))
	for _, p := range pending {
		operations = append(operations, p.op)
	}
	// 与 EditRequirement 落库指纹同构的预检:同键重放不重复写入,
	// 同键不同内容以稳定冲突拒绝(首次失败未落指纹,重试不受影响)。
	edit := RequirementEdit{ExpectedRevision: input.ExpectedRevision, Operations: operations}
	request, err := json.Marshal(edit)
	if err != nil {
		return PreferenceConfirmResult{}, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(request))
	if old, found, err := st.RequirementEditFingerprint(ctx, sessionID, requestID); err != nil {
		return PreferenceConfirmResult{}, err
	} else if found {
		if old != fingerprint {
			return PreferenceConfirmResult{}, store.ErrIdempotencyConflict
		}
		for _, p := range pending {
			result.Skipped = append(result.Skipped, PreferenceConfirmSkip{MemoryID: p.id, Reason: PreferenceSkipDuplicated})
		}
		detail, err := s.GetSession(ctx, ws.OwnerID, sessionID)
		if err != nil {
			return PreferenceConfirmResult{}, err
		}
		result.Detail = detail
		return result, nil
	}
	for _, p := range pending {
		result.Applied = append(result.Applied, p.id)
	}
	detail, err := s.EditRequirement(ctx, ws.OwnerID, sessionID, requestID, edit)
	if err != nil {
		return PreferenceConfirmResult{}, err
	}
	result.Detail = detail
	return result, nil
}
