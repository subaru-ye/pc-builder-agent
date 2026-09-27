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

func (f *fakePreferenceStore) ActivePreferenceMemoryForOwners(_ context.Context, owners []string, id string) (schemas.PreferenceMemory, error) {
	for _, owner := range owners {
		m, ok := f.memories[id]
		if ok && m.OwnerID == owner && m.Status == schemas.PreferenceStatusActive {
			return m, nil
		}
	}
	return schemas.PreferenceMemory{}, store.ErrPreferenceMemoryNotFound
}

func mustCreateMemory(t *testing.T, st *fakePreferenceStore, m schemas.PreferenceMemory) schemas.PreferenceMemory {
	t.Helper()
	created, err := st.CreatePreferenceMemory(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func seededSource(sessionID string) schemas.PreferenceSource {
	return schemas.PreferenceSource{Kind: "chat", SessionID: sessionID, MessageID: "m0", Quote: "早前原话"}
}

func TestPreferenceSuggestionsGrouping(t *testing.T) {
	st := newFakePreferenceStore()
	// 会话内已明确显卡偏好:该字段不得被历史建议建议覆盖。
	st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
		RequirementState: sourceSessionState(t, "chat", "msg-1",
			`[{"op":"set","field":"brand_pref.gpu","value":"nvidia","scope":"session","evidence":"stated","quote":"显卡要N卡"}]`,
			"预算8000，显卡要N卡")}
	addChatMessage(st, "user", "预算8000，显卡要N卡")
	svc := newPreferenceTestService(t, st)
	ctx := context.Background()
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "noise_pref",
		Value: json.RawMessage(`"silent"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "friend:xw", Field: "noise_pref",
		Value: json.RawMessage(`"normal"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "brand_pref.gpu",
		Value: json.RawMessage(`"amd"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-2", Subject: "self", Field: "brand_pref.gpu",
		Value: json.RawMessage(`"intel"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "brand_pref.cpu",
		Value: json.RawMessage(`"amd"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-2", Subject: "self", Field: "brand_pref.cpu",
		Value: json.RawMessage(`"intel"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-2", Subject: "self", Field: "noise_pref",
		Value: json.RawMessage(`"silent"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "budget_cny",
		Value: json.RawMessage("8000"), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "size_pref",
		Value: json.RawMessage(`"itx"`), Volatile: true, ObservedAt: "2020-01-01",
		Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	fresh := mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "appearance",
		Value: json.RawMessage(`"white"`), Volatile: true, ObservedAt: "2999-01-01",
		Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})

	list, err := svc.PreferenceSuggestions(ctx, []string{"owner-1", "owner-2"}, "session-1", "self")
	if err != nil {
		t.Fatal(err)
	}
	byField := map[string]PreferenceSuggestion{}
	for _, s := range list {
		byField[s.Field] = s
	}
	if got := byField["brand_pref.cpu"]; got.Status != PreferenceSuggestionConflict || len(got.Choices) != 2 {
		t.Fatalf("不同身份值冲突应列全部选项: %+v", got)
	}
	if got := byField["noise_pref"]; got.Status != PreferenceSuggestionSuggest || len(got.Choices) != 1 {
		t.Fatalf("同值跨身份应去重为单一建议: %+v", got)
	}
	if _, ok := byField["brand_pref.gpu"]; ok {
		t.Fatal("当前会话已生效字段不得进入建议(当前需求优先)")
	}
	if got := byField["appearance"]; got.Status != PreferenceSuggestionSuggest || got.Choices[0].ID != fresh.ID {
		t.Fatalf("窗口内易失记录可作为建议: %+v", got)
	}
	if _, ok := byField["size_pref"]; ok {
		t.Fatal("过期易失记录不得进入建议")
	}
	if _, ok := byField["budget_cny"]; ok {
		t.Fatal("白名单外字段不得进入建议")
	}

	friendList, err := svc.PreferenceSuggestions(ctx, []string{"owner-1"}, "session-1", "friend:xw")
	if err != nil || len(friendList) != 1 || friendList[0].Field != "noise_pref" {
		t.Fatalf("代配对象只应看到自己的记录: %+v err=%v", friendList, err)
	}
	if _, err := svc.PreferenceSuggestions(ctx, []string{"owner-2"}, "session-1", "self"); !errors.Is(err, store.ErrWebSessionNotFound) {
		t.Fatalf("会话不可达应稳定 404: %v", err)
	}
	// 确认前不产生任何写入。
	before, err := svc.GetSession(ctx, "owner-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PreferenceSuggestions(ctx, []string{"owner-1", "owner-2"}, "session-1", "self"); err != nil {
		t.Fatal(err)
	}
	after, err := svc.GetSession(ctx, "owner-1", "session-1")
	if err != nil || string(before.Session.RequirementState) != string(after.Session.RequirementState) {
		t.Fatalf("建议只读,不得改变需求状态: %v", err)
	}
}

func TestConfirmPreferencesLifecycle(t *testing.T) {
	st := newFakePreferenceStore()
	st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
		RequirementState: sourceSessionState(t, "chat", "msg-1",
			`[{"op":"set","field":"budget_cny","value":8000,"scope":"session","evidence":"stated","quote":"预算8000"}]`,
			"预算8000")}
	addChatMessage(st, "user", "预算8000")
	svc := newPreferenceTestService(t, st)
	ctx := context.Background()
	noise := mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "noise_pref",
		Value: json.RawMessage(`"silent"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	budget := mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "budget_cny",
		Value: json.RawMessage("9000"), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})

	// 白名单外字段即便被指名也跳过(防御直连调用)。
	result, err := svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "req-0",
		PreferenceConfirmInput{ExpectedRevision: 1, Subject: "self", MemoryIDs: []string{noise.ID}})
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("白名单内确认应写入: %+v err=%v", result, err)
	}
	state, err := schemas.DecodeRequirementState(result.Detail.Session.RequirementState)
	if err != nil {
		t.Fatal(err)
	}
	field := state.Fields["noise_pref"]
	if field.Status != "active" || string(field.Value) != `"silent"` || field.Scope != "session" {
		t.Fatalf("确认后应写入当前需求: %+v", field)
	}
	// 历史原话不得伪装成本轮用户消息:写入走面板编辑口径,证据不冒充 stated。
	if field.Source == nil || field.Source.Kind != "edit" || field.Source.Quote == "早前原话" {
		t.Fatalf("来源应为面板编辑而非历史原话: %+v", field.Source)
	}
	if field.Evidence != "" {
		t.Fatalf("确认写入不得声称本轮 stated 证据: %q", field.Evidence)
	}

	// 重复请求:已生效字段全部跳过,状态不再变化。
	result, err = svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "req-1",
		PreferenceConfirmInput{ExpectedRevision: 2, Subject: "self", MemoryIDs: []string{noise.ID}})
	if err != nil || len(result.Applied) != 0 || len(result.Skipped) != 1 || result.Skipped[0].Reason != PreferenceSkipAlreadySet {
		t.Fatalf("重复确认应按已生效跳过: %+v err=%v", result, err)
	}

	// 当前需求优先的第二层保障:预算属白名单外,确认侧直接跳过(保存与确认双重拦截)。
	// 已生效字段优先已由上方 noise 重复确认(field_already_set)证明。
	result, err = svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "req-2",
		PreferenceConfirmInput{ExpectedRevision: 2, Subject: "self", MemoryIDs: []string{budget.ID}})
	if err != nil || len(result.Applied) != 0 || result.Skipped[0].Reason != PreferenceSkipField {
		t.Fatalf("白名单外确认应跳过: %+v err=%v", result, err)
	}
	state, err = schemas.DecodeRequirementState(result.Detail.Session.RequirementState)
	if err != nil || string(state.Fields["budget_cny"].Value) != "8000" {
		t.Fatalf("会话内预算必须保持不变: %v %s", err, state.Fields["budget_cny"].Value)
	}

	// 跨身份冲突:同请求选同字段两条 → 只应用一条,另一条按重复跳过。
	itx := mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "size_pref",
		Value: json.RawMessage(`"itx"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	atx := mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-2", Subject: "self", Field: "size_pref",
		Value: json.RawMessage(`"atx"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	result, err = svc.ConfirmPreferences(ctx, []string{"owner-1", "owner-2"}, "session-1", "req-3",
		PreferenceConfirmInput{ExpectedRevision: 2, Subject: "self", MemoryIDs: []string{itx.ID, atx.ID}})
	if err != nil || len(result.Applied) != 1 || result.Skipped[0].Reason != PreferenceSkipDuplicated {
		t.Fatalf("同请求重复字段应去重: %+v err=%v", result, err)
	}

	// 删除重试:删除未实现请求级幂等,重试得到稳定 not found。
	if err := svc.DeletePreference(ctx, []string{"owner-1"}, itx.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePreference(ctx, []string{"owner-1"}, itx.ID); !errors.Is(err, store.ErrPreferenceMemoryNotFound) {
		t.Fatalf("重复删除应稳定 not found: %v", err)
	}
}

func TestSaveSessionPreferenceWhitelistAndScope(t *testing.T) {
	cases := []struct {
		name    string
		ops     string
		message string
		save    PreferenceSave
	}{
		{
			name:    "预算不在白名单",
			ops:     `[{"op":"set","field":"budget_cny","value":8000,"scope":"session","evidence":"stated","quote":"预算8000"}]`,
			message: "预算8000",
			save:    PreferenceSave{Field: "budget_cny", Subject: "self"},
		},
		{
			name:    "free 字段不在白名单",
			ops:     `[{"op":"set","field":"free.临时想要水冷","value":"yes","scope":"session","evidence":"stated","quote":"这次想要水冷"}]`,
			message: "这次想要水冷",
			save:    PreferenceSave{Field: "free.临时想要水冷", Subject: "self"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakePreferenceStore()
			st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
				RequirementState: sourceSessionState(t, "chat", "msg-1", tc.ops, tc.message)}
			addChatMessage(st, "user", tc.message)
			svc := newPreferenceTestService(t, st)
			_, err := svc.SaveSessionPreference(context.Background(), "owner-1", "session-1", tc.save)
			assertProblemCode(t, err, "preference_not_savable")
			if len(st.memories) != 0 {
				t.Fatal("白名单外不得写入")
			}
		})
	}
	t.Run("scope 缺失拒绝", func(t *testing.T) {
		st := newFakePreferenceStore()
		st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
			RequirementState: sourceSessionState(t, "chat", "msg-1",
				`[{"op":"set","field":"noise_pref","value":"silent","scope":"session","evidence":"stated","quote":"尽量安静"}]`, "尽量安静")}
		addChatMessage(st, "user", "尽量安静")
		var state schemas.RequirementState
		if err := json.Unmarshal(st.session.RequirementState, &state); err != nil {
			t.Fatal(err)
		}
		field := state.Fields["noise_pref"]
		field.Scope = ""
		state.Fields["noise_pref"] = field
		raw, _ := json.Marshal(state)
		st.session.RequirementState = raw
		svc := newPreferenceTestService(t, st)
		_, err := svc.SaveSessionPreference(context.Background(), "owner-1", "session-1",
			PreferenceSave{Field: "noise_pref", Subject: "self"})
		assertProblemCode(t, err, "preference_not_savable")
	})
}

func TestConfirmPreferencesIdempotencyKey(t *testing.T) {
	st := newFakePreferenceStore()
	st.session = store.WebSession{ID: "session-1", OwnerID: "owner-1",
		RequirementState: sourceSessionState(t, "chat", "msg-1",
			`[{"op":"set","field":"budget_cny","value":8000,"scope":"session","evidence":"stated","quote":"预算8000"}]`,
			"预算8000")}
	addChatMessage(st, "user", "预算8000")
	svc := newPreferenceTestService(t, st)
	ctx := context.Background()
	noise := mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "noise_pref",
		Value: json.RawMessage(`"silent"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})
	other := mustCreateMemory(t, st, schemas.PreferenceMemory{OwnerID: "owner-1", Subject: "self", Field: "brand_pref.gpu",
		Value: json.RawMessage(`"amd"`), Strength: schemas.PreferenceStrengthPrefer, Evidence: schemas.PreferenceEvidenceStated, Source: seededSource("s0")})

	// 首次成功:同键重放返回一致结果(applied 为空,按已生效跳过),不重复写入。
	first, err := svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "key-1",
		PreferenceConfirmInput{ExpectedRevision: 1, Subject: "self", MemoryIDs: []string{noise.ID}})
	if err != nil || len(first.Applied) != 1 {
		t.Fatalf("首次确认应写入: %+v err=%v", first, err)
	}
	retry, err := svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "key-1",
		PreferenceConfirmInput{ExpectedRevision: 2, Subject: "self", MemoryIDs: []string{noise.ID}})
	if err != nil || len(retry.Applied) != 0 || len(retry.Skipped) != 1 || retry.Skipped[0].Reason != PreferenceSkipAlreadySet {
		t.Fatalf("同键重放应按已生效跳过: %+v err=%v", retry, err)
	}

	// 同键不同请求(首次已成功):稳定冲突,不落任何变更。
	_, err = svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "key-1",
		PreferenceConfirmInput{ExpectedRevision: 2, Subject: "self", MemoryIDs: []string{other.ID}})
	if !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("同键不同请求应稳定冲突: %v", err)
	}
	state, err := schemas.DecodeRequirementState(st.session.RequirementState)
	if err != nil || state.Fields["brand_pref.gpu"].Status == "active" {
		t.Fatalf("冲突路径不得写入: %v", err)
	}

	// 首次失败(revision 冲突)不落指纹:同键重试用正确修订号即可成功。
	if _, err := svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "key-2",
		PreferenceConfirmInput{ExpectedRevision: 99, Subject: "self", MemoryIDs: []string{other.ID}}); !errors.Is(err, store.ErrRequirementRevision) {
		t.Fatalf("错误修订号应失败: %v", err)
	}
	retried, err := svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "key-2",
		PreferenceConfirmInput{ExpectedRevision: 2, Subject: "self", MemoryIDs: []string{other.ID}})
	if err != nil || len(retried.Applied) != 1 {
		t.Fatalf("失败后的同键重试应成功: %+v err=%v", retried, err)
	}

	// 同键不同请求但本次已无可写入项:不比对指纹、不产生任何写入,
	// 以 200 + 逐项 skipped 返回(合同:冲突只在会产生写入时拦截)。
	before, err := schemas.DecodeRequirementState(st.session.RequirementState)
	if err != nil {
		t.Fatal(err)
	}
	benign, err := svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "key-2",
		PreferenceConfirmInput{ExpectedRevision: 2, Subject: "self", MemoryIDs: []string{noise.ID, other.ID}})
	if err != nil || len(benign.Applied) != 0 || len(benign.Skipped) != 2 {
		t.Fatalf("无可写入项的同键不同请求应 200 跳过: %+v err=%v", benign, err)
	}
	for _, skip := range benign.Skipped {
		if skip.Reason != PreferenceSkipAlreadySet {
			t.Fatalf("应全部按已生效跳过: %+v", benign.Skipped)
		}
	}
	after, err := schemas.DecodeRequirementState(st.session.RequirementState)
	if err != nil || before.Revision != after.Revision ||
		string(before.Fields["noise_pref"].Value) != string(after.Fields["noise_pref"].Value) {
		t.Fatalf("该路径不得产生任何写入: %v", err)
	}

	// 重放且字段此后被撤销:同键同内容(含原修订号)重放不重复写入(撤销保持),
	// 按 duplicated 跳过;修订号变化即构成不同请求,走稳定冲突。
	if _, err := svc.EditRequirement(ctx, "owner-1", "session-1", "remove-1", RequirementEdit{
		ExpectedRevision: 3,
		Operations:       []schemas.RequirementOperation{{Op: "remove", Field: "noise_pref"}},
	}); err != nil {
		t.Fatal(err)
	}
	replay, err := svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "key-1",
		PreferenceConfirmInput{ExpectedRevision: 1, Subject: "self", MemoryIDs: []string{noise.ID}})
	if err != nil || len(replay.Applied) != 0 || replay.Skipped[0].Reason != PreferenceSkipDuplicated {
		t.Fatalf("重放且字段已撤销应按 duplicated 跳过: %+v err=%v", replay, err)
	}
	state, err = schemas.DecodeRequirementState(st.session.RequirementState)
	if err != nil || state.Fields["noise_pref"].Status != "removed" {
		t.Fatalf("重放不得复活已撤销字段: %v %+v", err, state.Fields["noise_pref"])
	}
	_, err = svc.ConfirmPreferences(ctx, []string{"owner-1"}, "session-1", "key-1",
		PreferenceConfirmInput{ExpectedRevision: 4, Subject: "self", MemoryIDs: []string{noise.ID}})
	if !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("同键不同内容(修订号变化)应稳定冲突: %v", err)
	}
}
