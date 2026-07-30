package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// P4 版本表读写集成测试(PG_TEST_DSN 未设置时跳过,见 store_test.go 顶部说明)。

// saveVersion 便捷落一版,失败即 fatal。
func saveVersion(t *testing.T, s *Store, p SaveBuildVersionParams) SavedBuild {
	t.Helper()
	got, err := s.SaveBuildVersion(context.Background(), p)
	if err != nil {
		t.Fatalf("SaveBuildVersion 失败: %v", err)
	}
	return got
}

func TestSaveBuildVersion_TreeAndReads(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	const sess = "sess_p4_tree"

	spec1 := json.RawMessage(`{"budget_cny": 8000}`)
	draft := json.RawMessage(`{"build_ref": "b1"}`)
	validation := json.RawMessage(`{"overall_status": "pass"}`)
	quote := json.RawMessage(`{"snapshot_date": "2026-07-28", "total_cny": "7791.00"}`)

	// v1:新需求单,无父版本。
	v1 := saveVersion(t, s, SaveBuildVersionParams{
		SessionID: sess, RequirementSpec: spec1,
		Draft: draft, Validation: validation, Quote: quote,
	})
	if v1.Version != 1 {
		t.Fatalf("首版应为 v1,得到 v%d", v1.Version)
	}

	// v2:降 500 → 新需求单 + change,父为 v1。
	spec2 := json.RawMessage(`{"budget_cny": 7500}`)
	change2 := json.RawMessage(`{"intent": "adjust_budget", "budget_delta_cny": -500}`)
	v2 := saveVersion(t, s, SaveBuildVersionParams{
		SessionID: sess, ParentID: &v1.ID, RequirementSpec: spec2, Change: change2,
		Draft: draft, Validation: validation, Quote: quote,
	})
	if v2.Version != 2 {
		t.Fatalf("第二版应为 v2,得到 v%d", v2.Version)
	}
	if v2.RequirementID == v1.RequirementID {
		t.Fatalf("v2 应插入新需求单,却复用了 v1 的 requirement_id %d", v1.RequirementID)
	}

	// v3:换 A 卡 → 复用 v2 需求单,父为 v2。
	change3 := json.RawMessage(`{"intent": "swap_part", "swap": {"category": "gpu"}}`)
	v3 := saveVersion(t, s, SaveBuildVersionParams{
		SessionID: sess, ParentID: &v2.ID, RequirementID: &v2.RequirementID, Change: change3,
		Draft: draft, Validation: validation, Quote: quote,
	})
	if v3.Version != 3 || v3.RequirementID != v2.RequirementID {
		t.Fatalf("v3 应复用 v2 需求单,得到 %+v", v3)
	}

	// 版本树回放:三版本按序,parent 链正确,change 原文保真。
	tree, err := s.BuildsBySession(ctx, sess)
	if err != nil {
		t.Fatalf("BuildsBySession 失败: %v", err)
	}
	if len(tree) != 3 {
		t.Fatalf("应有 3 个版本,得到 %d", len(tree))
	}
	if tree[0].ParentID != nil || tree[0].Change != nil {
		t.Fatalf("v1 应无父版本、无 change: %+v", tree[0])
	}
	if tree[1].ParentID == nil || *tree[1].ParentID != v1.ID {
		t.Fatalf("v2 父版本应为 v1(id=%d): %+v", v1.ID, tree[1])
	}
	if tree[2].ParentID == nil || *tree[2].ParentID != v2.ID {
		t.Fatalf("v3 父版本应为 v2(id=%d): %+v", v2.ID, tree[2])
	}

	// 单版本读取 + JSONB 原文往返(键序无关比较)。
	got, err := s.BuildByVersion(ctx, sess, 2)
	if err != nil {
		t.Fatalf("BuildByVersion 失败: %v", err)
	}
	assertJSONEqual(t, "v2.change", got.Change, change2)
	assertJSONEqual(t, "v2.quote", got.Quote, quote)

	// 需求单原文读取。
	specGot, err := s.RequirementSpecByID(ctx, v2.RequirementID)
	if err != nil {
		t.Fatalf("RequirementSpecByID 失败: %v", err)
	}
	assertJSONEqual(t, "v2.spec", specGot, spec2)

	// 会话摘要。
	sums, err := s.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions 失败: %v", err)
	}
	if len(sums) != 1 || sums[0].SessionID != sess || sums[0].VersionCount != 3 || sums[0].LatestVersion != 3 {
		t.Fatalf("会话摘要不符: %+v", sums)
	}
}

func TestSaveBuildVersion_Errors(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	draft := json.RawMessage(`{}`)
	ok := json.RawMessage(`{}`)

	// 需求单入参二选一都没给。
	_, err := s.SaveBuildVersion(ctx, SaveBuildVersionParams{
		SessionID: "sess_e", Draft: draft, Validation: ok, Quote: ok,
	})
	if err == nil {
		t.Fatalf("缺需求单入参应报错")
	}

	// 父版本跨会话。
	v1 := saveVersion(t, s, SaveBuildVersionParams{
		SessionID: "sess_a", RequirementSpec: ok, Draft: draft, Validation: ok, Quote: ok,
	})
	_, err = s.SaveBuildVersion(ctx, SaveBuildVersionParams{
		SessionID: "sess_b", ParentID: &v1.ID, RequirementSpec: ok,
		Draft: draft, Validation: ok, Quote: ok,
	})
	if err == nil {
		t.Fatalf("父版本跨会话应报错")
	}

	// 父版本不存在。
	missing := int64(999999)
	_, err = s.SaveBuildVersion(ctx, SaveBuildVersionParams{
		SessionID: "sess_a", ParentID: &missing, RequirementSpec: ok,
		Draft: draft, Validation: ok, Quote: ok,
	})
	if !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("父版本不存在应返回 ErrBuildNotFound,得到: %v", err)
	}

	// 版本不存在读取。
	_, err = s.BuildByVersion(ctx, "sess_a", 99)
	if !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("BuildByVersion 未命中应返回 ErrBuildNotFound,得到: %v", err)
	}
}

// assertJSONEqual JSONB 存取会重排键序,按解码后语义比较。
func assertJSONEqual(t *testing.T, field string, got, want json.RawMessage) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s 实际值非法 JSON: %v", field, err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("%s 期望值非法 JSON: %v", field, err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("%s 不符:\n实际: %s\n期望: %s", field, gb, wb)
	}
}
