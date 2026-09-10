package producthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSessionManagementHTTPPersistence(t *testing.T) {
	api, service, _ := requirementIntegrationAPI(t)
	owner := strings.Repeat("a", 43)
	session, err := service.CreateSession(context.Background(), owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, identity string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: identity})
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	path := "/api/v1/sessions/" + session.ID
	for _, method := range []string{"PATCH", "DELETE"} {
		request(method, path, `{"title":"越权"}`, strings.Repeat("b", 43), 404)
	}
	for _, body := range []string{`{}`, `{"title":"   "}`, `{"title":"第一行\n第二行"}`, `{"title":"` + strings.Repeat("字", 81) + `"}`} {
		request("PATCH", path, body, owner, 422)
	}
	request("PATCH", path, `{"title":"  朋友的电脑  ","archived":true}`, owner, 200)
	read := request("GET", path, "", owner, 200)
	var detail sessionDTO
	if err = json.Unmarshal(read.Body.Bytes(), &detail); err != nil || detail.Title != "朋友的电脑" || !detail.Archived {
		t.Fatalf("persisted detail: %s %v", read.Body.String(), err)
	}
	list := request("GET", "/api/v1/sessions", "", owner, 200)
	if strings.Contains(list.Body.String(), session.ID) {
		t.Fatal("archived session in recent list")
	}
	list = request("GET", "/api/v1/sessions?archived=true", "", owner, 200)
	if !strings.Contains(list.Body.String(), session.ID) {
		t.Fatal("archived session missing")
	}
	request("PATCH", path, `{"archived":false}`, owner, 200)
	list = request("GET", "/api/v1/sessions", "", owner, 200)
	if !strings.Contains(list.Body.String(), session.ID) {
		t.Fatal("restored session missing")
	}
	request("DELETE", path, "", owner, 204)
	request("GET", path, "", owner, 404)
	request("GET", path+"/builds", "", owner, 404)
}
