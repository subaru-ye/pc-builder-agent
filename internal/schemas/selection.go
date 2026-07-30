package schemas

import (
	"encoding/json"
	"fmt"
	"sort"
)

// BuildSelectionSchemaVersion 当前唯一支持的 BuildSelection schema 版本。
const BuildSelectionSchemaVersion = 1

// SSDSelection 一条 SSD 选择:SKU + 数量(quantity 必须为正整数)。
type SSDSelection struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

// BuildSelection P1 CLI 输入:schema_version=1、build_ref 与各部件 SKU。
// 除 gpu 外其余七类必选,缺失属于 schema error;gpu 必须显式给 SKU 或 null。
// 使用 DecodeBuildSelection 解码,不要直接 json.Unmarshal(区分不了 gpu 缺失与显式 null)。
type BuildSelection struct {
	SchemaVersion int
	BuildRef      string
	CPU           string
	Motherboard   string
	Memory        string
	SSDs          []SSDSelection
	GPU           *string // nil = 显式 null(无独显,靠核显点亮)
	PSU           string
	Case          string
	Cooler        string
}

// buildSelectionWire 与 selectionPartsWire 是 JSON 线上格式的中间形态,
// 用 RawMessage 区分「键缺失」与「显式 null」。
type buildSelectionWire struct {
	SchemaVersion *int                `json:"schema_version"`
	BuildRef      *string             `json:"build_ref"`
	Parts         *selectionPartsWire `json:"parts"`
}

type selectionPartsWire struct {
	CPU         json.RawMessage `json:"cpu"`
	Motherboard json.RawMessage `json:"motherboard"`
	Memory      json.RawMessage `json:"memory"`
	SSD         json.RawMessage `json:"ssd"`
	GPU         json.RawMessage `json:"gpu"`
	PSU         json.RawMessage `json:"psu"`
	Case        json.RawMessage `json:"case"`
	Cooler      json.RawMessage `json:"cooler"`
}

// DecodeBuildSelection 严格解码手写配置 JSON;任何违反契约之处均返回 error(schema error)。
func DecodeBuildSelection(data []byte) (BuildSelection, error) {
	var w buildSelectionWire
	if err := decodeStrict(data, &w); err != nil {
		return BuildSelection{}, fmt.Errorf("build selection: %w", err)
	}

	if w.SchemaVersion == nil {
		return BuildSelection{}, fmt.Errorf("build selection: 缺少 schema_version")
	}
	if *w.SchemaVersion != BuildSelectionSchemaVersion {
		return BuildSelection{}, fmt.Errorf("build selection: 不支持的 schema_version %d(当前仅 %d)", *w.SchemaVersion, BuildSelectionSchemaVersion)
	}
	if w.BuildRef == nil || *w.BuildRef == "" {
		return BuildSelection{}, fmt.Errorf("build selection: build_ref 缺失或为空")
	}
	if w.Parts == nil {
		return BuildSelection{}, fmt.Errorf("build selection: 缺少 parts")
	}

	return resolvePartsWire(w.Parts, *w.BuildRef)
}

// resolvePartsWire 校验 parts 线上格式并展开为 BuildSelection(附 buildRef)。
// DecodeBuildSelection(P1 CLI 输入)与 DecodeBuildDraft(P2 生成 Agent 产出)
// 复用同一套 parts 契约:七类必选、gpu 显式 SKU 或 null、ssd 为正数量数组。
func resolvePartsWire(parts *selectionPartsWire, buildRef string) (BuildSelection, error) {
	out := BuildSelection{
		SchemaVersion: BuildSelectionSchemaVersion,
		BuildRef:      buildRef,
	}

	// 七类必选部件:键必须存在且为非空字符串 SKU。
	required := []struct {
		name string
		raw  json.RawMessage
		dst  *string
	}{
		{"cpu", parts.CPU, &out.CPU},
		{"motherboard", parts.Motherboard, &out.Motherboard},
		{"memory", parts.Memory, &out.Memory},
		{"psu", parts.PSU, &out.PSU},
		{"case", parts.Case, &out.Case},
		{"cooler", parts.Cooler, &out.Cooler},
	}
	for _, r := range required {
		if r.raw == nil {
			return BuildSelection{}, fmt.Errorf("build selection: 必选部件 %s 缺失", r.name)
		}
		var sku string
		if err := json.Unmarshal(r.raw, &sku); err != nil {
			return BuildSelection{}, fmt.Errorf("build selection: 部件 %s 必须为 SKU 字符串: %w", r.name, err)
		}
		if sku == "" {
			return BuildSelection{}, fmt.Errorf("build selection: 部件 %s 的 SKU 不得为空", r.name)
		}
		*r.dst = sku
	}

	// SSD:必选,{sku, quantity} 数组,至少一条。
	if parts.SSD == nil {
		return BuildSelection{}, fmt.Errorf("build selection: 必选部件 ssd 缺失")
	}
	if err := decodeStrict(parts.SSD, &out.SSDs); err != nil {
		return BuildSelection{}, fmt.Errorf("build selection: ssd 必须为 {sku,quantity} 数组: %w", err)
	}
	if len(out.SSDs) == 0 {
		return BuildSelection{}, fmt.Errorf("build selection: ssd 数组不得为空")
	}
	for i, s := range out.SSDs {
		if s.SKU == "" {
			return BuildSelection{}, fmt.Errorf("build selection: ssd[%d].sku 不得为空", i)
		}
		if s.Quantity <= 0 {
			return BuildSelection{}, fmt.Errorf("build selection: ssd[%d].quantity 必须为正整数,得到 %d", i, s.Quantity)
		}
	}

	// GPU:键必须显式存在;值为 SKU 字符串或 null。
	if parts.GPU == nil {
		return BuildSelection{}, fmt.Errorf("build selection: gpu 必须显式为 SKU 或 null(键不可缺失)")
	}
	if string(parts.GPU) == "null" {
		out.GPU = nil
	} else {
		var sku string
		if err := json.Unmarshal(parts.GPU, &sku); err != nil {
			return BuildSelection{}, fmt.Errorf("build selection: gpu 必须为 SKU 字符串或 null: %w", err)
		}
		if sku == "" {
			return BuildSelection{}, fmt.Errorf("build selection: gpu 的 SKU 不得为空(无独显请用 null)")
		}
		out.GPU = &sku
	}

	return out, nil
}

// SKUs 返回本选择涉及的全部 SKU(去重、排序),供 store 层批量查询。
func (b BuildSelection) SKUs() []string {
	set := map[string]struct{}{
		b.CPU: {}, b.Motherboard: {}, b.Memory: {},
		b.PSU: {}, b.Case: {}, b.Cooler: {},
	}
	for _, s := range b.SSDs {
		set[s.SKU] = struct{}{}
	}
	if b.GPU != nil {
		set[*b.GPU] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for sku := range set {
		out = append(out, sku)
	}
	sort.Strings(out)
	return out
}
