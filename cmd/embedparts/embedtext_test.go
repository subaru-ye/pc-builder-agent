package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func strPtr(s string) *string { return &s }

// TestBuildEmbeddingTextWithStyle 有风格标注:各段人话化齐全、顺序稳定。
func TestBuildEmbeddingTextWithStyle(t *testing.T) {
	specs := map[string]any{
		"length_mm":      float64(310),
		"power_watt":     float64(285),
		"pcie_power":     []any{"8pin", "8pin"},
		"boost_mhz":      nil, // null = 未知,不进文本
		"vram_gb":        float64(16),
		"tdp_ratio_note": "x",
	}
	st := &styleEntry{
		Noise:     strPtr("silent"),
		Color:     strPtr("black|white"),
		SidePanel: strPtr("tempered_glass"),
		StyleTags: []string{"海景房", "双仓"},
		Summary:   "测试摘要",
	}
	got := buildEmbeddingText(schemas.CategoryGPU, "ASUS", "TUF-4080S", specs, st)

	for _, want := range []string{
		"显卡: ASUS TUF-4080S",
		"length_mm=310",
		"pcie_power=8pin/8pin",
		"噪音表现:安静低噪",
		"常见配色:黑色/白色",
		"钢化玻璃侧透",
		"海景房,双仓",
		"测试摘要",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("文本缺少 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "boost_mhz") {
		t.Errorf("null 值字段不应进文本:\n%s", got)
	}
	// specs 摘要按键名排序:length_mm 在 power_watt 之前。
	if strings.Index(got, "length_mm") > strings.Index(got, "power_watt") {
		t.Errorf("specs 摘要未按键名排序:\n%s", got)
	}
}

// TestBuildEmbeddingTextWithoutStyle 无标注降级:仅品类+品牌型号+specs 摘要,
// 不输出"未知"之类的占位。
func TestBuildEmbeddingTextWithoutStyle(t *testing.T) {
	got := buildEmbeddingText(schemas.CategoryCPU, "AMD", "R5-7600",
		map[string]any{"cores": float64(6)}, nil)
	want := "处理器: AMD R5-7600。cores=6。"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "未知") {
		t.Errorf("未标注不应输出占位文案: %q", got)
	}
}

// TestBuildEmbeddingTextEmptySpecs specs 为空时该段整体省略。
func TestBuildEmbeddingTextEmptySpecs(t *testing.T) {
	got := buildEmbeddingText(schemas.CategoryCase, "NZXT", "H6", nil, nil)
	if got != "机箱: NZXT H6。" {
		t.Errorf("got %q", got)
	}
}

// TestLoadStyles 跳过 "_" 说明键;非法条目报错。
func TestLoadStyles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "styles.json")
	content := `{
		"_comment": "说明",
		"case-x": {"noise": "silent", "color": null, "side_panel": "solid", "style_tags": ["静音舱"], "summary": "s"}
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadStyles(path)
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 条(跳过 _comment),得到 %d", len(got))
	}
	se, ok := got["case-x"]
	if !ok || se.Noise == nil || *se.Noise != "silent" || se.Color != nil {
		t.Errorf("条目解析不符: %+v", se)
	}

	if err := os.WriteFile(path, []byte(`{"case-x": {"noise": 3}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadStyles(path); err == nil {
		t.Error("非法条目应报错")
	}
}

// TestLoadStylesRealFile 仓库内真实标注文件必须始终可解析。
func TestLoadStylesRealFile(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "data", "styles", "styles.json")
	got, err := loadStyles(path)
	if err != nil {
		t.Fatalf("真实标注文件解析失败: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("真实标注文件不应为空")
	}
	for sku, se := range got {
		if se.Noise != nil {
			if _, ok := noiseZH[*se.Noise]; !ok {
				t.Errorf("SKU %q noise 枚举非法: %q", sku, *se.Noise)
			}
		}
		if se.SidePanel != nil {
			if _, ok := sidePanelZH[*se.SidePanel]; !ok {
				t.Errorf("SKU %q side_panel 枚举非法: %q", sku, *se.SidePanel)
			}
		}
	}
}
