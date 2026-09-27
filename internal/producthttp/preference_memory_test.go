package producthttp

// 偏好端点的 HTTP 行为测试:窄接口断言降级、会话归属、schema 校验、
// 幂等键要求与错误形状。服务层语义在 internal/product 的测试覆盖。
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/sharing"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// testOwner 是合法的 32 字节 base64url owner ID(与匿名 cookie 同构)。
const testOwner = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type fakePreferenceService struct {
	fakeService
	saveResult product.PreferenceSaveResult
	saveErr    error
	memories   []schemas.PreferenceMemory
	listErr    error
	deleteErr  error
	deleted    []string
}

func (f *fakePreferenceService) SaveSessionPreference(context.Context, string, string, product.PreferenceSave) (product.PreferenceSaveResult, error) {
	return f.saveResult, f.saveErr
}

func (f *fakePreferenceService) ListPreferences(context.Context, []string) ([]schemas.PreferenceMemory, error) {
	return f.memories, f.listErr
}

func (f *fakePreferenceService) DeletePreference(_ context.Context, _ []string, preferenceID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, preferenceID)
	return nil
}

func newPreferenceTestAPI(t *testing.T, service *fakePreferenceService) *API {
	t.Helper()
	api, err := New(service, fakeBuildPresenter{}, &fakeShareService{
		share:  sharing.Share{SchemaVersion: 1, ID: uuid.NewString(), Version: 1, Token: strings.Repeat("A", 43), URL: "http://localhost:3101/share/test", CreatedAt: time.Now().UTC()},
		public: sharing.PublicBuildView{SchemaVersion: 1},
	}, runevents.NewMemory(), fakeDB{}, fakeRedis{}, Config{PublicWebBaseURL: "http://localhost:3101", BuildsvcURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func preferenceRequest(t *testing.T, api *API, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: testOwner})
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodDelete {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	return rec
}

func TestSavePreferenceEndpoint(t *testing.T) {
	service := &fakePreferenceService{
		fakeService: fakeService{session: store.WebSession{ID: "session-1", OwnerID: testOwner, Phase: store.PhaseCollecting}},
		saveResult: product.PreferenceSaveResult{
			Preference: schemas.PreferenceMemory{ID: "pref-1", Subject: "self", Field: "brand_pref.gpu",
				Value: json.RawMessage(`"nvidia"`), Strength: "prefer", Evidence: "stated",
				Source: schemas.PreferenceSource{Kind: "chat", SessionID: "session-1", MessageID: "msg-1", Quote: "显卡要N卡"},
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
			Action: product.PreferenceActionCreated,
		},
	}
	api := newPreferenceTestAPI(t, service)

	rec := preferenceRequest(t, api, http.MethodPost, "/api/v1/sessions/session-1/preferences",
		`{"schema_version":1,"field":"brand_pref.gpu","subject":"self"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存应 200: %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		SchemaVersion int                      `json:"schema_version"`
		Preference    map[string]any           `json:"preference"`
		Action        string                   `json:"action"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Action != "created" || response.Preference["id"] != "pref-1" || response.Preference["source"] == nil {
		t.Fatalf("响应形状错误: %s", rec.Body.String())
	}

	// 归属:不属于该 owner 的会话统一 404,不泄露存在性。
	rec = preferenceRequest(t, api, http.MethodPost, "/api/v1/sessions/other/preferences",
		`{"schema_version":1,"field":"noise_pref","subject":"self"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("他人会话应 404: %d", rec.Code)
	}
	// schema 校验。
	rec = preferenceRequest(t, api, http.MethodPost, "/api/v1/sessions/session-1/preferences",
		`{"schema_version":2,"field":"noise_pref","subject":"self"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("schema_version 错误应 400: %d", rec.Code)
	}
	// 服务层问题原样透传(problem 形状)。
	service.saveErr = product.NewProblem("preference_not_savable", "该项不能保存为长期偏好", 422, "临时例外只在本次会话生效。", "")
	rec = preferenceRequest(t, api, http.MethodPost, "/api/v1/sessions/session-1/preferences",
		`{"schema_version":1,"field":"brand_pref.gpu","subject":"self"}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "preference_not_savable") {
		t.Fatalf("not_savable 应 422 problem: %d %s", rec.Code, rec.Body.String())
	}
}

func TestListPreferencesEndpoint(t *testing.T) {
	service := &fakePreferenceService{
		fakeService: fakeService{session: store.WebSession{ID: "session-1", OwnerID: testOwner}},
		memories: []schemas.PreferenceMemory{
			{ID: "pref-1", Subject: "self", Field: "noise_pref", Value: json.RawMessage(`"silent"`), Strength: "prefer", Evidence: "stated"},
			{ID: "pref-2", Subject: "friend:xw", Field: "size_pref", Value: json.RawMessage(`"itx"`), Strength: "prefer", Evidence: "stated"},
		},
	}
	api := newPreferenceTestAPI(t, service)
	rec := preferenceRequest(t, api, http.MethodGet, "/api/v1/preferences", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("列表应 200: %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Preferences []map[string]any `json:"preferences"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || len(response.Preferences) != 2 {
		t.Fatalf("列表形状错误: %s %v", rec.Body.String(), err)
	}
}

func TestDeletePreferenceEndpoint(t *testing.T) {
	service := &fakePreferenceService{
		fakeService: fakeService{session: store.WebSession{ID: "session-1", OwnerID: testOwner}},
		deleteErr:   store.ErrPreferenceMemoryNotFound,
	}
	api := newPreferenceTestAPI(t, service)

	// 幂等键必填(与其他删除端点一致)。
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/preferences/pref-1", nil)
	req.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: testOwner})
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺幂等键应 400: %d", rec.Code)
	}

	rec = preferenceRequest(t, api, http.MethodDelete, "/api/v1/preferences/pref-1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在/越权删除应 404: %d %s", rec.Code, rec.Body.String())
	}

	service.deleteErr = nil
	rec = preferenceRequest(t, api, http.MethodDelete, "/api/v1/preferences/pref-1", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("删除应 204: %d", rec.Code)
	}
	if len(service.deleted) != 1 || service.deleted[0] != "pref-1" {
		t.Fatalf("删除应传偏好 ID: %v", service.deleted)
	}
}

func TestPreferenceEndpointsUnavailable(t *testing.T) {
	api, _ := newTestAPI(t, runevents.NewMemory())
	rec := preferenceRequest(t, api, http.MethodPost, "/api/v1/sessions/session-1/preferences",
		`{"schema_version":1,"field":"noise_pref","subject":"self"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "preference_unavailable") {
		t.Fatalf("无偏好能力应 503 降级: %d %s", rec.Code, rec.Body.String())
	}
	rec = preferenceRequest(t, api, http.MethodGet, "/api/v1/preferences", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("列表降级应 503: %d", rec.Code)
	}
	rec = preferenceRequest(t, api, http.MethodDelete, "/api/v1/preferences/pref-1", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("删除降级应 503: %d", rec.Code)
	}
}
