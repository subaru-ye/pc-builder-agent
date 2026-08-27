package presenter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type fakeReader struct {
	builds []store.BuildVersion
	specs  map[int64]json.RawMessage
	names  map[string]string
	prices map[string]store.PriceMetadata
}

func (f fakeReader) BuildsBySession(context.Context, string) ([]store.BuildVersion, error) {
	return f.builds, nil
}
func (f fakeReader) BuildByVersion(_ context.Context, _ string, version int) (store.BuildVersion, error) {
	for _, build := range f.builds {
		if build.Version == version {
			return build, nil
		}
	}
	return store.BuildVersion{}, store.ErrBuildNotFound
}
func (f fakeReader) RequirementSpecByID(_ context.Context, id int64) (json.RawMessage, error) {
	return f.specs[id], nil
}
func (f fakeReader) PartNames(context.Context, []string) (map[string]string, error) {
	return f.names, nil
}
func (f fakeReader) PriceMetadataBySnapshotDate(context.Context, string, []string) (map[string]store.PriceMetadata, error) {
	return f.prices, nil
}

func fixture(t *testing.T, version, budget int, total, gpu string) store.BuildVersion {
	t.Helper()
	price := func(value string) *string { return &value }
	draft := WireDraft{BuildRef: "build", Selection: WireSelection{
		CPU: "cpu-1", GPU: &gpu, Motherboard: "board-1", Memory: "memory-1",
		SSD: []WireSSD{{SKU: "ssd-1", Quantity: 1}}, PSU: "psu-1", Case: "case-1", Cooler: "cooler-1",
	}, Rationale: map[string]string{"gpu": "适合目标分辨率"}}
	lines := make([]validate.QuoteLine, 0, len(schemas.AllCategories))
	for _, category := range schemas.AllCategories {
		sku := map[schemas.Category]string{
			schemas.CategoryCPU: "cpu-1", schemas.CategoryGPU: gpu, schemas.CategoryMotherboard: "board-1",
			schemas.CategoryMemory: "memory-1", schemas.CategorySSD: "ssd-1", schemas.CategoryPSU: "psu-1",
			schemas.CategoryCase: "case-1", schemas.CategoryCooler: "cooler-1",
		}[category]
		lines = append(lines, validate.QuoteLine{Category: category, SKU: sku, Quantity: 1,
			UnitPriceCNY: price("100.00"), SubtotalCNY: price("100.00")})
	}
	checks := make([]schemas.CheckResult, 0, 12)
	for _, rule := range schemas.AllRuleIDs {
		checks = append(checks, schemas.CheckResult{RuleID: rule, Outcome: schemas.OutcomePass,
			Severity: schemas.SeverityNone, Observed: map[string]any{}, MissingFields: []string{}})
	}
	marshal := func(value any) json.RawMessage {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return store.BuildVersion{ID: int64(version), SessionID: "s1", Version: version, RequirementID: int64(version),
		Draft: marshal(draft), Quote: marshal(validate.Quote{SnapshotDate: "2026-08-09", TotalCNY: total, Lines: lines}),
		Validation: marshal(schemas.ValidationReport{BuildRef: "build", OverallStatus: schemas.OverallPass, Checks: checks}),
		CreatedAt:  time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)}
}

func TestPriceFreshnessBoundaries(t *testing.T) {
	build := fixture(t, 1, 8000, "800.00", "gpu-amd")
	observed := map[string]store.PriceMetadata{}
	for index, sku := range []string{"cpu-1", "gpu-amd", "board-1", "memory-1", "ssd-1", "psu-1", "case-1", "cooler-1"} {
		day := time.Date(2026, 8, 20-index, 8, 0, 0, 0, time.UTC)
		observed[sku] = store.PriceMetadata{SKU: sku, ObservedAt: &day}
	}
	reader := fakeReader{builds: []store.BuildVersion{build}, specs: map[int64]json.RawMessage{1: requirement(t, 8000)}, prices: observed}
	service := newWithClock(reader, func() time.Time { return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC) })
	view, err := service.Build(context.Background(), "s1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Quote.PriceFreshness.Overall != PriceFreshnessAging || view.Quote.PriceFreshness.FreshCount != 1 || view.Quote.PriceFreshness.AgingCount != 7 {
		t.Fatalf("freshness=%+v", view.Quote.PriceFreshness)
	}
	if view.Parts[0].PriceObservedDate == nil || *view.Parts[0].PriceObservedDate != "2026-08-20" {
		t.Fatalf("part freshness=%+v", view.Parts[0])
	}
	markdown, err := service.Markdown(context.Background(), "s1", 1)
	if err != nil || !strings.Contains(markdown, "价格可能已变化") {
		t.Fatalf("markdown freshness missing: err=%v\n%s", err, markdown)
	}
}

func TestPriceFreshnessStaleAndUnknown(t *testing.T) {
	staleDay := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	status, age := classifyPriceFreshness(&staleDay, time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC))
	if status != PriceFreshnessStale || age != 26 {
		t.Fatalf("status=%s age=%d", status, age)
	}
	if status, _ := classifyPriceFreshness(nil, time.Now()); status != PriceFreshnessUnknown {
		t.Fatalf("nil observation=%s", status)
	}
}

func requirement(t *testing.T, budget int) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{"schema_version":1,"budget_cny":` + formatInteger(budget) + `,"use_case":{"type":"gaming","resolution":"2K"}}`)
}

func formatInteger(value int) string {
	return fmt.Sprintf("%d", value)
}

func TestBuildViewAndDiffMoneySemantics(t *testing.T) {
	v1 := fixture(t, 1, 8000, "6100.00", "gpu-nvidia")
	v2 := fixture(t, 2, 7500, "5600.00", "gpu-amd")
	parent := v1.ID
	v2.ParentID = &parent
	reader := fakeReader{builds: []store.BuildVersion{v1, v2}, specs: map[int64]json.RawMessage{
		1: requirement(t, 8000), 2: requirement(t, 7500),
	}, names: map[string]string{"gpu-amd": "AMD GPU"}}
	service := New(reader)

	view, err := service.Build(context.Background(), "s1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if view.Quote.BudgetCNY != "7500.00" || view.Quote.BudgetDeltaCNY != "1900.00" {
		t.Fatalf("quote=%+v", view.Quote)
	}
	if view.Summary.ParentVersion == nil || *view.Summary.ParentVersion != 1 || len(view.Validation.Checks) != 12 || len(view.Disclaimers) != 3 {
		t.Fatalf("view 不完整:%+v", view)
	}
	diff, err := service.Diff(context.Background(), "s1", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if diff.TotalDeltaCNY != "-500.00" || diff.BudgetDeltaCNY != "-500.00" || len(diff.Lines) != 8 {
		t.Fatalf("diff=%+v", diff)
	}
	if !diff.Lines[1].Changed || diff.Lines[1].PriceDeltaCNY == nil || *diff.Lines[1].PriceDeltaCNY != "0.00" {
		t.Fatalf("gpu diff=%+v", diff.Lines[1])
	}
}
