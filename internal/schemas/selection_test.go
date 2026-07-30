package schemas

import (
	"reflect"
	"strings"
	"testing"
)

const validSelectionJSON = `{
  "schema_version": 1,
  "build_ref": "build_demo_001",
  "parts": {
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

func TestDecodeBuildSelectionValid(t *testing.T) {
	got, err := DecodeBuildSelection([]byte(validSelectionJSON))
	if err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	if got.SchemaVersion != 1 || got.BuildRef != "build_demo_001" {
		t.Errorf("头部字段解析错误: %+v", got)
	}
	if got.CPU != "amd-ryzen5-7500f" || got.Cooler != "thermalright-pa120-se" {
		t.Errorf("部件 SKU 解析错误: %+v", got)
	}
	if got.GPU == nil || *got.GPU != "asus-dual-rtx4070s" {
		t.Errorf("gpu 应为显式 SKU: %+v", got.GPU)
	}
	if len(got.SSDs) != 1 || got.SSDs[0].Quantity != 1 {
		t.Errorf("ssd 解析错误: %+v", got.SSDs)
	}
}

func TestDecodeBuildSelectionGPUNull(t *testing.T) {
	j := strings.Replace(validSelectionJSON, `"gpu": "asus-dual-rtx4070s"`, `"gpu": null`, 1)
	got, err := DecodeBuildSelection([]byte(j))
	if err != nil {
		t.Fatalf("gpu 显式 null 合法: %v", err)
	}
	if got.GPU != nil {
		t.Errorf("gpu 应为 nil,得到 %+v", got.GPU)
	}
}

func TestDecodeBuildSelectionErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"gpu 键缺失", strings.Replace(validSelectionJSON, `"gpu": "asus-dual-rtx4070s",`, ``, 1)},
		{"必选部件 cooler 缺失", strings.Replace(validSelectionJSON, `,
    "cooler": "thermalright-pa120-se"`, ``, 1)},
		{"schema_version 不支持", strings.Replace(validSelectionJSON, `"schema_version": 1`, `"schema_version": 2`, 1)},
		{"schema_version 缺失", strings.Replace(validSelectionJSON, `"schema_version": 1,`, ``, 1)},
		{"build_ref 为空", strings.Replace(validSelectionJSON, `"build_ref": "build_demo_001"`, `"build_ref": ""`, 1)},
		{"未知字段", strings.Replace(validSelectionJSON, `"schema_version": 1,`, `"schema_version": 1, "extra": true,`, 1)},
		{"ssd 空数组", strings.Replace(validSelectionJSON, `[{"sku": "samsung-990pro-1tb", "quantity": 1}]`, `[]`, 1)},
		{"ssd 数量为零", strings.Replace(validSelectionJSON, `"quantity": 1`, `"quantity": 0`, 1)},
		{"ssd 数量为负", strings.Replace(validSelectionJSON, `"quantity": 1`, `"quantity": -2`, 1)},
		{"ssd 未知字段", strings.Replace(validSelectionJSON, `"quantity": 1`, `"quantity": 1, "color": "red"`, 1)},
		{"gpu 空字符串", strings.Replace(validSelectionJSON, `"gpu": "asus-dual-rtx4070s"`, `"gpu": ""`, 1)},
		{"cpu 为 null", strings.Replace(validSelectionJSON, `"cpu": "amd-ryzen5-7500f"`, `"cpu": null`, 1)},
		{"parts 缺失", `{"schema_version": 1, "build_ref": "b"}`},
		{"非 JSON", `not-json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := DecodeBuildSelection([]byte(c.json)); err == nil {
				t.Errorf("期望 schema error,却解析成功")
			}
		})
	}
}

func TestBuildSelectionSKUs(t *testing.T) {
	sel, err := DecodeBuildSelection([]byte(validSelectionJSON))
	if err != nil {
		t.Fatal(err)
	}
	got := sel.SKUs()
	want := []string{
		"amd-ryzen5-7500f",
		"asus-dual-rtx4070s",
		"fractal-north",
		"kingston-fury-beast-ddr5-6000-16gx2",
		"msi-b650m-mortar-wifi",
		"samsung-990pro-1tb",
		"seasonic-focus-gx-750",
		"thermalright-pa120-se",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SKUs() = %v, want %v", got, want)
	}
}
