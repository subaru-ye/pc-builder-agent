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

// conversations15 实录(cv-ownership-unknown-keeps-gate r1/r3)：否定/待确认
// 语境("先别当作全部新买""还得回家确认")不是清空证据,空数组必须拦截。
func TestPrepareNegatedExistingPartsIsNotClearedEvidence(t *testing.T) {
	state := schemas.NewRequirementState()
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Evidence: "stated", Quote: "先别当作全部新买"},
	}}, guardSource("旧配件能不能复用还得回家确认，先别当作全部新买"))
	if len(out.Operations) != 0 {
		t.Fatalf("待确认语境的空已有件必须拦截: %+v", out.Operations)
	}
	if len(out.Observations) != 1 || !strings.Contains(out.Observations[0].Reason, "全部新买") {
		t.Fatalf("拦截后原文保留为观察: %+v", out.Observations)
	}
	// 正例(cv-ownership-unknown 第3轮)：确认后明确全部新买。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Evidence: "stated", Quote: "主机配件全部新买"},
	}}, guardSource("确认过了没有可复用的旧件，主机配件全部新买"))
	if len(out.Operations) != 1 || out.Operations[0].Field != "existing_parts" {
		t.Fatalf("确认后的全部新买应写入: %+v", out.Operations)
	}
	// 正例(cv-owned-parts-withdrawal 第2轮)：撤销+全部新买混合语境仍以新值为准。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Evidence: "stated", Quote: "主机配件全部新买"},
	}}, guardSource("旧显卡不复用了，主机配件全部新买"))
	if len(out.Operations) != 1 {
		t.Fatalf("明确全部新买不得被否定词误拦: %+v", out.Operations)
	}
}

// conversations15 实录(cv-premature-ready r2)：普通预算金额被写成零弹性。
// 无严格表达时丢弃该写入,不留观察(金额本身已由 budget_cny 记录)。
func TestPrepareZeroBudgetFlexRequiresStrictEvidence(t *testing.T) {
	state := schemas.NewRequirementState()
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`7500`), Evidence: "stated", Quote: "预算7500"},
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`0`), Evidence: "stated", Quote: "预算7500"},
	}}, guardSource("预算7500，1080p，主机配件全新买"))
	if len(out.Operations) != 1 || out.Operations[0].Field != "budget_cny" {
		t.Fatalf("零弹性幻觉必须丢弃且不留观察: ops=%+v obs=%+v", out.Operations, out.Observations)
	}
	if len(out.Observations) != 0 {
		t.Fatalf("丢弃路径不产生观察: %+v", out.Observations)
	}
	// 正例(cv-budget-flex-revision 第2轮)：明确的严格不超表达。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`0`), Evidence: "stated", Quote: "预算不能超，一分都不能超"},
	}}, guardSource("预算不能超，一分都不能超"))
	if len(out.Operations) != 1 || out.Operations[0].Field != "budget_flex" {
		t.Fatalf("明确严格表达应写入: %+v", out.Operations)
	}
}

// conversations15 实录(cv-budget-flex-revision r2/r3)："改成预算最多上浮5%"
// 的模型输出 5 只有在原话出现对应百分比字面量时才换算为 0.05。
func TestPrepareNormalizesPercentBudgetFlex(t *testing.T) {
	state := schemas.NewRequirementState()
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`5`), Evidence: "stated", Quote: "改成预算最多上浮5%"},
	}}, guardSource("改成预算最多上浮5%"))
	if len(out.Operations) != 1 || string(out.Operations[0].Value) != "0.05" {
		t.Fatalf("带百分比字面量的百分数值应换算为比例: %+v", out.Operations)
	}
	// 百分之写法同样支持。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`20`), Evidence: "stated", Quote: "可以超百分之20"},
	}}, guardSource("预算可以超百分之20"))
	if len(out.Operations) != 1 || string(out.Operations[0].Value) != "0.2" {
		t.Fatalf("百分之N 应换算为比例: %+v", out.Operations)
	}
	// 反例：原话没有百分比字面量时不换算,非法值走原拒绝路径(转冲突)。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`5`), Evidence: "stated", Quote: "预算改9000"},
	}}, guardSource("预算改9000"))
	if len(out.Operations) != 1 || out.Operations[0].Op != "conflict" || out.Operations[0].Field != "budget_flex" {
		t.Fatalf("无百分比证据不得除100: %+v", out.Operations)
	}
	// 反例：合法范围内的值即使原话含百分比也不动(0.2 是合法比例)。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_flex", Value: json.RawMessage(`0.2`), Evidence: "stated", Quote: "上浮20%"},
	}}, guardSource("可以上浮20%"))
	if len(out.Operations) != 1 || string(out.Operations[0].Value) != "0.2" {
		t.Fatalf("合法比例不得被改写: %+v", out.Operations)
	}
}

// conversations15 实录(cv-owned-parts-withdrawal r1/r2)：set existing_parts=[]
// 之后的 remove owned_parts 把级联的明确空值退化成墓碑。全部新买证据下
// 该 remove 必须静默跳过(顺序无关),owned_parts 保持 active []。
func TestPrepareWithdrawalCascadesOwnedPartsToActiveEmpty(t *testing.T) {
	seed := func() schemas.RequirementState {
		state := schemas.NewRequirementState()
		state, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "existing_parts", Value: json.RawMessage(`["gpu"]`), Evidence: "stated", Quote: "有一张4070 Super显卡"},
			{Op: "set", Field: "owned_parts", Value: json.RawMessage(`[{"category":"gpu","model":"4070 Super"}]`), Evidence: "stated", Quote: "有一张4070 Super显卡"},
		}}, guardSource("打游戏用，我有一张4070 Super显卡"))
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	for _, order := range []int{0, 1} {
		ops := []schemas.RequirementOperation{
			{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Evidence: "stated", Quote: "主机配件全部新买"},
			{Op: "remove", Field: "owned_parts", Quote: "旧显卡不复用了"},
		}
		if order == 1 {
			ops[0], ops[1] = ops[1], ops[0]
		}
		out := prepareRequirementUpdate(seed(), schemas.RequirementUpdate{Operations: ops},
			guardSource("旧显卡不复用了，主机配件全部新买"))
		if len(out.Operations) != 1 || out.Operations[0].Op != "set" || out.Operations[0].Field != "existing_parts" {
			t.Fatalf("order=%d 冗余 remove owned_parts 必须跳过: %+v", order, out.Operations)
		}
		next, err := schemas.ApplyRequirementUpdate(seed(), schemas.RequirementUpdate{Operations: out.Operations}, schemas.RequirementSource{Kind: "chat", MessageID: "t2", Quote: "旧显卡不复用了，主机配件全部新买"})
		if err != nil {
			t.Fatalf("order=%d apply: %v", order, err)
		}
		for _, field := range []string{"existing_parts", "owned_parts"} {
			f := next.Fields[field]
			if f.Status != "active" || string(f.Value) != "[]" {
				t.Fatalf("order=%d %s 应为 active []: %+v", order, field, f)
			}
		}
	}
}

// conversations15 实录:原话已被同轮明确操作结构化的推断不再降级为观察
// (cv-budget-question-keeps-state r3 的 inferred budget_basis);独立片段的
// 推断仍降级保留原文;同字段同原文的降级与模型观察只记一条
// (cv-ownership-unknown r2 的重复观察)。
func TestPrepareDedupesGuardObservations(t *testing.T) {
	state := schemas.NewRequirementState()
	// 已结构化的推断:静默跳过,不产生观察。
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Evidence: "stated", Quote: "主机配件全新买"},
		{Op: "set", Field: "budget_basis", Value: json.RawMessage(`"full_build"`), Evidence: "inferred", Quote: "主机配件全新买"},
	}}, guardSource("预算7500，1080p，主机配件全新买"))
	if len(out.Operations) != 1 || len(out.Observations) != 0 {
		t.Fatalf("已结构化的推断不得重复降级: ops=%+v obs=%+v", out.Operations, out.Observations)
	}
	// 独立片段的推断:降级为观察保留原文。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "size_pref", Value: json.RawMessage(`"itx"`), Evidence: "inferred", Quote: "箱子能搬来搬去就行"},
	}}, guardSource("箱子能搬来搬去就行"))
	if len(out.Observations) != 1 || !strings.Contains(out.Observations[0].Quote, "箱子能搬来搬去") {
		t.Fatalf("独立推断应保留原文观察: %+v", out.Observations)
	}
	// 守卫降级与模型自身观察同字段同原文:只保留一条更完整的摘录。
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "existing_parts", Value: json.RawMessage(`"unknown"`), Evidence: "stated", Quote: "先别当作全部新买"},
		},
		Observations: []schemas.RequirementObservationInput{
			{Field: "existing_parts", Quote: "旧配件能不能复用还得回家确认，先别当作全部新买", Reason: "旧配件复用待确认"},
		},
	}, guardSource("旧配件能不能复用还得回家确认，先别当作全部新买"))
	if len(out.Observations) != 1 || !strings.Contains(out.Observations[0].Quote, "还得回家确认") {
		t.Fatalf("同字段同原文的观察应去重且保留完整摘录: %+v", out.Observations)
	}
}

// fix1 实录(ex-budget-ceiling 0/3):"预算不要超过7500"是真实上限语气,
// 零弹性必须放行(词表或模型摘录任一命中)。
func TestPrepareZeroBudgetFlexAcceptsCeilingPhrasings(t *testing.T) {
	state := schemas.NewRequirementState()
	for _, quote := range []string{"预算不要超过7500", "预算不能超过7500", "预算严格控制在7500以内", "最多7500封顶"} {
		out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
			{Op: "set", Field: "budget_cny", Value: json.RawMessage(`7500`), Evidence: "stated", Quote: quote},
			{Op: "set", Field: "budget_flex", Value: json.RawMessage(`0`), Evidence: "stated", Quote: quote},
		}}, guardSource(quote))
		if len(out.Operations) != 2 || out.Operations[1].Field != "budget_flex" {
			t.Fatalf("上限语气必须保留零弹性: quote=%q ops=%+v", quote, out.Operations)
		}
	}
}

// fix1 实录(cv-monitor-refusal 0/3):同一句原文的 monitor/keyboard/mouse
// 能力观察每能力一条,不参与去重;撤销能力(remove unsupported.*)依赖它们。
func TestPrepareKeepsPerCapabilityObservations(t *testing.T) {
	state := schemas.NewRequirementState()
	quote := "帮我配台电脑，还要显示器和键鼠"
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{},
		Observations: []schemas.RequirementObservationInput{
			{Field: "notes", Quote: quote, Reason: "unsupported_capability:monitor"},
			{Field: "notes", Quote: quote, Reason: "unsupported_capability:keyboard"},
			{Field: "notes", Quote: quote, Reason: "unsupported_capability:mouse"},
		},
	}, guardSource(quote))
	if len(out.Observations) != 3 {
		t.Fatalf("每个能力一条观察: %+v", out.Observations)
	}
	next, err := schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: out.Operations, Observations: out.Observations}, schemas.RequirementSource{Kind: "chat", MessageID: "m", Quote: quote})
	if err != nil {
		t.Fatal(err)
	}
	withdraw, err := schemas.ApplyRequirementUpdate(next, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "remove", Field: "unsupported.keyboard", Quote: "键鼠都先不要了"},
	}}, schemas.RequirementSource{Kind: "chat", MessageID: "m2", Quote: "显示器和键鼠都先不要了，只配主机"})
	if err != nil {
		t.Fatalf("能力撤销依赖观察记录: %v", err)
	}
	for _, o := range withdraw.Observations {
		if o.Reason == "unsupported_capability:keyboard" && !o.Resolved {
			t.Fatal("keyboard 能力未随撤销解除")
		}
	}
}

// fix1 实录(cv-ownership r1):模型顺手 remove notes 是噪声操作,
// 无备注撤销表达时静默跳过。
func TestPrepareSkipsNoiseRemoveNotes(t *testing.T) {
	state := schemas.NewRequirementState()
	out := prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Evidence: "stated", Quote: "主机配件全部新买"},
		{Op: "remove", Field: "notes", Evidence: "stated", Quote: "确认过了没有可复用的旧件，主机配件全部新买"},
	}}, guardSource("确认过了没有可复用的旧件，主机配件全部新买"))
	if len(out.Operations) != 1 || len(out.Observations) != 0 {
		t.Fatalf("噪声 remove notes 应静默跳过: ops=%+v obs=%+v", out.Operations, out.Observations)
	}
	out = prepareRequirementUpdate(state, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "remove", Field: "notes", Evidence: "stated", Quote: "补充说明删掉"},
	}}, guardSource("补充说明删掉"))
	if len(out.Operations) != 1 {
		t.Fatalf("明确撤销备注应放行: %+v", out.Operations)
	}
}

// recentAssistantView:提供列表时按旧→新渲染并声明避免重复询问用途;
// 未提供时退化为最近一条,旧调用方(评估回放、单测)渲染不变。
func TestRecentAssistantView(t *testing.T) {
	listed := recentAssistantView(schemas.ScreeningConversation{
		LastAssistant:     "最新一条",
		RecentAssistants: []string{"第一问：预算？", "第二问：用途？"},
	})
	if !strings.Contains(listed, "旧→新") || !strings.Contains(listed, "第1条：第一问：预算？") || !strings.Contains(listed, "第2条：第二问：用途？") {
		t.Fatalf("列表应按旧→新带序号渲染: %q", listed)
	}
	fallback := recentAssistantView(schemas.ScreeningConversation{LastAssistant: "只有一条"})
	if !strings.Contains(fallback, "最近一条助手回复") || !strings.Contains(fallback, "只有一条") {
		t.Fatalf("无列表时应退化为最近一条: %q", fallback)
	}
}
