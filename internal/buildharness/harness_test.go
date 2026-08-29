package buildharness

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type fakeModel struct {
	outputs  []string
	requests []*model.LLMRequest
}

type unexpectedToolModel struct{}

func (unexpectedToolModel) Name() string { return "unexpected-tool" }

func (unexpectedToolModel) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{
			FunctionCall: &genai.FunctionCall{Name: "search_parts"},
		}}}}, nil)
	}
}

func (m *fakeModel) Name() string { return "fake-builder" }

func (m *fakeModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.requests = append(m.requests, req)
		index := len(m.requests) - 1
		if index >= len(m.outputs) {
			yield(nil, fmt.Errorf("missing fake output"))
			return
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText(m.outputs[index], genai.RoleModel)}, nil)
	}
}

type fixedPlanner struct{ bundle CandidateBundle }

func (p fixedPlanner) Prepare(context.Context, BuildInput) (CandidateBundle, error) {
	return p.bundle, nil
}

type evalFunc func(context.Context, schemas.BuildSelection) (validate.Result, error)

func (f evalFunc) Evaluate(ctx context.Context, selection schemas.BuildSelection) (validate.Result, error) {
	return f(ctx, selection)
}

func TestHarnessOneShotPassHasNoTools(t *testing.T) {
	model := &fakeModel{outputs: []string{draftJSON("psu-a", "build-1")}}
	harness := newTestHarness(t, model, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
		return passingResult("8000.00"), nil
	})
	result, err := harness.Run(context.Background(), BuildInput{Requirement: fixtureRequirement()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Succeeded || result.Attempts != 1 || len(model.requests) != 1 {
		t.Fatalf("result=%+v calls=%d", result, len(model.requests))
	}
	if model.requests[0].Config == nil || len(model.requests[0].Config.Tools) != 0 || len(model.requests[0].Tools) != 0 {
		t.Fatal("Harness v2 不得注册工具")
	}
}

func TestHarnessRepairsFailedRule(t *testing.T) {
	model := &fakeModel{outputs: []string{draftJSON("psu-a", "build-1"), draftJSON("psu-b", "build-2")}}
	harness := newTestHarness(t, model, func(_ context.Context, selection schemas.BuildSelection) (validate.Result, error) {
		if selection.PSU == "psu-a" {
			return validate.Result{Report: schemas.ValidationReport{BuildRef: selection.BuildRef, OverallStatus: schemas.OverallFail,
				Checks: []schemas.CheckResult{{RuleID: schemas.RulePSUHeadroom, Outcome: schemas.OutcomeFail, Severity: schemas.SeverityError}}},
				Quote: testQuote("8000.00")}, nil
		}
		return passingResult("8000.00"), nil
	})
	result, err := harness.Run(context.Background(), BuildInput{Requirement: fixtureRequirement()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Succeeded || result.Attempts != 2 || result.Draft.Selection.PSU != "psu-b" {
		t.Fatalf("定向修复失败:%+v", result)
	}
}

func TestHarnessRepairsMalformedJSONAndStopsAtThree(t *testing.T) {
	t.Run("第二次修正", func(t *testing.T) {
		model := &fakeModel{outputs: []string{"not-json", draftJSON("psu-a", "build-2")}}
		harness := newTestHarness(t, model, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
			return passingResult("8000.00"), nil
		})
		result, err := harness.Run(context.Background(), BuildInput{Requirement: fixtureRequirement()})
		if err != nil || !result.Succeeded || result.Attempts != 2 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("三次上限", func(t *testing.T) {
		model := &fakeModel{outputs: []string{"bad-1", "bad-2", "bad-3", draftJSON("psu-a", "never")}}
		harness := newTestHarness(t, model, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
			return passingResult("8000.00"), nil
		})
		result, err := harness.Run(context.Background(), BuildInput{Requirement: fixtureRequirement()})
		if err != nil || result.Succeeded || result.Attempts != 3 || len(model.requests) != 3 {
			t.Fatalf("result=%+v calls=%d err=%v", result, len(model.requests), err)
		}
	})
}

func TestHarnessStopsRepeatedSelection(t *testing.T) {
	bad := draftJSON("psu-a", "build-1")
	model := &fakeModel{outputs: []string{bad, bad, draftJSON("psu-b", "never")}}
	harness := newTestHarness(t, model, func(_ context.Context, selection schemas.BuildSelection) (validate.Result, error) {
		return validate.Result{Report: schemas.ValidationReport{BuildRef: selection.BuildRef, OverallStatus: schemas.OverallFail,
			Checks: []schemas.CheckResult{{RuleID: schemas.RulePSUHeadroom, Outcome: schemas.OutcomeFail, Severity: schemas.SeverityError}}},
			Quote: testQuote("8000.00")}, nil
	})
	result, err := harness.Run(context.Background(), BuildInput{Requirement: fixtureRequirement()})
	if err != nil || result.Succeeded || result.Attempts != 2 || len(model.requests) != 2 {
		t.Fatalf("死循环门禁失败:result=%+v calls=%d err=%v", result, len(model.requests), err)
	}
}

func TestHarnessRejectsUnexpectedToolCall(t *testing.T) {
	harness := newTestHarness(t, unexpectedToolModel{}, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
		return passingResult("8000.00"), nil
	})
	if _, err := harness.Run(context.Background(), BuildInput{Requirement: fixtureRequirement()}); err == nil {
		t.Fatal("v2 不应接受任何工具调用")
	}
}

func TestBuildPromptCarriesCompleteOutputContract(t *testing.T) {
	prompt, err := buildPrompt(BuildInput{Requirement: fixtureRequirement()}, testBundle(), nil, nil, validate.Result{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		BudgetWindow   budgetWindow `json:"budget_window_cny"`
		OutputContract struct {
			Required []string       `json:"required"`
			Template map[string]any `json:"template"`
		} `json:"output_contract"`
	}
	if err := json.Unmarshal([]byte(prompt), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.OutputContract.Required) != 4 || envelope.OutputContract.Template["requirement_ref"] != "req_harness_v2" ||
		envelope.OutputContract.Template["build_ref"] != "build_harness_v2_2" {
		t.Fatalf("prompt 输出契约不完整: %+v", envelope.OutputContract)
	}
	if envelope.BudgetWindow.LowerCNY != "7200.00" || envelope.BudgetWindow.UpperCNY != "8800.00" {
		t.Fatalf("prompt 预算窗口不正确: %+v", envelope.BudgetWindow)
	}
	selection, ok := envelope.OutputContract.Template["selection"].(map[string]any)
	if !ok || selection["ssd"] == nil {
		t.Fatalf("prompt selection 模板不完整: %#v", selection)
	}
}

func TestFeedbackStatesRequiredBudgetAdjustment(t *testing.T) {
	got := feedback(passingResult("10913.00"), fixtureRequirement())
	if got.BudgetDirection != "over" || got.RequiredAdjustmentCNY != "2113.00" {
		t.Fatalf("预算修复反馈不完整: %+v", got)
	}
}

func TestRepairPromptExcludesCurrentMutableSKU(t *testing.T) {
	previous := testDraft("psu-a")
	plan := &RepairPlan{Mutable: []schemas.Category{schemas.CategoryPSU}, Reason: "budget_over"}
	prompt, err := buildPrompt(BuildInput{Requirement: fixtureRequirement()}, testBundle(), &previous, plan, passingResult("12000.00"), 2)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		CandidateBundle CandidateBundle `json:"candidate_bundle"`
	}
	if err := json.Unmarshal([]byte(prompt), &envelope); err != nil {
		t.Fatal(err)
	}
	group := bundleGroup(&envelope.CandidateBundle, schemas.CategoryPSU)
	if group == nil || len(group.Candidates) != 1 || group.Candidates[0].SKU != "psu-b" {
		t.Fatalf("修复轮候选仍包含当前 SKU: %+v", group)
	}
}

func TestFeasibleBudgetPreferenceRejectsIncompatibleCombination(t *testing.T) {
	h := &runner{eval: evalFunc(func(_ context.Context, selection schemas.BuildSelection) (validate.Result, error) {
		if selection.Memory == "memory-b" {
			return validate.Result{Report: schemas.ValidationReport{OverallStatus: schemas.OverallFail}}, nil
		}
		total := "8000.00"
		if selection.GPU != nil && *selection.GPU == "gpu-b" {
			total = "7500.00"
		}
		return validate.Result{Report: schemas.ValidationReport{OverallStatus: schemas.OverallPass}, Quote: testQuote(total)}, nil
	})}
	requirement := fixtureRequirement()
	requirement.BudgetCNY = 7500
	requirement.BudgetFlex = 0
	preferred, checked, err := h.feasibleBudgetPreference(context.Background(), requirement, testDraft("psu-a").Selection,
		testBundle(), []schemas.Category{schemas.CategoryGPU, schemas.CategoryMemory})
	if err != nil {
		t.Fatal(err)
	}
	if checked != 3 || preferred[schemas.CategoryGPU] != "gpu-b" || preferred[schemas.CategoryMemory] != "memory-a" {
		t.Fatalf("未筛掉不兼容预算组合: checked=%d preferred=%v", checked, preferred)
	}
}

func TestEnrichCandidateRationaleUsesSelectedSemanticEvidence(t *testing.T) {
	draft := testDraft("psu-a")
	bundle := testBundle()
	group := bundleGroup(&bundle, schemas.CategoryCase)
	group.Candidates[0].MatchText = "白色海景房风格"
	enrichCandidateRationale(&draft, bundle, fixtureRequirement())
	if draft.Rationale[string(schemas.CategoryCase)] != "偏好匹配：白色海景房风格" {
		t.Fatalf("未补充选中候选的语义理由: %+v", draft.Rationale)
	}
}

func TestEnrichCandidateRationaleExplainsUnverifiedSoftPreference(t *testing.T) {
	draft := testDraft("psu-a")
	requirement := fixtureRequirement()
	requirement.NoisePref = schemas.NoisePrefSilent
	requirement.Notes = "想要白色海景房"
	enrichCandidateRationale(&draft, testBundle(), requirement)
	if !strings.Contains(draft.Rationale[string(schemas.CategoryCase)], "白色版本需购买前核对") ||
		!strings.Contains(draft.Rationale[string(schemas.CategoryGPU)], "安静低噪") {
		t.Fatalf("未如实展示软偏好核对说明: %+v", draft.Rationale)
	}
}

func newTestHarness(t *testing.T, llm model.LLM, evaluator evalFunc) Harness {
	t.Helper()
	harness, err := New(Config{Model: llm, Planner: fixedPlanner{bundle: testBundle()},
		Repairer: NewRepairPlanner(), Eval: evaluator})
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

func draftJSON(psu, ref string) string {
	return fmt.Sprintf(`{"schema_version":1,"requirement_ref":"req-1","build_ref":%q,"selection":{"cpu":"cpu-a","gpu":"gpu-a","motherboard":"motherboard-a","memory":"memory-a","ssd":[{"sku":"ssd-a","quantity":1}],"psu":%q,"case":"case-a","cooler":"cooler-a"}}`, ref, psu)
}

func testDraft(psu string) schemas.BuildDraft {
	draft, err := schemas.DecodeBuildDraft([]byte(draftJSON(psu, "build-1")))
	if err != nil {
		panic(err)
	}
	return draft
}

func passingResult(total string) validate.Result {
	return validate.Result{Report: schemas.ValidationReport{BuildRef: "build-1", OverallStatus: schemas.OverallPass}, Quote: testQuote(total)}
}

func testQuote(total string) validate.Quote {
	price := "1000.00"
	quote := validate.Quote{SnapshotDate: "2026-08-27", TotalCNY: total}
	for _, category := range schemas.AllCategories {
		quote.Lines = append(quote.Lines, validate.QuoteLine{Category: category, SKU: string(category) + "-a", Quantity: 1,
			UnitPriceCNY: &price, SubtotalCNY: &price})
	}
	return quote
}
