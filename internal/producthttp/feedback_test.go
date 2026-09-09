package producthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type fakeFeedbackService struct {
	*fakeService
	calls int
	err   error
}

func (f *fakeFeedbackService) GetFeedback(context.Context, string, string) (*store.Feedback, error) {
	return nil, f.err
}
func (f *fakeFeedbackService) SubmitFeedback(_ context.Context, _, id, reason, comment string) (store.Feedback, error) {
	f.calls++
	return store.Feedback{ID: uuid.NewString(), RunID: id, Reason: reason, Comment: comment}, f.err
}

func TestRunFeedbackHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, method, body, owner string
		status                    int
		err                       error
		calls                     int
	}{
		{"empty read", "GET", "", "owner", 200, nil, 0},
		{"submit", "POST", `{"schema_version":1,"reason":"price_issue","comment":"价格偏高"}`, "owner", 200, nil, 1},
		{"ownership", "POST", `{"schema_version":1,"reason":"price_issue"}`, "other", 404, nil, 0},
		{"bad reason", "POST", `{"schema_version":1,"reason":"invented"}`, "owner", 422, nil, 0},
		{"other needs detail", "POST", `{"schema_version":1,"reason":"other","comment":" "}`, "owner", 422, nil, 0},
		{"bad schema", "POST", `{"schema_version":2,"reason":"price_issue"}`, "owner", 422, nil, 0},
		{"active", "POST", `{"schema_version":1,"reason":"price_issue"}`, "owner", 409, store.ErrFeedbackRunActive, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, base := newTestAPI(t, runevents.NewMemory())
			base.run.ID = uuid.NewString()
			base.session.OwnerID = strings.Repeat("a", 43)
			fake := &fakeFeedbackService{fakeService: base, err: tc.err}
			api.service = fake
			req := httptest.NewRequest(tc.method, "/api/v1/runs/"+base.run.ID+"/feedback", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			owner := base.session.OwnerID
			if tc.owner == "other" {
				owner = strings.Repeat("b", 43)
			}
			req.AddCookie(&http.Cookie{Name: anonymousCookieName, Value: owner})
			w := httptest.NewRecorder()
			api.Handler().ServeHTTP(w, req)
			if w.Code != tc.status || fake.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, fake.calls, w.Body.String())
			}
		})
	}
}
