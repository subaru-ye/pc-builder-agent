package buildharness

import (
	"context"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// pricedEvaluator makes the validator authoritative for totals and disk-slot capacity.
func pricedEvaluator(slots int) evalFunc {
	return func(_ context.Context, selection schemas.BuildSelection) (validate.Result, error) {
		var total int64
		var disks int
		for _, category := range schemas.AllCategories {
			for _, sku := range selectionSKUs(selection, category) {
				price := int64(100000)
				if strings.HasSuffix(sku, "-b") {
					price = 50000
				}
				if category == schemas.CategorySSD {
					for _, disk := range selection.SSDs {
						if disk.SKU == sku {
							price *= int64(disk.Quantity)
							disks += disk.Quantity
							break
						}
					}
				}
				total += price
			}
		}
		result := passingResult(formatPromptFen(total))
		if disks > slots {
			result.Report.OverallStatus = schemas.OverallFail
		}
		return result, nil
	}
}

func TestBudgetSearchCanIncreaseSSDQuantityWhenAllSinglePricesAreMaximal(t *testing.T) {
	input := BuildInput{Requirement: fixtureRequirement()}
	input.Requirement.BudgetCNY = 9000
	input.Requirement.BudgetFlex = 0
	current := testDraft("psu-a").Selection
	for _, tt := range []struct {
		name   string
		slots  int
		locked []schemas.Category
		want   bool
	}{
		{"two slots", 2, nil, true},
		{"one slot", 1, nil, false},
		{"locked disk", 2, []schemas.Category{schemas.CategorySSD}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := runner{eval: pricedEvaluator(tt.slots)}
			input.Locked = tt.locked
			input.BaseSelection = &current
			plan, _, err := h.budgetRepair(context.Background(), input, current, testQuote("8000.00"), testBundle(), RepairPlan{Reason: "budget_under"})
			if err != nil {
				t.Fatal(err)
			}
			if !tt.want {
				if len(plan.PreferredSSDs) > 0 || len(plan.PreferredSelection) > 0 {
					t.Fatalf("unsafe repair: %+v", plan)
				}
				return
			}
			if len(plan.Mutable) != 1 || plan.Mutable[0] != schemas.CategorySSD || len(plan.PreferredSSDs) != 1 || plan.PreferredSSDs[0].Quantity != 2 {
				t.Fatalf("missing quantity repair: %+v", plan)
			}
			bundle := restrictBundle(testBundle(), plan, current)
			if g := bundleGroup(&bundle, schemas.CategorySSD); len(g.Candidates) != 1 || g.Candidates[0].SKU != "ssd-a" {
				t.Fatal("current SKU incorrectly removed for quantity repair")
			}
			wrong := current
			if validateRepairSelection(plan, current, wrong) == nil {
				t.Fatal("quantity omission passed")
			}
			wrong.SSDs = plan.PreferredSSDs
			if err := validateRepairSelection(plan, current, wrong); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBudgetSearchRespectsRelativeChangeLimit(t *testing.T) {
	base := testDraft("psu-a").Selection
	current := base
	current.CPU = "cpu-b"
	gpu := "gpu-b"
	current.GPU = &gpu
	input := BuildInput{Requirement: fixtureRequirement(), BaseSelection: &base, Change: &schemas.ChangeRequest{Intent: schemas.IntentAdjustBudget}}
	input.Requirement.BudgetCNY = 9000
	input.Requirement.BudgetFlex = 0
	h := runner{eval: pricedEvaluator(2)}
	quote := testQuote("7000.00")
	for i := range quote.Lines {
		if quote.Lines[i].Category == schemas.CategoryCPU || quote.Lines[i].Category == schemas.CategoryGPU {
			v := "500.00"
			quote.Lines[i].SubtotalCNY = &v
		}
	}
	plan, _, err := h.budgetRepair(context.Background(), input, current, quote, testBundle(), RepairPlan{Reason: "budget_under"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Mutable) == 0 {
		t.Fatal("expected a valid repair by restoring base components")
	}
	selection := current
	for category, sku := range plan.PreferredSelection {
		setSelectionSKU(&selection, category, sku)
	}
	if len(plan.PreferredSSDs) > 0 {
		selection.SSDs = plan.PreferredSSDs
	}
	if err := validateChangeConstraints(input, selection); err != nil {
		t.Fatal(err)
	}
	result, _ := h.eval.Evaluate(context.Background(), selection)
	if !inBudgetWindow(input.Requirement, result.Quote) {
		t.Fatal("repair outside budget")
	}
}

func TestRepairRejectsUnopenedChangesAndCancellation(t *testing.T) {
	base := testDraft("psu-a").Selection
	current := base
	current.Memory = "memory-b"
	if validateRepairSelection(RepairPlan{Mutable: []schemas.Category{schemas.CategoryGPU}}, base, current) == nil {
		t.Fatal("unopened memory changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := runner{eval: pricedEvaluator(2)}
	if _, _, err := h.budgetRepair(ctx, BuildInput{Requirement: fixtureRequirement()}, base, testQuote("8000.00"), testBundle(), RepairPlan{}); err == nil {
		t.Fatal("cancelled search continued")
	}
}

func TestHarnessUsesQuantityRepairThroughModelAndValidator(t *testing.T) {
	first := draftJSON("psu-a", "first")
	second := strings.Replace(draftJSON("psu-a", "second"), `"quantity":1`, `"quantity":2`, 1)
	model := &fakeModel{outputs: []string{first, second}}
	h := newTestHarness(t, model, pricedEvaluator(2))
	input := BuildInput{Requirement: fixtureRequirement()}
	input.Requirement.BudgetCNY = 9000
	input.Requirement.BudgetFlex = 0
	result, err := h.Run(context.Background(), input)
	if err != nil || !result.Succeeded || result.Draft.Selection.SSDs[0].Quantity != 2 {
		t.Fatalf("quantity repair failed: %+v %v", result, err)
	}
}
