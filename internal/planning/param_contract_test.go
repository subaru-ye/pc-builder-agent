package planning

import (
	"context"
	"strings"
	"testing"
)

// paramExec 构造最小可执行环境：call 的校验路径不依赖 runner/result。
func paramExec() *execution {
	return &execution{
		seen:       map[string]bool{},
		date:       "2026-07-28",
		candidates: []Candidate{},
	}
}

// 红例（合同 v3）：显式 limit 越界不得静默改写为 16，必须报错并给出允许范围
// 与重试方式。Pass³ 审计确认旧代码静默改写（"显式非法值不得静默改写"合同）。
func TestParamExplicitLimitOutOfRangeRejected(t *testing.T) {
	got := paramExec().call(context.Background(), map[string]any{
		"action": "search_local", "payload": `{"category":"cpu","limit":100}`})
	err, _ := got["error"].(string)
	for _, want := range []string{"limit", "1", "24", "16", "重试"} {
		if !strings.Contains(err, want) {
			t.Fatalf("limit 越界错误缺 %q: %q", want, err)
		}
	}
	if got["candidates"] != nil {
		t.Fatal("越界 limit 不得返回候选")
	}
}

func TestParamExplicitNegativeOffsetRejected(t *testing.T) {
	got := paramExec().call(context.Background(), map[string]any{
		"action": "search_local", "payload": `{"category":"cpu","offset":-5}`})
	err, _ := got["error"].(string)
	for _, want := range []string{"offset", "0", "重试"} {
		if !strings.Contains(err, want) {
			t.Fatalf("负 offset 错误缺 %q: %q", want, err)
		}
	}
}

// 未知字段不得静默忽略：报错点名字段与允许集合。
func TestParamUnknownFieldRejected(t *testing.T) {
	got := paramExec().call(context.Background(), map[string]any{
		"action": "search_local", "payload": `{"query":"x","catgory":"cpu"}`})
	err, _ := got["error"].(string)
	for _, want := range []string{"catgory", "query", "category", "order_by", "offset", "limit"} {
		if !strings.Contains(err, want) {
			t.Fatalf("未知字段错误缺 %q: %q", want, err)
		}
	}
}

// category 显式非法（拼错）必须报错并列出合法品类，不得静默返回空结果。
func TestParamInvalidCategoryRejected(t *testing.T) {
	got := paramExec().call(context.Background(), map[string]any{
		"action": "search_local", "payload": `{"category":"gpus"}`})
	err, _ := got["error"].(string)
	for _, want := range []string{"category", "gpus", "cpu"} {
		if !strings.Contains(err, want) {
			t.Fatalf("非法 category 错误缺 %q: %q", want, err)
		}
	}
}

// payload JSON 语法错误必须带定位，不再只说"须为JSON对象字符串"
// （Pass³ 17 次该错误无位置提示，恢复靠模型重猜）。
func TestParamMalformedJSONReportsPosition(t *testing.T) {
	bad := `{"query":"x","limit":16` + `,,"}`
	got := paramExec().call(context.Background(), map[string]any{
		"action": "search_local", "payload": bad})
	err, _ := got["error"].(string)
	if !strings.Contains(err, "偏移") || !strings.Contains(err, "重试") {
		t.Fatalf("语法错误应含偏移与重试提示: %q", err)
	}
}

// evaluate 必须含 draft 包裹：缺失时错误说明包裹形态（Pass³ 6 次 EOF 无指引）。
func TestParamEvaluateRequiresDraftWrapper(t *testing.T) {
	got := paramExec().call(context.Background(), map[string]any{
		"action": "evaluate", "payload": `{"schema_version":1}`})
	err, _ := got["error"].(string)
	// 未知字段检查先行（点名 schema_version 与允许集 draft）或 draft 缺失检查
	// 均可——两条路都指明正确形态。
	if !strings.Contains(err, "draft") {
		t.Fatalf("evaluate 缺包裹错误应说明 draft 形态: %q", err)
	}
	if !strings.Contains(err, "允许的字段") && !strings.Contains(err, "缺失") {
		t.Fatalf("evaluate 错误应指明允许集或缺席: %q", err)
	}
}

// 未知 action 必须列出合法集合（Pass³ 4 次"未知工具操作"无指引）。
func TestParamUnknownActionListsKnown(t *testing.T) {
	got := paramExec().call(context.Background(), map[string]any{
		"action": "noop", "payload": `{}`})
	err, _ := got["error"].(string)
	for _, want := range []string{"noop", "search_local", "evaluate"} {
		if !strings.Contains(err, want) {
			t.Fatalf("未知 action 错误缺 %q: %q", want, err)
		}
	}
}

// 正例：字段缺席走文档化默认（limit=16、offset=0、order_by=relevance），
// 合法显式值原样生效——校验不得误伤合法调用。
func TestParamDefaultsAndLegalValuesUnchanged(t *testing.T) {
	x := paramExec()
	x.candidates = []Candidate{{ID: "cpu-a", Category: "cpu", Price: pricePtr("100.00")}}
	got := x.call(context.Background(), map[string]any{
		"action": "search_local", "payload": `{"category":"cpu"}`})
	if got["error"] != nil {
		t.Fatalf("默认参数调用不得报错: %v", got["error"])
	}
	if got["order_by"] != "relevance" {
		t.Fatalf("缺席 order_by 应默认 relevance: %v", got["order_by"])
	}
	got = x.call(context.Background(), map[string]any{
		"action": "search_local", "payload": `{"category":"cpu","limit":24,"offset":0,"order_by":"price_asc"}`})
	if got["error"] != nil {
		t.Fatalf("合法显式值不得报错: %v", got["error"])
	}
}

func pricePtr(s string) *string { return &s }
