package product

// 三轴派生的表驱动测试:覆盖 change 文档"典型组合必须可表达"的全部场景,
// 以及 running 优先、失败与 modified 并存、运行中编辑后落 outdated 等规则。
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func axesState(t *testing.T) (schemas.RequirementState, string, json.RawMessage) {
	t.Helper()
	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`8000`), Strength: "must"},
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must"},
		{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"2K"`), Kind: "fact", Strength: "must"},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Kind: "fact", Strength: "must"},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算8000，2K 游戏，全部新买"})
	if err != nil {
		t.Fatal(err)
	}
	spec, readiness, err := schemas.RequirementReviewSpec(state)
	if err != nil || !readiness.ConfirmationEligible {
		t.Fatalf("夹具应可确认: %+v %v", readiness, err)
	}
	hash, err := schemas.CanonicalHash(spec)
	if err != nil {
		t.Fatal(err)
	}
	return state, hash, spec
}

func axesConfirmation(id, hash string, revision int, spec json.RawMessage) store.RequirementConfirmation {
	return store.RequirementConfirmation{ID: id, ReviewHash: hash, Revision: revision, RequirementSpec: spec, CreatedAt: time.Now()}
}

func TestRequirementAxesScenarios(t *testing.T) {
	state, hash, spec := axesState(t)
	stateJSON, _ := json.Marshal(state)

	session := store.WebSession{ID: "s", RequirementState: stateJSON}
	conf := axesConfirmation("conf-1", hash, state.Revision, spec)

	edited, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`), Strength: "must"},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算改成9000"})
	if err != nil {
		t.Fatal(err)
	}
	editedJSON, _ := json.Marshal(edited)
	editedSession := store.WebSession{ID: "s", RequirementState: editedJSON}

	incomplete, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "remove", Field: "budget_cny"},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算先不定"})
	if err != nil {
		t.Fatal(err)
	}
	incompleteJSON, _ := json.Marshal(incomplete)
	incompleteSession := store.WebSession{ID: "s", RequirementState: incompleteJSON}

	buildRun := func(id, confID, hash string, status store.RunStatus, version int) store.BuildRun {
		return store.BuildRun{AgentRun: store.AgentRun{ID: id, Kind: store.RunBuild, Status: status},
			ConfirmationID: confID, ReviewHash: hash, BuilderInputHash: "bh-" + id, Version: version}
	}

	emptySession := store.WebSession{ID: "s", RequirementState: marshalRaw(t, schemas.NewRequirementState())}

	cases := []struct {
		name          string
		ws            store.WebSession
		conf          *store.RequirementConfirmation
		runs          []store.BuildRun
		active        *store.AgentRun
		wantConfirm   ConfirmationStatus
		wantBuild     BuildRelationStatus
		wantBuildVer  *int
		wantReadiness string
		wantRetry     string
		wantDiff      []schemas.RequirementReviewDiffEntry
	}{
		{"新会话信息不足", emptySession, nil, nil, nil, ConfirmationUnconfirmed, BuildNone, nil, "incomplete", "", nil},
		{"最低条件齐全", session, nil, nil, nil, ConfirmationUnconfirmed, BuildNone, nil, "ready", "", nil},
		{"已确认并生成中", session, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunRunning, 0)},
			&store.AgentRun{ID: "r1", Kind: store.RunBuild, Status: store.RunRunning},
			ConfirmationConfirmed, BuildRunning, nil, "ready", "", nil},
		{"生成完成", session, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunSucceeded, 1)},
			nil, ConfirmationConfirmed, BuildCurrent, intPtr(1), "ready", "", nil},
		{"成功配置后修改可选偏好", editedSession, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunSucceeded, 1)},
			nil, ConfirmationModified, BuildOutdated, intPtr(1), "ready", "",
			[]schemas.RequirementReviewDiffEntry{{Field: "budget_cny", Before: json.RawMessage(`8000`), After: json.RawMessage(`9000`)}}},
		{"成功配置后删除预算", incompleteSession, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunSucceeded, 1)},
			nil, ConfirmationModified, BuildOutdated, intPtr(1), "incomplete", "", nil},
		{"修改后恢复为确认值", session, &conf, []store.BuildRun{buildRun("r2", "conf-1", hash, store.RunSucceeded, 1)},
			nil, ConfirmationConfirmed, BuildCurrent, intPtr(1), "ready", "", nil},
		{"Builder 失败给出重试目标", session, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunFailed, 0)},
			nil, ConfirmationConfirmed, BuildFailed, nil, "ready", "r1", nil},
		{"失败但保留历史成功配置", session, &conf, []store.BuildRun{
			buildRun("r2", "conf-1", hash, store.RunFailed, 0),
			buildRun("r1", "conf-1", hash, store.RunSucceeded, 1)},
			nil, ConfirmationConfirmed, BuildFailed, intPtr(1), "ready", "r2", nil},
		{"取消中断不提供重试目标", session, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunInterrupted, 0)},
			nil, ConfirmationConfirmed, BuildFailed, nil, "ready", "", nil},
		{"失败后草稿已修改不提供重试目标", editedSession, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunFailed, 0)},
			nil, ConfirmationModified, BuildFailed, nil, "ready", "",
			[]schemas.RequirementReviewDiffEntry{{Field: "budget_cny", Before: json.RawMessage(`8000`), After: json.RawMessage(`9000`)}}},
		{"运行中编辑后完成落 outdated", editedSession, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunSucceeded, 1)},
			nil, ConfirmationModified, BuildOutdated, intPtr(1), "ready", "",
			[]schemas.RequirementReviewDiffEntry{{Field: "budget_cny", Before: json.RawMessage(`8000`), After: json.RawMessage(`9000`)}}},
		{"草稿改回原确认值恢复 current", session, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunSucceeded, 2)},
			nil, ConfirmationConfirmed, BuildCurrent, intPtr(2), "ready", "", nil},
		{"活动 screening 不是活动 Builder", session, &conf, []store.BuildRun{buildRun("r1", "conf-1", hash, store.RunSucceeded, 1)},
			&store.AgentRun{ID: "sr", Kind: store.RunScreening, Status: store.RunRunning},
			ConfirmationConfirmed, BuildCurrent, intPtr(1), "ready", "", nil},
		{"确认了新目标但尚未生成", session, &conf, []store.BuildRun{buildRun("r1", "conf-old", "old-hash", store.RunSucceeded, 1)},
			nil, ConfirmationConfirmed, BuildOutdated, intPtr(1), "ready", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hasConf := tc.conf != nil
			var conf store.RequirementConfirmation
			if hasConf {
				conf = *tc.conf
			}
			axes, err := requirementAxes(tc.ws, conf, hasConf, tc.runs, tc.active)
			if err != nil {
				t.Fatal(err)
			}
			if axes.Confirmation.Status != tc.wantConfirm {
				t.Errorf("confirmation=%s want %s", axes.Confirmation.Status, tc.wantConfirm)
			}
			if axes.Build.Status != tc.wantBuild {
				t.Errorf("build=%s want %s", axes.Build.Status, tc.wantBuild)
			}
			if tc.wantBuildVer == nil && axes.Build.Version != nil || tc.wantBuildVer != nil && (axes.Build.Version == nil || *axes.Build.Version != *tc.wantBuildVer) {
				t.Errorf("build version=%v want %v", axes.Build.Version, tc.wantBuildVer)
			}
			if axes.Readiness == nil || axes.Readiness.Status != tc.wantReadiness {
				t.Errorf("readiness=%+v want status %s", axes.Readiness, tc.wantReadiness)
			}
			if axes.Build.RetryRunID != tc.wantRetry {
				t.Errorf("retry_run_id=%q want %q", axes.Build.RetryRunID, tc.wantRetry)
			}
			// review_diff 只在已有快照且当前预览可投影时提供。
			if hasConf && tc.wantReadiness == "ready" {
				if tc.wantDiff == nil && len(axes.Confirmation.ReviewDiff) != 0 {
					t.Errorf("review_diff=%+v want 无差异", axes.Confirmation.ReviewDiff)
				}
				for _, want := range tc.wantDiff {
					found := false
					for _, got := range axes.Confirmation.ReviewDiff {
						found = found || (got.Field == want.Field && string(got.Before) == string(want.Before) && string(got.After) == string(want.After))
					}
					if !found {
						t.Errorf("review_diff 缺少 %+v,实际 %+v", want, axes.Confirmation.ReviewDiff)
					}
				}
			} else if axes.Confirmation.ReviewDiff != nil {
				t.Errorf("不可比较时 review_diff 应为 nil,实际 %+v", axes.Confirmation.ReviewDiff)
			}
			// review_spec/review_hash 只在可投影时出现。
			if tc.wantReadiness == "ready" && axes.ReviewHash == "" {
				t.Error("ready 会话必须有核定预览 hash")
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func marshalRaw(t *testing.T, state schemas.RequirementState) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// BudgetCeilingCNY 展示计算:预算 ×(1+弹性)。
func TestBudgetCeilingDisplay(t *testing.T) {
	state, _, _ := axesState(t)
	spec, _, err := schemas.RequirementReviewSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	ceiling := budgetCeilingCNY(spec)
	if ceiling == nil || *ceiling != 8800 { // 8000 × 1.1
		t.Fatalf("预算上限展示值=%v want 8800", ceiling)
	}
}
