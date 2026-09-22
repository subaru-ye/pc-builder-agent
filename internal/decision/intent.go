// Package decision defines narrow decision-domain contracts shared by the
// product flow and evaluation harnesses. It carries no metrics and no policy:
// consumers decide what a decision result is worth.
package decision

import (
	"context"
	"encoding/json"
	"time"
)

// Intent mirrors the current product authority boundary: what the assistant is
// allowed to do with the current turn. `ambiguous` is the explicit refusal
// class; it is never an autonomous product action.
type Intent string

const (
	IntentCollect   Intent = "collect"
	IntentConfirm   Intent = "confirm"
	IntentPlan      Intent = "plan"
	IntentAmbiguous Intent = "ambiguous"
)

// IntentInput is the bounded, already-domained view of one control decision.
// Providers must not learn about product.ScreenInput or storage shapes.
type IntentInput struct {
	CurrentTurn      string
	RequirementState json.RawMessage // Prompt view only; never full history.
	HasBuild         bool
	CanPlan          bool
	BuildVersion     int
	LastAssistant    string // Already bounded upstream; reference resolution only.
}

// IntentResult separates the provider's calibrated confidence from the
// probability of the selected option so evaluation can score both policies.
type IntentResult struct {
	Intent              Intent
	Probabilities       map[Intent]float64
	Confidence          float64
	SelectedProbability float64
	RequestedModel      string
	ResponseModel       string
	InputTokens         int
	OutputTokens        int
	Duration            time.Duration
}

// IntentClassifier is the only surface evaluation harnesses depend on. The nil
// value means "disabled": callers must treat it as absent, not as a provider.
type IntentClassifier interface {
	Classify(context.Context, IntentInput) (IntentResult, error)
}
