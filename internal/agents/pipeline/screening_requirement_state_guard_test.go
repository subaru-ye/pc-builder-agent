package pipeline

// Spec 6 认证（NO-GO）四项根因的守卫级回归：空已有件证据红线、数值系统默认
// 拷贝拦截、owned_parts 点路径形状归一。全部零模型、纯 prepare 层断言。
import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func guardSource(quote string) schemas.RequirementSource {
	return schemas.RequirementSource{Kind: "chat", MessageID: "guard-regression", Quote: quote}
}

func op(field string, value json.RawMessage) schemas.RequirementOperation {
	return schemas.RequirementOperation{Op: "set", Field: field, Value: value, Evidence: "stated", Quote: ""}
}

// ④ 泛购买语句不得写成空已有件；“全部新买/无已有件”原话才允许。
func TestPrepareEmptyExistingPartsRequiresClearEvidence(t *testing.T) {
	state := schemas.NewRequirementState()
	// 反例：认证批次 D 实录失败原话，无任何采购范围表达。
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		op("existing_parts", json.RawMessage(`[]`)),
	}}, guardSource("帮我配台电脑，还要显示器和键鼠"))
	if len(out.Operations) != 0 {
		t.Fatalf("无证据空已有件必须降级: %+v", out.Operations)
	}
	if len(out.Observations) != 1 || !strings.Contains(out.Observations[0].Reason, "全部新买") || out.Observations[0].Field != "existing_parts" {
		t.Fatalf("原文必须保留为观察: %+v", out.Observations)
	}
	// 正例：明确“全部新买/没有已有件”（quote 逐字摘录本轮原文，与真实输出一致）。
	for _, quote := range []string{"预算8000，配件全新买", "全部新买", "我没有已有件"} {
		out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Evidence: "stated", Quote: quote},
		}}, guardSource(quote))
		if len(out.Operations) != 1 || out.Operations[0].Field != "existing_parts" {
			t.Fatalf("明确表达时应写入: quote=%q out=%+v", quote, out)
		}
	}
	// 非空数组（登记已有品类）不受该红线约束。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`["gpu"]`), Evidence: "stated", Quote: "有张旧显卡"},
	}}, guardSource("有张旧显卡"))
	if len(out.Operations) != 1 {
		t.Fatalf("非空已有件不得被空数组红线拦截: %+v", out)
	}
}

// ② 数值型系统默认（budget_flex 0.1）不是用户事实；本轮原话出现该值才接受。
func TestPrepareRejectsCopiedNumericSystemDefault(t *testing.T) {
	state := schemas.NewRequirementState()
	// 反例：认证批次 B 实录失败——0.1 来自确定性参考，quote 与弹性无关。
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`), Evidence: "stated", Quote: "预算改9000"},
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`0.1`), Evidence: "stated", Quote: "预算改9000"},
	}}, guardSource("预算改9000，然后开始吧"))
	if len(out.Operations) != 1 || out.Operations[0].Field != "budget_cny" {
		t.Fatalf("预算应保留、默认拷贝必须降级: %+v", out.Operations)
	}
	if len(out.Observations) != 1 || !strings.Contains(out.Observations[0].Reason, "系统默认") {
		t.Fatalf("默认拷贝须保留观察: %+v", out.Observations)
	}
	// 正例：本轮原话出现该值。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`0.1`), Evidence: "stated", Quote: "浮动按0.1就行"},
	}}, guardSource("预算浮动按0.1就行"))
	if len(out.Operations) != 1 {
		t.Fatalf("原话明确给出的默认值应写入: %+v", out.Operations)
	}
	// 非默认值不受约束。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`0.2`), Evidence: "stated", Quote: "预算改9000"},
	}}, guardSource("预算改9000"))
	if len(out.Operations) != 1 {
		t.Fatalf("非默认值不得被默认拷贝红线拦截: %+v", out.Operations)
	}
}

// ③ 认证批次 B 实录：单段点路径 owned_parts.gpu 携带对象/字符串时归一为
// owned_parts 数组合并；型号不 grounded 或类目不符时不归一（维持原拒绝路径）。
func TestPrepareNormalizesOwnedCategoryDotPathShapes(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["existing_parts"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`["gpu"]`), Strength: "must", Scope: "session"}
	for name, value := range map[string]json.RawMessage{
		"对象形状": json.RawMessage(`{"category":"gpu","model":"4070 Super"}`),
		"字符串形状": json.RawMessage(`"4070 Super"`),
	} {
		out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "owned_parts.gpu", Value: value, Evidence: "stated", Quote: "显卡是4070 Super"},
		}}, guardSource("显卡是4070 Super"))
		if len(out.Operations) != 1 || out.Operations[0].Field != "owned_parts" {
			t.Fatalf("%s 应归一为 owned_parts: %+v", name, out.Operations)
		}
		var owned []schemas.OwnedPart
		if err := json.Unmarshal(out.Operations[0].Value, &owned); err != nil || len(owned) != 1 || owned[0].Model != "4070 Super" || string(owned[0].Category) != "gpu" {
			t.Fatalf("%s 归一结果错误: %s", name, out.Operations[0].Value)
		}
	}
	// 类目与路径不一致：不归一、不静默改写。
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "owned_parts.gpu", Value: json.RawMessage(`{"category":"cpu","model":"i5"}`), Evidence: "stated", Quote: "显卡是4070 Super"},
	}}, guardSource("显卡是4070 Super"))
	if len(out.Operations) != 0 {
		t.Fatalf("类目不符必须拒绝: %+v", out.Operations)
	}
}

// ⑤ 修复二跑实录：模型把型号并入 owned_parts 的同时顺手 remove
// existing_parts——无撤销表达的关键字移除必须降级为观察。
func TestPrepareRemoveExistingPartsRequiresRemovalEvidence(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["existing_parts"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`["gpu"]`), Strength: "must", Scope: "session"}
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "owned_parts", Value: json.RawMessage(`[{"category":"gpu","model":"4070 Super"}]`), Evidence: "stated", Quote: "显卡是4070 Super"},
		{Op: "remove", Field: "existing_parts", Quote: "显卡是4070 Super"},
	}}, guardSource("显卡是4070 Super"))
	if len(out.Operations) != 1 || out.Operations[0].Op != "set" || out.Operations[0].Field != "owned_parts" {
		t.Fatalf("型号写入应保留、无证据移除必须降级: %+v", out.Operations)
	}
	if len(out.Observations) != 1 || !strings.Contains(out.Observations[0].Reason, "撤销已有件") {
		t.Fatalf("移除原文必须保留为观察: %+v", out.Observations)
	}
	// 正例：用户明确表达不再保留旧件。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "remove", Field: "existing_parts", Quote: "旧件都不要了"},
	}}, guardSource("旧件都不要了，全部换成新的"))
	if len(out.Operations) != 1 || out.Operations[0].Op != "remove" {
		t.Fatalf("明确撤回表达应执行移除: %+v", out.Operations)
	}
}
