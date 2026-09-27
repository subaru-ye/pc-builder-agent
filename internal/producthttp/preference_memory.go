package producthttp

// 用户显式保存的跨会话偏好端点:从当前会话保存、跨 owner 查看、删除。
// 保存与删除走窄接口断言(与 feedback 相同模式),服务未启用时降级 503。
// 自动召回注入与 Builder 接入不在本阶段。设计见 docs/tech/偏好记忆设计草案.md。
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type preferenceService interface {
	SaveSessionPreference(context.Context, string, string, product.PreferenceSave) (product.PreferenceSaveResult, error)
	ListPreferences(context.Context, []string) ([]schemas.PreferenceMemory, error)
	DeletePreference(context.Context, []string, string) error
	PreferenceSuggestions(context.Context, []string, string, string) ([]product.PreferenceSuggestion, error)
	ConfirmPreferences(context.Context, []string, string, string, product.PreferenceConfirmInput) (product.PreferenceConfirmResult, error)
}

type preferenceSourceDTO struct {
	Kind      string `json:"kind"`
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Quote     string `json:"quote"`
}

type preferenceDTO struct {
	ID         string              `json:"id"`
	Subject    string              `json:"subject"`
	Field      string              `json:"field"`
	Value      json.RawMessage     `json:"value"`
	Strength   string              `json:"strength"`
	Evidence   string              `json:"evidence"`
	Volatile   bool                `json:"volatile"`
	ObservedAt string              `json:"observed_at,omitempty"`
	Source     preferenceSourceDTO `json:"source"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
}

func newPreferenceDTO(m schemas.PreferenceMemory) preferenceDTO {
	return preferenceDTO{
		ID: m.ID, Subject: m.Subject, Field: m.Field, Value: m.Value,
		Strength: m.Strength, Evidence: m.Evidence, Volatile: m.Volatile, ObservedAt: m.ObservedAt,
		Source: preferenceSourceDTO(m.Source), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

// savePreference 是 POST /api/v1/sessions/{session_id}/preferences。
// 请求体只有 field 与 subject;值与来源由服务端从会话状态核验提取,
// 重复保存同一值幂等返回 unchanged,值变化构成改主意(superseded)。
func (a *API) savePreference(w http.ResponseWriter, r *http.Request) {
	service, ok := a.service.(preferenceService)
	if !ok {
		a.writeError(w, r, product.NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", requestID(r)))
		return
	}
	owner, ok := a.ownerForSession(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	var body struct {
		SchemaVersion int    `json:"schema_version"`
		Field         string `json:"field"`
		Subject       string `json:"subject"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.SchemaVersion != 1 {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "保存请求无效", 400,
			"schema_version 必须为 1。", requestID(r)))
		return
	}
	result, err := service.SaveSessionPreference(r.Context(), owner, r.PathValue("session_id"),
		product.PreferenceSave{Field: body.Field, Subject: body.Subject})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1, "preference": newPreferenceDTO(result.Preference), "action": result.Action,
	})
}

// listPreferences 是 GET /api/v1/preferences:当前身份名下(含认领的匿名 owner)
// 全部 active 偏好,本人(self)在前;这些是待确认建议,不是自动生效约束。
func (a *API) listPreferences(w http.ResponseWriter, r *http.Request) {
	service, ok := a.service.(preferenceService)
	if !ok {
		a.writeError(w, r, product.NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", requestID(r)))
		return
	}
	p, ok := a.principal(w, r)
	if !ok {
		return
	}
	memories, err := service.ListPreferences(r.Context(), p.Owners)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	dtos := make([]preferenceDTO, 0, len(memories))
	for _, m := range memories {
		dtos = append(dtos, newPreferenceDTO(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": 1, "preferences": dtos})
}

// sessionPreferenceSuggestions 是 GET /api/v1/sessions/{session_id}/preferences/suggestions:
// 召回当前身份可访问、指定 subject 的有效偏好供逐项确认。
// 只读不写;当前会话已生效的字段不出现在建议中,值冲突列全部选项由用户挑选。
func (a *API) sessionPreferenceSuggestions(w http.ResponseWriter, r *http.Request) {
	service, ok := a.service.(preferenceService)
	if !ok {
		a.writeError(w, r, product.NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", requestID(r)))
		return
	}
	p, ok := a.principal(w, r)
	if !ok {
		return
	}
	subject := r.URL.Query().Get("subject")
	if subject == "" {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "缺少归属对象", 400,
			"subject 必填：self 表示本人，其余为具名代配对象。", requestID(r)))
		return
	}
	suggestions, err := service.PreferenceSuggestions(r.Context(), p.Owners, r.PathValue("session_id"), subject)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": 1, "subject": subject, "suggestions": suggestions})
}

// confirmSessionPreferences 是 POST /api/v1/sessions/{session_id}/preferences/confirm:
// 用户逐项确认后写入当前 RequirementState。复用 EditRequirement 的 revision
// 校验与指纹幂等;当前会话已生效的字段跳过不覆盖,白名单外与过期记忆跳过。
func (a *API) confirmSessionPreferences(w http.ResponseWriter, r *http.Request) {
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	service, ok := a.service.(preferenceService)
	if !ok {
		a.writeError(w, r, product.NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", requestID(r)))
		return
	}
	var body struct {
		SchemaVersion    int      `json:"schema_version"`
		ExpectedRevision *int     `json:"expected_revision"`
		Subject          string   `json:"subject"`
		MemoryIDs        []string `json:"memory_ids"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.SchemaVersion != 1 || body.ExpectedRevision == nil || *body.ExpectedRevision < 0 ||
		body.Subject == "" || len(body.MemoryIDs) == 0 || len(body.MemoryIDs) > 32 {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "确认请求无效", 400,
			"schema_version 必须为 1，expected_revision 来自当前需求，memory_ids 为 1–32 条且 subject 必填。", requestID(r)))
		return
	}
	p, ok := a.principal(w, r)
	if !ok {
		return
	}
	result, err := service.ConfirmPreferences(r.Context(), p.Owners, r.PathValue("session_id"), key,
		product.PreferenceConfirmInput{ExpectedRevision: *body.ExpectedRevision, Subject: body.Subject, MemoryIDs: body.MemoryIDs})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	skipped := result.Skipped
	if skipped == nil {
		skipped = []product.PreferenceConfirmSkip{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1, "applied": result.Applied, "skipped": skipped, "session": toSession(result.Detail),
	})
}

// deletePreference 是 DELETE /api/v1/preferences/{preference_id}:
// 物理删除(含 supersede 链墓碑),跨 owner 未命中统一 404。
func (a *API) deletePreference(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.idempotencyKey(w, r); !ok {
		return
	}
	service, ok := a.service.(preferenceService)
	if !ok {
		a.writeError(w, r, product.NewProblem("preference_unavailable", "偏好记忆暂不可用", 503, "请稍后重试。", requestID(r)))
		return
	}
	p, ok := a.principal(w, r)
	if !ok {
		return
	}
	if err := service.DeletePreference(r.Context(), p.Owners, r.PathValue("preference_id")); err != nil {
		if errors.Is(err, store.ErrPreferenceMemoryNotFound) {
			// 删除尚未实现请求级幂等:重试同一删除会到达这里(已生效)。
			a.writeProblem(w, r, product.NewProblem("not_found", "偏好不存在或已删除", 404,
				"该偏好当前不可见；如果刚删除过，删除已经生效，无需重试。", requestID(r)))
			return
		}
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
