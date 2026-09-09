// Package evaljudge 为已验证的评估产物补充解释质量诊断，不参与硬断言和发布门禁。
package evaljudge

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

var Dimensions = []string{"grounding", "relevance", "actionability"}

type Selection struct {
	SchemaVersion int      `json:"schema_version"`
	CaseIDs       []string `json:"case_ids"`
	SourceRepeat  int      `json:"source_repeat"`
	Provenance    string   `json:"provenance"`
}

type Input struct {
	CaseID       string            `json:"case_id"`
	SourceRepeat int               `json:"source_repeat"`
	Title        string            `json:"title"`
	Texts        map[string]string `json:"texts"`
	Facts        map[string]string `json:"facts"`
}

type Evidence struct {
	OutputKey   string `json:"output_key"`
	OutputQuote string `json:"output_quote"`
	FactKey     string `json:"fact_key"`
	FactQuote   string `json:"fact_quote"`
}
type Rating struct {
	Score    int        `json:"score"`
	Reason   string     `json:"reason"`
	Evidence []Evidence `json:"evidence"`
}
type Judgement struct {
	Ratings map[string]Rating `json:"ratings"`
}

func marshal(v any) string { raw, _ := json.Marshal(v); return string(raw) }

// Prepare 固定题目和原运行重复编号，不按评分挑选更漂亮的输出；空理由保留为空。
func Prepare(records []evalsuite.CaseRecord, selection Selection) ([]Input, error) {
	if selection.SchemaVersion != 1 || selection.SourceRepeat < 1 || len(selection.CaseIDs) == 0 || selection.Provenance == "" {
		return nil, fmt.Errorf("无效解释诊断清单")
	}
	index := map[string]evalsuite.CaseRecord{}
	for _, r := range records {
		if r.Seed == selection.SourceRepeat {
			index[r.CaseID] = r
		}
	}
	seen := map[string]bool{}
	var inputs []Input
	for _, id := range selection.CaseIDs {
		if seen[id] {
			return nil, fmt.Errorf("重复诊断用例 %s", id)
		}
		seen[id] = true
		r, ok := index[id]
		if !ok || r.Stage != evalsuite.StageBuild || r.Result == nil || r.RunErr != "" {
			return nil, fmt.Errorf("%s 缺少可验证构建结果", id)
		}
		in := Input{CaseID: id, SourceRepeat: r.Seed, Title: r.Title, Texts: map[string]string{}, Facts: map[string]string{"requirement": string(r.Requirement), "outcome": marshal(r.Result.Succeeded)}}
		if r.Result.Succeeded {
			for category, text := range r.Result.Draft.Rationale {
				if strings.TrimSpace(text) != "" {
					in.Texts["rationale."+category] = text
				}
			}
			in.Facts["quote"] = marshal(r.Result.Result.Quote)
			in.Facts["rules"] = marshal(r.Result.Result.Report)
			selected := map[string]bool{}
			for _, sku := range r.Result.Draft.Selection.SKUs() {
				selected[sku] = true
			}
			if r.Snapshot.Catalog == nil {
				return nil, fmt.Errorf("%s 缺少选中配件的目录事实", id)
			}
			for _, candidate := range r.Snapshot.Catalog.Candidates {
				if selected[candidate.SKU] {
					in.Facts["part."+candidate.SKU] = marshal(map[string]any{"category": candidate.Category, "brand": candidate.Brand, "model": candidate.Model, "specs": candidate.Specs})
				}
			}
		} else {
			if strings.TrimSpace(r.Result.Message) != "" {
				in.Texts["message"] = r.Result.Message
			}
			if r.Result.Decision != nil {
				var d map[string]json.RawMessage
				_ = json.Unmarshal([]byte(marshal(r.Result.Decision)), &d)
				delete(d, "message") // 被评分原文不能复制进事实区充当自己的依据。
				in.Facts["decision"] = marshal(d)
			}
		}
		inputs = append(inputs, in)
	}
	return inputs, nil
}

// Decode 校验评分范围及逐字证据引用；引用存在不等于语义判断已获人工认可。
func Decode(raw string, input Input) (Judgement, error) {
	var out Judgement
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return out, fmt.Errorf("评分 JSON 后有额外内容")
	}
	if len(out.Ratings) != len(Dimensions) {
		return out, fmt.Errorf("必须提供三个评分维度")
	}
	var fields map[string]map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return out, err
	}
	for _, name := range Dimensions {
		r, ok := out.Ratings[name]
		score := fields["ratings"][name]["score"]
		if !ok || len(score) == 0 || string(score) == "null" || r.Score < 0 || r.Score > 2 || strings.TrimSpace(r.Reason) == "" || len(r.Evidence) == 0 {
			return out, fmt.Errorf("%s 评分或证据不完整", name)
		}
		for _, e := range r.Evidence {
			if strings.TrimSpace(e.OutputQuote) == "" || !strings.Contains(input.Texts[e.OutputKey], e.OutputQuote) || strings.TrimSpace(e.FactQuote) == "" || !strings.Contains(input.Facts[e.FactKey], e.FactQuote) {
				return out, fmt.Errorf("%s 引用了不存在的原文或事实", name)
			}
		}
	}
	return out, nil
}

func Family(model string) string {
	model = strings.ToLower(model)
	for _, family := range []string{"qwen", "deepseek"} {
		if strings.HasPrefix(model, family) {
			return family
		}
	}
	return ""
}

func CheckJudgeFamily(source evalsuite.ReportMeta, judgeModel string) error {
	cfg := source.Models["builder"]
	chain := reflect.ValueOf(cfg["model_chain"])
	if chain.IsValid() && chain.Kind() == reflect.Slice && chain.Len() > 0 {
		return fmt.Errorf("来源 Builder 启用了切换链，不能证明异构评分")
	}
	builder, _ := cfg["model"].(string)
	if Family(builder) == "" || Family(judgeModel) == "" || Family(builder) == Family(judgeModel) {
		return fmt.Errorf("judge 必须使用与来源 Builder 不同且已识别的模型家族")
	}
	return nil
}

// FactKeys 为报告提供稳定的证据索引。
func FactKeys(input Input) []string {
	keys := make([]string, 0, len(input.Facts))
	for k := range input.Facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
