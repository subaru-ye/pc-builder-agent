package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoTrueClientPasswordAndLogout(t *testing.T) {
	var sawAuthorization bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.URL.Path == "/token" && r.URL.Query().Get("grant_type") == "password":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Tokens{AccessToken: "access-secret", RefreshToken: "refresh-secret", ExpiresIn: 3600, User: ProviderUser{ID: "subject", Email: "user@example.com"}})
		case r.URL.Path == "/logout":
			sawAuthorization = r.Header.Get("Authorization") == "Bearer access-secret"
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := NewGoTrueClient(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	tokens, err := c.PasswordGrant(context.Background(), "user@example.com", "password-value")
	if err != nil || tokens.User.ID != "subject" {
		t.Fatalf("tokens=%+v err=%v", tokens, err)
	}
	if err := c.Logout(context.Background(), tokens.AccessToken, "local"); err != nil || !sawAuthorization {
		t.Fatalf("logout err=%v authorization=%v", err, sawAuthorization)
	}
}

func TestGoTrueClientClassifiesWithoutLeakingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error_code":"invalid_credentials","msg":"request-secret-password"}`))
	}))
	defer server.Close()
	c, _ := NewGoTrueClient(server.URL, time.Second)
	_, err := c.PasswordGrant(context.Background(), "user@example.com", "request-secret-password")
	if err != ErrInvalidCredentials || strings.Contains(err.Error(), "request-secret") {
		t.Fatalf("unsafe or wrong error: %v", err)
	}
}
