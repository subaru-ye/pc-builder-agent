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
	"github.com/subaru-ye/pc-builder-agent/internal/sharing"
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

type fakeShareService struct {
	share   sharing.Share
	records []sharing.ShareRecord
	public  sharing.PublicBuildView
	err     error
}

func (f *fakeShareService) Create(context.Context, string, string, int, string) (sharing.Share, bool, error) {
	return f.share, true, f.err
}
func (f *fakeShareService) List(context.Context, string, string, int) ([]sharing.ShareRecord, error) {
	return f.records, f.err
}
func (f *fakeShareService) RevokeByID(context.Context, string, string, int, string) error {
	return f.err
}
func (f *fakeShareService) RevokeByToken(context.Context, string, string) error { return f.err }
func (f *fakeShareService) Public(context.Context, string) (sharing.PublicBuildView, error) {
	return f.public, f.err
}
func (f *fakeShareService) PublicMarkdown(context.Context, string) (string, int, string, error) {
	return "# public\n", 3, "2026-08-09", f.err
}

func newTestAPI(t *testing.T, events runevents.Store) (*API, *fakeService) {
	t.Helper()
	now := time.Now().UTC()
	f := &fakeService{
		session: store.WebSession{ID: "session-1", Title: "新会话", Phase: store.PhaseCollecting, CreatedAt: now, UpdatedAt: now},
		run:     store.AgentRun{ID: "run-1", SessionID: "session-1", Kind: store.RunScreening, Status: store.RunSucceeded, StartedAt: now},
	}
	shareService := &fakeShareService{
		share:  sharing.Share{SchemaVersion: 1, ID: uuid.NewString(), Version: 1, Token: strings.Repeat("A", 43), URL: "http://localhost:3000/share/test", CreatedAt: now},
		public: sharing.PublicBuildView{SchemaVersion: 1},
	}
	api, err := New(f, fakeBuildPresenter{}, shareService, events, fakeDB{}, fakeRedis{}, Config{PublicWebBaseURL: "http://localhost:3000", BuildsvcURL: "http://127.0.0.1:1"})
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

func TestCORSDefaultsToPublicWebOrigin(t *testing.T) {
	api, _ := newTestAPI(t, runevents.NewMemory())
	for name, tc := range map[string]struct {
		origin string
		want   int
	}{
		"public web origin": {origin: "http://localhost:3000", want: http.StatusCreated},
		"other origin":      {origin: "http://127.0.0.1:3000", want: http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Idempotency-Key", uuid.NewString())
			rec := httptest.NewRecorder()
			api.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("origin=%q status=%d body=%s", tc.origin, rec.Code, rec.Body.String())
			}
		})
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

func TestShareRoutesAndPublicResponses(t *testing.T) {
	api, service := newTestAPI(t, runevents.NewMemory())
	owner := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	service.session.OwnerID = owner
	shareService := api.shares.(*fakeShareService)
	shareService.records = []sharing.ShareRecord{{SchemaVersion: 1, ID: uuid.NewString(), Version: 1, CreatedAt: time.Now()}}

	create := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/session-1/builds/1/shares", nil)
	create.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: owner})
	create.Header.Set("Idempotency-Key", uuid.NewString())
	created := httptest.NewRecorder()
	api.Handler().ServeHTTP(created, create)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"token"`) {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session-1/builds/1/shares", nil)
	list.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: owner})
	listed := httptest.NewRecorder()
	api.Handler().ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), `"token"`) || !strings.Contains(listed.Body.String(), `"shares"`) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}

	public := httptest.NewRecorder()
	api.Handler().ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/public/shares/"+strings.Repeat("A", 43), nil))
	if public.Code != http.StatusOK || public.Header().Get("Cache-Control") != "no-store" || len(public.Result().Cookies()) != 0 {
		t.Fatalf("public status=%d cache=%q cookies=%v", public.Code, public.Header().Get("Cache-Control"), public.Result().Cookies())
	}

	markdown := httptest.NewRecorder()
	api.Handler().ServeHTTP(markdown, httptest.NewRequest(http.MethodGet, "/api/v1/public/shares/"+strings.Repeat("A", 43)+"/export.md", nil))
	if markdown.Code != http.StatusOK || markdown.Header().Get("Cache-Control") != "no-store" || !strings.Contains(markdown.Header().Get("Content-Disposition"), "pc-build-v3-2026-08-09.md") {
		t.Fatalf("markdown status=%d headers=%v body=%s", markdown.Code, markdown.Header(), markdown.Body.String())
	}
}

func TestShareNotFoundIsUniform(t *testing.T) {
	api, _ := newTestAPI(t, runevents.NewMemory())
	api.shares.(*fakeShareService).err = store.ErrShareNotFound
	for _, path := range []string{
		"/api/v1/public/shares/bad-token",
		"/api/v1/public/shares/bad-token/export.md",
	} {
		rec := httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
			t.Fatalf("path=%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
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
