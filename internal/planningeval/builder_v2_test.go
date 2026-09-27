package planningeval

// Builder v2 判卷增量与 fixture 完整性的零依赖单测：不触数据库、不触模型。
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func builderTraceReport(request json.RawMessage) *Report {
	trace := Trace{Role: "builder", Request: request}
	step := StepRecord{Trace: []Trace{trace}}
	record := CaseRecord{Steps: []StepRecord{step}}
	return &Report{Cases: []CaseRecord{record}}
}

func TestFrozenConstraintsGrading(t *testing.T) {
	constraints := json.RawMessage(`{
		"spec": {"schema_version":2,"budget_cny":4000,"budget_flex":0.1,
			"use_case":{"type":"general","titles":[]},
			"configuration_scope":["tower"],
			"constraint_strengths":{"budget_cny":"must"}},
		"defaults": [{"field":"budget_flex","value":0.1,"origin":"system_default"}]
	}`)
	// 通过 JSON 往返构造真实 PlanningInput，避免手写嵌套结构。
	raw, err := json.Marshal(map[string]any{
		"planning_input": map[string]any{
			"schema_version":        2,
			"effective_constraints": json.RawMessage(constraints),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	step := StepRecord{}
	if err := json.Unmarshal(raw, &step); err != nil {
		t.Fatal(err)
	}
	if step.PlanningInput == nil || step.PlanningInput.EffectiveConstraints == nil {
		t.Fatal("fixture must decode effective_constraints into PlanningInput")
	}

	passGold := &FrozenConstraintsGold{
		Spec: map[string]json.RawMessage{
			"budget_cny":                      json.RawMessage(`4000`),
			"budget_flex":                     json.RawMessage(`0.1`),
			"use_case.type":                   json.RawMessage(`"general"`),
			"configuration_scope":             json.RawMessage(`["tower"]`),
			"constraint_strengths.budget_cny": json.RawMessage(`"must"`),
		},
		Defaults: []ReqV2DefaultGold{{Field: "budget_flex", Value: json.RawMessage(`0.1`), Origin: "system_default"}},
	}
	for name, ok := range runFrozenGold(t, &step, passGold) {
		if !ok {
			t.Errorf("%s should pass", name)
		}
	}

	// 用户显式设定的字段不得再以默认出现：expanded default 上的 absent 断言必须失败。
	strictGold := &FrozenConstraintsGold{
		Spec:           map[string]json.RawMessage{"budget_cny": json.RawMessage(`4000`)},
		AbsentDefaults: []string{"budget_flex"},
	}
	if checks := runFrozenGold(t, &step, strictGold); checks["frozen_constraints_default_absent:budget_flex"] {
		t.Error("absent_default on expanded default should fail")
	}

	// 错误的 spec 值必须失败。
	wrongGold := &FrozenConstraintsGold{Spec: map[string]json.RawMessage{"budget_cny": json.RawMessage(`5000`)}}
	if checks := runFrozenGold(t, &step, wrongGold); checks["frozen_constraints_spec:budget_cny"] {
		t.Error("wrong spec value must fail")
	}

	// 无冻结载荷必须失败（历史载荷无 effective_constraints）。
	bare := StepRecord{}
	if checks := runFrozenGold(t, &bare, passGold); checks["frozen_constraints_present"] {
		t.Error("missing effective_constraints must fail")
	}
}

func runFrozenGold(t *testing.T, step *StepRecord, gold *FrozenConstraintsGold) map[string]bool {
	t.Helper()
	checks := map[string]bool{}
	gradeFrozenConstraints(step, gold, func(name string, pass bool, _ any) { checks[name] = pass })
	return checks
}

func builderContractRequest(contents string) json.RawMessage {
	return json.RawMessage(`{
		"model": "offline-oracle-no-provider",
		"config": {
			"system_instruction": {"parts":[{"text":"你是装机顾问"}]},
			"tools": [{"functionDeclarations":[{"name":"planning_action"}]}],
			"temperature": 0.7
		},
		"contents": ` + contents + `
	}`)
}

func TestAttachToolContract(t *testing.T) {
	report := builderTraceReport(builderContractRequest(`[{"parts":[{"text":"per-case input"}]}]`))
	AttachToolContract(report)
	if report.ToolContract == nil {
		t.Fatal("tool contract must be extracted from the first builder trace")
	}
	if report.ToolContract.Model != "offline-oracle-no-provider" {
		t.Errorf("model = %q", report.ToolContract.Model)
	}
	first := report.ToolContract.SHA256

	// 合同本体（instruction+tools）不变、per-case 输入变化时指纹必须稳定。
	report2 := builderTraceReport(builderContractRequest(`[{"parts":[{"text":"different case input"}]}]`))
	AttachToolContract(report2)
	if report2.ToolContract.SHA256 != first {
		t.Errorf("contract hash must ignore per-case contents: %s vs %s", first, report2.ToolContract.SHA256)
	}

	// 合同变化（工具声明不同）必须改变指纹。
	changed := json.RawMessage(`{
		"model": "offline-oracle-no-provider",
		"config": {
			"system_instruction": {"parts":[{"text":"你是装机顾问"}]},
			"tools": [{"functionDeclarations":[{"name":"named_tools_action"}]}]
		}
	}`)
	report3 := builderTraceReport(changed)
	AttachToolContract(report3)
	if report3.ToolContract.SHA256 == first {
		t.Error("tool declaration change must change the contract hash")
	}

	// 无 builder 请求时不得虚构指纹。
	empty := &Report{}
	AttachToolContract(empty)
	if empty.ToolContract != nil {
		t.Error("no builder trace must leave ToolContract nil")
	}
}

// TestLoadBuilderV2Fixture 在提交时即拒绝 fixture 漂移：Load 严格解码 +
// provenance 哈希校验，零依赖。
func TestLoadBuilderV2Fixture(t *testing.T) {
	const dir = "testdata/builder-v2-20260925"
	raw, err := os.ReadFile(filepath.Join(dir, "suite.json"))
	if err != nil {
		t.Fatal(err)
	}
	suite, err := Load(raw)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(suite.Cases) != 10 {
		t.Fatalf("cases = %d, want 10", len(suite.Cases))
	}
	provenance, err := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyProvenance(raw, provenance); err != nil {
		t.Fatalf("provenance: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range suite.Cases {
		if seen[c.ID] {
			t.Errorf("duplicate case %s", c.ID)
		}
		seen[c.ID] = true
	}
}
