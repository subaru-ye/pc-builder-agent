package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// fixtureBuild 组一行 builds 表数据(v1 整单生成,可选覆写)。
func fixtureBuild(t *testing.T, version int, mutate func(*wireDraft, *validate.Quote)) store.BuildVersion {
	t.Helper()
	price := func(s string) *string { return &s }
	draft := wireDraft{
		BuildRef: "build_001",
		Selection: wireSel{
			CPU: "amd-ryzen5-7500f", Motherboard: "msi-b650m-mortar-wifi",
			Memory: "kingston-fury-beast-ddr5-6000-16gx2",
			SSD:    []wireSSD{{SKU: "samsung-990pro-1tb", Quantity: 1}},
			GPU:    price("asus-dual-rtx4070s"),
			PSU:    "seasonic-focus-gx-750", Case: "fractal-north", Cooler: "thermalright-pa120-se",
		},
	}
	quote := validate.Quote{
		SnapshotDate: "2026-07-28",
		TotalCNY:     "6100.00",
		Lines: []validate.QuoteLine{
			{Category: schemas.CategoryCPU, SKU: "amd-ryzen5-7500f", Quantity: 1, UnitPriceCNY: price("1099.00"), SubtotalCNY: price("1099.00")},
			{Category: schemas.CategoryGPU, SKU: "asus-dual-rtx4070s", Quantity: 1, UnitPriceCNY: price("4599.00"), SubtotalCNY: price("4599.00")},
		},
	}
	if mutate != nil {
		mutate(&draft, &quote)
	}

	draftJSON, err := json.Marshal(draft)
	if err != nil {
		t.Fatalf("序列化 draft 失败: %v", err)
	}
	quoteJSON, err := json.Marshal(quote)
	if err != nil {
		t.Fatalf("序列化 quote 失败: %v", err)
	}
	report := schemas.ValidationReport{
		BuildRef:      "build_001",
		OverallStatus: schemas.OverallPass,
		Checks: []schemas.CheckResult{
			{RuleID: schemas.RuleSocketMatch, Outcome: schemas.OutcomePass, Severity: schemas.SeverityNone},
		},
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("序列化 report 失败: %v", err)
	}
	return store.BuildVersion{
		ID: int64(10 + version), SessionID: "sess_1", Version: version,
		RequirementID: 21, Draft: draftJSON, Validation: reportJSON, Quote: quoteJSON,
		CreatedAt: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
	}
}

const specJSON = `{"schema_version":2,"configuration_scope":["tower"],"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"}}`

func TestDecodeBuildIntentLabels(t *testing.T) {
	b := fixtureBuild(t, 1, nil)
	row, err := decodeBuild(b, json.RawMessage(specJSON))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if row.Intent != "整单生成" || row.budgetCNY() != 8000 {
		t.Errorf("v1 应为整单生成且预算 8000, 得到 %q, %d", row.Intent, row.budgetCNY())
	}

	b.Change = json.RawMessage(`{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget","budget_delta_cny":-500}`)
	row, err = decodeBuild(b, nil)
	if err != nil || row.Intent != "adjust_budget" {
		t.Errorf("应取 change.intent, 得到 %q, %v", row.Intent, err)
	}
}

func TestRenderListShowsTreeAndTotals(t *testing.T) {
	v1 := fixtureBuild(t, 1, nil)
	v2 := fixtureBuild(t, 2, func(_ *wireDraft, q *validate.Quote) { q.TotalCNY = "5800.00" })
	parent := v1.ID
	v2.ParentID = &parent
	v2.Change = json.RawMessage(`{"intent":"swap_part"}`)

	rows, err := decodeBuilds([]store.BuildVersion{v1, v2})
	if err != nil {
		t.Fatalf("批量解码失败: %v", err)
	}
	out := renderList("sess_1", rows)
	for _, want := range []string{"v1 ← 根", "v2 ← v1", "swap_part", "¥5800.00", "快照 2026-07-28"} {
		if !strings.Contains(out, want) {
			t.Errorf("回放缺少 %q:\n%s", want, out)
		}
	}
}

func TestRenderDiff(t *testing.T) {
	price := func(s string) *string { return &s }
	v1 := fixtureBuild(t, 1, nil)
	v3 := fixtureBuild(t, 3, func(d *wireDraft, q *validate.Quote) {
		d.Selection.GPU = price("sapphire-pulse-rx7800xt")
		q.TotalCNY = "5600.00"
		q.Lines[1] = validate.QuoteLine{
			Category: schemas.CategoryGPU, SKU: "sapphire-pulse-rx7800xt", Quantity: 1,
			UnitPriceCNY: price("4099.00"), SubtotalCNY: price("4099.00"),
		}
	})

	fromRow, err := decodeBuild(v1, json.RawMessage(specJSON))
	if err != nil {
		t.Fatalf("解码 v1 失败: %v", err)
	}
	toRow, err := decodeBuild(v3, json.RawMessage(`{"schema_version":2,"configuration_scope":["tower"],"budget_cny":7500,"use_case":{"type":"gaming","resolution":"2K"}}`))
	if err != nil {
		t.Fatalf("解码 v3 失败: %v", err)
	}

	out, err := renderDiff(fromRow, toRow)
	if err != nil {
		t.Fatalf("diff 失败: %v", err)
	}
	for _, want := range []string{
		"版本对比:v1 → v3",
		"cpu: 不变(amd-ryzen5-7500f)",
		"gpu: asus-dual-rtx4070s → sapphire-pulse-rx7800xt(¥4599.00 → ¥4099.00,-500.00)",
		"合计:¥6100.00 → ¥5600.00(-500.00)",
		"预算:¥8000 → ¥7500(-500)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("diff 缺少 %q:\n%s", want, out)
		}
	}
}

func TestRenderDiffGPUToNull(t *testing.T) {
	v1 := fixtureBuild(t, 1, nil)
	v2 := fixtureBuild(t, 2, func(d *wireDraft, q *validate.Quote) {
		d.Selection.GPU = nil
		q.TotalCNY = "1501.00"
		q.Lines = q.Lines[:1] // 只剩 CPU 行
	})
	fromRow, _ := decodeBuild(v1, nil)
	toRow, _ := decodeBuild(v2, nil)
	out, err := renderDiff(fromRow, toRow)
	if err != nil {
		t.Fatalf("diff 失败: %v", err)
	}
	if !strings.Contains(out, "gpu: asus-dual-rtx4070s → 无独显(¥4599.00 → ¥0.00,-4599.00)") {
		t.Errorf("gpu→null 差额应按 0 计:\n%s", out)
	}
	if !strings.Contains(out, "预算:需求单解不出预算,略。") {
		t.Errorf("无需求单应优雅降级:\n%s", out)
	}
}

func TestRenderExport(t *testing.T) {
	row, err := decodeBuild(fixtureBuild(t, 3, nil), json.RawMessage(specJSON))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	md, err := renderExport(row, map[string]string{"amd-ryzen5-7500f": "AMD Ryzen 5 7500F"})
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	for _, want := range []string{
		"# 装机配置单 v3",
		"需求:预算 ¥8000,用途 gaming(2K)",
		"| cpu | amd-ryzen5-7500f | AMD Ryzen 5 7500F | 1 | 1099.00 | 1099.00 |",
		"| gpu | asus-dual-rtx4070s | - | 1 | 4599.00 | 4599.00 |", // 名称缺失降级为 -
		"**合计:¥6100.00**(价格快照 2026-07-28)",
		"- 总体:pass",
		"报价为 2026-07-28 快照参考价,非实时价格。",
		"下单前请以官方规格页复核",
		"不构成购买建议",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("导出缺少 %q:\n%s", want, md)
		}
	}
}

func TestParseFormatFen(t *testing.T) {
	cases := []struct {
		in  string
		fen int
		ok  bool
	}{
		{"6100.00", 610000, true},
		{"6100", 610000, true},
		{"0.05", 5, true},
		{"-12.30", -1230, true},
		{"1.234", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		fen, ok := parseFen(c.in)
		if fen != c.fen || ok != c.ok {
			t.Errorf("parseFen(%q) = %d,%v, 期望 %d,%v", c.in, fen, ok, c.fen, c.ok)
		}
	}
	if got := signedFen(-500); got != "-5.00" {
		t.Errorf("signedFen(-500) = %q", got)
	}
	if got := signedFen(500); got != "+5.00" {
		t.Errorf("signedFen(500) = %q", got)
	}
}
