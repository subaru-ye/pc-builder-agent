package producthttp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestChatUpgradeContinuesIntoToolsWithoutReconfirmation(t *testing.T) {
	_, service, st := requirementIntegrationAPI(t, true)
	ctx := context.Background()
	owner := strings.Repeat("u", 43)
	ws, err := service.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	wait := func(start product.StartResult, err error) product.SessionDetail {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 300; i++ {
			r, e := service.GetRun(ctx, owner, start.Run.ID)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != store.RunRunning {
				if r.Status != store.RunSucceeded {
					t.Fatalf("run failed %s", r.Error)
				}
				d, e := service.GetSession(ctx, owner, ws.ID)
				if e != nil {
					t.Fatal(e)
				}
				return d
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("run timed out")
		return product.SessionDetail{}
	}
	message := func(text string) product.SessionDetail {
		t.Helper()
		return wait(service.StartMessage(ctx, owner, ws.ID, uuid.NewString(), text))
	}
	initial := message("预算7000，剪1080p多轨视频，不要求静音")
	if initial.Session.VersionCount != 0 || initial.Session.Phase != store.PhaseRequirementReady {
		t.Fatal("initial confirmation skipped")
	}
	// Even a mistaken model plan before the first confirmation cannot execute.
	initial = message("没有具体偏好，直接继续选配")
	if initial.Session.VersionCount != 0 || len(initial.Proposal) > 0 {
		t.Fatal("model skipped initial confirmation")
	}
	first := wait(service.StartConfirm(ctx, owner, ws.ID, uuid.NewString()))
	if first.Session.VersionCount != 1 {
		t.Fatalf("no baseline %s", first.Proposal)
	}
	v1, err := st.BuildByVersion(ctx, ws.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	text := "把处理器换更好的，预算还很充足啊，其他配件尽量不动"
	start, err := service.StartMessage(ctx, owner, ws.ID, requestID, text)
	upgraded := wait(start, err)
	if upgraded.Session.Phase != store.PhaseReady || upgraded.Session.VersionCount != 2 {
		t.Fatalf("chat did not deliver: %s", upgraded.Proposal)
	}
	run, err := service.GetRun(ctx, owner, start.Run.ID)
	if err != nil || run.Kind != store.RunChange {
		t.Fatalf("not one continued change run: %+v %v", run, err)
	}
	var result struct {
		Result      planning.Result
		Requirement schemas.PlanningInput
	}
	if err = json.Unmarshal(upgraded.Proposal, &result); err != nil {
		t.Fatal(err)
	}
	if result.Result.ToolCalls != 2 || result.Result.ModelCalls != 3 || result.Result.Delivery.Status != "delivered" || result.Result.Quote.TotalCNY != "4929.90" {
		t.Fatalf("not a priced tool-backed upgrade: %s", upgraded.Proposal)
	}
	if result.Requirement.Request == nil || result.Requirement.Request.Quote != text || result.Requirement.State.Fields["noise_pref"].Status != "removed" || result.Requirement.State.Fields["free.preserve_other_parts"].Strength != "prefer" || string(result.Requirement.State.Fields["budget_cny"].Value) != "7000" {
		t.Fatalf("lost execution request/requirements: %+v", result.Requirement)
	}
	v2, err := st.BuildByVersion(ctx, ws.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	oldDraft, err := schemas.DecodeBuildDraft(v1.Draft)
	if err != nil {
		t.Fatal(err)
	}
	newDraft, err := schemas.DecodeBuildDraft(v2.Draft)
	if err != nil {
		t.Fatal(err)
	}
	if oldDraft.Selection.CPU != "cpu-r5-5600" || newDraft.Selection.CPU != "cpu-r7-5700x" || v2.ParentID == nil || *v2.ParentID != v1.ID {
		t.Fatal("wrong upgrade or parent")
	}
	newDraft.Selection.CPU = oldDraft.Selection.CPU
	if !reflect.DeepEqual(newDraft.Selection, oldDraft.Selection) {
		t.Fatal("unnecessary part changes")
	}
	again, err := service.StartMessage(ctx, owner, ws.ID, requestID, text)
	if err != nil || !again.Duplicate || again.Run.ID != start.Run.ID {
		t.Fatal("chat retry not idempotent")
	}
	refreshed, err := service.GetSession(ctx, owner, ws.ID)
	if err != nil || refreshed.Session.VersionCount != 2 || string(refreshed.Proposal) != string(upgraded.Proposal) {
		t.Fatal("refresh/retry changed result")
	}
	old, err := st.BuildByVersion(ctx, ws.ID, 1)
	if err != nil || !reflect.DeepEqual(old, v1) {
		t.Fatal("immutable version overwritten")
	}
	discuss := message("如果换更好的CPU会怎样，先别执行")
	if discuss.Session.VersionCount != 2 || string(discuss.Proposal) != string(upgraded.Proposal) {
		t.Fatal("discussion triggered generation")
	}
	var state schemas.RequirementState
	_ = json.Unmarshal(discuss.Session.RequirementState, &state)
	if len(state.Alternatives) != 1 {
		t.Fatal("lost alternative")
	}
	record := message("预算先记8000，先不要重新生成")
	if record.Session.VersionCount != 2 || string(record.Proposal) != string(upgraded.Proposal) {
		t.Fatal("record-only request triggered generation")
	}
	_ = json.Unmarshal(record.Session.RequirementState, &state)
	if state.Fields["free.workload_resolution"].Status != "active" || state.Fields["noise_pref"].Status != "removed" {
		t.Fatal("lost unchanged/removed fields")
	}
	continued := message("没有具体偏好，直接继续选配")
	if continued.Session.VersionCount != 3 {
		t.Fatalf("continuation failed: %s", continued.Proposal)
	}
}
