package producthttp

import (
	"context"
	"net/http"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
)

func (a *API) manageSession(w http.ResponseWriter, r *http.Request) {
	owner, ok := a.ownerForSession(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	var body struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	remove := r.Method == http.MethodDelete
	if !remove && !a.decodeJSON(w, r, &body) {
		return
	}
	manager, ok := a.service.(interface {
		ManageSession(context.Context, string, string, *string, *bool, bool) error
	})
	if !ok {
		a.writeProblem(w, r, product.NewProblem("internal_error", "会话管理不可用", 503, "请稍后重试。", requestID(r)))
		return
	}
	if err := manager.ManageSession(r.Context(), owner, r.PathValue("session_id"), body.Title, body.Archived, remove); err != nil {
		a.writeError(w, r, err)
		return
	}
	if remove {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	a.getSession(w, r)
}
