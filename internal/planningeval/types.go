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
}
type Step struct {
	Kind    string                         `json:"kind"`
	Text    string                         `json:"text,omitempty"`
	Edit    []schemas.RequirementOperation `json:"edit,omitempty"`
	Screen  json.RawMessage                `json:"screen_oracle,omitempty"`
	Builder []*genai.Content               `json:"builder_oracle,omitempty"`
	Expect  Expect                         `json:"expect"`
}
type FieldExpect struct {
	Value    json.RawMessage `json:"value,omitempty"`
	Status   string          `json:"status,omitempty"`
	Strength string          `json:"strength,omitempty"`
	Kind     string          `json:"kind,omitempty"`
}
type Expect struct {
	OutcomeOneOf       []string                              `json:"outcome_one_of,omitempty"`
	IssuesAny          []string                              `json:"issues_any,omitempty"`
	SelectedParts      map[string]string                     `json:"selected_parts,omitempty"`
	SelectedOptions    map[string][]string                   `json:"selected_options,omitempty"`
	SelectedBrands     map[string]string                     `json:"selected_brands,omitempty"`
	SelectedSpecs      map[string]map[string]json.RawMessage `json:"selected_specs,omitempty"`
	PurchaseBudget     bool                                  `json:"purchase_budget,omitempty"`
	NextAction         string                                `json:"next_action,omitempty"`
	CPUChanged         bool                                  `json:"cpu_changed,omitempty"`
	BudgetCeilingCNY   string                                `json:"budget_ceiling_cny,omitempty"`
	Versions           int                                   `json:"versions"`
	Fields             map[string]FieldExpect                `json:"fields,omitempty"`
	Outcome            string                                `json:"outcome,omitempty"`
	Validation         string                                `json:"validation,omitempty"`
	IssuesContain      []string                              `json:"issues_contain,omitempty"`
	RequireTools       []string                              `json:"require_tools,omitempty"`
	ForbidTools        []string                              `json:"forbid_tools,omitempty"`
	BuilderCalls       *int                                  `json:"builder_calls,omitempty"`
	ReplyForbidden     []string                              `json:"reply_forbidden,omitempty"`
	ModelInputContains []string                              `json:"model_input_contains,omitempty"`
	CPU                string                                `json:"cpu,omitempty"`
	PreserveOtherParts bool                                  `json:"preserve_other_parts,omitempty"`
	MissingPrices      *int                                  `json:"missing_prices,omitempty"`
	Alternatives       *int                                  `json:"alternatives,omitempty"`
	CandidateSpecs     map[string]map[string]json.RawMessage `json:"candidate_specs,omitempty"`
	SearchCandidates   map[string]bool                       `json:"search_candidates,omitempty"`
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
	Kind           string                   `json:"kind"`
	Text           string                   `json:"text,omitempty"`
	State          schemas.RequirementState `json:"state"`
	ScreenInput    *product.ScreenInput     `json:"screen_input,omitempty"`
	PlanningInput  *schemas.PlanningInput   `json:"planning_input,omitempty"`
	Result         *planning.Result         `json:"result,omitempty"`
	Versions       int                      `json:"versions"`
	Reply          string                   `json:"reply"`
	Trace          []Trace                  `json:"trace"`
	Checks         []Check                  `json:"checks"`
	DurationMS     int64                    `json:"duration_ms"`
	Error          string                   `json:"error,omitempty"`
	Classification string                   `json:"classification"`
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
}
