package schemas

import (
	"strings"
	"testing"
)

const validBuildDraftJSON = `{
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
  },
  "rationale": {"cpu": "性价比甜点", "gpu": "2K 主力"},
  "budget_allocation": {"gpu": 0.42, "cpu": 0.18}
}`

func TestDecodeBuildDraftValid(t *testing.T) {
	got, err := DecodeBuildDraft([]byte(validBuildDraftJSON))
	if err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	if got.SchemaVersion != 1 || got.RequirementRef != "req_001" || got.BuildRef != "build_001" {
		t.Errorf("头部字段解析错误: %+v", got)
	}
	// selection 复用 §四.1 契约:build_ref 应透传进 Selection。
	if got.Selection.BuildRef != "build_001" || got.Selection.CPU != "amd-ryzen5-7500f" {
		t.Errorf("selection 解析错误: %+v", got.Selection)
	}
	if got.Selection.GPU == nil || *got.Selection.GPU != "asus-dual-rtx4070s" {
		t.Errorf("selection.gpu 应为显式 SKU: %+v", got.Selection.GPU)
	}
	if got.Rationale["cpu"] != "性价比甜点" || got.BudgetAllocation["gpu"] != 0.42 {
		t.Errorf("装饰字段透传错误: rationale=%v allocation=%v", got.Rationale, got.BudgetAllocation)
	}
}

// TestDecodeBuildDraftOptionalDecorations 缺装饰字段仍合法,gpu 显式 null 合法。
func TestDecodeBuildDraftOptionalDecorations(t *testing.T) {
	minimal := `{
  "schema_version": 1,
  "requirement_ref": "req_002",
  "build_ref": "build_002",
  "selection": {
    "cpu": "amd-ryzen5-8600g",
    "motherboard": "msi-b650m-mortar-wifi",
    "memory": "kingston-fury-beast-ddr5-6000-16gx2",
    "ssd": [{"sku": "samsung-990pro-1tb", "quantity": 1}],
    "gpu": null,
    "psu": "seasonic-focus-gx-750",
    "case": "fractal-north",
    "cooler": "thermalright-pa120-se"
  }
}`
	got, err := DecodeBuildDraft([]byte(minimal))
	if err != nil {
		t.Fatalf("缺装饰字段应合法: %v", err)
	}
	if got.Selection.GPU != nil {
		t.Errorf("gpu 应为 nil(核显方案): %+v", got.Selection.GPU)
	}
	if got.Rationale != nil || got.BudgetAllocation != nil {
		t.Errorf("缺装饰字段应为 nil: rationale=%v allocation=%v", got.Rationale, got.BudgetAllocation)
	}
}

func TestDecodeBuildDraftErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"schema_version 缺失", strings.Replace(validBuildDraftJSON, `"schema_version": 1,`, ``, 1)},
		{"schema_version 不支持", strings.Replace(validBuildDraftJSON, `"schema_version": 1`, `"schema_version": 2`, 1)},
		{"requirement_ref 缺失", strings.Replace(validBuildDraftJSON, `"requirement_ref": "req_001",`, ``, 1)},
		{"requirement_ref 为空", strings.Replace(validBuildDraftJSON, `"requirement_ref": "req_001"`, `"requirement_ref": ""`, 1)},
		{"build_ref 为空", strings.Replace(validBuildDraftJSON, `"build_ref": "build_001"`, `"build_ref": ""`, 1)},
		{"selection 缺失", strings.Replace(validBuildDraftJSON, `"selection": {
    "cpu": "amd-ryzen5-7500f",
    "motherboard": "msi-b650m-mortar-wifi",
    "memory": "kingston-fury-beast-ddr5-6000-16gx2",
    "ssd": [{"sku": "samsung-990pro-1tb", "quantity": 1}],
    "gpu": "asus-dual-rtx4070s",
    "psu": "seasonic-focus-gx-750",
    "case": "fractal-north",
    "cooler": "thermalright-pa120-se"
  },`, ``, 1)},
		{"selection 缺必选件", strings.Replace(validBuildDraftJSON, `"cooler": "thermalright-pa120-se"`, `"cooler": ""`, 1)},
		{"selection.gpu 键缺失", strings.Replace(validBuildDraftJSON, `"gpu": "asus-dual-rtx4070s",`, ``, 1)},
		{"未知字段", strings.Replace(validBuildDraftJSON, `"schema_version": 1,`, `"schema_version": 1, "extra": true,`, 1)},
		{"非 JSON", `not-json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := DecodeBuildDraft([]byte(c.json)); err == nil {
				t.Errorf("期望 schema error,却解析成功")
			}
		})
	}
}
