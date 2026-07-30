package pipeline

import (
	"context"
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
