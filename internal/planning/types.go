// Package planning runs an evidence-backed, bounded tool conversation. Catalog
// coverage and preference mismatches are observations, never admission gates.
package planning

import (
	"context"
	"encoding/json"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type Evidence struct {
	CandidateID string `json:"candidate_id,omitempty"`
	Field       string `json:"field,omitempty"`
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	CapturedAt  string `json:"captured_at"`
	Kind        string `json:"kind"`
	FinalURL    string `json:"final_url,omitempty"`
	Reader      string `json:"reader,omitempty"`
	HTTPStatus  int    `json:"http_status,omitempty"`
	ReadBytes   int    `json:"read_bytes,omitempty"`
}

// Delivery is the server's assessment, independent of the model's routing label.
type Delivery struct {
	Status string   `json:"status"`
	Issues []string `json:"issues"`
}

type Candidate struct {
	ID              string                     `json:"id"`
	Category        schemas.Category           `json:"category"`
	Brand           string                     `json:"brand"`
	Model           string                     `json:"model"`
	Specs           json.RawMessage            `json:"specs"`
	Price           *string                    `json:"price_cny"`
	Evidence        []string                   `json:"evidence"`
	FieldEvidence   map[string]string          `json:"field_evidence,omitempty"`
	FieldQuotes     map[string]string          `json:"field_quotes,omitempty"`
	Attributes      map[string]json.RawMessage `json:"attributes,omitempty"`
	Merchant        string                     `json:"merchant,omitempty"`
	Currency        string                     `json:"currency,omitempty"`
	PriceObservedAt string                     `json:"price_observed_at,omitempty"`
	Unknown         []string                   `json:"unknown,omitempty"`
	External        bool                       `json:"external"`
}

type Assessment struct {
	Field       string   `json:"field"`
	Status      string   `json:"status"`
	Explanation string   `json:"explanation"`
	Evidence    []string `json:"evidence"`
}

// Result persists the proposal and evidence even when a final build is not ready.
type Result struct {
	ModelOutcome   string                    `json:"model_outcome,omitempty"`
	Delivery       *Delivery                 `json:"delivery,omitempty"`
	BuildVersion   int                       `json:"build_version,omitempty"`
	SchemaVersion  int                       `json:"schema_version"`
	Outcome        string                    `json:"outcome"`
	Reply          string                    `json:"reply"`
	Draft          json.RawMessage           `json:"draft,omitempty"`
	Assessments    []Assessment              `json:"assessments"`
	Issues         []string                  `json:"issues"`
	Assumptions    []string                  `json:"assumptions"`
	Candidates     []Candidate               `json:"candidates"`
	Evidence       []Evidence                `json:"evidence"`
	Validation     *schemas.ValidationReport `json:"validation,omitempty"`
	Quote          *validate.Quote           `json:"quote,omitempty"`
	ModelCalls     int                       `json:"model_calls"`
	ToolCalls      int                       `json:"tool_calls"`
	SearchCalls    int                       `json:"search_calls"`
	SearchRequests int                       `json:"search_requests"`
	PageCalls      int                       `json:"page_calls"`
	ReadAttempts   []ReadAttempt             `json:"read_attempts,omitempty"`
	Tokens         int32                     `json:"tokens"`
	DurationMS     int64                     `json:"duration_ms"`
	StageMS        map[string]int64          `json:"stage_ms"`
}

type Catalog interface {
	ActiveCatalogSnapshot(context.Context) (store.CatalogSnapshot, error)
}

type snapshotResolver struct {
	snapshotID int64
	candidates []Candidate
	date       string
}

func (r snapshotResolver) ResolveBuild(_ context.Context, s schemas.BuildSelection) (schemas.ResolvedBuild, error) {
	rows := make([]store.Candidate, 0, len(r.candidates))
	for _, c := range r.candidates {
		rows = append(rows, store.Candidate{SKU: c.ID, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: c.Specs, PriceCNY: c.Price})
	}
	return store.ResolveCandidateSnapshot(s, rows)
}
func (r snapshotResolver) LatestSnapshot(context.Context) (store.Snapshot, error) {
	t, _ := time.Parse("2006-01-02", r.date)
	return store.Snapshot{ID: r.snapshotID, SnapshotDate: t}, nil
}
func (r snapshotResolver) PricesBySnapshot(context.Context, int64) ([]store.Price, error) {
	var rows []store.Price
	for _, c := range r.candidates {
		if c.Price != nil {
			rows = append(rows, store.Price{SKU: c.ID, PriceCNY: *c.Price})
		}
	}
	return rows, nil
}
