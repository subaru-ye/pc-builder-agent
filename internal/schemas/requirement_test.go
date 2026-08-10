package schemas

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const validRequirementJSON = `{
  "schema_version": 1,
  "budget_cny": 8000,
  "budget_flex": 0.15,
  "use_case": {
    "type": "gaming",
    "titles": ["黑神话:悟空"],
    "resolution": "2K",
    "fps_target": 120
  },
  "size_pref": "matx",
  "noise_pref": "silent",
  "brand_pref": {"cpu": "amd", "gpu": "nvidia"},
  "existing_parts": ["ssd"],
  "priority": ["gpu", "cpu"],
  "notes": "机箱要小"
}`

func TestDecodeRequirementSpecValid(t *testing.T) {
	got, err := DecodeRequirementSpec([]byte(validRequirementJSON))
	if err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	if got.SchemaVersion != 1 || got.BudgetCNY != 8000 || got.BudgetFlex != 0.15 {
		t.Errorf("头部字段解析错误: %+v", got)
	}
	if got.UseCase.Type != UseCaseGaming || got.UseCase.Resolution != Resolution2K {
		t.Errorf("use_case 解析错误: %+v", got.UseCase)
	}
	if got.UseCase.FPSTarget == nil || *got.UseCase.FPSTarget != 120 {
		t.Errorf("fps_target 解析错误: %+v", got.UseCase.FPSTarget)
	}
	if got.SizePref != SizePrefMATX || got.NoisePref != NoisePrefSilent {
		t.Errorf("偏好解析错误: %+v", got)
	}
	if got.BrandPref.CPU != CPUBrandAMD || got.BrandPref.GPU != GPUBrandNvidia {
		t.Errorf("brand_pref 解析错误: %+v", got.BrandPref)
	}
	if !reflect.DeepEqual(got.ExistingParts, []Category{"ssd"}) {
		t.Errorf("existing_parts 解析错误: %+v", got.ExistingParts)
	}
	if !reflect.DeepEqual(got.Priority, []Category{"gpu", "cpu"}) {
		t.Errorf("priority 解析错误: %+v", got.Priority)
	}
}

// TestDecodeRequirementSpecDefaults 只给必填字段,验证可缺省字段落到默认值。
func TestDecodeRequirementSpecDefaults(t *testing.T) {
	minimal := `{
  "schema_version": 1,
  "budget_cny": 5000,
  "use_case": {"type": "general"}
}`
	got, err := DecodeRequirementSpec([]byte(minimal))
	if err != nil {
		t.Fatalf("最小合法输入不应报错: %v", err)
	}
	if got.BudgetFlex != 0.1 {
		t.Errorf("budget_flex 应缺省 0.1,得到 %v", got.BudgetFlex)
	}
	if got.SizePref != SizePrefAny || got.NoisePref != NoisePrefAny {
		t.Errorf("size/noise 偏好应缺省 any: %+v", got)
	}
	if got.BrandPref.CPU != CPUBrandAny || got.BrandPref.GPU != GPUBrandAny {
		t.Errorf("brand_pref 应缺省 {any,any}: %+v", got.BrandPref)
	}
	if got.UseCase.Resolution != "" || got.UseCase.FPSTarget != nil {
		t.Errorf("非 gaming 用途 resolution/fps 应为空: %+v", got.UseCase)
	}
}

func TestEncodeRequirementSpecMaterializesDefaults(t *testing.T) {
	spec, err := DecodeRequirementSpec([]byte(`{
  "schema_version": 1,
  "budget_cny": 5000,
  "use_case": {"type": "general"}
}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeRequirementSpec(spec)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["budget_flex"] != 0.1 || got["size_pref"] != "any" || got["noise_pref"] != "any" {
		t.Fatalf("缺省值未展开: %s", encoded)
	}
	if !reflect.DeepEqual(got["existing_parts"], []any{}) || !reflect.DeepEqual(got["priority"], []any{}) {
		t.Fatalf("列表应编码为空数组而非 null: %s", encoded)
	}
	brand, ok := got["brand_pref"].(map[string]any)
	if !ok || brand["cpu"] != "any" || brand["gpu"] != "any" {
		t.Fatalf("品牌缺省值未展开: %s", encoded)
	}
	if _, err := DecodeRequirementSpec(encoded); err != nil {
		t.Fatalf("编码结果必须能严格回读: %v", err)
	}
}

func TestDecodeRequirementSpecErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"schema_version 缺失", strings.Replace(validRequirementJSON, `"schema_version": 1,`, ``, 1)},
		{"schema_version 不支持", strings.Replace(validRequirementJSON, `"schema_version": 1`, `"schema_version": 2`, 1)},
		{"budget_cny 缺失", strings.Replace(validRequirementJSON, `"budget_cny": 8000,`, ``, 1)},
		{"budget_cny 为零", strings.Replace(validRequirementJSON, `"budget_cny": 8000`, `"budget_cny": 0`, 1)},
		{"budget_cny 为负", strings.Replace(validRequirementJSON, `"budget_cny": 8000`, `"budget_cny": -100`, 1)},
		{"budget_flex 超上限", strings.Replace(validRequirementJSON, `"budget_flex": 0.15`, `"budget_flex": 0.5`, 1)},
		{"budget_flex 为负", strings.Replace(validRequirementJSON, `"budget_flex": 0.15`, `"budget_flex": -0.1`, 1)},
		{"use_case 缺失", strings.Replace(validRequirementJSON, `"use_case": {
    "type": "gaming",
    "titles": ["黑神话:悟空"],
    "resolution": "2K",
    "fps_target": 120
  },`, ``, 1)},
		{"use_case.type 缺失", strings.Replace(validRequirementJSON, `"type": "gaming",`, ``, 1)},
		{"use_case.type 非法", strings.Replace(validRequirementJSON, `"type": "gaming"`, `"type": "mining"`, 1)},
		{"gaming 缺 resolution", strings.Replace(validRequirementJSON, `"resolution": "2K",`, ``, 1)},
		{"resolution 非法", strings.Replace(validRequirementJSON, `"resolution": "2K"`, `"resolution": "8K"`, 1)},
		{"fps_target 为零", strings.Replace(validRequirementJSON, `"fps_target": 120`, `"fps_target": 0`, 1)},
		{"size_pref 非法", strings.Replace(validRequirementJSON, `"size_pref": "matx"`, `"size_pref": "huge"`, 1)},
		{"noise_pref 非法", strings.Replace(validRequirementJSON, `"noise_pref": "silent"`, `"noise_pref": "loud"`, 1)},
		{"brand_pref.cpu 非法", strings.Replace(validRequirementJSON, `"cpu": "amd"`, `"cpu": "via"`, 1)},
		{"existing_parts 非法品类", strings.Replace(validRequirementJSON, `"existing_parts": ["ssd"]`, `"existing_parts": ["fan"]`, 1)},
		{"priority 非法品类", strings.Replace(validRequirementJSON, `"priority": ["gpu", "cpu"]`, `"priority": ["rgb"]`, 1)},
		{"未知字段", strings.Replace(validRequirementJSON, `"schema_version": 1,`, `"schema_version": 1, "extra": true,`, 1)},
		{"非 JSON", `not-json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := DecodeRequirementSpec([]byte(c.json)); err == nil {
				t.Errorf("期望 schema error,却解析成功")
			}
		})
	}
}
