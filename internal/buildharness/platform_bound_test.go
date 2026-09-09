package buildharness

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func platformProofFixture() (BuildInput, store.CatalogSnapshot) {
	spec := fixtureRequirement()
	spec.UseCase.Type = schemas.UseCaseGeneral
	spec.BudgetCNY, spec.BudgetFlex = 1000, 0.1
	cat := store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)}}
	add := func(sku string, category schemas.Category, price, specs string) {
		cat.Candidates = append(cat.Candidates, store.Candidate{SKU: sku, Category: category, PriceCNY: &price, Specs: []byte(specs)})
	}
	add("cpu-cheap", schemas.CategoryCPU, "100.00", `{"socket":"AM4","supported_chipsets":["B450"],"has_igpu":false}`)
	add("cpu-igpu", schemas.CategoryCPU, "500.00", `{"socket":"AM5","supported_chipsets":["A620"],"has_igpu":true}`)
	add("board-cheap", schemas.CategoryMotherboard, "50.00", `{"socket":"AM4","chipset":"B450","memory_generation":"ddr4"}`)
	add("board-am5", schemas.CategoryMotherboard, "300.00", `{"socket":"AM5","chipset":"A620","memory_generation":"ddr5"}`)
	add("memory-cheap", schemas.CategoryMemory, "20.00", `{"generation":"ddr4"}`)
	add("memory-ddr5", schemas.CategoryMemory, "200.00", `{"generation":"ddr5"}`)
	add("gpu", schemas.CategoryGPU, "1000.00", `{}`)
	for _, category := range []schemas.Category{schemas.CategorySSD, schemas.CategoryPSU, schemas.CategoryCase, schemas.CategoryCooler} {
		add(string(category), category, "50.00", `{}`)
	}
	return BuildInput{Requirement: spec}, cat
}

func TestGeneralPlatformProofConservativeBoundaries(t *testing.T) {
	input, cat := platformProofFixture()
	d := AssessCatalog(input, cat)
	if d == nil || d.Reason != "platform_budget_lower_bound" || d.LowerBoundCNY != "1200.00" || !strings.Contains(d.Message, "不是可交付报价") || !strings.Contains(d.Message, "已有配件") {
		t.Fatalf("expected platform proof with actionable explanation: %+v", d)
	}
	input.Requirement.BudgetCNY, input.Requirement.BudgetFlex = 1200, 0
	if d := AssessCatalog(input, cat); d != nil {
		t.Fatalf("lower bound at budget upper endpoint must not reject: %+v", d)
	}
	for _, mutate := range []struct {
		name  string
		apply func(*store.CatalogSnapshot)
	}{
		{"unknown price", func(c *store.CatalogSnapshot) { c.Candidates[0].PriceCNY = nil }},
		{"unknown display", func(c *store.CatalogSnapshot) {
			c.Candidates[0].Specs = []byte(`{"socket":"AM4","supported_chipsets":["B450"]}`)
		}},
		{"unknown generation", func(c *store.CatalogSnapshot) { c.Candidates[4].Specs = []byte(`{}`) }},
		{"malformed specs", func(c *store.CatalogSnapshot) { c.Candidates[0].Specs = []byte(`invalid`) }},
		{"cheaper compatible option", func(c *store.CatalogSnapshot) { p := "50.00"; c.Candidates[1].PriceCNY = &p }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			input, cat := platformProofFixture()
			mutate.apply(&cat)
			if d := AssessCatalog(input, cat); d != nil {
				t.Fatalf("uncertain or cheap path must not be rejected: %+v", d)
			}
		})
	}
	input, cat = platformProofFixture()
	input.BaseSelection = &schemas.BuildSelection{}
	if _, ok := generalPlatformLowerBound(input, cat); ok {
		t.Fatal("unsupported base/owned scenarios must not use this proof")
	}
}

func TestPlatformProofStopsBeforeModelAndQuote(t *testing.T) {
	input, cat := platformProofFixture()
	embedder := &countingEmbedder{}
	planner, err := NewCandidatePlanner(&fakeCatalogSource{catalog: cat}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeModel{}
	h, err := New(Config{Model: m, Planner: planner, Repairer: NewRepairPlanner(), Eval: evalFunc(func(context.Context, schemas.BuildSelection) (validate.Result, error) {
		t.Fatal("must not produce a quote after budget proof")
		return validate.Result{}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.Run(context.Background(), input)
	if err != nil || r.Succeeded || r.Attempts != 0 || r.Decision == nil || r.Decision.Reason != "platform_budget_lower_bound" || len(m.requests) != 0 || embedder.calls != 0 {
		t.Fatalf("proof path leaked into model/quote: %+v %v", r, err)
	}
}
