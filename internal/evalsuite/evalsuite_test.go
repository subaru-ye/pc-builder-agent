package evalsuite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// stubHarness 返回预置结果,驱动断言矩阵全分支,不触模型与数据库。
type stubHarness struct {
	result buildharness.BuildResult
	err    error
}

func (h *stubHarness) Run(context.Context, buildharness.BuildInput) (buildharness.BuildResult, error) {
	return h.result, h.err
}

func testSnapshot() SnapshotView {
	return SnapshotView{
		SnapshotDate:   "2026-07-27",
		SKUs:           map[string]bool{"cpu-a": true, "gpu-a": true, "mb-a": true, "ram-a": true, "ssd-a": true, "psu-a": true, "case-a": true, "cool-a": true},
		BrandBySKU:     map[string]string{"cpu-a": "AMD", "gpu-a": "NVIDIA", "mb-a": "MSI", "ram-a": "Kingston", "ssd-a": "WD", "psu-a": "Corsair", "case-a": "Fractal", "cool-a": "DeepCool"},
		CPUVendorBySKU: map[string]string{"cpu-a": "amd"},
		GPUFamilyBySKU: map[string]string{"gpu-a": "nvidia"},
	}
}

func passingSelection() schemas.BuildSelection {
	gpu := "gpu-a"
	return schemas.BuildSelection{
		SchemaVersion: 1, BuildRef: "build_001",
		CPU: "cpu-a", Motherboard: "mb-a", Memory: "ram-a",
		SSDs: []schemas.SSDSelection{{SKU: "ssd-a", Quantity: 1}},
		GPU:  &gpu, PSU: "psu-a", Case: "case-a", Cooler: "cool-a",
	}
}

func passingResult(totalCNY string) buildharness.BuildResult {
	quote := validate.Quote{
		SnapshotDate: "2026-07-27", TotalCNY: totalCNY,
		Lines: []validate.QuoteLine{{Category: schemas.CategoryCPU, SKU: "cpu-a", Quantity: 1, UnitPriceCNY: ptr(totalCNY), SubtotalCNY: ptr(totalCNY)}},
	}
	return buildharness.BuildResult{
		Succeeded: true, Attempts: 1,
		Draft:  schemas.BuildDraft{SchemaVersion: 1, RequirementRef: "req_001", BuildRef: "build_001", Selection: passingSelection()},
		Result: validate.Result{Quote: quote},
	}
}

func ptr(s string) *string { return &s }

func testCase() Case {
	raw := json.RawMessage(`{"schema_version":2,"configuration_scope":["tower"],"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"},"brand_pref":{"cpu":"amd","gpu":"nvidia"}}`)
	spec, err := schemas.DecodeRequirementSpec(raw)
	if err != nil {
		panic(err)
	}
	return Case{ID: "L1-000", Title: "断言矩阵测试", Requirement: spec, Expect: Expect{Outcome: "pass"}}
}

func hasFailure(t *testing.T, verdict Verdict, id string) AssertionFailure {
	t.Helper()
	for _, failure := range verdict.Failures {
		if failure.ID == id {
			return failure
		}
	}
	t.Fatalf("缺少断言 %s 的失败记录:%+v", id, verdict.Failures)
	return AssertionFailure{}
}

func TestAssertCaseAllPass(t *testing.T) {
	verdict := AssertCase(testCase(), passingResult("8800.00"), testSnapshot())
	// 8800 = 8000 × 1.1(默认弹性上界,含端点)。
	if !verdict.Passed {
		t.Fatalf("应全部通过:%+v", verdict.Failures)
	}
}

func TestAssertCaseBudgetLowerBound(t *testing.T) {
	verdict := AssertCase(testCase(), passingResult("7200.00"), testSnapshot())
	// 7200 = 8000 × 0.9(下界,含端点)。
	if !verdict.Passed {
		t.Fatalf("下界应通过:%+v", verdict.Failures)
	}
	verdict = AssertCase(testCase(), passingResult("7199.99"), testSnapshot())
	if verdict.Passed {
		t.Fatal("低于下界应失败")
	}
	if got := hasFailure(t, verdict, "A4"); got.Detail == "" {
		t.Fatal("A4 明细为空")
	}
}

func TestAssertCaseNotSucceededSkipsDownstream(t *testing.T) {
	result := buildharness.BuildResult{
		Succeeded: false, Attempts: 3,
		Result:  validate.Result{Quote: validate.Quote{SnapshotDate: "2026-07-27", TotalCNY: "0.00"}},
		Message: "预算与规则均未满足",
	}
	verdict := AssertCase(testCase(), result, testSnapshot())
	if verdict.Passed {
		t.Fatal("未交付应失败")
	}
	hasFailure(t, verdict, "A2")
	if len(verdict.Failures) != 1 {
		t.Fatalf("下游断言应跳过:%+v", verdict.Failures)
	}
}

func TestAssertCaseMissingPriceIsDataError(t *testing.T) {
	result := passingResult("8800.00")
	result.Result.Quote.MissingCount = 1
	result.Result.Quote.MissingSKUs = []string{"gpu-a"}
	verdict := AssertCase(testCase(), result, testSnapshot())
	if verdict.Passed || !verdict.DataError {
		t.Fatalf("缺价应记 data-error:%+v", verdict)
	}
	hasFailure(t, verdict, "A4")
}

func TestAssertCaseBrandPref(t *testing.T) {
	result := passingResult("8800.00")
	gpu := "gpu-amd"
	result.Draft.Selection.GPU = &gpu
	verdict := AssertCase(testCase(), result, testSnapshot())
	if verdict.Passed {
		t.Fatal("gpu 品牌不符应失败")
	}
	hasFailure(t, verdict, "A5")

	// any 期望不校验;nil GPU 且要求品牌 → 失败。
	spec := testCase()
	spec.Requirement.BrandPref.GPU = schemas.GPUBrandAny
	result2 := passingResult("8800.00")
	result2.Draft.Selection.GPU = nil
	if verdict := AssertCase(spec, result2, testSnapshot()); !verdict.Passed {
		t.Fatalf("any 品牌下 gpu=null 应通过:%+v", verdict.Failures)
	}
	spec.Requirement.BrandPref.GPU = schemas.GPUBrandNvidia
	if verdict := AssertCase(spec, result2, testSnapshot()); verdict.Passed {
		t.Fatal("要求品牌但 gpu=null 应失败")
	}
}

func TestAssertCaseForeignSKU(t *testing.T) {
	result := passingResult("8800.00")
	result.Draft.Selection.CPU = "cpu-not-in-catalog"
	verdict := AssertCase(testCase(), result, testSnapshot())
	if verdict.Passed {
		t.Fatal("快照外 SKU 应失败")
	}
	hasFailure(t, verdict, "A6")
}

func TestAssertCaseQuoteIntegrity(t *testing.T) {
	result := passingResult("8800.00")
	result.Result.Quote.Lines[0].SubtotalCNY = ptr("100.00")
	verdict := AssertCase(testCase(), result, testSnapshot())
	if verdict.Passed {
		t.Fatal("分项之和 ≠ 合计应失败")
	}
	hasFailure(t, verdict, "A7")
}

func TestAssertCaseDraftShape(t *testing.T) {
	result := passingResult("8800.00")
	result.Draft.Selection.Cooler = ""
	verdict := AssertCase(testCase(), result, testSnapshot())
	if verdict.Passed {
		t.Fatal("必填品类为空应失败")
	}
	hasFailure(t, verdict, "A1")
}

func TestParseCents(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1299.00", 129900, true},
		{"0.5", 50, true},
		{"8800.00", 880000, true},
		{"-1.00", 0, false},
		{"1.234", 0, false},
		{"abc", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseCents(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseCents(%q) = %d,%v want %d,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	if got := formatCents(129900); got != "1299.00" {
		t.Errorf("formatCents = %q", got)
	}
}

func TestLoadCasesValidates(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.json", `{"id":"L1-002","title":"B","requirement":{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"}},"expect":{"outcome":"pass"}}`)
	write("a.json", `{"id":"L1-001","title":"A","requirement":{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"}},"expect":{"outcome":"pass"}}`)

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 || cases[0].ID != "L1-001" || cases[1].ID != "L1-002" {
		t.Fatalf("应按文件名排序加载:%+v", cases)
	}

	write("c.json", `{"id":"L1-003","title":"C","requirement":{"schema_version":1,"budget_cny":8000},"expect":{"outcome":"fail"}}`)
	if _, err := LoadCases(dir); err == nil {
		t.Fatal("expect.outcome=fail 应报错(P0 仅支持 pass)")
	}
}

func TestRunCasesClassifiesErrors(t *testing.T) {
	cases := []Case{testCase()}
	snap := testSnapshot()

	// 未结构化执行错误仍计失败，不能靠文案剔除分母。
	stub := &stubHarness{err: fmt.Errorf("buildharness: CPU 硬约束下没有候选")}
	records, err := RunCases(context.Background(), cases, Deps{Harness: stub, Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	if records[0].Verdict.DataError || records[0].Verdict.Passed {
		t.Fatalf("原始执行错误必须计失败:%+v", records[0])
	}

	// 其他错误 → 用例失败(保守口径,计入分母)。
	stub = &stubHarness{err: context.DeadlineExceeded}
	records, err = RunCases(context.Background(), cases, Deps{Harness: stub, Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	if records[0].Verdict.DataError || records[0].Verdict.Passed {
		t.Fatalf("超时应记用例失败:%+v", records[0].Verdict)
	}

	// 正常路径:结果与断言一并留存。
	stub = &stubHarness{result: passingResult("8800.00")}
	records, err = RunCases(context.Background(), cases, Deps{Harness: stub, Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	if !records[0].Verdict.Passed || records[0].Result == nil {
		t.Fatalf("正常路径应通过:%+v", records[0])
	}
}

func TestRecordRoundTrip(t *testing.T) {
	cases := []Case{testCase()}
	records, err := RunCases(context.Background(), cases, Deps{Harness: &stubHarness{result: passingResult("8800.00")}, Snapshot: testSnapshot()})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	summary := Summarize(ReportMeta{Mode: "run", SnapshotDate: "2026-07-27", GeneratedAt: time.Now()}, records)
	if err := summary.WriteJSONL(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadRecords(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].CaseID != records[0].CaseID {
		t.Fatalf("回读不一致:%+v", loaded)
	}
	// 重放断言:同一记录重算,结论必须一致。
	replay := AssertCase(cases[0], *loaded[0].Result, loaded[0].Snapshot)
	if replay.Passed != loaded[0].Verdict.Passed {
		t.Fatalf("重放结论漂移:%+v vs %+v", replay, loaded[0].Verdict)
	}
	if err := summary.WriteReport(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.md")); err != nil {
		t.Fatal(err)
	}
}

func TestShippedFixturesDecode(t *testing.T) {
	cases, err := LoadCases("testdata/cases")
	if err != nil {
		t.Fatalf("随包评估集必须全部合法:%v", err)
	}
	if len(cases) < 30 {
		t.Fatalf("评估集应不少于 30 条(L1+L2+L3),得到 %d", len(cases))
	}
	stages := map[Stage]int{}
	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.ID] {
			t.Errorf("用例 ID 重复:%s", c.ID)
		}
		seen[c.ID] = true
		stages[c.Stage]++
		switch c.Stage {
		case StageBuild:
			if c.Requirement.BudgetCNY <= 0 {
				t.Errorf("%s: budget_cny 必须为正", c.ID)
			}
			if c.Requirement.UseCase.Type == schemas.UseCaseGaming && c.Requirement.UseCase.Resolution == "" {
				t.Errorf("%s: gaming 用例必须带分辨率", c.ID)
			}
		case StageScreening:
			if c.Input == "" && len(c.Turns) == 0 {
				t.Errorf("%s: screening 用例必须带 input", c.ID)
			}
		}
	}
	if stages[StageBuild] != 34 || stages[StageScreening] != 21 {
		t.Errorf("build/screening 用例数不符:%+v", stages)
	}
}

func TestAssertCaseLockedViolation(t *testing.T) {
	c := testCase()
	c.Locked = []schemas.Category{schemas.CategoryGPU}
	base := passingSelection()
	c.BaseSelection = &base
	// 交付与基线一致 → 通过
	result := passingResult("8800.00")
	if verdict := AssertCase(c, result, testSnapshot()); !verdict.Passed {
		t.Fatalf("锁定品类未变应通过:%+v", verdict.Failures)
	}
	// assertLocked 直接验证:基线 gpu-a,交付 gpu-b → 明细非空
	draft := passingSelection()
	gpu := "gpu-b"
	draft.GPU = &gpu
	if detail := assertLocked([]schemas.Category{schemas.CategoryGPU}, &base, &draft); detail == "" {
		t.Fatal("锁定品类被改应报 A8 明细")
	}
	if detail := assertLocked(nil, &base, &draft); detail != "" {
		t.Fatalf("未声明锁定品类时不应报:%s", detail)
	}
	// A8 失败必须带 veto 标记
	violation := Attribute([]AssertionFailure{{ID: "A8", Detail: "gpu 被改", Veto: true}})
	if len(violation) != 1 || violation[0].Code != "B4" {
		t.Fatalf("A8 应映射 B4:%+v", violation)
	}
}

func TestAttributeMapping(t *testing.T) {
	failures := []AssertionFailure{
		{ID: "A5", Detail: "gpu 家族不符"},
		{ID: "A4", Detail: "预算越界", Veto: true},
		{ID: "A2", Detail: `message="…BuildDraft schema…"`},
	}
	got := Attribute(failures)
	if len(got) != 3 {
		t.Fatalf("应产出 3 条归因:%+v", got)
	}
	if !got[0].Primary || got[0].Code != "B8" {
		t.Errorf("首条应为主因 B8:%+v", got[0])
	}
	if got[1].Code != "B2" {
		t.Errorf("A4 应映射 B2:%+v", got[1])
	}
	if got[2].Code != "B7" {
		t.Errorf("A2+schema 应映射 B7:%+v", got[2])
	}
	// A2 无 schema 字样 → B6
	if got := Attribute([]AssertionFailure{{ID: "A2", Detail: "overall=fail 规则 DISPLAY_OUTPUT"}}); got[0].Code != "B6" {
		t.Errorf("A2+规则失败应映射 B6:%+v", got[0])
	}
}

func TestAssertScreeningCase(t *testing.T) {
	specJSON := json.RawMessage(`{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"},"brand_pref":{"cpu":"any","gpu":"any"}}`)
	c := Case{ID: "L3-T", Stage: StageScreening, Expect: Expect{Kind: "spec", BudgetCNY: intPtr(8000), CPUBrand: "any", GPUBrand: "any"}}
	if verdict := AssertScreeningCase(c, string(specJSON)); !verdict.Passed {
		t.Fatalf("spec 期望应通过:%+v", verdict.Failures)
	}
	// 品牌被违规推断 → S2 失败
	inferred := json.RawMessage(`{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"},"brand_pref":{"cpu":"amd","gpu":"nvidia"}}`)
	if verdict := AssertScreeningCase(c, string(inferred)); verdict.Passed {
		t.Fatal("any 期望下推断出品牌应失败")
	}
	// clarify 期望:输出 JSON 即失败;必须实际追问。
	cl := Case{ID: "L3-T2", Stage: StageScreening, Expect: Expect{Kind: "clarify"}}
	if verdict := AssertScreeningCase(cl, string(specJSON)); verdict.Passed {
		t.Fatal("clarify 期望下输出 JSON 应失败")
	}
	if verdict := AssertScreeningCase(cl, "请问您的预算是多少？"); !verdict.Passed {
		t.Fatalf("clarify 期望下追问应通过:%+v", verdict.Failures)
	}
}

func TestSeedsAggregation(t *testing.T) {
	cases := []Case{testCase()}
	// 3 seeds:2 pass 1 fail → 用例级 Pass^k 不通过
	harness := &seqHarness{results: []buildharness.BuildResult{passingResult("8800.00"), passingResult("8800.00"), passingResult("99999.00")}}
	records, err := RunCases(context.Background(), cases, Deps{Harness: harness, Snapshot: testSnapshot(), Seeds: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("应产出 3 条记录:%d", len(records))
	}
	summary := Summarize(ReportMeta{Mode: "run", GeneratedAt: time.Now()}, records)
	if summary.Seeds != 3 || summary.CaseCount != 1 || summary.PassKPassed != 0 {
		t.Fatalf("Pass^k 聚合不符:%+v", summary)
	}
	if summary.VetoTriggers != 1 {
		t.Fatalf("预算越界应记 1 次 veto:%d", summary.VetoTriggers)
	}
}

// seqHarness 依序返回预置结果(每次 Run 消耗一个)。
type seqHarness struct {
	results []buildharness.BuildResult
	mu      sync.Mutex
}

func (h *seqHarness) Run(context.Context, buildharness.BuildInput) (buildharness.BuildResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.results) == 0 {
		return buildharness.BuildResult{}, errors.New("seqHarness: 预置结果耗尽")
	}
	next := h.results[0]
	h.results = h.results[1:]
	return next, nil
}

func intPtr(v int) *int { return &v }
