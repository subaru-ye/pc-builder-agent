package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// fakeEval 实现 tools.BuildEvaluator,decide 全分支无 DB 单测。
type fakeEval struct {
	res    validate.Result
	err    error
	gotSel schemas.BuildSelection
}

func (f *fakeEval) Evaluate(_ context.Context, sel schemas.BuildSelection) (validate.Result, error) {
	f.gotSel = sel
	if f.err != nil {
		return validate.Result{}, f.err
	}
	return f.res, nil
}

const draftJSON = `{
  "schema_version": 1,
  "requirement_ref": "req_001",
  "build_ref": "build_001",
  "selection": {
    "cpu": "amd-ryzen5-7500f",
    "motherboard": "msi-b650m-mortar-wifi",
    "memory": "kingston-fury-beast-ddr5-6000-16gx2",
    "ssd": [{"sku": "samsung-990pro-1tb", "quantity": 1}],
    "gpu": "asus-dual-rtx4070s",
    "psu": "seasonic-focus-gx-750",
    "case": "fractal-north",
    "cooler": "thermalright-pa120-se"
  }
}`

func check(id schemas.RuleID, o schemas.Outcome, s schemas.Severity, detail string) schemas.CheckResult {
	return schemas.CheckResult{RuleID: id, Outcome: o, Severity: s, Detail: detail}
}

func passResult() validate.Result {
	price := "6100.00"
	return validate.Result{
		Report: schemas.ValidationReport{
			BuildRef:      "build_001",
			OverallStatus: schemas.OverallPass,
			Checks:        []schemas.CheckResult{check(schemas.RuleSocketMatch, schemas.OutcomePass, schemas.SeverityNone, "")},
		},
		Quote: validate.Quote{
			SnapshotDate: "2026-07-28",
			TotalCNY:     "6100.00",
			Lines: []validate.QuoteLine{
				{Category: schemas.CategoryCPU, SKU: "amd-ryzen5-7500f", Quantity: 1, UnitPriceCNY: &price, SubtotalCNY: &price},
			},
		},
	}
}

func failResult() validate.Result {
	res := passResult()
	res.Report.OverallStatus = schemas.OverallFail
	res.Report.Checks = []schemas.CheckResult{
		check(schemas.RuleSocketMatch, schemas.OutcomeFail, schemas.SeverityError, "CPU 插槽 AM5 与主板 LGA1700 不匹配"),
	}
	return res
}

func TestDecideNoDraftEscalatesSilently(t *testing.T) {
	for _, draft := range []string{"", "等待需求确认后再生成配置", "```\n```"} {
		v := decide(context.Background(), &fakeEval{}, draft, "", 1, nil)
		if !v.escalate || v.message != "" {
			t.Errorf("draft=%q: 无草稿应静默出栈, 得到 %+v", draft, v)
		}
	}
}

func TestDecideSchemaErrorFeedsBack(t *testing.T) {
	v := decide(context.Background(), &fakeEval{}, `{"schema_version": 2}`, "", 1, nil)
	if v.escalate {
		t.Error("schema 错非末轮不应出栈")
	}
	if !strings.Contains(v.message, "schema 不合法") {
		t.Errorf("应回喂 schema 错误, 得到 %q", v.message)
	}
}

func TestDecideSchemaErrorFinalRoundEscalates(t *testing.T) {
	v := decide(context.Background(), &fakeEval{}, `{"schema_version": 2}`, "", maxLoopRounds, nil)
	if !v.escalate || !strings.Contains(v.message, "轮数用尽") {
		t.Errorf("末轮 schema 错应如实终止, 得到 %+v", v)
	}
}

func TestDecideUnknownSKUFeedsBack(t *testing.T) {
	f := &fakeEval{err: fmt.Errorf("store: %w", store.ErrUnknownSKU)}
	v := decide(context.Background(), f, draftJSON, "", 1, nil)
	if v.escalate {
		t.Error("未知 SKU 非末轮不应出栈")
	}
	if !strings.Contains(v.message, "search_parts") || v.selection == "" {
		t.Errorf("应回喂换用真实候选并记录 selection, 得到 %+v", v)
	}
}

func TestDecideUnknownSKURepeatedSelectionIsDeadLoop(t *testing.T) {
	f := &fakeEval{err: fmt.Errorf("store: %w", store.ErrUnknownSKU)}
	first := decide(context.Background(), f, draftJSON, "", 1, nil)
	second := decide(context.Background(), f, draftJSON, first.selection, 2, nil)
	if !second.escalate || !strings.Contains(second.message, "死循环") {
		t.Errorf("重复 selection 应判死循环出栈, 得到 %+v", second)
	}
}

func TestDecideUnrecoverableErrorBreaksCircuit(t *testing.T) {
	f := &fakeEval{err: errors.New("dial tcp: 数据库不可达")}
	v := decide(context.Background(), f, draftJSON, "", 1, nil)
	if !v.escalate || !strings.Contains(v.message, "熔断") {
		t.Errorf("DB 错误应立即熔断, 得到 %+v", v)
	}
}

func TestDecidePassDelivers(t *testing.T) {
	f := &fakeEval{res: passResult()}
	v := decide(context.Background(), f, draftJSON, "", 1, nil)
	if !v.escalate {
		t.Error("pass 应出栈交付")
	}
	for _, want := range []string{"pass", "build_001", "6100.00", "2026-07-28"} {
		if !strings.Contains(v.message, want) {
			t.Errorf("交付文本缺少 %q:\n%s", want, v.message)
		}
	}
	if v.reportJSON == "" || !strings.Contains(v.reportJSON, `"overall_status":"pass"`) {
		t.Errorf("应写入报告 JSON, 得到 %q", v.reportJSON)
	}
	if f.gotSel.CPU != "amd-ryzen5-7500f" {
		t.Errorf("selection 未传入校验核心: %+v", f.gotSel)
	}
}

func TestDecideReviewDeliversWithNotes(t *testing.T) {
	res := passResult()
	res.Report.OverallStatus = schemas.OverallReview
	res.Report.Checks = []schemas.CheckResult{
		check(schemas.RuleMemorySpeed, schemas.OutcomeFail, schemas.SeverityWarning, "内存超主板标称频率"),
	}
	v := decide(context.Background(), &fakeEval{res: res}, draftJSON, "", 1, nil)
	if !v.escalate || !strings.Contains(v.message, "review") || !strings.Contains(v.message, "内存超主板标称频率") {
		t.Errorf("review 应交付并列注意项, 得到 %+v", v)
	}
}

func TestDecideReviewRetriesUnknownBeforeFinalRound(t *testing.T) {
	res := passResult()
	res.Report.OverallStatus = schemas.OverallReview
	res.Report.Checks = []schemas.CheckResult{
		{
			RuleID:        schemas.RuleMemorySpeed,
			Outcome:       schemas.OutcomeUnknown,
			Severity:      schemas.SeverityNone,
			MissingFields: []string{"motherboard.memory_speed_max_mts"},
			Detail:        "内存频率字段缺失,无法判定",
		},
	}

	v := decide(context.Background(), &fakeEval{res: res}, draftJSON, "", 1, nil)
	if v.escalate || v.deliver {
		t.Fatalf("非最后轮的 unknown review 应定向重试, 得到 %+v", v)
	}
	for _, want := range []string{"MEMORY_SPEED", "motherboard.memory_speed_max_mts", "字段非 null"} {
		if !strings.Contains(v.message, want) {
			t.Errorf("重试反馈缺少 %q:\n%s", want, v.message)
		}
	}
	if v.reportJSON == "" {
		t.Error("重试时应保留最后一版校验报告")
	}
}

func TestDecideReviewDeliversUnknownOnFinalRound(t *testing.T) {
	res := passResult()
	res.Report.OverallStatus = schemas.OverallReview
	res.Report.Checks = []schemas.CheckResult{
		{
			RuleID:        schemas.RuleCoolerThermalCapacity,
			Outcome:       schemas.OutcomeUnknown,
			Severity:      schemas.SeverityNone,
			MissingFields: []string{"cooler.cooling_capacity_w"},
			Detail:        "散热能力字段缺失,无法判定",
		},
	}

	v := decide(context.Background(), &fakeEval{res: res}, draftJSON, "", maxLoopRounds, nil)
	if !v.escalate || !v.deliver || !strings.Contains(v.message, "review") {
		t.Fatalf("最后一轮 unknown review 应如实交付, 得到 %+v", v)
	}
}

func budgetChangeCtx(budget int, flex float64) *changeCtx {
	raw := json.RawMessage(fmt.Sprintf(`{"schema_version":1,"budget_cny":%d,"budget_flex":%g,"use_case":{"type":"general"}}`, budget, flex))
	return &changeCtx{ActiveSpec: raw}
}

func TestDecideBudgetWindow(t *testing.T) {
	t.Run("区间内正常交付", func(t *testing.T) {
		v := decide(context.Background(), &fakeEval{res: passResult()}, draftJSON, "", 1, budgetChangeCtx(6500, 0.1))
		if !v.deliver || !v.escalate {
			t.Fatalf("预算区间内应交付,得到 %+v", v)
		}
	})

	for _, tc := range []struct {
		name   string
		budget int
		want   string
	}{
		{name: "低于下界", budget: 8000, want: "¥7200.00–¥8800.00"},
		{name: "超过上界", budget: 5000, want: "¥4500.00–¥5500.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := decide(context.Background(), &fakeEval{res: passResult()}, draftJSON, "", 1, budgetChangeCtx(tc.budget, 0.1))
			if v.escalate || v.deliver || v.selection == "" || !strings.Contains(v.message, tc.want) {
				t.Fatalf("预算越界应回喂定向修复,得到 %+v", v)
			}
		})
	}
}

func TestDecideBudgetWindowDoesNotSaveFinalOrRepeatedDraft(t *testing.T) {
	ctx := budgetChangeCtx(8000, 0.1)
	first := decide(context.Background(), &fakeEval{res: passResult()}, draftJSON, "", 1, ctx)
	repeated := decide(context.Background(), &fakeEval{res: passResult()}, draftJSON, first.selection, 2, ctx)
	if !repeated.escalate || repeated.deliver || !strings.Contains(repeated.message, "死循环") {
		t.Fatalf("重复的预算越界配置应停止且不交付,得到 %+v", repeated)
	}
	final := decide(context.Background(), &fakeEval{res: passResult()}, draftJSON, "", maxLoopRounds, ctx)
	if !final.escalate || final.deliver || !strings.Contains(final.message, "不保存版本") {
		t.Fatalf("末轮预算越界应停止且不交付,得到 %+v", final)
	}
}

func TestBudgetWindowSkipsIncompleteQuote(t *testing.T) {
	res := passResult()
	res.Quote.MissingCount = 1
	v := decide(context.Background(), &fakeEval{res: res}, draftJSON, "", 1, budgetChangeCtx(8000, 0.1))
	if !v.deliver {
		t.Fatalf("缺价时无法判定下界,应保留原有降级交付,得到 %+v", v)
	}
}

func TestDecideFailFeedsBackFailedChecks(t *testing.T) {
	v := decide(context.Background(), &fakeEval{res: failResult()}, draftJSON, "", 1, nil)
	if v.escalate {
		t.Error("fail 非末轮不应出栈")
	}
	if !strings.Contains(v.message, "SOCKET_MATCH") || !strings.Contains(v.message, "定向修复") {
		t.Errorf("应回喂具体失败项, 得到 %q", v.message)
	}
}

func TestDecideFailRepeatedSelectionIsDeadLoop(t *testing.T) {
	f := &fakeEval{res: failResult()}
	first := decide(context.Background(), f, draftJSON, "", 1, nil)
	second := decide(context.Background(), f, draftJSON, first.selection, 2, nil)
	if !second.escalate || !strings.Contains(second.message, "死循环") {
		t.Errorf("重复 selection 应判死循环出栈, 得到 %+v", second)
	}
}

func TestDecideFailFinalRoundDeliversFailureReport(t *testing.T) {
	v := decide(context.Background(), &fakeEval{res: failResult()}, draftJSON, "", maxLoopRounds, nil)
	if !v.escalate || !strings.Contains(v.message, "轮数用尽") || !strings.Contains(v.message, "SOCKET_MATCH") {
		t.Errorf("末轮 fail 应带失败报告如实出栈, 得到 %+v", v)
	}
}

func TestDecideStripsCodeFence(t *testing.T) {
	fenced := "```json\n" + draftJSON + "\n```"
	f := &fakeEval{res: passResult()}
	v := decide(context.Background(), f, fenced, "", 1, nil)
	if !v.escalate || f.gotSel.CPU != "amd-ryzen5-7500f" {
		t.Errorf("应容忍 markdown 围栏, 得到 %+v", v)
	}
}

func TestDecideExtractsDraftFromMixedText(t *testing.T) {
	// Pass@k 回归发现的真实失败模式:思考文字(含花括号碎片与无关 JSON)+ BuildDraft 混排。
	mixed := "预算分配 {gpu: 45%} 如下。参考需求 {\"budget_cny\": 8000} 完成选件。\n" + draftJSON + "\n以上是最终配置。"
	f := &fakeEval{res: passResult()}
	v := decide(context.Background(), f, mixed, "", 1, nil)
	if !v.escalate || f.gotSel.CPU != "amd-ryzen5-7500f" {
		t.Errorf("应从混排文本提取 BuildDraft, 得到 %+v", v)
	}
	if !strings.Contains(v.message, "pass") {
		t.Errorf("应正常交付, 得到 %q", v.message)
	}
}

func TestExtractBuildDraft(t *testing.T) {
	if _, err := extractBuildDraft("等待需求确认后再生成配置"); !errors.Is(err, errNoDraft) {
		t.Errorf("纯文本应报 errNoDraft, 得到 %v", err)
	}
	if _, err := extractBuildDraft("说明 {\"schema_version\": 2} 结束"); err == nil || errors.Is(err, errNoDraft) {
		t.Errorf("只有非法 draft 时应报 schema 错, 得到 %v", err)
	}
	draft, err := extractBuildDraft("前缀 {不是JSON} " + draftJSON)
	if err != nil || draft.BuildRef != "build_001" {
		t.Errorf("应跳过非法片段提取 draft, 得到 %+v, %v", draft, err)
	}
}
