package planningeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/decision"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type stubClassifier struct {
	result  decision.IntentResult
	err     error
	calls   int
	lastIn  decision.IntentInput
	onCall  func()
	journal func(any) error
}

func (s *stubClassifier) Classify(_ context.Context, in decision.IntentInput) (decision.IntentResult, error) {
	s.calls++
	s.lastIn = in
	if s.onCall != nil {
		s.onCall()
	}
	return s.result, s.err
}

func intentResult(intent decision.Intent, confidence float64) decision.IntentResult {
	return decision.IntentResult{
		Intent: intent,
		Probabilities: map[decision.Intent]float64{
			decision.IntentCollect: 0.1, decision.IntentConfirm: 0.2,
			decision.IntentPlan: 0.6, decision.IntentAmbiguous: 0.1,
		},
		Confidence: confidence, SelectedProbability: 0.6,
		RequestedModel: "jev-1.13.0", ResponseModel: "jev-1.13.0",
		InputTokens: 10, OutputTokens: 2, Duration: 40_000_000,
	}
}

func TestIntentInputUsesQuoteAndPromptView(t *testing.T) {
	state := schemas.NewRequirementState()
	input := &product.ScreenInput{
		Text:              "fallback text",
		RequirementState:  &state,
		HasBuild:          true,
		RequirementSource: schemas.RequirementSource{Kind: "chat", Quote: "预算8000"},
	}
	in := intentInputFrom(input)
	if in.CurrentTurn != "预算8000" {
		t.Fatalf("CurrentTurn = %q, want the source quote", in.CurrentTurn)
	}
	if !in.HasBuild || !json.Valid(in.RequirementState) {
		t.Fatalf("execution facts lost: %+v", in)
	}
	var view map[string]any
	if err := json.Unmarshal(in.RequirementState, &view); err != nil {
		t.Fatalf("prompt view invalid: %v", err)
	}
	if _, has := view["history"]; has {
		t.Fatalf("prompt view must not serialize full history")
	}
	empty := intentInputFrom(&product.ScreenInput{Text: "fallback text"})
	if empty.CurrentTurn != "fallback text" {
		t.Fatalf("quote fallback to Text missing: %q", empty.CurrentTurn)
	}
	if empty.RequirementState != nil {
		t.Fatal("nil requirement state must stay nil")
	}
}

func TestObserveIntentRecordsResultBesideScreening(t *testing.T) {
	stub := &stubClassifier{result: intentResult(decision.IntentPlan, 0.9)}
	g := &gateway{models: Models{Intent: stub, MaxIntentCalls: 3}}
	g.begin(Step{Kind: "message"})
	if err := g.observeIntent(context.Background(), &product.ScreenInput{Text: "生成"}); err != nil {
		t.Fatalf("observeIntent: %v", err)
	}
	g.mu.Lock()
	obs := g.record.Intent
	g.mu.Unlock()
	if obs == nil || obs.Prediction != "plan" || obs.ErrorClass != "" {
		t.Fatalf("observation = %+v", obs)
	}
	if obs.Confidence != 0.9 || obs.SelectedProbability != 0.6 || obs.InputTokens != 10 {
		t.Fatalf("scores not persisted separately: %+v", obs)
	}
	if stub.calls != 1 {
		t.Fatalf("classifier calls = %d", stub.calls)
	}
}

func TestObserveIntentProviderFailureLeavesHarnessAlive(t *testing.T) {
	stub := &stubClassifier{err: &classedStub{class: "rate_limited", msg: "http 429"}}
	g := &gateway{models: Models{Intent: stub, MaxIntentCalls: 5}}
	g.begin(Step{Kind: "message"})
	if err := g.observeIntent(context.Background(), &product.ScreenInput{Text: "生成"}); err != nil {
		t.Fatalf("provider failure must not fail the harness: %v", err)
	}
	g.mu.Lock()
	obs := g.record.Intent
	g.mu.Unlock()
	if obs == nil || obs.ErrorClass != "rate_limited" || obs.Prediction != "" {
		t.Fatalf("failure not recorded honestly: %+v", obs)
	}
}

type classedStub struct {
	class string
	msg   string
}

func (e *classedStub) Error() string      { return e.msg }
func (e *classedStub) ErrorClass() string { return e.class }

func TestObserveIntentCapAndJournalStopSpending(t *testing.T) {
	stub := &stubClassifier{result: intentResult(decision.IntentCollect, 0.5)}
	g := &gateway{models: Models{Intent: stub, MaxIntentCalls: 1}}
	g.begin(Step{Kind: "message"})
	if err := g.observeIntent(context.Background(), &product.ScreenInput{Text: "a"}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := g.observeIntent(context.Background(), &product.ScreenInput{Text: "b"}); err == nil {
		t.Fatal("exhausted intent budget must be a harness failure")
	}
	if stub.calls != 1 {
		t.Fatalf("calls after exhaustion = %d", stub.calls)
	}

	failing := &stubClassifier{result: intentResult(decision.IntentCollect, 0.5)}
	g2 := &gateway{models: Models{Intent: failing, MaxIntentCalls: 5, Journal: func(any) error { return errors.New("disk full") }}}
	g2.begin(Step{Kind: "message"})
	if err := g2.observeIntent(context.Background(), &product.ScreenInput{Text: "a"}); err == nil {
		t.Fatal("failed pre-request journal write must stop the harness")
	}
	if failing.calls != 0 {
		t.Fatalf("classifier called without evidence record: %d", failing.calls)
	}
}

func TestObserveIntentJournalAfterResponseStillStops(t *testing.T) {
	calls := 0
	stub := &stubClassifier{result: intentResult(decision.IntentCollect, 0.5), journal: nil}
	g := &gateway{models: Models{Intent: stub, MaxIntentCalls: 5, Journal: func(v any) error {
		calls++
		if calls == 1 {
			return nil // request evidence written
		}
		return errors.New("disk full after response")
	}}}
	g.begin(Step{Kind: "message"})
	if err := g.observeIntent(context.Background(), &product.ScreenInput{Text: "a"}); err == nil {
		t.Fatal("failed post-response journal write must stop the harness")
	}
}

func TestBuildIntentReportMetricsAndThresholds(t *testing.T) {
	obs := func(prediction string, confidence, selected float64, correct, agreement bool, gt, existing string) *IntentObservation {
		return &IntentObservation{
			Prediction: prediction, Confidence: confidence, SelectedProbability: selected,
			RequestedModel: "jev-1.13.0", ResponseModel: "jev-1.13.0",
			InputTokens: 100, OutputTokens: 10, DurationMS: 50,
			GroundTruth: gt, ExistingDecision: existing, Correct: correct, Agreement: agreement,
		}
	}
	report := &Report{Cases: []CaseRecord{
		{ID: "cal-1", Steps: []StepRecord{
			{Intent: obs("confirm", 0.9, 0.8, true, true, "confirm", "confirm")},
			{Intent: obs("plan", 0.4, 0.5, false, false, "confirm", "confirm")},
		}},
		{ID: "hold-1", Steps: []StepRecord{
			{Intent: obs("ambiguous", 0.3, 0.34, false, false, "plan", "plan")}, // refusal, never accepted
			{Intent: obs("plan", 0.95, 0.9, true, true, "plan", "plan")},
		}},
		{ID: "fail-1", Steps: []StepRecord{
			{Intent: &IntentObservation{ErrorClass: "timeout", Error: "deadline"}},
			{Intent: obs("collect", 0.7, 0.6, false, false, "", "")}, // success without label
		}},
	}}
	suite := Suite{IntentSplit: &IntentSplit{Calibration: []string{"cal-1"}, Holdout: []string{"hold-1", "fail-1"}}}
	models := Models{Intent: &stubClassifier{}, IntentInputPricePerMTok: 1, IntentOutputPricePerMTok: 0}
	r := buildIntentReport(suite, report, models)
	if r == nil {
		t.Fatal("intent report missing")
	}
	if r.Calls != 6 || r.Successes != 5 || r.Failures != 1 || r.Errors["timeout"] != 1 {
		t.Fatalf("call accounting wrong: %+v", r)
	}
	if r.Ambiguous != 1 || r.Labelled != 4 || r.PredictedUnlabelled != 1 {
		t.Fatalf("label accounting wrong: ambiguous=%d labelled=%d unlabelled=%d", r.Ambiguous, r.Labelled, r.PredictedUnlabelled)
	}
	if r.Accuracy == nil || *r.Accuracy != 0.5 {
		t.Fatalf("accuracy = %v", r.Accuracy)
	}
	if r.AgreementTotal != 4 || r.AgreementCount != 2 {
		t.Fatalf("agreement = %d/%d", r.AgreementCount, r.AgreementTotal)
	}
	if r.Confusion["confirm"]["confirm"] != 1 || r.Confusion["confirm"]["plan"] != 1 || r.Confusion["plan"]["plan"] != 1 || r.Confusion["plan"]["ambiguous"] != 1 {
		t.Fatalf("confusion matrix wrong: %+v", r.Confusion)
	}
	if r.CalibrationLabelled != 2 || r.HoldoutLabelled != 2 {
		t.Fatalf("split counts wrong: cal=%d hold=%d", r.CalibrationLabelled, r.HoldoutLabelled)
	}
	if r.InputTokens != 500 || r.OutputTokens != 50 || r.CashCost == nil || *r.CashCost != 0.0005 {
		t.Fatalf("tokens/cash wrong: %d/%d %v", r.InputTokens, r.OutputTokens, r.CashCost)
	}
	if r.LatencyP50MS != 50 || r.LatencyP95MS != 50 {
		t.Fatalf("latency wrong: %d/%d", r.LatencyP50MS, r.LatencyP95MS)
	}
	var calConf, holdConf *IntentThresholdCurve
	for i := range r.Thresholds {
		c := r.Thresholds[i]
		if c.Policy == "confidence" && c.Split == "calibration" {
			calConf = &r.Thresholds[i]
		}
		if c.Policy == "confidence" && c.Split == "holdout" {
			holdConf = &r.Thresholds[i]
		}
	}
	if calConf == nil || holdConf == nil {
		t.Fatalf("threshold curves missing: %+v", r.Thresholds)
	}
	if len(calConf.Points) != len(holdConf.Points) {
		t.Fatal("holdout must be evaluated at the calibration grid")
	}
	// The holdout grid is the calibration's confidence scores {0.9, 0.4, 0}.
	// The ambiguous refusal (0.3) never joins any accepted set.
	found := false
	for _, p := range holdConf.Points {
		if p.Threshold == 0.9 {
			found = true
			if p.Accepted != 1 || p.Correct != 1 || p.Precision == nil || *p.Precision != 1 {
				t.Fatalf("holdout t=0.9 wrong: %+v", p)
			}
			if p.Coverage != 0.5 || p.Fallback != 0.5 || p.CorrectShare != 0.5 {
				t.Fatalf("holdout t=0.9 share wrong: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("holdout curve lacks the calibration threshold 0.9")
	}
	// Calibration t=0.4 mixes one correct and one incorrect: precision 0.5.
	for _, p := range calConf.Points {
		if p.Threshold == 0.4 {
			if p.Accepted != 2 || p.Correct != 1 || p.Precision == nil || *p.Precision != 0.5 || p.Coverage != 1 {
				t.Fatalf("calibration t=0.4 wrong: %+v", p)
			}
		}
	}
}

func TestBuildIntentReportEmptyAcceptedPrecisionUndefined(t *testing.T) {
	report := &Report{Cases: []CaseRecord{{ID: "c1", Steps: []StepRecord{
		{Intent: &IntentObservation{Prediction: "ambiguous", Confidence: 0.4, SelectedProbability: 0.4, GroundTruth: "confirm", Correct: false}},
	}}}}
	r := buildIntentReport(Suite{}, report, Models{Intent: &stubClassifier{}})
	if r.Calls != 1 || r.Successes != 1 || r.Labelled != 1 {
		t.Fatalf("accounting: %+v", r)
	}
	if r.Accuracy == nil || *r.Accuracy != 0 {
		t.Fatal("a lone wrong labelled prediction has a defined zero accuracy")
	}
	for _, c := range r.Thresholds {
		for _, p := range c.Points {
			if p.Accepted == 0 && p.Precision != nil {
				t.Fatalf("empty accepted set must report undefined precision: %+v", p)
			}
		}
	}
	if len(r.Thresholds) == 0 || r.Thresholds[0].Split != "all" {
		t.Fatalf("missing split must fall back to the combined curve: %+v", r.Thresholds)
	}
	for _, l := range r.Limitations {
		if len(l) == 0 {
			t.Fatal("empty limitation entry")
		}
	}
}

func TestBuildIntentReportDisabled(t *testing.T) {
	if r := buildIntentReport(Suite{}, &Report{}, Models{}); r != nil {
		t.Fatal("nil classifier must keep the report free of intent sections")
	}
}

func TestLoadValidatesIntentSplit(t *testing.T) {
	base := `{"version":"v","provenance":"p","catalog":{"candidates":[{"id":"x"}]},"cases":[` +
		`{"id":"a","source":"s","steps":[{"kind":"confirm"}]},{"id":"b","source":"s","steps":[{"kind":"confirm"}]}],` +
		`"intent_split":{"calibration":[%s],"holdout":[%s]}}`
	unknown := fmt.Sprintf(base, `"ghost"`, `"b"`)
	if _, err := Load([]byte(unknown)); err == nil {
		t.Fatal("split entry for unknown case must be rejected")
	}
	duplicate := fmt.Sprintf(base, `"a"`, `"a"`)
	if _, err := Load([]byte(duplicate)); err == nil {
		t.Fatal("case listed in both splits must be rejected")
	}
	valid := fmt.Sprintf(base, `"a"`, `"b"`)
	if _, err := Load([]byte(valid)); err != nil {
		t.Fatalf("valid split rejected: %v", err)
	}
}

// The synthetic jev-intent-v1 cases carry hand-written screening oracles. This
// offline check feeds them through the real requirement reducer so fixture
// mistakes surface in unit tests, not only in a database replay.
func TestJevIntentV1SyntheticOraclesReduceToExpectations(t *testing.T) {
	raw, err := os.ReadFile("testdata/jev-intent-v1/suite.json")
	if err != nil {
		t.Skipf("jev-intent-v1 suite not present: %v", err)
	}
	suite, err := Load(raw)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if suite.Live {
		t.Fatal("intent suite must stay offline")
	}
	labelled := 0
	for _, c := range suite.Cases {
		if !strings.HasPrefix(c.ID, "JEV-") {
			continue
		}
		state := schemas.NewRequirementState()
		for _, step := range c.Steps {
			if step.Kind != "message" {
				continue
			}
			// 冻结 Jev 轨迹是 v1 传输外形:经 legacy turn decoder 取剥离后的
			// 领域更新;动作声明只作对照标签,不再有产品权威。
			turn, err := pipeline.DecodeLegacyRequirementTurn(step.Screen)
			if err != nil {
				t.Fatalf("%s: oracle does not decode: %v", c.ID, err)
			}
			update := turn.Update()
			next, err := schemas.ApplyRequirementUpdate(state, update, schemas.RequirementSource{Kind: "chat", Quote: step.Text})
			if err != nil {
				t.Fatalf("%s: oracle does not reduce: %v", c.ID, err)
			}
			// Mirror the product authority gate: can_plan looks at the saved
			// state before this turn, so the first message keeps an explicit
			// confirmation even when the model hands over plan.
			canPlan := state.Revision > 0
			final := turn.NextAction
			if final == "plan" && !canPlan {
				final = "confirm"
			}
			if step.Expect.NextAction != final {
				t.Fatalf("%s: oracle action (final %s) != label %s", c.ID, final, step.Expect.NextAction)
			}
			assertFieldExpectations(t, c.ID, next, step.Expect.Fields)
			state = next
			labelled++
		}
	}
	if labelled < 10 {
		t.Fatalf("synthetic coverage dropped: only %d labelled steps", labelled)
	}
}

func assertFieldExpectations(t *testing.T, caseID string, state schemas.RequirementState, fields map[string]FieldExpect) {
	t.Helper()
	for key, want := range fields {
		got, ok := state.Fields[key]
		if !ok {
			t.Fatalf("%s: field %q missing from reduced state", caseID, key)
		}
		if want.Status != "" && got.Status != want.Status {
			t.Fatalf("%s: field %q status = %q, want %q", caseID, key, got.Status, want.Status)
		}
		if want.Value != nil && string(got.Value) != string(want.Value) {
			t.Fatalf("%s: field %q value = %s, want %s", caseID, key, got.Value, want.Value)
		}
		for _, fragment := range want.Contains {
			if !strings.Contains(string(got.Value), fragment) {
				t.Fatalf("%s: field %q value %s lacks fragment %q", caseID, key, got.Value, fragment)
			}
		}
	}
}
