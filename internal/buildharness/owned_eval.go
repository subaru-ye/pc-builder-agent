package buildharness

import (
	"context"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type ownedEvaluator struct {
	base Evaluator
	spec schemas.RequirementSpec
}

func (e ownedEvaluator) Evaluate(ctx context.Context, selection schemas.BuildSelection) (validate.Result, error) {
	r, err := e.base.Evaluate(ctx, selection)
	if err == nil {
		r.Quote = validate.WithOwnership(r.Quote, e.spec)
	}
	return r, err
}

func unresolvedResult(attempt int, draft schemas.BuildDraft, result validate.Result, message string) BuildResult {
	d := &Decision{Kind: "search_exhausted", Reason: "no_verified_solution", Scope: "current_run", Message: message + " 当前搜索未找到可交付方案，不能据此认定整个目录或市场无解。"}
	return BuildResult{Attempts: attempt, Draft: draft, Result: result, Message: d.Message, Decision: d}
}
