package product

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// fakePreferenceStore 在 fakeProductStore 之上实现偏好记忆窄接口的内存版,
// 语义与真库一致:同身份至多一条 active、supersede 链、物理删除含墓碑。
type fakePreferenceStore struct {
	*fakeProductStore
	memories map[string]schemas.PreferenceMemory
	seq      int
}

func newFakePreferenceStore() *fakePreferenceStore {
	return &fakePreferenceStore{fakeProductStore: newFakeProductStore(), memories: map[string]schemas.PreferenceMemory{}}
}

func (f *fakePreferenceStore) WebMessageByID(_ context.Context, sessionID, messageID string) (store.WebMessage, error) {
	for _, m := range f.messages {
		if m.ID == messageID && m.SessionID == sessionID {
			return m, nil
		}
	}
	return store.WebMessage{}, store.ErrWebMessageNotFound
}

func (f *fakePreferenceStore) ActivePreferenceMemoryByIdentity(_ context.Context, ownerID, subject, field string) (schemas.PreferenceMemory, error) {
	for _, m := range f.memories {
		if m.OwnerID == ownerID && m.Subject == subject && m.Field == field && m.Status == schemas.PreferenceStatusActive {
			return m, nil
		}
	}
	return schemas.PreferenceMemory{}, store.ErrPreferenceMemoryNotFound
}

func (f *fakePreferenceStore) ActivePreferenceMemoriesByOwner(_ context.Context, ownerID string) ([]schemas.PreferenceMemory, error) {
	var out []schemas.PreferenceMemory
	for _, m := range f.memories {
		if m.OwnerID == ownerID && m.Status == schemas.PreferenceStatusActive {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakePreferenceStore) CreatePreferenceMemory(_ context.Context, m schemas.PreferenceMemory) (schemas.PreferenceMemory, error) {
	if err := schemas.ValidatePreferenceMemory(m); err != nil {
		return schemas.PreferenceMemory{}, err
	}
	return f.insertMemory(m)
}

// insertMemory 跳过输入校验落库,对应真库 Supersede 在校验后回填 supersedes 的顺序。
func (f *fakePreferenceStore) insertMemory(m schemas.PreferenceMemory) (schemas.PreferenceMemory, error) {
	if _, err := f.ActivePreferenceMemoryByIdentity(context.Background(), m.OwnerID, m.Subject, m.Field); err == nil {
		return schemas.PreferenceMemory{}, store.ErrPreferenceIdentityConflict
	}
	f.seq++
	m.ID = "pref-" + string(rune('a'+f.seq))
	m.Status = schemas.PreferenceStatusActive
	f.memories[m.ID] = m
	return m, nil
}

func (f *fakePreferenceStore) SupersedePreferenceMemory(_ context.Context, ownerID, prevID string, next schemas.PreferenceMemory) (schemas.PreferenceMemory, error) {
	prev, ok := f.memories[prevID]
	if !ok || prev.OwnerID != ownerID {
		return schemas.PreferenceMemory{}, store.ErrPreferenceMemoryNotFound
	}
	if prev.Status != schemas.PreferenceStatusActive {
		return schemas.PreferenceMemory{}, store.ErrPreferenceMemoryNotActive
	}
	if prev.Subject != next.Subject || prev.Field != next.Field {
		return schemas.PreferenceMemory{}, schemas.ErrPreferenceMemoryInvalid
	}
	if schemas.EqualPreferenceValue(prev.Value, next.Value) {
		return schemas.PreferenceMemory{}, store.ErrPreferenceUnchanged
	}
	prev.Status = schemas.PreferenceStatusSupersede
	f.memories[prevID] = prev
	next.Supersedes = prevID
	return f.insertMemory(next)
}

func (f *fakePreferenceStore) DeletePreferenceMemory(_ context.Context, ownerID, id string) error {
	target, ok := f.memories[id]
	if !ok || target.OwnerID != ownerID {
		return store.ErrPreferenceMemoryNotFound
	}
	for memoryID, m := range f.memories {
		if m.OwnerID == ownerID && m.Subject == target.Subject && m.Field == target.Field {
			delete(f.memories, memoryID)
		}
	}
	return nil
}

// sourceSessionState 以指定来源类型构造会话需求状态(ops 的 quote 必须是消息原文子串)。
func sourceSessionState(t *testing.T, kind, messageID, ops, message string) json.RawMessage {
	t.Helper()
	update, err := schemas.DecodeRequirementUpdate([]byte(`{"operations":` + ops + `}`))
	if err != nil {
		t.Fatal(err)
	}
	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), update,
		schemas.RequirementSource{Kind: kind, MessageID: messageID, Quote: message})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newPreferenceTestService(t *testing.T, st *fakePreferenceStore) *Service {
	t.Helper()
	svc, err := NewService(context.Background(), st, &fakeAgent{}, newFakeSink())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func addChatMessage(st *fakePreferenceStore, role, content string) {
	st.messages = append(st.messages, store.WebMessage{ID: "msg-1", SessionID: "session-1", Role: role, Content: content})
}

func addChatMessageID(st *fakePreferenceStore, messageID, role, content string) {
	st.messages = append(st.messages, store.WebMessage{ID: messageID, SessionID: "session-1", Role: role, Content: content})
}

func assertProblemCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	var problem Problem
	if !errors.As(err, &problem) || problem.Code != wantCode {
		t.Fatalf("期望 problem %s,得到 %v", wantCode, err)
	}
}

func TestSaveSessionPreferenceLifecycle(t *testing.T) {
	st := newFakePreferenceStore()
	st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
		RequirementState: sourceSessionState(t, "chat", "msg-1",
			`[{"op":"set","field":"brand_pref.gpu","value":"nvidia","strength":"prefer","scope":"session","evidence":"stated","quote":"显卡要N卡"}]`,
			"预算8000，显卡要N卡，尽量安静")}
	addChatMessage(st, "user", "预算8000，显卡要N卡，尽量安静")
	svc := newPreferenceTestService(t, st)
	ctx := context.Background()

	created, err := svc.SaveSessionPreference(ctx, "owner-1", "session-1",
		PreferenceSave{Field: "brand_pref.gpu", Subject: schemas.PreferenceSubjectSelf})
	if err != nil || created.Action != PreferenceActionCreated {
		t.Fatalf("首次保存应 created: %+v err=%v", created, err)
	}
	if string(created.Preference.Value) != `"nvidia"` || created.Preference.Strength != schemas.PreferenceStrengthPrefer {
		t.Fatalf("值与强度应来自服务端状态: %+v", created.Preference)
	}
	if created.Preference.Source.SessionID != "session-1" || created.Preference.Source.MessageID != "msg-1" || created.Preference.Source.Quote != "显卡要N卡" {
		t.Fatalf("来源应逐项核验回填: %+v", created.Preference.Source)
	}

	// 幂等:重复保存相同值返回 unchanged,不产生新记录。
	repeat, err := svc.SaveSessionPreference(ctx, "owner-1", "session-1",
		PreferenceSave{Field: "brand_pref.gpu", Subject: schemas.PreferenceSubjectSelf})
	if err != nil || repeat.Action != PreferenceActionUnchanged || repeat.Preference.ID != created.Preference.ID {
		t.Fatalf("重复保存应 unchanged: %+v err=%v", repeat, err)
	}
	if len(st.memories) != 1 {
		t.Fatalf("重复保存不得新增记录: %+v", st.memories)
	}

	// 改主意:会话状态换成 A 卡后再次保存 → superseded,旧记录不再 active。
	st.session.RequirementState = sourceSessionState(t, "chat", "msg-2",
		`[{"op":"set","field":"brand_pref.gpu","value":"amd","evidence":"stated","quote":"还是用A卡吧"}]`,
		"预算8000，还是用A卡吧")
	st.messages = nil
	addChatMessageID(st, "msg-2", "user", "预算8000，还是用A卡吧")
	changed, err := svc.SaveSessionPreference(ctx, "owner-1", "session-1",
		PreferenceSave{Field: "brand_pref.gpu", Subject: schemas.PreferenceSubjectSelf})
	if err != nil || changed.Action != PreferenceActionSupersede || changed.Preference.Supersedes != created.Preference.ID {
		t.Fatalf("值变化应 superseded: %+v err=%v", changed, err)
	}
	active, err := st.ActivePreferenceMemoriesByOwner(ctx, "owner-1")
	if err != nil || len(active) != 1 || active[0].ID != changed.Preference.ID {
		t.Fatalf("改主意后只应有一条 active: %+v err=%v", active, err)
	}

	// 删除:物理删除整条链;跨 owner 一律 not found。
	if err := svc.DeletePreference(ctx, []string{"owner-2"}, changed.Preference.ID); !errors.Is(err, store.ErrPreferenceMemoryNotFound) {
		t.Fatalf("跨 owner 删除应 not found: %v", err)
	}
	if err := svc.DeletePreference(ctx, []string{"owner-1", "owner-2"}, changed.Preference.ID); err != nil {
		t.Fatalf("本人 owner 删除应成功: %v", err)
	}
	if len(st.memories) != 0 {
		t.Fatalf("删除应移除整条 supersede 链: %+v", st.memories)
	}
}

func TestSaveSessionPreferenceRejections(t *testing.T) {
	cases := []struct {
		name     string
		kind     string // 需求来源类型:chat 或 edit
		ops      string
		message  string
		save     PreferenceSave
		wantCode string
	}{
		{
			name: "临时例外不升级", kind: "chat",
			ops:  `[{"op":"set","field":"brand_pref.gpu","value":"nvidia","scope":"temporary","evidence":"stated","quote":"这次先试试N卡"}]`,
			message: "这次先试试N卡，就这一次", save: PreferenceSave{Field: "brand_pref.gpu", Subject: "self"},
			wantCode: "preference_not_savable",
		},
		{
			name: "未生效字段拒绝", kind: "chat",
			ops:  `[{"op":"set","field":"brand_pref.gpu","value":"nvidia","evidence":"stated","quote":"显卡要N卡"}]`,
			message: "显卡要N卡", save: PreferenceSave{Field: "budget_cny", Subject: "self"},
			wantCode: "preference_not_savable",
		},
		{
			name: "面板编辑来源拒绝", kind: "edit",
			ops:  `[{"op":"set","field":"noise_pref","value":"silent","evidence":"stated","quote":"通过需求面板修改：静音改为silent"}]`,
			message: "通过需求面板修改：静音改为silent", save: PreferenceSave{Field: "noise_pref", Subject: "self"},
			wantCode: "preference_not_savable",
		},
		{
			name: "会话中不存在的字段拒绝", kind: "chat",
			ops:  `[{"op":"set","field":"brand_pref.gpu","value":"nvidia","evidence":"stated","quote":"显卡要N卡"}]`,
			message: "显卡要N卡", save: PreferenceSave{Field: "free.xiaowang", Subject: "self"},
			wantCode: "preference_not_savable",
		},
		{
			name: "subject 缺失", kind: "chat",
			ops:  `[{"op":"set","field":"brand_pref.gpu","value":"nvidia","evidence":"stated","quote":"显卡要N卡"}]`,
			message: "显卡要N卡", save: PreferenceSave{Field: "brand_pref.gpu"},
			wantCode: "invalid_request",
		},
		{
			name: "subject 超长", kind: "chat",
			ops:  `[{"op":"set","field":"brand_pref.gpu","value":"nvidia","evidence":"stated","quote":"显卡要N卡"}]`,
			message: "显卡要N卡", save: PreferenceSave{Field: "brand_pref.gpu", Subject: strings.Repeat("朋", 41)},
			wantCode: "invalid_request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakePreferenceStore()
			st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
				RequirementState: sourceSessionState(t, tc.kind, "msg-1", tc.ops, tc.message)}
			addChatMessage(st, "user", tc.message)
			svc := newPreferenceTestService(t, st)
			_, err := svc.SaveSessionPreference(context.Background(), "owner-1", "session-1", tc.save)
			assertProblemCode(t, err, tc.wantCode)
			if len(st.memories) != 0 {
				t.Fatalf("拒绝路径不得写入记忆: %+v", st.memories)
			}
		})
	}

	t.Run("证据标签缺失拒绝", func(t *testing.T) {
		st := newFakePreferenceStore()
		st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
			RequirementState: sourceSessionState(t, "chat", "msg-1",
				`[{"op":"set","field":"brand_pref.gpu","value":"nvidia","evidence":"stated","quote":"显卡要N卡"}]`, "显卡要N卡")}
		addChatMessage(st, "user", "显卡要N卡")
		var state schemas.RequirementState
		if err := json.Unmarshal(st.session.RequirementState, &state); err != nil {
			t.Fatal(err)
		}
		field := state.Fields["brand_pref.gpu"]
		field.Evidence = ""
		state.Fields["brand_pref.gpu"] = field
		raw, _ := json.Marshal(state)
		st.session.RequirementState = raw
		svc := newPreferenceTestService(t, st)
		_, err := svc.SaveSessionPreference(context.Background(), "owner-1", "session-1",
			PreferenceSave{Field: "brand_pref.gpu", Subject: "self"})
		assertProblemCode(t, err, "preference_not_savable")
		if len(st.memories) != 0 {
			t.Fatal("缺证据不得写入")
		}
	})
}

func TestSaveSessionPreferenceSourceVerification(t *testing.T) {
	ops := `[{"op":"set","field":"brand_pref.gpu","value":"nvidia","evidence":"stated","quote":"显卡要N卡"}]`
	message := "预算8000，显卡要N卡"
	newSvc := func(t *testing.T, messages []store.WebMessage) *Service {
		st := newFakePreferenceStore()
		st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
			RequirementState: sourceSessionState(t, "chat", "msg-1", ops, message)}
		st.messages = messages
		return newPreferenceTestService(t, st)
	}
	t.Run("来源消息不存在", func(t *testing.T) {
		svc := newSvc(t, nil)
		_, err := svc.SaveSessionPreference(context.Background(), "owner-1", "session-1",
			PreferenceSave{Field: "brand_pref.gpu", Subject: "self"})
		assertProblemCode(t, err, "preference_not_savable")
	})
	t.Run("助手消息不可作为来源", func(t *testing.T) {
		svc := newSvc(t, []store.WebMessage{{ID: "msg-1", SessionID: "session-1", Role: "assistant", Content: message}})
		_, err := svc.SaveSessionPreference(context.Background(), "owner-1", "session-1",
			PreferenceSave{Field: "brand_pref.gpu", Subject: "self"})
		assertProblemCode(t, err, "preference_not_savable")
	})
	t.Run("引用与原文不符", func(t *testing.T) {
		svc := newSvc(t, []store.WebMessage{{ID: "msg-1", SessionID: "session-1", Role: "user", Content: "预算8000，想要N卡"}})
		_, err := svc.SaveSessionPreference(context.Background(), "owner-1", "session-1",
			PreferenceSave{Field: "brand_pref.gpu", Subject: "self"})
		assertProblemCode(t, err, "preference_not_savable")
	})
}

func TestSaveSessionPreferenceOwnership(t *testing.T) {
	st := newFakePreferenceStore()
	st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
		RequirementState: sourceSessionState(t, "chat", "msg-1",
			`[{"op":"set","field":"brand_pref.gpu","value":"nvidia","evidence":"stated","quote":"显卡要N卡"}]`, "显卡要N卡")}
	addChatMessage(st, "user", "显卡要N卡")
	svc := newPreferenceTestService(t, st)
	_, err := svc.SaveSessionPreference(context.Background(), "owner-2", "session-1",
		PreferenceSave{Field: "brand_pref.gpu", Subject: "self"})
	if !errors.Is(err, store.ErrWebSessionNotFound) {
		t.Fatalf("非本人会话应 not found: %v", err)
	}
	if len(st.memories) != 0 {
		t.Fatal("越权保存不得写入")
	}
}

func TestListPreferencesOrdering(t *testing.T) {
	st := newFakePreferenceStore()
	svc := newPreferenceTestService(t, st)
	ctx := context.Background()
	mustCreate := func(owner, subject, field, value string) {
		t.Helper()
		if _, err := st.CreatePreferenceMemory(ctx, schemas.PreferenceMemory{
			OwnerID: owner, Subject: subject, Field: field, Value: json.RawMessage(value),
			Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated,
			Source: schemas.PreferenceSource{Kind: "chat", SessionID: "s", MessageID: "m", Quote: "q"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	mustCreate("owner-2", "self", "noise_pref", `"silent"`)
	mustCreate("owner-1", "friend:xw", "size_pref", `"itx"`)
	mustCreate("owner-1", "self", "brand_pref.gpu", `"nvidia"`)
	list, err := svc.ListPreferences(ctx, []string{"owner-1", "owner-2"})
	if err != nil || len(list) != 3 {
		t.Fatalf("多 owner 汇总: %+v err=%v", list, err)
	}
	if list[0].Subject != "self" || list[0].OwnerID != "owner-1" || list[0].Field != "brand_pref.gpu" {
		t.Fatalf("本人应排最前: %+v", list)
	}
	if list[1].Subject != "self" || list[1].OwnerID != "owner-2" {
		t.Fatalf("其余本人记录次之: %+v", list[1])
	}
	if !strings.HasPrefix(list[2].Subject, "friend:") {
		t.Fatalf("本人之后的应是代配对象: %+v", list[2])
	}
}

func TestPreferenceUnavailableWithoutCapability(t *testing.T) {
	svc, err := NewService(context.Background(), newFakeProductStore(), &fakeAgent{}, newFakeSink())
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SaveSessionPreference(context.Background(), "owner-1", "session-1", PreferenceSave{Field: "noise_pref", Subject: "self"})
	assertProblemCode(t, err, "preference_unavailable")
	err = svc.DeletePreference(context.Background(), []string{"owner-1"}, "x")
	assertProblemCode(t, err, "preference_unavailable")
}
