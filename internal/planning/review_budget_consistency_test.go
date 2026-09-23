package planning

// 确认/Builder gate v2 冻结约束一致性:确认事务冻结的 Builder 有效约束必须
// 与 review_spec 展示的系统默认一致。重点场景"预算 7000、弹性未填写":
// 核定预览的上限(7000×1.1=7700)必须等于规划侧实际预算门槛。
// 默认值来源保持 system_default:默认只进入有效约束展示,不改写成用户事实。
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func reviewConsistencyInput(t *testing.T, flexStated bool, flexValue string) schemas.PlanningInput {
	t.Helper()
	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: []byte(`7000`), Strength: "must"},
		{Op: "set", Field: "use_case.type", Value: []byte(`"gaming"`), Kind: "fact", Strength: "must"},
		{Op: "set", Field: "use_case.resolution", Value: []byte(`"2K"`), Kind: "fact", Strength: "must"},
		{Op: "set", Field: "existing_parts", Value: []byte(`[]`), Kind: "fact", Strength: "must"},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算7000，2K 游戏，全部新买"})
	if err != nil {
		t.Fatal(err)
	}
	if flexStated {
		state, err = schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_flex", Value: []byte(flexValue), Strength: "must"},
		}}, schemas.RequirementSource{Kind: "edit", Quote: "弹性" + flexValue})
		if err != nil {
			t.Fatal(err)
		}
	}
	return schemas.PlanningInput{SchemaVersion: 2, State: state}
}

// previewCeilingCNY 与产品核定预览同一口径:RequirementReviewSpec 物化后的
// budget_cny×(1+budget_flex) 取整(product.budgetCeilingCNY 同源)。
func previewCeilingCNY(t *testing.T, state schemas.RequirementState) *big.Rat {
	t.Helper()
	spec, readiness, err := schemas.RequirementReviewSpec(state)
	if err != nil || !readiness.ConfirmationEligible {
		t.Fatalf("夹具应可核定: %+v %v", readiness, err)
	}
	decoded, err := schemas.DecodeRequirementSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	upper := new(big.Rat).SetInt64(int64(decoded.BudgetCNY))
	flex, _ := new(big.Rat).SetString(fmt.Sprintf("%v", decoded.BudgetFlex))
	if flex != nil && flex.Sign() > 0 {
		upper.Mul(upper, new(big.Rat).Add(big.NewRat(1, 1), flex))
	}
	return upper
}

func TestBuilderBudgetGateMatchesReviewPreview(t *testing.T) {
	for _, tc := range []struct {
		name        string
		flexStated  bool
		flexValue   string
		wantCeiling string // 精确有理数(planing gate 用 big.Rat 比较)
	}{
		{"弹性未填写用系统默认0.1", false, "", "7700"},
		{"显式0弹性按陈述执行", true, "0", "7000"},
		{"显式0.2按陈述执行", true, "0.2", "8400"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := reviewConsistencyInput(t, tc.flexStated, tc.flexValue)
			x := &execution{input: input}
			gate, ok := x.budgetCeiling()
			if !ok {
				t.Fatal("must 预算应产生硬上限")
			}
			want, _ := new(big.Rat).SetString(tc.wantCeiling)
			if gate.Cmp(want) != 0 {
				t.Fatalf("规划侧预算门槛=%s want %s(与核定预览不一致)", gate.RatString(), tc.wantCeiling)
			}
			// 与核定预览(RequirementReviewSpec 物化)同口径比较。
			if preview := previewCeilingCNY(t, input.State); preview.Cmp(gate) != 0 {
				t.Fatalf("预览上限=%s 与规划门槛=%s 不一致", preview.RatString(), gate.RatString())
			}
		})
	}
}

// TestBudgetFlexDefaultKeepsSystemDefaultOrigin 证明默认值来源保留:
// 弹性未填写时 readiness 以 origin=system_default 列出默认,用户草稿中的
// budget_flex 保持未知(默认不被改写成用户事实);规划侧会计按同一默认执行。
func TestBudgetFlexDefaultKeepsSystemDefaultOrigin(t *testing.T) {
	input := reviewConsistencyInput(t, false, "")
	state := input.State
	if field := state.Fields["budget_flex"]; field.Status != "unknown" || len(field.Value) != 0 {
		t.Fatalf("系统默认不得写入用户草稿字段: %+v", field)
	}
	readiness, err := schemas.EvaluateRequirementReadiness(state)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range readiness.EffectiveDefaults {
		if d.Field == "budget_flex" {
			found = true
			if d.Origin != "system_default" || d.Value == nil {
				t.Fatalf("默认来源必须是 system_default: %+v", d)
			}
			var value float64
			if err := mustUnmarshal(d.Value, &value); err != nil || value != schemas.DefaultBudgetFlex {
				t.Fatalf("默认值=%v want %v err=%v", value, schemas.DefaultBudgetFlex, err)
			}
		}
	}
	if !found {
		t.Fatal("弹性未填写时应列出 budget_flex 系统默认")
	}
	// 规划侧会计按同一默认执行(而非 0)。
	x := &execution{input: input}
	if got := x.accountingSpec().BudgetFlex; got != schemas.DefaultBudgetFlex {
		t.Fatalf("规划侧弹性=%v want 系统默认 %v", got, schemas.DefaultBudgetFlex)
	}
}

func mustUnmarshal(raw []byte, dst any) error {
	return json.Unmarshal(raw, dst)
}

// frozenConstraintsInput 组装带冻结有效约束的载荷:constraints 为确认事务
// 冻结的 review_spec(含系统默认),state 保持用户事实;performance_goal 等
// 默认只在冻结约束中出现。
func frozenConstraintsInput(t *testing.T, budget, flex, performanceGoal string) schemas.PlanningInput {
	t.Helper()
	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(budget), Strength: "must"},
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Kind: "fact", Strength: "must"},
		{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"2K"`), Kind: "fact", Strength: "must"},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Kind: "fact", Strength: "must"},
	}}, schemas.RequirementSource{Kind: "edit", Quote: "预算7000，2K 游戏，全部新买"})
	if err != nil {
		t.Fatal(err)
	}
	specValues := map[string]any{
		"schema_version":       schemas.RequirementSpecSchemaVersion,
		"budget_cny":           7000,
		"budget_flex":          json.RawMessage(flex),
		"configuration_scope":  []string{"tower"},
		"constraint_strengths": map[string]string{"budget_cny": "must", "budget_flex": "must"},
		"use_case":             map[string]any{"type": "gaming", "resolution": "2K", "performance_goal": performanceGoal},
		"existing_parts":       []string{},
	}
	specRaw, err := json.Marshal(specValues)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.DecodeRequirementSpec(specRaw); err != nil {
		t.Fatalf("冻结约束夹具必须是合法 RequirementSpec: %v", err)
	}
	return schemas.PlanningInput{SchemaVersion: 2, State: state, EffectiveConstraints: &schemas.EffectiveConstraints{
		Spec: specRaw,
		Defaults: []schemas.RequirementDefault{
			{Field: "budget_flex", Value: json.RawMessage(flex), Origin: "system_default"},
			{Field: "performance_goal", Value: json.RawMessage(`"` + performanceGoal + `"`), Origin: "system_default"},
		},
	}}
}

// firstModelRequest 捕获 Builder 模型第一条请求(输入 JSON 与系统指令)。
func firstModelRequest(t *testing.T, input schemas.PlanningInput) *model.LLMRequest {
	t.Helper()
	var firstRequest atomic.Value
	m := &scriptedModel{respond: func(call int, req *model.LLMRequest) *genai.Content {
		if call == 1 {
			firstRequest.Store(req)
		}
		return genai.NewContentFromText(`{"outcome":"clarify","reply":"需要更多信息。","issues":["missing"],"assessments":[]}`, genai.RoleModel)
	}}
	_, _ = (Runner{Model: m, Catalog: recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{ID: 1, SnapshotDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}}, MaxTurns: 1}).Run(context.Background(), input)
	req, ok := firstRequest.Load().(*model.LLMRequest)
	if !ok {
		t.Fatal("模型未收到任何请求")
	}
	return req
}

// TestBuilderConsumesFrozenEffectiveConstraints 证明 Builder 的模型输入与
// 确定性门槛都读取确认事务冻结的有效选型约束:gaming 未填写 performance_goal
// 时预览与 Builder 都使用 balanced;冻结后即使系统默认规则改变(此处用一份
// 按旧规则冻结的载荷模拟),旧 run 的执行语义也不变。
func TestBuilderConsumesFrozenEffectiveConstraints(t *testing.T) {
	input := frozenConstraintsInput(t, "7000", "0.1", "balanced")
	x := &execution{input: input}
	if got := x.accountingSpec().BudgetFlex; got != 0.1 {
		t.Fatalf("冻结弹性=%v want 0.1", got)
	}
	gate, ok := x.budgetCeiling()
	if !ok {
		t.Fatal("冻结 must 预算应产生硬上限")
	}
	if want, _ := new(big.Rat).SetString("7700"); gate.Cmp(want) != 0 {
		t.Fatalf("冻结门槛=%s want 7700", gate.RatString())
	}
	modelInput := firstModelRequest(t, input).Contents[0].Parts[0].Text
	if !strings.Contains(modelInput, `"effective_constraints"`) || !strings.Contains(modelInput, `"performance_goal":"balanced"`) {
		t.Fatal("Builder 模型输入必须携带冻结的有效约束(gaming 默认 balanced)")
	}

	// 旧默认规则冻结的载荷:弹性 0.05、performance_goal=quality。当前代码
	// 默认规则已"改变"(0.1/balanced),执行语义必须仍读冻结值。
	legacy := frozenConstraintsInput(t, "7000", "0.05", "quality_first")
	x2 := &execution{input: legacy}
	if got := x2.accountingSpec().BudgetFlex; got != 0.05 {
		t.Fatalf("旧规则冻结的弹性=%v want 0.05(不得按当前默认重新推导)", got)
	}
	gate2, ok := x2.budgetCeiling()
	if !ok {
		t.Fatal("冻结 must 预算应产生硬上限")
	}
	if want, _ := new(big.Rat).SetString("7350"); gate2.Cmp(want) != 0 {
		t.Fatalf("旧规则冻结门槛=%s want 7350", gate2.RatString())
	}
	modelInput2 := firstModelRequest(t, legacy).Contents[0].Parts[0].Text
	if !strings.Contains(modelInput2, `"performance_goal":"quality_first"`) {
		t.Fatal("旧规则冻结的 performance_goal 不得被当前默认改写")
	}
}

// TestReviewSpecMaterializesGamingPerformanceGoal:核定预览对 gaming 未填写
// performance_goal 物化 balanced,默认来源保留 system_default,用户草稿不被
// 改写;冻结约束与预览逐字节一致(store 确认事务直接冻结该 spec)。
func TestReviewSpecMaterializesGamingPerformanceGoal(t *testing.T) {
	input := reviewConsistencyInput(t, false, "")
	spec, readiness, err := schemas.RequirementReviewSpec(input.State)
	if err != nil || !readiness.ConfirmationEligible {
		t.Fatalf("核定预览失败: %v", err)
	}
	decoded, err := schemas.DecodeRequirementSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.UseCase.PerformanceGoal != schemas.PerformanceGoalBalanced {
		t.Fatalf("gaming 未填写 performance_goal 预览应为 balanced,得到 %q", decoded.UseCase.PerformanceGoal)
	}
	found := false
	for _, d := range readiness.EffectiveDefaults {
		if d.Field == "performance_goal" {
			found = true
			if d.Origin != "system_default" {
				t.Fatalf("默认来源=%q want system_default", d.Origin)
			}
		}
	}
	if !found {
		t.Fatal("readiness 应列出 performance_goal 系统默认")
	}
	if field := input.State.Fields["use_case.performance_goal"]; field.Status != "unknown" {
		t.Fatalf("系统默认不得写入用户草稿: %+v", field)
	}
	// 冻结的 effective_constraints.spec 与核定预览完全一致。
	var constraints schemas.EffectiveConstraints
	payload, err := schemas.PlanningBuilderInput("run-1", input.State, &schemas.EffectiveConstraints{Spec: spec, Defaults: readiness.EffectiveDefaults}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var parsed schemas.PlanningInput
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatal(err)
	}
	constraints = *parsed.EffectiveConstraints
	if !bytes.Equal(constraints.Spec, spec) {
		t.Fatal("冻结约束的 spec 与核定预览不一致")
	}
	if len(constraints.Defaults) != len(readiness.EffectiveDefaults) {
		t.Fatalf("冻结默认清单=%d want %d", len(constraints.Defaults), len(readiness.EffectiveDefaults))
	}
}

// TestRunnerRejectsCorruptedFrozenConstraints:冻结约束损坏时 Runner.Run 必须
// 在调用任何模型或工具之前返回错误——不回退原始 state、不关闭预算门槛继续
// 生成;模型调用数为 0。
func TestRunnerRejectsCorruptedFrozenConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, spec string
	}{
		{"非法枚举值", `{"schema_version":2,"budget_cny":7000,"budget_flex":0.1,"configuration_scope":["tower"],"use_case":{"type":"gaming","resolution":"2K","performance_goal":"ultra"},"existing_parts":[]}`},
		{"截断JSON", `{"schema_version":2,"budget_cny":7000`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := frozenConstraintsInput(t, "7000", "0.1", "balanced")
			input.EffectiveConstraints.Spec = json.RawMessage(tc.spec)
			m := &scriptedModel{respond: func(int, *model.LLMRequest) *genai.Content {
				t.Fatal("冻结约束损坏时模型不得被调用")
				return nil
			}}
			result, err := (Runner{Model: m, Catalog: recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{ID: 1, SnapshotDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}}, MaxTurns: 4}).Run(context.Background(), input)
			if err == nil || !strings.Contains(err.Error(), "冻结的有效选型约束无效") {
				t.Fatalf("损坏的冻结约束应返回错误,得到 %v", err)
			}
			if result.Outcome == "ready" || result.Draft != nil {
				t.Fatalf("不得在损坏载荷上继续生成: %+v", result.Outcome)
			}
			if m.calls != 0 {
				t.Fatalf("模型调用数=%d want 0", m.calls)
			}
		})
	}
	// 完整性对照:同一夹具的合法冻结约束正常进入模型循环(守卫没有误伤)。
	valid := frozenConstraintsInput(t, "7000", "0.1", "balanced")
	m := &scriptedModel{respond: func(int, *model.LLMRequest) *genai.Content {
		return genai.NewContentFromText(`{"outcome":"clarify","reply":"需要更多信息。","issues":["missing"],"assessments":[]}`, genai.RoleModel)
	}}
	if _, err := (Runner{Model: m, Catalog: recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{ID: 1, SnapshotDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}}, MaxTurns: 1}).Run(context.Background(), valid); err != nil {
		t.Fatalf("合法冻结约束不得被拒绝: %v", err)
	}
	if m.calls == 0 {
		t.Fatal("合法载荷应进入模型循环")
	}
}

// TestModelInputExpressesFrozenPerformanceGoal:原始 state 中
// performance_goal 未知而冻结值为 balanced 时,模型输入必须明确表达该有效
// 优先级——独立成块的 effective_constraints(含冻结 spec 与默认来源),且系统
// 指令明确"选型约束以冻结 spec 为准、state 只用于事实溯源"。
func TestModelInputExpressesFrozenPerformanceGoal(t *testing.T) {
	input := frozenConstraintsInput(t, "7000", "0.1", "balanced")
	if field := input.State.Fields["use_case.performance_goal"]; field.Status != "unknown" {
		t.Fatalf("夹具的原始 state 应保持 performance_goal 未知: %+v", field)
	}
	var firstRequest atomic.Value
	m := &scriptedModel{respond: func(call int, req *model.LLMRequest) *genai.Content {
		if call == 1 {
			firstRequest.Store(req)
		}
		return genai.NewContentFromText(`{"outcome":"clarify","reply":"需要更多信息。","issues":["missing"],"assessments":[]}`, genai.RoleModel)
	}}
	_, _ = (Runner{Model: m, Catalog: recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{ID: 1, SnapshotDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}}, MaxTurns: 1}).Run(context.Background(), input)
	req, ok := firstRequest.Load().(*model.LLMRequest)
	if !ok || len(req.Contents) == 0 || len(req.Contents[0].Parts) == 0 {
		t.Fatal("模型未收到任何请求")
	}
	modelInput := req.Contents[0].Parts[0].Text
	// 独立成块的冻结约束:模型无需从 state 的 unknown 状态推断优先级。
	if !strings.Contains(modelInput, `"effective_constraints":{"spec":{`) {
		t.Fatal("模型输入应显式携带独立成块的 effective_constraints")
	}
	if !strings.Contains(modelInput, `"performance_goal":"balanced"`) {
		t.Fatal("模型输入应明确表达冻结的 performance_goal=balanced")
	}
	if strings.Contains(modelInput, `"use_case.performance_goal":{"status":"active"`) {
		t.Fatal("未知 priority 不得在原始 state 中被伪造成用户事实")
	}
	// 系统指令明确冻结权威与溯源边界。
	var system string
	if req.Config != nil && req.Config.SystemInstruction != nil {
		for _, part := range req.Config.SystemInstruction.Parts {
			system += part.Text
		}
	}
	if !strings.Contains(system, "以 input.effective_constraints.spec 为唯一权威") ||
		!strings.Contains(system, "只用于用户事实与来源溯源") {
		t.Fatalf("系统指令未声明冻结权威与溯源边界: %s", system)
	}
}
