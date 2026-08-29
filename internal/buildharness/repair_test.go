package buildharness

import (
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestRepairPlannerMapsAllRules(t *testing.T) {
	planner := NewRepairPlanner()
	draft := testDraft("psu-a")
	bundle := testBundle()
	tests := []struct {
		rule schemas.RuleID
		want schemas.Category
	}{
		{schemas.RuleSocketMatch, schemas.CategoryMotherboard},
		{schemas.RuleChipsetSupport, schemas.CategoryMotherboard},
		{schemas.RuleMemoryGeneration, schemas.CategoryMemory},
		{schemas.RuleMemorySpeed, schemas.CategoryMemory},
		{schemas.RuleGPUClearance, schemas.CategoryCase},
		{schemas.RuleCoolerClearance, schemas.CategoryCooler},
		{schemas.RulePSUHeadroom, schemas.CategoryPSU},
		{schemas.RuleFormFactorSupport, schemas.CategoryCase},
		{schemas.RuleM2SlotCapacity, schemas.CategoryMotherboard},
		{schemas.RuleGPUPowerConnectors, schemas.CategoryPSU},
		{schemas.RuleDisplayOutput, schemas.CategoryGPU},
		{schemas.RuleCoolerThermalCapacity, schemas.CategoryCooler},
	}
	for _, tt := range tests {
		t.Run(string(tt.rule), func(t *testing.T) {
			result := validate.Result{Report: schemas.ValidationReport{OverallStatus: schemas.OverallFail,
				Checks: []schemas.CheckResult{{RuleID: tt.rule, Outcome: schemas.OutcomeFail, Severity: schemas.SeverityError}}}}
			plan, err := planner.Plan(result, draft, RepairConstraints{Requirement: fixtureRequirement(), Bundle: bundle})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Mutable) != 1 || plan.Mutable[0] != tt.want {
				t.Fatalf("mutable=%v, want %s", plan.Mutable, tt.want)
			}
		})
	}
}

func TestRepairPlannerSkipsLockedPrimary(t *testing.T) {
	result := validate.Result{Report: schemas.ValidationReport{OverallStatus: schemas.OverallFail,
		Checks: []schemas.CheckResult{{RuleID: schemas.RulePSUHeadroom, Outcome: schemas.OutcomeFail, Severity: schemas.SeverityError}}}}
	plan, err := NewRepairPlanner().Plan(result, testDraft("psu-a"), RepairConstraints{
		Requirement: fixtureRequirement(), Bundle: testBundle(), Locked: []schemas.Category{schemas.CategoryPSU},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mutable[0] != schemas.CategoryGPU {
		t.Fatalf("PSU 锁定后应修 GPU，得到 %v", plan.Mutable)
	}
}

func TestRepairPlannerBudgetUsesAtMostTwoCategories(t *testing.T) {
	result := passingResult("12000.00")
	plan, err := NewRepairPlanner().Plan(result, testDraft("psu-a"), RepairConstraints{
		Requirement: fixtureRequirement(), Bundle: testBundle(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reason != "budget_over" || len(plan.Mutable) == 0 || len(plan.Mutable) > 2 {
		t.Fatalf("预算修复计划错误:%+v", plan)
	}
}

func TestRepairPlannerCombinesRuleAndBudgetRepair(t *testing.T) {
	result := passingResult("12000.00")
	result.Report.OverallStatus = schemas.OverallFail
	result.Report.Checks = []schemas.CheckResult{{
		RuleID: schemas.RuleCoolerClearance, Outcome: schemas.OutcomeFail, Severity: schemas.SeverityError,
	}}
	plan, err := NewRepairPlanner().Plan(result, testDraft("psu-a"), RepairConstraints{
		Requirement: fixtureRequirement(), Bundle: testBundle(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reason != "validation_rules+budget_over" || len(plan.Mutable) < 2 || len(plan.Mutable) > 3 || plan.Mutable[0] != schemas.CategoryCooler {
		t.Fatalf("规则与预算应在同一轮修复:%+v", plan)
	}
}

func testBundle() CandidateBundle {
	priceA, priceB := "1000.00", "500.00"
	bundle := CandidateBundle{SchemaVersion: 1, SnapshotDate: "2026-08-27"}
	for _, category := range schemas.AllCategories {
		bundle.Groups = append(bundle.Groups, CandidateGroup{Category: category, Candidates: []Candidate{
			{SKU: string(category) + "-a", PriceCNY: &priceA, Specs: map[string]any{}},
			{SKU: string(category) + "-b", PriceCNY: &priceB, Specs: map[string]any{}},
		}})
	}
	return bundle
}
