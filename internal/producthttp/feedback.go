package producthttp

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type feedbackService interface {
	SubmitFeedback(context.Context, string, string, string, string) (store.Feedback, error)
	GetFeedback(context.Context, string, string) (*store.Feedback, error)
}

func (a *API) runFeedback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run_id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(w, r, store.ErrRunNotFound)
		return
	}
	owner, _, ok := a.ownerForRun(w, r, id)
	if !ok {
		return
	}
	service, ok := a.service.(feedbackService)
	if !ok {
		a.writeError(w, r, product.NewProblem("feedback_unavailable", "反馈暂不可用", 503, "请稍后重试。", requestID(r)))
		return
	}
	if r.Method == http.MethodGet {
		feedback, err := service.GetFeedback(r.Context(), owner, id)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"schema_version": 1, "feedback": feedback})
		return
	}
	var body struct {
		SchemaVersion int    `json:"schema_version"`
		Reason        string `json:"reason"`
		Comment       string `json:"comment"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.SchemaVersion != 1 || !store.ValidFeedback(body.Reason, body.Comment) {
		a.writeError(w, r, store.ErrFeedbackInvalid)
		return
	}
	feedback, err := service.SubmitFeedback(r.Context(), owner, id, body.Reason, body.Comment)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": 1, "feedback": feedback})
}
