package producthttp

import (
	"context"
	"net/http"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func (a *API) editRequirementState(w http.ResponseWriter, r *http.Request) {
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision *int                           `json:"expected_revision"`
		Operations       []schemas.RequirementOperation `json:"operations"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.ExpectedRevision == nil {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "缺少需求修订号", 400, "请刷新当前需求后重试。", requestID(r)))
		return
	}
	owner, ok := a.ownerForSession(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	editor, ok := a.service.(interface {
		EditRequirement(context.Context, string, string, string, product.RequirementEdit) (product.SessionDetail, error)
	})
	if !ok {
		a.writeProblem(w, r, product.NewProblem("internal_error", "需求编辑不可用", 503, "请稍后重试。", requestID(r)))
		return
	}
	detail, err := editor.EditRequirement(r.Context(), owner, r.PathValue("session_id"), key,
		product.RequirementEdit{ExpectedRevision: *body.ExpectedRevision, Operations: body.Operations})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSession(detail))
}
