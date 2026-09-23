package schemas

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const validRequirementJSON = `{
  "schema_version": 2, "configuration_scope": ["tower"],
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
  "owned_parts": [{"category": "ssd", "model": "三星 990 Pro 1TB"}],
  "budget_basis": "new_purchase",
  "priority": ["gpu", "cpu"],
  "notes": "机箱要小"
}`

// 直接解码与 Projection 使用同一组合不变量:最低矩阵与 ownership 约束
// 不允许两条路径语义不同。
func TestDecodeRequirementSpecCombinationInvariants(t *testing.T) {
	base := func(mutate func(map[string]any)) string {
		value := map[string]any{
			"schema_version": 2, "configuration_scope": []string{"tower"},
			"budget_cny":     8000,
			"use_case":       map[string]any{"type": "productivity", "titles": []string{"Premiere"}, "resolution": "4K"},
			"existing_parts": []string{"gpu"},
			"owned_parts":    []any{map[string]any{"category": "gpu", "model": "RTX 4060"}},
			"budget_basis":   "new_purchase",
		}
		mutate(value)
		raw, _ := json.Marshal(value)
		return string(raw)
	}
	cases := []struct {
		name  string
		json  string
		valid bool
	}{
		{"合法基线", base(func(map[string]any) {}), true},
		{"existing 空但 owned 非空", base(func(v map[string]any) { v["existing_parts"] = []string{} }), false},
		{"owned 品类不在 existing", base(func(v map[string]any) {
			v["existing_parts"] = []string{"gpu", "memory"}
			v["owned_parts"] = []any{map[string]any{"category": "gpu", "model": "RTX 4060"}}
		}), false},
		{"existing 品类缺型号", base(func(v map[string]any) {
			v["existing_parts"] = []string{"gpu", "memory"}
			v["owned_parts"] = []any{
				map[string]any{"category": "gpu", "model": "RTX 4060"},
				map[string]any{"category": "memory", "model": " "},
			}
		}), false},
		{"已有件缺预算口径", base(func(v map[string]any) { v["budget_basis"] = "" }), false},
		{"productivity 缺 titles", base(func(v map[string]any) {
			v["use_case"] = map[string]any{"type": "productivity"}
			v["existing_parts"] = []string{}
			v["owned_parts"] = nil
		}), false},
		{"productivity 空白标题", base(func(v map[string]any) {
			v["use_case"] = map[string]any{"type": "productivity", "titles": []string{"  "}}
			v["existing_parts"] = []string{}
			v["owned_parts"] = nil
		}), false},
		{"gaming any 分辨率", base(func(v map[string]any) {
			v["use_case"] = map[string]any{"type": "gaming", "resolution": "any"}
		}), false},
		{"gaming 缺分辨率", base(func(v map[string]any) {
			v["use_case"] = map[string]any{"type": "gaming"}
		}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeRequirementSpec([]byte(c.json))
			if c.valid && err != nil {
				t.Fatalf("合法组合被拒绝: %v", err)
			}
			if !c.valid && err == nil {
				t.Fatal("非法组合被接受")
			}
		})
	}
}

func TestDecodeRequirementSpecValid(t *testing.T) {
	got, err := DecodeRequirementSpec([]byte(validRequirementJSON))
	if err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	if got.SchemaVersion != RequirementSpecSchemaVersion || got.BudgetCNY != 8000 || got.BudgetFlex != 0.15 {
		t.Errorf("头部字段解析错误: %+v", got)
	}
	if got.UseCase.PerformanceGoal != PerformanceGoalBalanced || len(got.ConfigurationScope) != 1 || got.ConfigurationScope[0] != ConfigurationScopeTower {
		t.Errorf("v2 组合默认解析错误: %+v %+v", got.UseCase, got.ConfigurationScope)
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
  "schema_version": 2, "configuration_scope": ["tower"],
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
  "schema_version": 2, "configuration_scope": ["tower"],
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
		{"schema_version 缺失", strings.Replace(validRequirementJSON, `"schema_version": 2, "configuration_scope": ["tower"],`, ``, 1)},
		{"schema_version 不支持", strings.Replace(validRequirementJSON, `"schema_version": 2, "configuration_scope": ["tower"]`, `"schema_version": 2`, 1)},
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
		{"未知字段", strings.Replace(validRequirementJSON, `"schema_version": 2,`, `"schema_version": 2, "extra": true,`, 1)},
		{"缺少 configuration_scope", strings.Replace(validRequirementJSON, `"configuration_scope": ["tower"],`, ``, 1)},
		{"非法 configuration_scope", strings.Replace(validRequirementJSON, `"configuration_scope": ["tower"],`, `"configuration_scope": ["monitor"],`, 1)},
		{"gaming 拒绝 resolution=any", strings.Replace(validRequirementJSON, `"resolution": "2K"`, `"resolution": "any"`, 1)},
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
