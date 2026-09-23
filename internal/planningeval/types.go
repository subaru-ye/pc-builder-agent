// Package planningeval evaluates the current product/planning path against
// immutable fixtures. Oracle decisions are never reported as live model quality.
package planningeval

import (
	"encoding/json"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/genai"
)

type Suite struct {
	Live       bool              `json:"live,omitempty"`
	Version    string            `json:"version"`
	Provenance string            `json:"provenance"`
	Catalog    CatalogFixture    `json:"catalog"`
	Pages      map[string]string `json:"pages"`
	Cases      []Case            `json:"cases"`
	// IntentSplit partitions case IDs into session-disjoint calibration and
	// holdout sets for threshold selection and reporting. Optional; only used
	// when a Jev observer is attached.
	IntentSplit *IntentSplit `json:"intent_split,omitempty"`
}

// IntentSplit lists case IDs; every case is one session, so a split by case is
// session-disjoint. Unlisted cases are reported as unassigned, never silently
// folded into a split.
type IntentSplit struct {
	Calibration []string `json:"calibration"`
	Holdout     []string `json:"holdout"`
}
type CatalogFixture struct {
	Date          string                  `json:"date"`
	Candidates    []planning.Candidate    `json:"candidates"`
	Evidence      []planning.Evidence     `json:"evidence"`
	PriceMetadata map[string]PriceFixture `json:"price_metadata,omitempty"`
}
type PriceFixture struct {
	Source            string  `json:"source"`
	ObservedAt        *string `json:"observed_at"`
	PriceType         *string `json:"price_type"`
	AvailabilityBasis string  `json:"availability_basis"`
}
type Case struct {
	ID            string                `json:"id"`
	Title         string                `json:"title"`
	Source        string                `json:"source"`
	Steps         []Step                `json:"steps"`
	PreviousBuild *PreviousBuildFixture `json:"previous_build,omitempty"`
}

// PreviousBuildFixture preserves a historical modification precondition. It is
// inserted only into the launcher's empty evaluation database, never generated
// by a model or counted as a delivered evaluation result.
type PreviousBuildFixture struct {
	Selection   json.RawMessage `json:"selection"`
	Requirement json.RawMessage `json:"requirement"`
	Source      string          `json:"source"`
	// Snapshot preserves a recorded version whose parts may have left the catalog.
	Snapshot *planning.Result `json:"snapshot,omitempty"`
}
type Step struct {
	Kind   string                         `json:"kind"`
	Text   string                         `json:"text,omitempty"`
	Edit   []schemas.RequirementOperation `json:"edit,omitempty"`
	Screen json.RawMessage                `json:"screen_oracle,omitempty"`
	// ScreenFallback 是 guard 纠偏重调时的第二条 scripted 输出；仅 v2
	// requirement 评估使用，v1 冻结件不设置。
	ScreenFallback json.RawMessage  `json:"screen_oracle_fallback,omitempty"`
	Builder        []*genai.Content `json:"builder_oracle,omitempty"`
	Expect         Expect           `json:"expect"`
}
type FieldExpect struct {
	Value    json.RawMessage `json:"value,omitempty"`
	Status   string          `json:"status,omitempty"`
	Strength string          `json:"strength,omitempty"`
	Kind     string          `json:"kind,omitempty"`
	Contains []string        `json:"contains,omitempty"`
}
type Expect struct {
	OutcomeOneOf           []string                              `json:"outcome_one_of,omitempty"`
	IssuesAny              []string                              `json:"issues_any,omitempty"`
	SelectedParts          map[string]string                     `json:"selected_parts,omitempty"`
	SelectedOptions        map[string][]string                   `json:"selected_options,omitempty"`
	SelectedBrands         map[string]string                     `json:"selected_brands,omitempty"`
	SelectedSpecs          map[string]map[string]json.RawMessage `json:"selected_specs,omitempty"`
	PurchaseBudget         bool                                  `json:"purchase_budget,omitempty"`
	NextAction             string                                `json:"next_action,omitempty"`
	CPUChanged             bool                                  `json:"cpu_changed,omitempty"`
	CPUTarget              string                                `json:"cpu_target,omitempty"`
	BudgetCeilingCNY       string                                `json:"budget_ceiling_cny,omitempty"`
	Versions               int                                   `json:"versions"`
	Fields                 map[string]FieldExpect                `json:"fields,omitempty"`
	Outcome                string                                `json:"outcome,omitempty"`
	Validation             string                                `json:"validation,omitempty"`
	IssuesContain          []string                              `json:"issues_contain,omitempty"`
	RequireTools           []string                              `json:"require_tools,omitempty"`
	ForbidTools            []string                              `json:"forbid_tools,omitempty"`
	BuilderCalls           *int                                  `json:"builder_calls,omitempty"`
	ReplyForbidden         []string                              `json:"reply_forbidden,omitempty"`
	ModelInputContains     []string                              `json:"model_input_contains,omitempty"`
	CPU                    string                                `json:"cpu,omitempty"`
	PreserveOtherParts     bool                                  `json:"preserve_other_parts,omitempty"`
	PreserveEssentialParts bool                                  `json:"preserve_essential_parts,omitempty"`
	MissingPrices          *int                                  `json:"missing_prices,omitempty"`
	Alternatives           *int                                  `json:"alternatives,omitempty"`
	RetainedReferences     [][]string                            `json:"retained_references,omitempty"`
	CandidateSpecs         map[string]map[string]json.RawMessage `json:"candidate_specs,omitempty"`
	SearchCandidates       map[string]bool                       `json:"search_candidates,omitempty"`
}
type Trace struct {
	Role           string          `json:"role"`
	Model          string          `json:"model"`
	ProviderCalled bool            `json:"provider_called"`
	Request        json.RawMessage `json:"request"`
	Response       *genai.Content  `json:"response"`
	DurationMS     int64           `json:"duration_ms"`
	Tokens         *int32          `json:"tokens"` // nil for offline oracle; not a billed zero.
	InputTokens    *int32          `json:"input_tokens,omitempty"`
	OutputTokens   *int32          `json:"output_tokens,omitempty"`
	Error          string          `json:"error,omitempty"`
}
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}
type StepRecord struct {
	Kind  string                   `json:"kind"`
	Text  string                   `json:"text,omitempty"`
	State schemas.RequirementState `json:"state"`
	// StateNextAction 只为冻结的 v1 轨迹回放保留:从原始状态 JSON 读取
	// 旧记录的 next_action;v2 运行恒为空,不参与任何产品判定。
	StateNextAction string                 `json:"state_next_action,omitempty"`
	ScreenInput     *product.ScreenInput   `json:"screen_input,omitempty"`
	PlanningInput   *schemas.PlanningInput `json:"planning_input,omitempty"`
	Result          *planning.Result       `json:"result,omitempty"`
	PlanningAttempt *planning.Result       `json:"planning_attempt,omitempty"` // Diagnostics/accounting, never proof of a saved proposal.
	Versions        int                    `json:"versions"`
	Reply           string                 `json:"reply"`
	Trace           []Trace                `json:"trace"`
	Checks          []Check                `json:"checks"`
	DurationMS      int64                  `json:"duration_ms"`
	Error           string                 `json:"error,omitempty"`
	Classification  string                 `json:"classification"`
	Intent          *IntentObservation     `json:"intent,omitempty"`
}

// IntentObservation records one bounded Jev call beside the Screening decision.
// It never carries the raw input: ScreenInput evidence already holds it.
type IntentObservation struct {
	Prediction          string             `json:"prediction,omitempty"`
	Probabilities       map[string]float64 `json:"probabilities,omitempty"`
	Confidence          float64            `json:"confidence,omitempty"`
	SelectedProbability float64            `json:"selected_probability,omitempty"`
	RequestedModel      string             `json:"requested_model,omitempty"`
	ResponseModel       string             `json:"response_model,omitempty"`
	InputTokens         int                `json:"input_tokens,omitempty"`
	OutputTokens        int                `json:"output_tokens,omitempty"`
	DurationMS          int64              `json:"duration_ms,omitempty"`
	ExistingDecision    string             `json:"existing_decision,omitempty"`
	GroundTruth         string             `json:"ground_truth,omitempty"`
	Agreement           bool               `json:"agreement,omitempty"`
	Correct             bool               `json:"correct,omitempty"`
	ErrorClass          string             `json:"error_class,omitempty"`
	Error               string             `json:"error,omitempty"`
}
type CaseRecord struct {
	ID    string       `json:"id"`
	Pass  bool         `json:"pass"`
	Steps []StepRecord `json:"steps"`
}
type Report struct {
	SchemaVersion       int            `json:"schema_version"`
	SuiteVersion        string         `json:"suite_version"`
	SuiteSHA256         string         `json:"suite_sha256"`
	CatalogSHA256       string         `json:"catalog_sha256"`
	Mode                string         `json:"mode"`
	Cases               []CaseRecord   `json:"cases"`
	Passed              int            `json:"passed"`
	ScreeningCalls      int            `json:"screening_calls"`
	BuilderCalls        int            `json:"builder_calls"`
	ToolCalls           int            `json:"tool_calls"`
	SearchCalls         int            `json:"search_calls"`
	PageCalls           int            `json:"page_calls"`
	ExternalRequests    int            `json:"external_requests"`
	ActualModelRequests int            `json:"actual_model_requests"`
	Tokens              *int64         `json:"tokens"`
	CashCost            *float64       `json:"cash_cost"`
	Classifications     map[string]int `json:"classifications"`
	DurationMS          int64          `json:"duration_ms"`
	Limitations         []string       `json:"limitations"`
	Intent              *IntentReport  `json:"intent,omitempty"`
}

// IntentReport aggregates the optional Jev observations. Confidence and the
// selected-option probability are scored as independent policies; agreement
// with the existing decision is reported separately from correctness.
type IntentReport struct {
	RequestedModel      string                    `json:"requested_model,omitempty"`
	ResponseModels      map[string]int            `json:"response_models,omitempty"`
	Calls               int                       `json:"calls"`
	Successes           int                       `json:"successes"`
	Failures            int                       `json:"failures"`
	Errors              map[string]int            `json:"errors,omitempty"`
	Ambiguous           int                       `json:"ambiguous"`
	LatencyP50MS        int64                     `json:"latency_p50_ms,omitempty"`
	LatencyP95MS        int64                     `json:"latency_p95_ms,omitempty"`
	InputTokens         int64                     `json:"input_tokens,omitempty"`
	OutputTokens        int64                     `json:"output_tokens,omitempty"`
	CashCost            *float64                  `json:"cash_cost,omitempty"`
	Labelled            int                       `json:"labelled"`
	PredictedUnlabelled int                       `json:"predicted_unlabelled,omitempty"`
	Confusion           map[string]map[string]int `json:"confusion,omitempty"`
	Accuracy            *float64                  `json:"accuracy,omitempty"`
	AgreementTotal      int                       `json:"agreement_total"`
	AgreementCount      int                       `json:"agreement_count"`
	CalibrationLabelled int                       `json:"calibration_labelled,omitempty"`
	HoldoutLabelled     int                       `json:"holdout_labelled,omitempty"`
	Thresholds          []IntentThresholdCurve    `json:"thresholds"`
	Limitations         []string                  `json:"limitations"`
}

// IntentThresholdCurve evaluates one score policy on one split. Thresholds are
// the calibration split's observed scores; the holdout curve is evaluated at
// those same thresholds once.
type IntentThresholdCurve struct {
	Policy string                 `json:"policy"`
	Split  string                 `json:"split"`
	Points []IntentThresholdPoint `json:"points"`
}

type IntentThresholdPoint struct {
	Threshold float64 `json:"threshold"`
	Accepted  int     `json:"accepted"`
	Correct   int     `json:"correct"`
	// Precision is nil (undefined) when the accepted set is empty; an empty
	// accepted set is never reported as perfect.
	Precision *float64 `json:"precision"`
	Coverage  float64  `json:"coverage"`
	Fallback  float64  `json:"fallback"`
	// CorrectShare = precision*coverage. Explicit upper bound on the share of
	// turns a later safe route policy could serve correctly; not a production
	// reduction claim.
	CorrectShare float64 `json:"correct_share"`
}
