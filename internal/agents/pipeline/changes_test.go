package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const baseSpecJSON = `{
  "schema_version": 2,
  "configuration_scope": ["tower"],
  "budget_cny": 8000,
  "use_case": {"type": "gaming", "resolution": "2K"}
}`

const baseSelectionJSON = `{
  "cpu": "amd-ryzen5-7500f",
  "motherboard": "msi-b650m-mortar-wifi",
  "memory": "kingston-fury-beast-ddr5-6000-16gx2",
  "ssd": [{"sku": "samsung-990pro-1tb", "quantity": 1}],
  "gpu": "asus-dual-rtx4070s",
  "psu": "seasonic-focus-gx-750",
  "case": "fractal-north",
  "cooler": "thermalright-pa120-se"
}`

// stateJSON 一份已落库的改单状态块(prepare/persistVersion 测试共用)。
func stateJSON(t *testing.T) string {
	t.Helper()
	bs := buildState{
		SessionID:     "sess_1",
		Version:       1,
		BuildID:       11,
		RequirementID: 21,
		Spec:          json.RawMessage(baseSpecJSON),
		Selection:     json.RawMessage(baseSelectionJSON),
		TotalCNY:      "7900.00",
		SnapshotDate:  "2026-07-28",
		BudgetCNY:     8000,
	}
	b, err := json.Marshal(bs)
	if err != nil {
		t.Fatalf("构造状态块失败: %v", err)
	}
	return string(b)
}

func TestPrepareNoPayloadClearsContext(t *testing.T) {
	ctx, directive := prepare("预算多少?请补充。", stateJSON(t))
	if ctx.Change != nil || ctx.ActiveSpec != nil || ctx.Invalid != "" || directive != "" {
		t.Errorf("无载荷应清空改单上下文, 得到 %+v, %q", ctx, directive)
	}
}

func TestPrepareFullSpecWithoutState(t *testing.T) {
	ctx, directive := prepare(baseSpecJSON, "")
	if string(ctx.ActiveSpec) == "" || ctx.ParentBuildID != nil || directive != "" {
		t.Errorf("首次整单生成不应挂父版本, 得到 %+v", ctx)
	}
}

func TestPrepareFullSpecWithStateChainsParent(t *testing.T) {
	ctx, _ := prepare(baseSpecJSON, stateJSON(t))
	if ctx.ParentBuildID == nil || *ctx.ParentBuildID != 11 || ctx.BaseVersion != 1 {
		t.Errorf("会话已有版本时整单重生成应挂链, 得到 %+v", ctx)
	}
}

func TestPrepareChangeWithoutStateIsInvalid(t *testing.T) {
	cr := `{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget","budget_delta_cny":-500}`
	ctx, directive := prepare(cr, "")
	if ctx.Invalid == "" || !strings.Contains(directive, "无法执行") {
		t.Errorf("无基版本改单应判无效, 得到 %+v, %q", ctx, directive)
	}
}

func TestPrepareBadChangeRequestIsInvalid(t *testing.T) {
	// intent 键存在但载荷夹带他类字段 → DecodeChangeRequest 报错。
	cr := `{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget","budget_delta_cny":-500,"swap":{"category":"gpu"}}`
	ctx, directive := prepare(cr, stateJSON(t))
	if !strings.Contains(ctx.Invalid, "ChangeRequest schema") || !strings.Contains(directive, "只输出一句话") {
		t.Errorf("非法 ChangeRequest 应判无效并指示只转述, 得到 %+v, %q", ctx, directive)
	}
}

func TestPrepareSwapPartReusesRequirement(t *testing.T) {
	cr := `{"schema_version":1,"base_build_ref":"v1","intent":"swap_part","swap":{"category":"gpu","target_hint":"换 AMD 显卡"}}`
	ctx, directive := prepare(cr, stateJSON(t))
	if ctx.ReuseReqID == nil || *ctx.ReuseReqID != 21 {
		t.Fatalf("swap_part 应复用基版本需求单, 得到 %+v", ctx)
	}
	// 状态块序列化会压缩 RawMessage,按语义比对:生效需求单即基需求单。
	spec, err := schemas.DecodeRequirementSpec(ctx.ActiveSpec)
	if err != nil || spec.BudgetCNY != 8000 || spec.UseCase.Type != schemas.UseCaseGaming {
		t.Errorf("swap_part 生效需求单应为基需求单, 得到 %+v, %v", spec, err)
	}
	// 解锁集仅 gpu,其余七类硬锁定。
	if len(ctx.Locked) != 7 {
		t.Errorf("swap gpu 应硬锁定其余七类, 得到 %v", ctx.Locked)
	}
	for _, c := range ctx.Locked {
		if c == schemas.CategoryGPU {
			t.Errorf("gpu 不应在锁定清单里: %v", ctx.Locked)
		}
	}
	for _, want := range []string{"swap_part", "换 AMD 显卡", "硬锁定品类"} {
		if !strings.Contains(directive, want) {
			t.Errorf("改单指令缺少 %q:\n%s", want, directive)
		}
	}
}

func TestPrepareAdjustBudgetDerivesSpec(t *testing.T) {
	cr := `{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget","budget_delta_cny":-500}`
	ctx, directive := prepare(cr, stateJSON(t))
	if ctx.Invalid != "" {
		t.Fatalf("合法 adjust_budget 不应判无效: %+v", ctx)
	}
	spec, err := schemas.DecodeRequirementSpec(ctx.ActiveSpec)
	if err != nil || spec.BudgetCNY != 7500 {
		t.Errorf("派生需求单预算应为 7500, 得到 %+v, %v", spec, err)
	}
	if ctx.ReuseReqID != nil {
		t.Error("adjust_budget 应插入新需求单行,不应复用")
	}
	if !strings.Contains(directive, "7500") {
		t.Errorf("改单指令应含派生后需求单:\n%s", directive)
	}
}

func TestPrepareChangeConstraintPatchesSpec(t *testing.T) {
	cr := `{"schema_version":1,"base_build_ref":"v1","intent":"change_constraint","constraint_patch":{"noise_pref":"silent"},"locked_categories":["gpu"]}`
	ctx, _ := prepare(cr, stateJSON(t))
	spec, err := schemas.DecodeRequirementSpec(ctx.ActiveSpec)
	if err != nil || spec.NoisePref != schemas.NoisePrefSilent {
		t.Errorf("补丁应覆盖 noise_pref, 得到 %+v, %v", spec, err)
	}
	if len(ctx.Locked) != 1 || ctx.Locked[0] != schemas.CategoryGPU {
		t.Errorf("仅显式锁定 gpu, 得到 %v", ctx.Locked)
	}
}

func TestDeriveSpecRejectsNonPositiveBudget(t *testing.T) {
	delta := -9000
	cr := schemas.ChangeRequest{Intent: schemas.IntentAdjustBudget, BudgetDeltaCNY: &delta}
	if _, err := deriveSpec(json.RawMessage(baseSpecJSON), cr); err == nil {
		t.Error("调整后预算非正应报错")
	}
}

func TestDeriveSpecRejectsIllegalPatch(t *testing.T) {
	cr := schemas.ChangeRequest{
		Intent:          schemas.IntentChangeConstraint,
		ConstraintPatch: json.RawMessage(`{"noise_pref": "超静音"}`),
	}
	if _, err := deriveSpec(json.RawMessage(baseSpecJSON), cr); err == nil {
		t.Error("非法枚举补丁应被严格解码兜底拦下")
	}
}

func TestExtractJSONObjectPrefersSchemaVersion(t *testing.T) {
	text := `分析 {"foo": 1} 之后输出:{"schema_version": 1, "intent": "swap_part"}`
	raw := extractJSONObject(text)
	if !hasTopLevelKey(raw, "schema_version") {
		t.Errorf("应优先取含 schema_version 的对象, 得到 %s", raw)
	}
	if extractJSONObject("没有任何对象") != nil {
		t.Error("无 JSON 对象应返回 nil")
	}
	if raw := extractJSONObject(`只有 {"foo": 1} 一个`); !hasTopLevelKey(raw, "foo") {
		t.Errorf("无 schema_version 时应回退第一个对象, 得到 %s", raw)
	}
}

func TestLockedViolations(t *testing.T) {
	cur, err := schemas.DecodeBuildSelection([]byte(draftSelectionOnly(t)))
	if err != nil {
		t.Fatalf("构造当前 selection 失败: %v", err)
	}
	// cpu 改动、gpu 改为 null、ssd 数量翻倍;memory 未动。
	cur.CPU = "amd-ryzen7-7800x3d"
	cur.GPU = nil
	cur.SSDs = []schemas.SSDSelection{{SKU: "samsung-990pro-1tb", Quantity: 2}}

	locked := []schemas.Category{
		schemas.CategoryCPU, schemas.CategoryGPU, schemas.CategorySSD, schemas.CategoryMemory,
	}
	viol, err := lockedViolations(locked, json.RawMessage(baseSelectionJSON), cur)
	if err != nil {
		t.Fatalf("锁定比对失败: %v", err)
	}
	if len(viol) != 3 {
		t.Fatalf("应检出 3 项违规, 得到 %v", viol)
	}
	joined := strings.Join(viol, ";")
	for _, want := range []string{"cpu:", "gpu:", "ssd:", "null", "×2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("违规描述缺少 %q: %s", want, joined)
		}
	}
}

// draftSelectionOnly 用包级 draftJSON 抽出合法 BuildSelection 输入。
func draftSelectionOnly(t *testing.T) string {
	t.Helper()
	var w struct {
		Selection json.RawMessage `json:"selection"`
	}
	if err := json.Unmarshal([]byte(draftJSON), &w); err != nil {
		t.Fatalf("解析 draftJSON 失败: %v", err)
	}
	return `{"schema_version":1,"build_ref":"build_001","parts":` + string(w.Selection) + `}`
}

func TestRemainingCNY(t *testing.T) {
	cases := []struct {
		budget int
		total  string
		want   string
	}{
		{8000, "7900.00", "100.00"},
		{8000, "8123.45", "-123.45"},
		{8000, "8000", "0.00"},
		{0, "7900.00", ""},
		{8000, "", ""},
		{8000, "7900.123", ""}, // 三位小数不合报价口径
	}
	for _, c := range cases {
		if got := remainingCNY(c.budget, c.total); got != c.want {
			t.Errorf("remainingCNY(%d, %q) = %q, 期望 %q", c.budget, c.total, got, c.want)
		}
	}
}

func TestChangeIntentLabel(t *testing.T) {
	if got := changeIntentLabel(nil); got != "整单生成" {
		t.Errorf("空 change 应为整单生成, 得到 %q", got)
	}
	if got := changeIntentLabel(json.RawMessage(`{"intent":"swap_part"}`)); got != "swap_part" {
		t.Errorf("应取 intent 字段, 得到 %q", got)
	}
}

// fakeSaver 记录落库入参;可注入错误验证"落库失败要响"。
type fakeSaver struct {
	got   store.SaveBuildVersionParams
	saved store.SavedBuild
	err   error
}

func (f *fakeSaver) SaveBuildVersion(_ context.Context, p store.SaveBuildVersionParams) (store.SavedBuild, error) {
	f.got = p
	if f.err != nil {
		return store.SavedBuild{}, f.err
	}
	return f.saved, nil
}

func deliverVerdict(t *testing.T) verdict {
	t.Helper()
	f := &fakeEval{res: passResult()}
	v := decide(context.Background(), f, draftJSON, "", 1, nil)
	if !v.deliver {
		t.Fatalf("pass 应产出 deliver verdict: %+v", v)
	}
	return v
}

func TestPersistVersionSuccessUpdatesState(t *testing.T) {
	saver := &fakeSaver{saved: store.SavedBuild{ID: 12, Version: 2, RequirementID: 22}}
	chg := &changeCtx{ActiveSpec: json.RawMessage(baseSpecJSON)}
	suffix, bsJSON := persistVersion(context.Background(), saver, "sess_1", chg, deliverVerdict(t), stateJSON(t))
	if !strings.Contains(suffix, "已落库:版本 v2") {
		t.Errorf("成功后缀应报版本号, 得到 %q", suffix)
	}
	var bs buildState
	if err := json.Unmarshal([]byte(bsJSON), &bs); err != nil {
		t.Fatalf("状态块非法: %v", err)
	}
	if bs.Version != 2 || bs.BuildID != 12 || bs.RequirementID != 22 || bs.BudgetCNY != 8000 {
		t.Errorf("状态块字段不符: %+v", bs)
	}
	if bs.BudgetRemainingCNY != "1900.00" {
		t.Errorf("预算余额应为 1900.00, 得到 %q", bs.BudgetRemainingCNY)
	}
	if len(bs.History) != 1 || !strings.Contains(bs.History[0], "v2(整单生成)") {
		t.Errorf("历史摘要不符: %v", bs.History)
	}
	if saver.got.SessionID != "sess_1" || len(saver.got.Draft) == 0 || len(saver.got.Quote) == 0 {
		t.Errorf("落库入参不完整: %+v", saver.got)
	}
}

func TestPersistVersionSaveErrorIsLoud(t *testing.T) {
	saver := &fakeSaver{err: errors.New("connection reset")}
	chg := &changeCtx{ActiveSpec: json.RawMessage(baseSpecJSON)}
	suffix, bsJSON := persistVersion(context.Background(), saver, "sess_1", chg, deliverVerdict(t), "")
	if !strings.Contains(suffix, "落库失败") || !strings.Contains(suffix, "connection reset") || bsJSON != "" {
		t.Errorf("落库失败应如实报错且不更新状态块, 得到 %q, %q", suffix, bsJSON)
	}
}

func TestPersistVersionWithoutContextIsLoud(t *testing.T) {
	suffix, bsJSON := persistVersion(context.Background(), &fakeSaver{}, "sess_1", nil, deliverVerdict(t), "")
	if !strings.Contains(suffix, "落库失败") || bsJSON != "" {
		t.Errorf("缺上下文应如实报错, 得到 %q, %q", suffix, bsJSON)
	}
}

func TestDecideLockedViolationFeedsBack(t *testing.T) {
	// 基版本 gpu 为 4070s,draftJSON 也是 4070s;锁定 cpu 但把基版本 cpu 改成别的。
	base := strings.Replace(baseSelectionJSON, "amd-ryzen5-7500f", "amd-ryzen7-7800x3d", 1)
	chg := &changeCtx{
		Locked:        []schemas.Category{schemas.CategoryCPU},
		BaseSelection: json.RawMessage(base),
	}
	f := &fakeEval{res: passResult()}
	v := decide(context.Background(), f, draftJSON, "", 1, chg)
	if v.escalate || !strings.Contains(v.message, "锁定校验未通过") {
		t.Errorf("锁定违规应打回不出栈, 得到 %+v", v)
	}
	if f.gotSel.CPU != "" {
		t.Error("锁定违规不应进入规则引擎")
	}
}

func TestDecideLockedViolationFinalRoundEscalates(t *testing.T) {
	base := strings.Replace(baseSelectionJSON, "amd-ryzen5-7500f", "amd-ryzen7-7800x3d", 1)
	chg := &changeCtx{
		Locked:        []schemas.Category{schemas.CategoryCPU},
		BaseSelection: json.RawMessage(base),
	}
	v := decide(context.Background(), &fakeEval{res: passResult()}, draftJSON, "", maxLoopRounds, chg)
	if !v.escalate || !strings.Contains(v.message, "轮数用尽") {
		t.Errorf("末轮锁定违规应如实终止, 得到 %+v", v)
	}
}

func TestDecideLockedPassProceedsToRules(t *testing.T) {
	// 锁定品类与基版本一致 → 正常进入规则引擎并交付。
	chg := &changeCtx{
		Locked:        []schemas.Category{schemas.CategoryCPU, schemas.CategorySSD},
		BaseSelection: json.RawMessage(baseSelectionJSON),
	}
	f := &fakeEval{res: passResult()}
	v := decide(context.Background(), f, draftJSON, "", 1, chg)
	if !v.deliver || f.gotSel.CPU != "amd-ryzen5-7500f" {
		t.Errorf("锁定合规应正常交付, 得到 %+v", v)
	}
}

func TestDecideBudgetChangeRejectsMoreThanTwoChangedCategories(t *testing.T) {
	base := strings.NewReplacer(
		"amd-ryzen5-7500f", "amd-ryzen7-7800x3d",
		"msi-b650m-mortar-wifi", "asus-b650-plus",
		"thermalright-pa120-se", "deepcool-ak620",
	).Replace(baseSelectionJSON)
	chg := &changeCtx{
		Change:        json.RawMessage(`{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget","budget_delta_cny":-500}`),
		BaseSelection: json.RawMessage(base),
	}
	f := &fakeEval{res: passResult()}
	v := decide(context.Background(), f, draftJSON, "", 1, chg)
	if v.escalate || v.deliver || !strings.Contains(v.message, "改动过多") || !strings.Contains(v.message, "3 个品类") {
		t.Fatalf("预算改单超过两类应定向打回, 得到 %+v", v)
	}
	if f.gotSel.CPU != "" {
		t.Error("改动数超限时不应进入兼容性规则引擎")
	}
}

func TestDecideBudgetChangeAllowsZeroToTwoChangedCategories(t *testing.T) {
	change := json.RawMessage(`{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget","budget_delta_cny":-500}`)
	for _, tc := range []struct {
		name string
		base string
	}{
		{name: "零改动", base: baseSelectionJSON},
		{name: "两类改动", base: strings.NewReplacer(
			"amd-ryzen5-7500f", "amd-ryzen7-7800x3d",
			"thermalright-pa120-se", "deepcool-ak620",
		).Replace(baseSelectionJSON)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeEval{res: passResult()}
			v := decide(context.Background(), f, draftJSON, "", 1, &changeCtx{Change: change, BaseSelection: json.RawMessage(tc.base)})
			if !v.deliver || !v.escalate || f.gotSel.CPU == "" {
				t.Fatalf("预算改单不超过两类应继续校验并交付, 得到 %+v", v)
			}
		})
	}
}
