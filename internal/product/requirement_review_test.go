package product

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// This store extends the existing service fake only with persistence of the new
// requirement state; no model or database is used.
type requirementReviewStore struct{ *fakeProductStore }

func (f requirementReviewStore) CompleteRun(ctx context.Context, p store.CompleteRunParams) (*store.WebMessage, error) {
	message, err := f.fakeProductStore.CompleteRun(ctx, p)
	if err == nil && p.SetRequirementState {
		f.mu.Lock()
		f.session.RequirementState = append(json.RawMessage(nil), p.RequirementState...)
		f.mu.Unlock()
	}
	return message, err
}

func newRequirementReviewService(t *testing.T) (*Service, requirementReviewStore, schemas.RequirementState) {
	t.Helper()
	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`8000`), Strength: "must"},
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"general"`), Strength: "must"},
		{Op: "set", Field: "brand_pref.gpu", Value: json.RawMessage(`"nvidia"`), Strength: "must"},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Strength: "must"},
	}}, schemas.RequirementSource{Kind: "edit", MessageID: "setup", Quote: "预算8000，办公，显卡必须英伟达，配件全部新买"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	pending, _, err := schemas.RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	st := requirementReviewStore{newFakeProductStore()}
	st.session.RequirementState, st.session.PendingRequirement = raw, pending
	st.session.Phase = store.PhaseRequirementReady
	svc, err := NewService(context.Background(), st, &fakeAgent{store: st.fakeProductStore}, newFakeSink())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })
	return svc, st, state
}

func TestRequirementEditSourceDoesNotCallInheritedMustAPreference(t *testing.T) {
	svc, _, state := newRequirementReviewService(t)
	detail, err := svc.EditRequirement(context.Background(), "owner-1", "session-1", "source-review", RequirementEdit{
		ExpectedRevision: state.Revision,
		Operations:       []schemas.RequirementOperation{{Op: "set", Field: "brand_pref.gpu", Value: json.RawMessage(`"amd"`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var updated schemas.RequirementState
	_ = json.Unmarshal(detail.Session.RequirementState, &updated)
	field := updated.Fields["brand_pref.gpu"]
	if field.Strength != "must" {
		t.Fatalf("editing the value weakened the inherited must: %+v", field)
	}
	if field.Source == nil || strings.Contains(field.Source.Quote, "尽量满足") {
		t.Fatalf("source describes a soft preference while state requires must: %+v", field.Source)
	}
}

func TestRequirementEditSourceDistinguishesFactsFromConditions(t *testing.T) {
	for _, kind := range []string{"fact", "context", "constraint"} {
		text := requirementEditText([]schemas.RequirementOperation{{Op: "set", Field: "notes", Value: json.RawMessage(`"旅行素材"`), Kind: kind, Strength: "must"}})
		if strings.Contains(text, "必须满足") != (kind == "constraint") {
			t.Fatalf("source misrepresents information kind %s: %s", kind, text)
		}
	}
}

func TestRequirementEditIdempotencyDistinguishesAlternativeFromAdoption(t *testing.T) {
	svc, _, state := newRequirementReviewService(t)
	edit := RequirementEdit{ExpectedRevision: state.Revision, Operations: []schemas.RequirementOperation{{Op: "alternative", Field: "brand_pref.gpu", Value: json.RawMessage(`"amd"`), Strength: "prefer"}}}
	if _, err := svc.EditRequirement(context.Background(), "owner-1", "session-1", "idempotency-review", edit); err != nil {
		t.Fatal(err)
	}
	edit.Operations[0].Op = "set"
	if _, err := svc.EditRequirement(context.Background(), "owner-1", "session-1", "idempotency-review", edit); err == nil {
		t.Fatal("same key with a different operation was silently reported as success")
	}
}

// 同键同请求重放必须返回原始 run,不能用变化后的草稿重新校验或生成新载荷:
// 先真实确认一次,再把草稿改成 incomplete,重放仍命中幂等。
func TestConfirmReplayUsesOriginalRunWhenCurrentDraftIsIncomplete(t *testing.T) {
	svc, st, state := newRequirementReviewService(t)
	detail, err := svc.GetSession(context.Background(), "owner-1", "session-1")
	if err != nil || detail.Axes.ReviewHash == "" {
		t.Fatalf("核定预览不可用: %v", err)
	}
	requestID := "confirm-key"
	result, err := svc.StartConfirm(context.Background(), "owner-1", "session-1", requestID, ConfirmRequest{
		ExpectedRevision: state.Revision, ExpectedReviewHash: detail.Axes.ReviewHash})
	if err != nil || result.Duplicate || result.Run.ID == "" {
		t.Fatalf("首次确认应创建 run: %+v err=%v", result, err)
	}
	updated, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{{Op: "remove", Field: "budget_cny"}}}, schemas.RequirementSource{Kind: "edit", MessageID: "later", Quote: "预算还没确定"})
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.session.RequirementState, _ = json.Marshal(updated)
	st.session.PendingRequirement = nil
	st.session.Phase = store.PhaseCollecting
	st.mu.Unlock()
	replay, err := svc.StartConfirm(context.Background(), "owner-1", "session-1", requestID, ConfirmRequest{
		ExpectedRevision: state.Revision, ExpectedReviewHash: detail.Axes.ReviewHash})
	if err != nil || !replay.Duplicate || replay.Run.ID != result.Run.ID {
		t.Fatalf("completed confirmation replay was blocked by a newer draft: %+v err=%v", replay, err)
	}
}

func TestLegacySessionRejectsIncrementalEditInsteadOfStartingAnEmptyMemory(t *testing.T) {
	svc, st, _ := newRequirementReviewService(t)
	st.session.RequirementState = nil
	original := append(json.RawMessage(nil), st.session.PendingRequirement...)
	_, err := svc.EditRequirement(context.Background(), "owner-1", "session-1", "legacy-review", RequirementEdit{ExpectedRevision: 0, Operations: []schemas.RequirementOperation{{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`)}}})
	if err == nil {
		t.Fatal("legacy session accepted an incremental edit without sourced state and discarded its other requirements")
	}
	if len(st.session.RequirementState) != 0 || !sameRequirementJSON(st.session.PendingRequirement, original) {
		t.Fatal("rejected legacy edit changed the pending requirement or enabled inferred memory")
	}
}

func TestRequirementReplacementOnlyChangedValuePreservesMustAndUnknownFields(t *testing.T) {
	svc, st, state := newRequirementReviewService(t)
	var replacement map[string]json.RawMessage
	_ = json.Unmarshal(st.session.PendingRequirement, &replacement)
	replacement["budget_cny"] = json.RawMessage(`9000`)
	raw, _ := json.Marshal(replacement)
	if err := svc.ReplaceRequirement(context.Background(), "owner-1", "session-1", raw); err != nil {
		t.Fatal(err)
	}
	var updated schemas.RequirementState
	_ = json.Unmarshal(st.session.RequirementState, &updated)
	if updated.Fields["brand_pref.gpu"].Strength != "must" || updated.Fields["noise_pref"].Status != "unknown" || updated.Fields["budget_flex"].Status != "unknown" {
		t.Fatalf("unmodified fields changed through the compatibility form: %+v", updated.Fields)
	}
	if updated.Revision != state.Revision+1 || len(updated.Changes) != 1 || updated.Changes[0].Field != "budget_cny" {
		t.Fatalf("compatibility form generated unrelated changes: %+v", updated.Changes)
	}
}
