package presenter

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type Reader interface {
	BuildsBySession(context.Context, string) ([]store.BuildVersion, error)
	BuildByVersion(context.Context, string, int) (store.BuildVersion, error)
	RequirementSpecByID(context.Context, int64) (json.RawMessage, error)
	PartNames(context.Context, []string) (map[string]string, error)
}

type priceMetadataReader interface {
	PriceMetadataBySnapshotDate(context.Context, string, []string) (map[string]store.PriceMetadata, error)
}

type Service struct {
	reader Reader
	now    func() time.Time
}

func New(reader Reader) *Service { return &Service{reader: reader, now: time.Now} }

func newWithClock(reader Reader, now func() time.Time) *Service {
	return &Service{reader: reader, now: now}
}

type PriceFreshness string

const (
	PriceFreshnessFresh   PriceFreshness = "fresh"
	PriceFreshnessAging   PriceFreshness = "aging"
	PriceFreshnessStale   PriceFreshness = "stale"
	PriceFreshnessUnknown PriceFreshness = "unknown"
)

type PriceFreshnessSummary struct {
	Overall            PriceFreshness `json:"overall"`
	OldestObservedDate *string        `json:"oldest_observed_date"`
	MaxAgeDays         *int           `json:"max_age_days"`
	FreshCount         int            `json:"fresh_count"`
	AgingCount         int            `json:"aging_count"`
	StaleCount         int            `json:"stale_count"`
	UnknownCount       int            `json:"unknown_count"`
}

type BuildSummary struct {
	SchemaVersion  int                   `json:"schema_version"`
	Version        int                   `json:"version"`
	ParentVersion  *int                  `json:"parent_version"`
	Intent         string                `json:"intent"`
	TotalCNY       string                `json:"total_cny"`
	SnapshotDate   string                `json:"snapshot_date"`
	PriceFreshness PriceFreshness        `json:"price_freshness,omitempty"`
	OverallStatus  schemas.OverallStatus `json:"overall_status"`
	CreatedAt      string                `json:"created_at"`
}

type PartLine struct {
	Owned                  bool             `json:"owned,omitempty"`
	Category               schemas.Category `json:"category"`
	SKU                    string           `json:"sku"`
	Name                   string           `json:"name"`
	Quantity               int              `json:"quantity"`
	UnitPriceCNY           *string          `json:"unit_price_cny"`
	SubtotalCNY            *string          `json:"subtotal_cny"`
	Rationale              string           `json:"rationale"`
	PriceObservedDate      *string          `json:"price_observed_date,omitempty"`
	PriceFreshness         PriceFreshness   `json:"price_freshness,omitempty"`
	PriceAvailabilityBasis string           `json:"price_availability_basis,omitempty"`
}

type QuoteView struct {
	PurchaseTotalCNY *string               `json:"purchase_total_cny,omitempty"`
	BudgetBasis      string                `json:"budget_basis,omitempty"`
	SnapshotDate     string                `json:"snapshot_date"`
	TotalCNY         string                `json:"total_cny"`
	BudgetCNY        string                `json:"budget_cny"`
	BudgetKnown      bool                  `json:"budget_known"`
	BudgetDeltaCNY   string                `json:"budget_delta_cny"`
	MissingCount     int                   `json:"missing_count"`
	MissingSKUs      []string              `json:"missing_skus"`
	PriceFreshness   PriceFreshnessSummary `json:"price_freshness"`
}

type ValidationView struct {
	OverallStatus schemas.OverallStatus `json:"overall_status"`
	Checks        []schemas.CheckResult `json:"checks"`
}

type BuildView struct {
	CandidateSnapshot json.RawMessage `json:"candidate_snapshot,omitempty"`
	SchemaVersion     int             `json:"schema_version"`
	Summary           BuildSummary    `json:"summary"`
	Requirement       json.RawMessage `json:"requirement"`
	Parts             []PartLine      `json:"parts"`
	Quote             QuoteView       `json:"quote"`
	Validation        ValidationView  `json:"validation"`
	Disclaimers       []string        `json:"disclaimers"`
}

type DiffLine struct {
	Category      schemas.Category `json:"category"`
	Changed       bool             `json:"changed"`
	Before        string           `json:"before"`
	After         string           `json:"after"`
	PriceDeltaCNY *string          `json:"price_delta_cny"`
}

type BuildDiff struct {
	SchemaVersion   int        `json:"schema_version"`
	FromVersion     int        `json:"from_version"`
	ToVersion       int        `json:"to_version"`
	Lines           []DiffLine `json:"lines"`
	TotalDeltaCNY   string     `json:"total_delta_cny"`
	BudgetDeltaCNY  string     `json:"budget_delta_cny"`
	SnapshotWarning string     `json:"snapshot_warning"`
}

func (s *Service) Builds(ctx context.Context, sessionID string) ([]BuildSummary, error) {
	builds, err := s.reader.BuildsBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	rows, err := DecodeBuilds(builds)
	if err != nil {
		return nil, err
	}
	versions := make(map[int64]int, len(rows))
	for _, row := range rows {
		versions[row.ID] = row.Version
	}
	out := make([]BuildSummary, 0, len(rows))
	for _, row := range rows {
		item := summary(row, versions)
		freshness, _, err := s.priceInfo(ctx, row)
		if err != nil {
			return nil, err
		}
		item.PriceFreshness = freshness.Overall
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) Build(ctx context.Context, sessionID string, version int) (BuildView, error) {
	row, err := s.load(ctx, sessionID, version)
	if err != nil {
		return BuildView{}, err
	}
	all, err := s.reader.BuildsBySession(ctx, sessionID)
	if err != nil {
		return BuildView{}, err
	}
	versions := make(map[int64]int, len(all))
	for _, build := range all {
		versions[build.ID] = build.Version
	}
	names, err := s.reader.PartNames(ctx, row.SKUs())
	if err != nil {
		return BuildView{}, err
	}
	for _, id := range row.SKUs() {
		if name := rSnapshotName(row.CandidateSnapshot, id); name != "" {
			names[id] = name
		}
	}
	freshness, metadata, err := s.priceInfo(ctx, row)
	if err != nil {
		return BuildView{}, err
	}
	parts := make([]PartLine, 0, len(row.Quote.Lines)+1)
	for _, category := range schemas.AllCategories {
		found := false
		for _, line := range row.Quote.Lines {
			if line.Category != category {
				continue
			}
			found = true
			part := PartLine{Owned: line.Owned, Category: category, SKU: line.SKU, Name: names[line.SKU], Quantity: line.Quantity,
				UnitPriceCNY: line.UnitPriceCNY, SubtotalCNY: line.SubtotalCNY, Rationale: row.Draft.Rationale[string(category)]}
			if line.UnitPriceCNY == nil {
				part.PriceFreshness = PriceFreshnessUnknown
			} else if meta, ok := metadata[line.SKU]; ok && meta.ObservedAt != nil {
				date := meta.ObservedAt.UTC().Format("2006-01-02")
				part.PriceObservedDate = &date
				part.PriceFreshness, _ = classifyPriceFreshness(meta.ObservedAt, s.now())
				part.PriceAvailabilityBasis = meta.AvailabilityBasis
			} else {
				part.PriceFreshness = PriceFreshnessUnknown
			}
			parts = append(parts, part)
		}
		if !found && category == schemas.CategoryGPU && row.Draft.Selection.GPU == nil {
			parts = append(parts, PartLine{Category: category, Name: "无独显", Quantity: 1,
				Rationale: row.Draft.Rationale[string(category)]})
		}
	}
	budgetFen := row.BudgetCNY() * 100
	totalFen, ok := ParseFen(row.Quote.TotalCNY)
	if !ok {
		return BuildView{}, fmt.Errorf("v%d total_cny 无效:%q", row.Version, row.Quote.TotalCNY)
	}
	basis := "full_build"
	budgetTotal := totalFen
	if spec, err := schemas.DecodeRequirementSpec(row.Spec); err == nil {
		if spec.BudgetBasis != "" {
			basis = spec.BudgetBasis
		}
		if value, ok := ParseFen(validate.BudgetQuote(spec, row.Quote).TotalCNY); ok {
			budgetTotal = value
		}
	}
	var planningInput schemas.PlanningInput
	if json.Unmarshal(row.Spec, &planningInput) == nil && planningInput.SchemaVersion == 2 {
		if f := planningInput.State.Fields["budget_basis"]; f.Status == "active" {
			_ = json.Unmarshal(f.Value, &basis)
		}
		if basis == "new_purchase" && row.Quote.PurchaseTotalCNY != nil {
			if value, ok := ParseFen(*row.Quote.PurchaseTotalCNY); ok {
				budgetTotal = value
			}
		}
	}
	missing := row.Quote.MissingSKUs
	if missing == nil {
		missing = []string{}
	}
	return BuildView{
		CandidateSnapshot: row.CandidateSnapshot, SchemaVersion: 1, Summary: summary(row, versions), Requirement: row.Spec, Parts: parts,
		Quote: QuoteView{BudgetKnown: budgetFen > 0, PurchaseTotalCNY: row.Quote.PurchaseTotalCNY, BudgetBasis: basis, SnapshotDate: row.Quote.SnapshotDate, TotalCNY: FormatFen(totalFen), BudgetCNY: FormatFen(budgetFen),
			BudgetDeltaCNY: FormatFen(budgetFen - budgetTotal), MissingCount: row.Quote.MissingCount, MissingSKUs: missing,
			PriceFreshness: freshness},
		Validation:  ValidationView{OverallStatus: row.Report.OverallStatus, Checks: row.Report.Checks},
		Disclaimers: disclaimers(row.Quote.SnapshotDate),
	}, nil
}

func (s *Service) Diff(ctx context.Context, sessionID string, fromVersion, toVersion int) (BuildDiff, error) {
	from, err := s.load(ctx, sessionID, fromVersion)
	if err != nil {
		return BuildDiff{}, err
	}
	to, err := s.load(ctx, sessionID, toVersion)
	if err != nil {
		return BuildDiff{}, err
	}
	lines := make([]DiffLine, 0, len(schemas.AllCategories))
	for _, category := range schemas.AllCategories {
		before, after := SKURepr(from.Draft.Selection, category), SKURepr(to.Draft.Selection, category)
		line := DiffLine{Category: category, Changed: before != after, Before: before, After: after}
		beforeFen, beforeOK := CategoryFen(from.Quote, category)
		afterFen, afterOK := CategoryFen(to.Quote, category)
		if beforeOK && afterOK {
			delta := FormatFen(afterFen - beforeFen)
			line.PriceDeltaCNY = &delta
		}
		lines = append(lines, line)
	}
	fromTotal, ok := ParseFen(from.Quote.TotalCNY)
	if !ok {
		return BuildDiff{}, fmt.Errorf("v%d total_cny 无效", from.Version)
	}
	toTotal, ok := ParseFen(to.Quote.TotalCNY)
	if !ok {
		return BuildDiff{}, fmt.Errorf("v%d total_cny 无效", to.Version)
	}
	warning := ""
	if from.Quote.SnapshotDate != to.Quote.SnapshotDate {
		warning = fmt.Sprintf("两版本报价快照不同（%s vs %s），差额受快照影响。", from.Quote.SnapshotDate, to.Quote.SnapshotDate)
	}
	return BuildDiff{SchemaVersion: 1, FromVersion: fromVersion, ToVersion: toVersion, Lines: lines,
		TotalDeltaCNY: FormatFen(toTotal - fromTotal), BudgetDeltaCNY: FormatFen((to.BudgetCNY() - from.BudgetCNY()) * 100),
		SnapshotWarning: warning}, nil
}

func (s *Service) Markdown(ctx context.Context, sessionID string, version int) (string, error) {
	row, err := s.load(ctx, sessionID, version)
	if err != nil {
		return "", err
	}
	names, err := s.reader.PartNames(ctx, row.SKUs())
	if err != nil {
		return "", err
	}
	for _, id := range row.SKUs() {
		if name := rSnapshotName(row.CandidateSnapshot, id); name != "" {
			names[id] = name
		}
	}
	freshness, metadata, err := s.priceInfo(ctx, row)
	if err != nil {
		return "", err
	}
	return RenderExportWithPriceMetadata(row, names, freshness, metadata)
}

func (s *Service) priceInfo(ctx context.Context, row BuildRow) (PriceFreshnessSummary, map[string]store.PriceMetadata, error) {
	metadata := map[string]store.PriceMetadata{}
	if reader, ok := s.reader.(priceMetadataReader); ok {
		loaded, err := reader.PriceMetadataBySnapshotDate(ctx, row.Quote.SnapshotDate, row.SKUs())
		if err != nil {
			return PriceFreshnessSummary{}, nil, err
		}
		metadata = loaded
	}
	if metadata == nil {
		metadata = map[string]store.PriceMetadata{}
	}
	var snapshot struct {
		Candidates []struct {
			ID              string `json:"id"`
			External        bool   `json:"external"`
			PriceObservedAt string `json:"price_observed_at"`
		} `json:"candidates"`
	}
	if json.Unmarshal(row.CandidateSnapshot, &snapshot) == nil {
		for _, c := range snapshot.Candidates {
			if c.External {
				date, err := time.Parse(time.RFC3339, c.PriceObservedAt)
				if err != nil {
					date, err = time.Parse("2006-01-02", c.PriceObservedAt)
				}
				if err == nil {
					metadata[c.ID] = store.PriceMetadata{SKU: c.ID, ObservedAt: &date, AvailabilityBasis: "unknown"}
				}
			}
		}
	}
	summary := PriceFreshnessSummary{Overall: PriceFreshnessFresh}
	var oldest *time.Time
	var maxAge *int
	for _, line := range row.Quote.Lines {
		status, age := PriceFreshnessUnknown, 0
		meta, ok := metadata[line.SKU]
		if line.UnitPriceCNY != nil && ok && meta.ObservedAt != nil {
			status, age = classifyPriceFreshness(meta.ObservedAt, s.now())
			if oldest == nil || meta.ObservedAt.Before(*oldest) {
				value := *meta.ObservedAt
				oldest = &value
			}
			if maxAge == nil || age > *maxAge {
				value := age
				maxAge = &value
			}
		}
		switch status {
		case PriceFreshnessFresh:
			summary.FreshCount++
		case PriceFreshnessAging:
			summary.AgingCount++
		case PriceFreshnessStale:
			summary.StaleCount++
		default:
			summary.UnknownCount++
		}
	}
	if oldest != nil {
		value := oldest.UTC().Format("2006-01-02")
		summary.OldestObservedDate = &value
	}
	summary.MaxAgeDays = maxAge
	switch {
	case summary.StaleCount > 0:
		summary.Overall = PriceFreshnessStale
	case summary.UnknownCount > 0:
		summary.Overall = PriceFreshnessUnknown
	case summary.AgingCount > 0:
		summary.Overall = PriceFreshnessAging
	default:
		summary.Overall = PriceFreshnessFresh
	}
	return summary, metadata, nil
}

func classifyPriceFreshness(observedAt *time.Time, now time.Time) (PriceFreshness, int) {
	if observedAt == nil {
		return PriceFreshnessUnknown, 0
	}
	observedDay := time.Date(observedAt.UTC().Year(), observedAt.UTC().Month(), observedAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
	nowDay := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	age := int(nowDay.Sub(observedDay).Hours() / 24)
	if age < 0 {
		return PriceFreshnessUnknown, age
	}
	if age <= 7 {
		return PriceFreshnessFresh, age
	}
	if age <= 14 {
		return PriceFreshnessAging, age
	}
	return PriceFreshnessStale, age
}

func (s *Service) load(ctx context.Context, sessionID string, version int) (BuildRow, error) {
	build, err := s.reader.BuildByVersion(ctx, sessionID, version)
	if err != nil {
		return BuildRow{}, err
	}
	spec, err := s.reader.RequirementSpecByID(ctx, build.RequirementID)
	if err != nil {
		return BuildRow{}, err
	}
	return DecodeBuild(build, spec)
}

func summary(row BuildRow, versions map[int64]int) BuildSummary {
	var parent *int
	if row.ParentID != nil {
		if value, ok := versions[*row.ParentID]; ok {
			parent = &value
		}
	}
	return BuildSummary{SchemaVersion: 1, Version: row.Version, ParentVersion: parent, Intent: row.Intent,
		TotalCNY: row.Quote.TotalCNY, SnapshotDate: row.Quote.SnapshotDate, OverallStatus: row.Report.OverallStatus,
		CreatedAt: row.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
}

func disclaimers(snapshot string) []string {
	if snapshot == "" {
		snapshot = "未知日期"
	}
	return []string{
		fmt.Sprintf("报价为 %s 快照参考价，非实时价格。", snapshot),
		"兼容性结论基于本库收录参数，下单前请以官方规格页复核。",
		"本文档不构成购买建议。",
	}
}
