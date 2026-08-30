package producthttp

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/account"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
)

type accountDTO struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

type authStateDTO struct {
	SchemaVersion       int         `json:"schema_version"`
	Enabled             bool        `json:"enabled"`
	Authenticated       bool        `json:"authenticated"`
	Account             *accountDTO `json:"account"`
	ClaimedSessionCount int         `json:"claimed_session_count"`
}

func authState(enabled bool, p account.Principal, claimed int) authStateDTO {
	state := authStateDTO{SchemaVersion: 1, Enabled: enabled, Authenticated: p.Authenticated, ClaimedSessionCount: claimed}
	if p.User != nil {
		state.Account = &accountDTO{ID: p.User.ID, Email: p.User.Email, DisplayName: p.User.DisplayName, CreatedAt: p.User.CreatedAt}
	}
	return state
}

func (a *API) authMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.accounts == nil {
		writeJSON(w, http.StatusOK, authState(false, account.Principal{}, 0))
		return
	}
	p, ok := a.principal(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, authState(a.accounts.Enabled(), p, 0))
}

func (a *API) authRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		SchemaVersion int    `json:"schema_version"`
		Email         string `json:"email"`
		Password      string `json:"password"`
		DisplayName   string `json:"display_name"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.SchemaVersion != 1 {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "注册信息无效", 400, "schema_version 必须为 1。", requestID(r)))
		return
	}
	if a.accounts == nil || !a.accounts.Enabled() {
		a.writeError(w, r, account.ErrDisabled)
		return
	}
	result, err := a.accounts.Register(r.Context(), a.owner(w, r), body.Email, body.Password, body.DisplayName, key)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.setAuthCookie(w, result.SessionToken)
	writeJSON(w, http.StatusCreated, authState(true, result.Principal, result.ClaimedSessionCount))
}

func (a *API) authLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		SchemaVersion int    `json:"schema_version"`
		Email         string `json:"email"`
		Password      string `json:"password"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.SchemaVersion != 1 {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "登录信息无效", 400, "schema_version 必须为 1。", requestID(r)))
		return
	}
	if a.accounts == nil || !a.accounts.Enabled() {
		a.writeError(w, r, account.ErrDisabled)
		return
	}
	result, err := a.accounts.Login(r.Context(), a.owner(w, r), body.Email, body.Password, key)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.setAuthCookie(w, result.SessionToken)
	writeJSON(w, http.StatusOK, authState(true, result.Principal, result.ClaimedSessionCount))
}

func (a *API) authLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var token string
	if c, err := r.Cookie(authCookieName); err == nil {
		token = c.Value
	}
	if a.accounts != nil && token != "" {
		if err := a.accounts.Logout(r.Context(), token); err != nil && !errors.Is(err, account.ErrSessionExpired) {
			a.writeError(w, r, err)
			return
		}
	}
	a.clearAuthCookie(w)
	a.rotateOwner(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) authProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		SchemaVersion int    `json:"schema_version"`
		DisplayName   string `json:"display_name"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	p, ok := a.principal(w, r)
	if !ok {
		return
	}
	if body.SchemaVersion != 1 || !p.Authenticated {
		a.writeError(w, r, account.ErrSessionExpired)
		return
	}
	u, err := a.accounts.UpdateProfile(r.Context(), p, body.DisplayName, key)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	p.User = &u
	writeJSON(w, http.StatusOK, authState(true, p, 0))
}

func (a *API) authChangePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		SchemaVersion   int    `json:"schema_version"`
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.SchemaVersion != 1 || body.NewPassword != body.ConfirmPassword {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "密码信息无效", 400, "两次输入的新密码必须一致。", requestID(r)))
		return
	}
	p, ok := a.principal(w, r)
	if !ok {
		return
	}
	cookie, err := r.Cookie(authCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		a.writeError(w, r, account.ErrSessionExpired)
		return
	}
	if err := a.accounts.ChangePassword(r.Context(), p, cookie.Value, body.CurrentPassword, body.NewPassword, key); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: authCookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: a.secure, MaxAge: 30 * 24 * 60 * 60,
		Expires: time.Now().Add(30 * 24 * time.Hour)})
}

func (a *API) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: authCookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: a.secure, MaxAge: -1, Expires: time.Unix(1, 0)})
}
