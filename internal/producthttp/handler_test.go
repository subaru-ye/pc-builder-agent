package producthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type fakeService struct {
	session store.WebSession
	run     store.AgentRun
}

func (f *fakeService) CreateSession(_ context.Context, owner, request string) (store.WebSession, error) {
	f.session.OwnerID = owner
	f.session.CreateRequestID = request
	return f.session, nil
}
func (f *fakeService) ListSessions(context.Context, string) ([]store.WebSession, error) {
	return []store.WebSession{f.session}, nil
}
func (f *fakeService) GetSession(_ context.Context, owner, id string) (product.SessionDetail, error) {
	if id != f.session.ID || owner != f.session.OwnerID {
		return product.SessionDetail{}, store.ErrWebSessionNotFound
	}
	return product.SessionDetail{Session: f.session}, nil
}
func (f *fakeService) GetRun(_ context.Context, owner, id string) (store.AgentRun, error) {
	if id != f.run.ID || owner != f.session.OwnerID {
		return store.AgentRun{}, store.ErrRunNotFound
	}
	return f.run, nil
}
func (f *fakeService) OwnSession(_ context.Context, owner, id string) error {
	if id != f.session.ID || owner != f.session.OwnerID {
		return store.ErrWebSessionNotFound
	}
	return nil
}
func (*fakeService) ReplaceRequirement(context.Context, string, string, json.RawMessage) error {
	return nil
}
func (f *fakeService) StartMessage(context.Context, string, string, string, string) (product.StartResult, error) {
	return product.StartResult{Run: f.run}, nil
}
func (f *fakeService) StartConfirm(context.Context, string, string, string) (product.StartResult, error) {
	return product.StartResult{Run: f.run}, nil
}

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

type fakeRedis struct {
	available bool
	err       error
}

func (f fakeRedis) Available() bool            { return f.available }
func (f fakeRedis) Ping(context.Context) error { return f.err }

type fakeBuildPresenter struct{}

func (fakeBuildPresenter) Builds(context.Context, string) ([]presenter.BuildSummary, error) {
	return []presenter.BuildSummary{}, nil
}
func (fakeBuildPresenter) Build(context.Context, string, int) (presenter.BuildView, error) {
	return presenter.BuildView{SchemaVersion: 1}, nil
}
func (fakeBuildPresenter) Diff(context.Context, string, int, int) (presenter.BuildDiff, error) {
	return presenter.BuildDiff{SchemaVersion: 1}, nil
}
func (fakeBuildPresenter) Markdown(context.Context, string, int) (string, error) {
	return "# test\n", nil
}

func newTestAPI(t *testing.T, events runevents.Store) (*API, *fakeService) {
	t.Helper()
	now := time.Now().UTC()
	f := &fakeService{
		session: store.WebSession{ID: "session-1", Title: "新会话", Phase: store.PhaseCollecting, CreatedAt: now, UpdatedAt: now},
		run:     store.AgentRun{ID: "run-1", SessionID: "session-1", Kind: store.RunScreening, Status: store.RunSucceeded, StartedAt: now},
	}
	api, err := New(f, fakeBuildPresenter{}, events, fakeDB{}, fakeRedis{}, Config{PublicWebBaseURL: "http://localhost:3000", BuildsvcURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	return api, f
}

func TestCreateSessionSetsAnonymousCookie(t *testing.T) {
	api, service := newTestAPI(t, runevents.NewMemory())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", nil)
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != anonymousCookieName {
		t.Fatalf("cookies=%v", cookies)
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure || c.Path != "/" || !validOwnerID(c.Value) {
		t.Fatalf("cookie 属性不符:%+v", c)
	}
	if service.session.OwnerID != c.Value {
		t.Fatalf("owner=%q cookie=%q", service.session.OwnerID, c.Value)
	}
}

func TestMessageRequiresUUIDAndStrictJSON(t *testing.T) {
	api, service := newTestAPI(t, runevents.NewMemory())
	service.session.OwnerID = strings.Repeat("A", 43)
	for name, tc := range map[string]struct {
		key, body string
		want      int
	}{
		"missing key":   {body: `{"schema_version":1,"text":"hello"}`, want: 400},
		"unknown field": {key: uuid.NewString(), body: `{"schema_version":1,"text":"hello","extra":true}`, want: 400},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/session-1/messages", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", tc.key)
			rec := httptest.NewRecorder()
			api.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
				t.Fatalf("status=%d content-type=%q body=%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
			}
		})
	}
}

func TestReadyReportsRedisDegraded(t *testing.T) {
	api, _ := newTestAPI(t, runevents.NewMemory())
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"redis":"degraded"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSSEReplaysUntilCompleted(t *testing.T) {
	events := runevents.NewMemory()
	api, service := newTestAPI(t, events)
	ownerBytes := make([]byte, 32)
	owner := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if !validOwnerID(owner) {
		t.Fatalf("test owner invalid: %d", len(ownerBytes))
	}
	service.session.OwnerID = owner
	_, _ = events.Append(context.Background(), service.run.ID, "run.started", map[string]any{"kind": "screening"})
	_, _ = events.Append(context.Background(), service.run.ID, "run.completed", map[string]any{"status": "succeeded"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runs/run-1/events", nil)
	req.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: owner})
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: run.started") || !strings.Contains(rec.Body.String(), "event: run.completed") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBuildReadRoutesRequireOwnershipAndKeepContentTypes(t *testing.T) {
	api, service := newTestAPI(t, runevents.NewMemory())
	owner := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	service.session.OwnerID = owner
	tests := []struct {
		path        string
		contentType string
	}{
		{"/api/v1/sessions/session-1/builds", "application/json"},
		{"/api/v1/sessions/session-1/builds/1", "application/json"},
		{"/api/v1/sessions/session-1/diff?from=1&to=2", "application/json"},
		{"/api/v1/sessions/session-1/builds/1/export.md", "text/markdown; charset=utf-8"},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: owner})
		rec := httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), tc.contentType) {
			t.Fatalf("path=%s status=%d type=%q body=%s", tc.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session-1/builds", nil)
	req.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"})
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("越权读取 status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWriteErrorDoesNotExposeUnknownError(t *testing.T) {
	api, _ := newTestAPI(t, runevents.NewMemory())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	api.writeError(rec, req, errors.New("secret database detail"))
	if strings.Contains(rec.Body.String(), "secret") || !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
