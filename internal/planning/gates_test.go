package planning

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func mustBudget(t *testing.T, input *schemas.PlanningInput, value, strength string) {
	t.Helper()
	input.State.Fields["budget_cny"] = schemas.RequirementField{
		Status: "active", Kind: "constraint", Strength: strength, Value: json.RawMessage(value),
	}
}

func catalogCandidates(c recordedCatalog) []Candidate {
	out := []Candidate{}
	for _, sc := range c.Candidates {
		out = append(out, Candidate{ID: sc.SKU, Category: sc.Category, Brand: sc.Brand, Model: sc.Model, Specs: sc.Specs, Price: sc.PriceCNY})
	}
	return out
}

func gateExecution(input schemas.PlanningInput, record Result, catalog recordedCatalog) *execution {
	return &execution{input: input, result: record, candidates: catalogCandidates(catalog)}
}

// 录制的候选即已选件本身；补一个严格更便宜的候选，让“存在自纠空间”可测。
func withCheaperAlternative(catalog recordedCatalog) recordedCatalog {
	catalog.Candidates = append(catalog.Candidates, store.Candidate{SKU: "psu-cheap", Category: schemas.CategoryPSU, Brand: "Test", Model: "Cheap 500W", PriceCNY: strPtr("100.00")})
	return catalog
}

func TestBudgetGateLoopsBackOnlyWithCheaperAlternatives(t *testing.T) {
	input, record, catalog := completeRecording(t)
	mustBudget(t, &input, "1000", "must")
	x := gateExecution(input, record, withCheaperAlternative(catalog))
	x.result.Outcome = "clarify"
	fb := x.budgetGateFeedback("clarify")
	if !strings.Contains(fb, "预算自纠反馈") || !strings.Contains(fb, "ceiling_cny") || !strings.Contains(fb, "budget_alternatives") {
		t.Fatalf("over-budget clarify with cheaper alternatives must loop back: %q", fb)
	}
	// proposal 同样拦截；ready 不拦。
	if fb = x.budgetGateFeedback("proposal"); !strings.Contains(fb, "预算自纠反馈") {
		t.Fatalf("over-budget proposal must loop back: %q", fb)
	}
	if fb = x.budgetGateFeedback("ready"); fb != "" {
		t.Fatalf("ready must not be gated: %q", fb)
	}
	// 总价已在 ceiling 内：合法交付，不触发。
	field := input.State.Fields["budget_cny"]
	field.Value = json.RawMessage("6000")
	input.State.Fields["budget_cny"] = field
	x = gateExecution(input, record, withCheaperAlternative(catalog))
	if fb = x.budgetGateFeedback("clarify"); fb != "" {
		t.Fatalf("within-budget delivery gated: %q", fb)
	}
	// 没有严格更便宜的同品类候选：那是合法用户取舍，不触发。
	field.Value = json.RawMessage("1000")
	input.State.Fields["budget_cny"] = field
	x = &execution{input: input, result: record}
	if fb = x.budgetGateFeedback("clarify"); fb != "" {
		t.Fatalf("no cheaper alternative must not gate: %q", fb)
	}
	// 非 must 预算不构成硬上限。
	field.Strength = "prefer"
	input.State.Fields["budget_cny"] = field
	x = gateExecution(input, record, catalog)
	if fb = x.budgetGateFeedback("clarify"); fb != "" {
		t.Fatalf("prefer budget must not gate: %q", fb)
	}
}

func TestUnknownGateListsFieldCompleteAlternatives(t *testing.T) {
	input, record, _ := completeRecording(t)
	x := &execution{input: input, result: record}
	x.result.Validation = &schemas.ValidationReport{Checks: []schemas.CheckResult{{
		Outcome: schemas.OutcomeUnknown, MissingFields: []string{"cooler.cooling_capacity_w"},
	}}}
	specs := json.RawMessage(`{"cooling_capacity_w":220}`)
	x.candidates = []Candidate{
		{ID: "cooler-alt", Category: schemas.CategoryCooler, Price: strPtr("129.00"), Specs: specs},
		{ID: "cooler-blank", Category: schemas.CategoryCooler, Price: strPtr("99.00"), Specs: json.RawMessage(`{}`)},
	}
	fb := x.unknownGateFeedback("proposal", false)
	if !strings.Contains(fb, "unknown替代反馈") || !strings.Contains(fb, "cooler-alt") || strings.Contains(fb, "cooler-blank") {
		t.Fatalf("unknown gate must list only field-complete alternatives: %q", fb)
	}
	if fb = x.unknownGateFeedback("ready", false); fb != "" {
		t.Fatalf("ready must not be gated: %q", fb)
	}
	// 已选候选不算替代：替代集为空时不拦截。
	x.candidates[0].ID = "cooler-selected"
	x.result.Draft = json.RawMessage(strings.ReplaceAll(string(x.result.Draft), "cooler-deepcool-ag400", "cooler-selected"))
	if fb = x.unknownGateFeedback("proposal", false); fb != "" {
		t.Fatalf("selected candidate must not be listed as alternative: %q", fb)
	}
}

func TestHardRequirementGateRequiresVerifiedAlternativesBeforeClarify(t *testing.T) {
	input, record, _ := completeRecording(t)
	input.State.Fields["size_pref"] = schemas.RequirementField{
		Status: "active", Kind: "constraint", Strength: "must", Value: json.RawMessage(`"itx"`),
	}
	x := &execution{input: input, result: record}
	x.result.Outcome = "clarify"
	x.result.Assessments = nil
	fb := x.hardRequirementGateFeedback()
	if !strings.Contains(fb, "must交付核验") || !strings.Contains(fb, "size_pref") {
		t.Fatalf("clarify with unresolved must must be gated: %q", fb)
	}
	// must 已 met：不再拦截。
	x.result.Assessments = []Assessment{{Field: "size_pref", Status: "met", Evidence: []string{"local:case-asus-prime-ap201"}}}
	if fb = x.hardRequirementGateFeedback(); fb != "" {
		t.Fatalf("met must must not gate: %q", fb)
	}
	// proposal 不走此门（预算与 unknown 门覆盖交付拦截）。
	x.result.Assessments = nil
	x.result.Outcome = "proposal"
	if fb = x.hardRequirementGateFeedback(); fb != "" {
		t.Fatalf("proposal must not hit hard-requirement gate: %q", fb)
	}
}

func TestDeliveryGateBoundsAndTurnShortCircuit(t *testing.T) {
	input, record, catalog := completeRecording(t)
	mustBudget(t, &input, "1000", "must")
	x := gateExecution(input, record, withCheaperAlternative(catalog))
	gates := &deliveryGateCounters{}
	if fb := x.deliveryGate("clarify", false, gates, 6, 8); fb != "" {
		t.Fatalf("last repair turns must not gate: %q", fb)
	}
	if gates.total != 0 {
		t.Fatalf("short-circuit consumed budget: %+v", gates)
	}
	fb := x.deliveryGate("clarify", false, gates, 0, 8)
	if fb == "" || gates.total != 1 {
		t.Fatalf("gate must fire and count: %q %+v", fb, gates)
	}
	x.deliveryGate("clarify", false, gates, 1, 8)
	if fb := x.deliveryGate("clarify", false, gates, 2, 8); fb != "" || gates.total != 2 {
		t.Fatalf("budget bound violated: %q %+v", fb, gates)
	}
	*gates = deliveryGateCounters{total: 3}
	if fb := x.deliveryGate("clarify", false, gates, 0, 8); fb != "" {
		t.Fatalf("total cap violated: %q", fb)
	}
}

func TestGatesSmokeInRunnerLoop(t *testing.T) {
	input, record, catalog := completeRecording(t)
	mustBudget(t, &input, "1000", "must")
	catalog = withCheaperAlternative(catalog)
	// 模型反复停在 clarify：预算门最多回环 2 次，turn>=turns-2 短路后返回原答案。
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		record.Outcome = "clarify"
		record.Reply = "预算超了，您看怎么办？"
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 8}).Run(context.Background(), input)
	if err != nil || got.Outcome != "clarify" {
		t.Fatalf("gate loop broke outcome: %+v %v", got, err)
	}
	if got.ModelCalls < 2 || got.ModelCalls > 8 {
		t.Fatalf("gate loop unbounded: %d calls", got.ModelCalls)
	}
	_ = store.CatalogSnapshot{}
}
