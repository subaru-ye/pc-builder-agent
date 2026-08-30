package producthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/subaru-ye/pc-builder-agent/internal/account"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type fakeAccountService struct {
	user        store.ProductUser
	owner       string
	logoutToken string
}

func (*fakeAccountService) Enabled() bool                { return true }
func (*fakeAccountService) Health(context.Context) error { return nil }
func (f *fakeAccountService) Resolve(_ context.Context, token, owner string) (account.Principal, error) {
	if token == "session-token" {
		return account.Principal{Authenticated: true, User: &f.user, Owners: []string{f.owner}, PrimaryOwner: f.owner}, nil
	}
	return account.Principal{Owners: []string{owner}, PrimaryOwner: owner}, nil
}
func (f *fakeAccountService) Register(_ context.Context, owner, _, _, _, _ string) (account.AuthResult, error) {
	f.owner = owner
	return account.AuthResult{
		Principal:           account.Principal{Authenticated: true, User: &f.user, Owners: []string{owner}, PrimaryOwner: owner},
		SessionToken:        "session-token",
		ClaimedSessionCount: 1,
	}, nil
}
func (f *fakeAccountService) Login(ctx context.Context, owner, email, password, key string) (account.AuthResult, error) {
	return f.Register(ctx, owner, email, password, "", key)
}
func (f *fakeAccountService) Logout(_ context.Context, token string) error {
	f.logoutToken = token
	return nil
}
func (f *fakeAccountService) UpdateProfile(_ context.Context, _ account.Principal, name, _ string) (store.ProductUser, error) {
	f.user.DisplayName = name
	return f.user, nil
}
func (*fakeAccountService) ChangePassword(context.Context, account.Principal, string, string, string, string) error {
	return nil
}

func TestAuthRegisterSetsOpaqueCookieAndReturnsSafeState(t *testing.T) {
	api, _ := newTestAPI(t, runevents.NewMemory())
	accounts := &fakeAccountService{user: store.ProductUser{
		ID: "product-user-1", Email: "learner@example.test", DisplayName: "装机新手", CreatedAt: time.Now().UTC(),
	}}
	api.accounts = accounts
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{"schema_version":1,"email":"learner@example.test","password":"test-password-123","display_name":"装机新手"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", rec.Header().Get("Cache-Control"))
	}
	var authCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == authCookieName {
			authCookie = cookie
		}
	}
	if authCookie == nil || authCookie.Value != "session-token" || !authCookie.HttpOnly || authCookie.SameSite != http.SameSiteLaxMode || authCookie.Secure || authCookie.Path != "/" || authCookie.Domain != "" {
		t.Fatalf("auth cookie 属性不符: %+v", authCookie)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"authenticated":true`) || !strings.Contains(body, `"claimed_session_count":1`) {
		t.Fatalf("body=%s", body)
	}
	for _, forbidden := range []string{"test-password-123", "session-token", "auth_subject", "owner_id"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("响应泄漏 %q: %s", forbidden, body)
		}
	}
}

func TestAuthMeAndLogoutUseServerSessionCookie(t *testing.T) {
	api, _ := newTestAPI(t, runevents.NewMemory())
	accounts := &fakeAccountService{
		owner: strings.Repeat("A", 43),
		user:  store.ProductUser{ID: "product-user-1", Email: "learner@example.test", DisplayName: "装机新手", CreatedAt: time.Now().UTC()},
	}
	api.accounts = accounts

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: authCookieName, Value: "session-token"})
	meRec := httptest.NewRecorder()
	api.Handler().ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK || !strings.Contains(meRec.Body.String(), `"authenticated":true`) {
		t.Fatalf("me status=%d body=%s", meRec.Code, meRec.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: authCookieName, Value: "session-token"})
	logoutRec := httptest.NewRecorder()
	api.Handler().ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent || accounts.logoutToken != "session-token" {
		t.Fatalf("logout status=%d token=%q body=%s", logoutRec.Code, accounts.logoutToken, logoutRec.Body.String())
	}
	var clearedAuth, rotatedOwner bool
	for _, cookie := range logoutRec.Result().Cookies() {
		switch cookie.Name {
		case authCookieName:
			clearedAuth = cookie.MaxAge < 0 && cookie.Value == ""
		case anonymousCookieName:
			rotatedOwner = cookie.Value != "" && cookie.Value != accounts.owner
		}
	}
	if !clearedAuth || !rotatedOwner {
		t.Fatalf("logout cookies=%v", logoutRec.Result().Cookies())
	}
}
