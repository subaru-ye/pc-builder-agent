package store

// 偏好记忆存储的针对性集成测试:走 setupStore 的临时库 + 全量迁移模式,
// 覆盖归属隔离、本人/代配对象隔离、改主意 supersede 链、撤销与删除语义、易失召回边界。
import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func preferenceFixture(ownerID, subject, field, value string) schemas.PreferenceMemory {
	return schemas.PreferenceMemory{
		OwnerID:  ownerID,
		Subject:  subject,
		Field:    field,
		Value:    json.RawMessage(value),
		Strength: schemas.PreferenceStrengthPrefer,
		Evidence: schemas.PreferenceEvidenceStated,
		Source:   schemas.PreferenceSource{Kind: "chat", SessionID: "sess-1", MessageID: "msg-1", Quote: "用户原话"},
	}
}

func TestPreferenceMemoryLifecycle(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	created, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"nvidia"`))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != schemas.PreferenceStatusActive || created.CreatedAt.IsZero() {
		t.Fatalf("服务端应生成 id/status/时间戳: %+v", created)
	}
	if created.Source.Quote != "用户原话" || created.Source.Kind != "chat" {
		t.Fatalf("来源应完整回读: %+v", created.Source)
	}

	recall, err := s.ActivePreferenceMemories(ctx, "owner-a", schemas.PreferenceSubjectSelf)
	if err != nil || len(recall) != 1 || recall[0].ID != created.ID {
		t.Fatalf("写入后应可召回: %v %+v", err, recall)
	}

	// 改主意:同字段换值 → 旧记录转 superseded 不再召回,新记录回指 supersedes。
	updated, err := s.SupersedePreferenceMemory(ctx, "owner-a", created.ID,
		preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"amd"`))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Supersedes != created.ID {
		t.Fatalf("新记录应回指前驱: %+v", updated)
	}
	var prevStatus, prevSupersedes string
	if err := s.pool.QueryRow(ctx,
		`SELECT status::text, COALESCE(supersedes::text,'') FROM owner_preference_memories WHERE id = $1`, created.ID).
		Scan(&prevStatus, &prevSupersedes); err != nil || prevStatus != schemas.PreferenceStatusSupersede {
		t.Fatalf("原记录应墓碑化: %v %s", err, prevStatus)
	}
	recall, err = s.ActivePreferenceMemories(ctx, "owner-a", schemas.PreferenceSubjectSelf)
	if err != nil || len(recall) != 1 || recall[0].ID != updated.ID {
		t.Fatalf("召回应只返回新 active: %v %+v", err, recall)
	}

	// 重申相同值不是改主意。
	if _, err := s.SupersedePreferenceMemory(ctx, "owner-a", updated.ID,
		preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", ` "amd" `)); !errors.Is(err, ErrPreferenceUnchanged) {
		t.Fatalf("重申相同值应返回 ErrPreferenceUnchanged: %v", err)
	}

	// 撤销:墓碑保留,不再召回;同身份可重新建立偏好(唯一索引只约束 active)。
	if err := s.RetractPreferenceMemory(ctx, "owner-a", updated.ID); err != nil {
		t.Fatal(err)
	}
	if recall, err = s.ActivePreferenceMemories(ctx, "owner-a", schemas.PreferenceSubjectSelf); err != nil || len(recall) != 0 {
		t.Fatalf("撤销后不应召回: %v %+v", err, recall)
	}
	if err := s.RetractPreferenceMemory(ctx, "owner-a", updated.ID); !errors.Is(err, ErrPreferenceMemoryNotActive) {
		t.Fatalf("重复撤销应返回 ErrPreferenceMemoryNotActive: %v", err)
	}
	recreated, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"nvidia"`))
	if err != nil {
		t.Fatalf("撤销后重建同身份偏好应成功: %v", err)
	}

	// 删除:同一偏好的旧值、墓碑及来源原话一并物理删除。
	if err := s.DeletePreferenceMemory(ctx, "owner-a", recreated.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM owner_preference_memories WHERE owner_id = $1 AND subject = $2 AND field = $3`,
		"owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu").Scan(&count); err != nil || count != 0 {
		t.Fatalf("删除必须清除同一偏好的全部历史: %v count=%d", err, count)
	}
	if err := s.DeletePreferenceMemory(ctx, "owner-a", recreated.ID); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("重复删除应返回 ErrPreferenceMemoryNotFound: %v", err)
	}
	if err := s.DeletePreferenceMemory(ctx, "owner-a", updated.ID); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("旧墓碑也应已删除: %v", err)
	}
}

func TestPreferenceMemoryIsolation(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	self, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "noise_pref", `"silent"`))
	if err != nil {
		t.Fatal(err)
	}
	friend, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", "friend:xiaowang", "size_pref", `"itx"`))
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-b", schemas.PreferenceSubjectSelf, "noise_pref", `"quiet"`))
	if err != nil {
		t.Fatal(err)
	}

	assertRecall := func(ownerID, subject string, wantIDs ...string) {
		t.Helper()
		got, err := s.ActivePreferenceMemories(ctx, ownerID, subject)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, m := range got {
			ids[m.ID] = true
		}
		if len(ids) != len(wantIDs) {
			t.Fatalf("%s/%s 召回数错误: %+v", ownerID, subject, got)
		}
		for _, id := range wantIDs {
			if !ids[id] {
				t.Fatalf("%s/%s 召回应含 %s: %+v", ownerID, subject, id, got)
			}
		}
	}
	// 本人/代配对象/他人三条线互不可见:owner-b 只能看到自己的记录。
	assertRecall("owner-a", schemas.PreferenceSubjectSelf, self.ID)
	assertRecall("owner-a", "friend:xiaowang", friend.ID)
	assertRecall("owner-b", schemas.PreferenceSubjectSelf, other.ID)

	// 跨 owner 的变更一律 not found,不泄露存在性。
	if _, err := s.SupersedePreferenceMemory(ctx, "owner-b", self.ID,
		preferenceFixture("owner-b", schemas.PreferenceSubjectSelf, "noise_pref", `"silent"`)); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("跨 owner supersede 应 not found: %v", err)
	}
	if err := s.RetractPreferenceMemory(ctx, "owner-b", self.ID); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("跨 owner 撤销应 not found: %v", err)
	}
	if err := s.DeletePreferenceMemory(ctx, "owner-b", self.ID); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("跨 owner 删除应 not found: %v", err)
	}
	if _, err := s.ActivePreferenceMemories(ctx, "owner-a", ""); !errors.Is(err, schemas.ErrPreferenceMemoryInvalid) {
		t.Fatalf("召回必须显式传 subject: %v", err)
	}
}

func TestPreferenceMemorySupersedeValidation(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	created, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"nvidia"`))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SupersedePreferenceMemory(ctx, "owner-a", created.ID,
		preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "noise_pref", `"silent"`)); !errors.Is(err, schemas.ErrPreferenceMemoryInvalid) {
		t.Fatalf("跨 field 的 supersede 应拒绝: %v", err)
	}
	friendUpdate := preferenceFixture("owner-a", "friend:xiaowang", "brand_pref.gpu", `"nvidia"`)
	if _, err := s.SupersedePreferenceMemory(ctx, "owner-a", created.ID, friendUpdate); !errors.Is(err, schemas.ErrPreferenceMemoryInvalid) {
		t.Fatalf("跨 subject 的 supersede 应拒绝: %v", err)
	}
	badEvidence := preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"amd"`)
	badEvidence.Evidence = "inferred"
	if _, err := s.SupersedePreferenceMemory(ctx, "owner-a", created.ID, badEvidence); !errors.Is(err, schemas.ErrPreferenceMemoryInvalid) {
		t.Fatalf("supersede 目标同样要过校验: %v", err)
	}
	if _, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"nvidia"`)); err == nil {
		t.Fatal("同身份重复 active 应被部分唯一索引拒绝")
	}

	// store 层不做时效过滤:易失记录原样返回,过期判定由 FilterPreferenceSuggestions 负责。
	volatile := preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "budget_basis", `"promotional_price"`)
	volatile.Volatile = true
	volatile.ObservedAt = "2026-01-01"
	if _, err := s.CreatePreferenceMemory(ctx, volatile); err != nil {
		t.Fatal(err)
	}
	recall, err := s.ActivePreferenceMemories(ctx, "owner-a", schemas.PreferenceSubjectSelf)
	if err != nil || len(recall) != 2 {
		t.Fatalf("store 召回应原样返回易失记录: %v %+v", err, recall)
	}
	today := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	suggestions := schemas.FilterPreferenceSuggestions(recall, today, 7*24*time.Hour)
	if len(suggestions) != 1 || suggestions[0].Field != "brand_pref.gpu" {
		t.Fatalf("过滤后应只剩稳定偏好,易失证据不得作为建议复用: %+v", suggestions)
	}
}

func TestActivePreferenceMemoryByIdentityAndOwnerList(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	first, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"nvidia"`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", "friend:xw", "noise_pref", `"silent"`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-b", schemas.PreferenceSubjectSelf, "noise_pref", `"quiet"`)); err != nil {
		t.Fatal(err)
	}

	byIdentity, err := s.ActivePreferenceMemoryByIdentity(ctx, "owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu")
	if err != nil || byIdentity.ID != first.ID {
		t.Fatalf("身份查询应命中: %+v err=%v", byIdentity, err)
	}
	if _, err := s.ActivePreferenceMemoryByIdentity(ctx, "owner-a", schemas.PreferenceSubjectSelf, "noise_pref"); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("身份未命中应 not found: %v", err)
	}

	list, err := s.ActivePreferenceMemoriesByOwner(ctx, "owner-a")
	if err != nil || len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("owner 列表应含全部 subject 且按 subject,field 排序: %+v err=%v", list, err)
	}
	if other, err := s.ActivePreferenceMemoriesByOwner(ctx, "owner-b"); err != nil || len(other) != 1 {
		t.Fatalf("owner 隔离: %+v err=%v", other, err)
	}

	// supersede 后身份查询只认 active。
	if _, err := s.SupersedePreferenceMemory(ctx, "owner-a", first.ID,
		preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"amd"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivePreferenceMemoryByIdentity(ctx, "owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu"); err != nil {
		t.Fatalf("supersede 后身份查询应命中新记录: %v", err)
	}
}

func TestWebMessageByIDScopedToSession(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	if _, err := s.CreateWebSession(ctx, "sess-1", "owner-a", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWebSession(ctx, "sess-2", "owner-a", "11111111-1111-1111-1111-111111111112"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO web_messages (id, session_id, role, content) VALUES ($1, $2, 'user', $3)`,
		"11111111-1111-1111-1111-111111111111", "sess-1", "预算8000，显卡要N卡"); err != nil {
		t.Fatal(err)
	}

	message, err := s.WebMessageByID(ctx, "sess-1", "11111111-1111-1111-1111-111111111111")
	if err != nil || message.Content != "预算8000，显卡要N卡" || message.Role != "user" {
		t.Fatalf("应按会话+ID 命中: %+v err=%v", message, err)
	}
	// 跨会话引用一律不存在,防止用别处消息伪造来源。
	if _, err := s.WebMessageByID(ctx, "sess-2", "11111111-1111-1111-1111-111111111111"); !errors.Is(err, ErrWebMessageNotFound) {
		t.Fatalf("跨会话引用应 not found: %v", err)
	}
}

func TestActivePreferenceMemoryForOwnersScoping(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	mine, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"nvidia"`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePreferenceMemory(ctx, preferenceFixture("owner-b", schemas.PreferenceSubjectSelf, "noise_pref", `"quiet"`)); err != nil {
		t.Fatal(err)
	}

	found, err := s.ActivePreferenceMemoryForOwners(ctx, []string{"owner-a", "owner-b"}, mine.ID)
	if err != nil || found.ID != mine.ID {
		t.Fatalf("多 owner 任一命中即可: %+v err=%v", found, err)
	}
	if _, err := s.ActivePreferenceMemoryForOwners(ctx, []string{"owner-b"}, mine.ID); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("不在 owners 内应 not found: %v", err)
	}
	if _, err := s.ActivePreferenceMemoryForOwners(ctx, []string{"owner-a", "owner-b"}, "11111111-1111-1111-1111-111111111111"); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("不存在应 not found: %v", err)
	}
	// superseded 墓碑不可被确认为 active。
	if _, err := s.SupersedePreferenceMemory(ctx, "owner-a", mine.ID,
		preferenceFixture("owner-a", schemas.PreferenceSubjectSelf, "brand_pref.gpu", `"amd"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivePreferenceMemoryForOwners(ctx, []string{"owner-a"}, mine.ID); !errors.Is(err, ErrPreferenceMemoryNotFound) {
		t.Fatalf("墓碑不可作为 active 读取: %v", err)
	}
}
