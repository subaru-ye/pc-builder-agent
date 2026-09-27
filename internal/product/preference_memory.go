package product

// 用户显式保存的跨会话偏好:唯一写入路径是"从当前会话选择字段并保存"。
// 值、强度与来源一律取自服务端需求状态,请求体只允许 field 与 subject;
// 服务端核验会话归属、原始消息(role=user、quote 是原话子串)、字段与 scope,
// 拒绝 temporary、uncertain/inferred 与伪造来源。自动提取、模型召回与
// Builder 注入待 jev-v2/Builder 分支合并后另定。设计见 docs/tech/偏好记忆设计草案.md。
import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const (
	PreferenceActionCreated   = "created"
	PreferenceActionSupersede = "superseded"
	PreferenceActionUnchanged = "unchanged"
)

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
	DeletePreferenceMemory(ctx context.Context, ownerID, id string) error
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
	if fr.Scope == "temporary" {
		return schemas.PreferenceMemory{}, preferenceNotSavable("临时例外只在本次会话生效，不会保存为长期偏好。")
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
