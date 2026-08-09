package sharing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type fakeStore struct {
	record store.BuildShare
	last   store.CreateBuildShareParams
}

func (f *fakeStore) CreateBuildShare(_ context.Context, p store.CreateBuildShareParams) (store.BuildShare, bool, error) {
	f.last = p
	f.record.PublicID, f.record.SessionID, f.record.Version = p.PublicID, p.SessionID, p.Version
	f.record.ClientRequestID, f.record.TokenHash = p.ClientRequestID, p.TokenHash
	return f.record, true, nil
}
func (f *fakeStore) BuildSharesByOwner(context.Context, string, string, int) ([]store.BuildShare, error) {
	return []store.BuildShare{f.record}, nil
}
func (f *fakeStore) RevokeBuildShareByID(context.Context, string, string, int, string) error {
	return nil
}
func (f *fakeStore) RevokeBuildShareByToken(context.Context, string, []byte) error { return nil }
func (f *fakeStore) PublicBuildShare(_ context.Context, hash []byte) (store.BuildShare, error) {
	if string(hash) != string(f.record.TokenHash) {
		return store.BuildShare{}, store.ErrShareNotFound
	}
	return f.record, nil
}

type fakePresenter struct{ view presenter.BuildView }

func (f fakePresenter) Build(context.Context, string, int) (presenter.BuildView, error) {
	return f.view, nil
}
func (fakePresenter) Markdown(context.Context, string, int) (string, error) { return "# export\n", nil }

func newTestService(t *testing.T, st *fakeStore, view presenter.BuildView) *Service {
	t.Helper()
	codec, err := NewTokenCodec(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(st, fakePresenter{view: view}, codec, "http://localhost:3000/base")
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestCreateReturnsStableURLAndHashOnlyRecord(t *testing.T) {
	st := &fakeStore{record: store.BuildShare{CreatedAt: time.Now()}}
	service := newTestService(t, st, presenter.BuildView{})
	share, created, err := service.Create(context.Background(), "owner", "session", 3, "00000000-0000-0000-0000-000000000001")
	if err != nil || !created {
		t.Fatalf("Create err=%v created=%v", err, created)
	}
	if !strings.HasPrefix(share.URL, "http://localhost:3000/base/share/") || !ValidToken(share.Token) {
		t.Fatalf("share=%+v", share)
	}
	if string(st.last.TokenHash) == share.Token || string(st.last.TokenHash) != string(HashToken(share.Token)) {
		t.Fatal("store 必须只收到 token hash")
	}
	second, _, err := service.Create(context.Background(), "owner", "session", 3, "00000000-0000-0000-0000-000000000001")
	if err != nil || second.Token != share.Token || second.URL != share.URL {
		t.Fatalf("幂等重放不一致:first=%+v second=%+v err=%v", share, second, err)
	}
}

func TestPublicViewExcludesNotesAndObserved(t *testing.T) {
	requirement := json.RawMessage(`{"schema_version":1,"budget_cny":8000,"budget_flex":0.1,"use_case":{"type":"gaming","titles":["黑神话"],"resolution":"2K"},"notes":"private-note"}`)
	view := presenter.BuildView{
		SchemaVersion: 1,
		Summary: presenter.BuildSummary{SchemaVersion: 1, Version: 3, Intent: "swap_part", TotalCNY: "7000.00",
			SnapshotDate: "2026-08-09", OverallStatus: schemas.OverallReview, CreatedAt: "2026-08-09T00:00:00Z"},
		Requirement: requirement,
		Parts:       []presenter.PartLine{},
		Quote:       presenter.QuoteView{SnapshotDate: "2026-08-09", TotalCNY: "7000.00", BudgetCNY: "8000.00", BudgetDeltaCNY: "1000.00", MissingSKUs: []string{}},
		Validation: presenter.ValidationView{OverallStatus: schemas.OverallReview, Checks: []schemas.CheckResult{{
			RuleID: schemas.RuleSocketMatch, Outcome: schemas.OutcomeUnknown, Severity: schemas.SeverityNone,
			Observed: map[string]any{"internal": "secret-observed"}, MissingFields: []string{"cpu.socket"}, Detail: "数据不足",
		}}},
		Disclaimers: []string{"a", "b", "c"},
	}
	st := &fakeStore{record: store.BuildShare{SessionID: "session", Version: 3, CreatedAt: time.Now()}}
	service := newTestService(t, st, view)
	share, _, err := service.Create(context.Background(), "owner", "session", 3, "request")
	if err != nil {
		t.Fatal(err)
	}
	public, err := service.Public(context.Background(), share.Token)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, forbidden := range []string{"private-note", "secret-observed", `"notes"`, `"observed"`, `"session_id"`, `"owner_id"`, `"run_id"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("公开 DTO 泄漏 %q: %s", forbidden, text)
		}
	}
	if public.Summary.IntentLabel != "更换配件" || public.Requirement.BudgetCNY != "8000.00" {
		t.Fatalf("公开映射不正确:%+v", public)
	}
}
