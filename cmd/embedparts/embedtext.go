package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// styleEntry scripts/data/styles/styles.json 的单条风格标注(P3-语义选件设计.md §2)。
// 指针字段 nil = 未标注/未知,拼接时直接省略,不输出"未知"。
type styleEntry struct {
	Noise     *string  `json:"noise"`      // silent|normal|loud
	Color     *string  `json:"color"`      // 常见配色,| 分隔
	SidePanel *string  `json:"side_panel"` // 仅机箱:tempered_glass|mesh|solid
	StyleTags []string `json:"style_tags"`
	Summary   string   `json:"summary"`
}

// loadStyles 读取风格标注文件;跳过 "_" 前缀的说明键。
func loadStyles(path string) (map[string]styleEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取风格标注失败: %w", err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("解析风格标注失败: %w", err)
	}
	out := make(map[string]styleEntry, len(all))
	for sku, entry := range all {
		if strings.HasPrefix(sku, "_") {
			continue
		}
		var se styleEntry
		if err := json.Unmarshal(entry, &se); err != nil {
			return nil, fmt.Errorf("风格标注 %q 非法: %w", sku, err)
		}
		out[sku] = se
	}
	return out, nil
}

// categoryZH 品类中文名(embedding 文本的品类上下文,工程实践指引 §七.3)。
var categoryZH = map[schemas.Category]string{
	schemas.CategoryCPU:         "处理器",
	schemas.CategoryGPU:         "显卡",
	schemas.CategoryMotherboard: "主板",
	schemas.CategoryMemory:      "内存",
	schemas.CategorySSD:         "固态硬盘",
	schemas.CategoryPSU:         "电源",
	schemas.CategoryCase:        "机箱",
	schemas.CategoryCooler:      "散热器",
}

var noiseZH = map[string]string{
	"silent": "噪音表现:安静低噪",
	"normal": "噪音表现:正常",
	"loud":   "噪音偏大",
}

var colorZH = map[string]string{
	"black": "黑色", "white": "白色", "brown": "棕色", "red": "红色", "silver": "银色",
}

var sidePanelZH = map[string]string{
	"tempered_glass": "钢化玻璃侧透",
	"mesh":           "网孔侧板",
	"solid":          "封闭侧板",
}

// buildEmbeddingText 拼接单个零件的 embedding 文本(设计 §3 模板):
// 品类 + 品牌型号 + specs 摘要 + 风格字段人话化 + 标签 + 摘要;缺失段省略。
func buildEmbeddingText(category schemas.Category, brand, model string, specs map[string]any, st *styleEntry) string {
	segs := []string{
		fmt.Sprintf("%s: %s %s", categoryZH[category], brand, model),
	}
	if s := specsSummary(specs); s != "" {
		segs = append(segs, s)
	}
	if st != nil {
		if st.Noise != nil {
			if zh, ok := noiseZH[*st.Noise]; ok {
				segs = append(segs, zh)
			}
		}
		if st.Color != nil {
			var names []string
			for _, c := range strings.Split(*st.Color, "|") {
				if zh, ok := colorZH[c]; ok {
					names = append(names, zh)
				} else if c != "" {
					names = append(names, c)
				}
			}
			if len(names) > 0 {
				segs = append(segs, "常见配色:"+strings.Join(names, "/"))
			}
		}
		if st.SidePanel != nil {
			if zh, ok := sidePanelZH[*st.SidePanel]; ok {
				segs = append(segs, zh)
			}
		}
		if len(st.StyleTags) > 0 {
			segs = append(segs, strings.Join(st.StyleTags, ","))
		}
		if st.Summary != "" {
			segs = append(segs, st.Summary)
		}
	}
	return strings.Join(segs, "。") + "。"
}

// specsSummary canonical specs 摘要:按键名排序的 k=v 列表(数组值展开)。
func specsSummary(specs map[string]any) string {
	if len(specs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(specs))
	for k := range specs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := specs[k]
		if v == nil {
			continue // null = 未知,不进文本
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, formatSpecValue(v)))
	}
	return strings.Join(parts, ", ")
}

func formatSpecValue(v any) string {
	switch t := v.(type) {
	case []any:
		items := make([]string, 0, len(t))
		for _, it := range t {
			items = append(items, formatSpecValue(it))
		}
		return strings.Join(items, "/")
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
