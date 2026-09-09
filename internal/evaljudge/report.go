package evaljudge

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

// VerifyRecords 复验评分引用与完整性，不把首跑的无效评分升级成有效评分。
func VerifyRecords(inputs []Input, records []Record, repeats int) error {
	if repeats < 1 || len(records) != len(inputs)*repeats {
		return fmt.Errorf("judge 记录不完整")
	}
	index := map[string]Input{}
	for _, in := range inputs {
		if _, ok := index[in.CaseID]; ok {
			return fmt.Errorf("重复诊断输入")
		}
		index[in.CaseID] = in
	}
	seen := map[string]bool{}
	for _, r := range records {
		in, ok := index[r.CaseID]
		key := fmt.Sprintf("%s/%d", r.CaseID, r.Repeat)
		if !ok || r.Repeat < 1 || r.Repeat > repeats || seen[key] {
			return fmt.Errorf("未知/重复 Judge 记录 %s", key)
		}
		seen[key] = true
		if len(in.Texts) == 0 {
			if r.Status != "missing_explanation" || r.Raw != "" || r.Judgement != nil || r.Error != "" || r.Usage != (evalsuite.Usage{}) {
				return fmt.Errorf("空解释被补造评分 %s", key)
			}
			continue
		}
		switch r.Status {
		case "scored":
			j, err := Decode(r.Raw, in)
			if err != nil || r.Error != "" || !reflect.DeepEqual(&j, r.Judgement) {
				return fmt.Errorf("评分重放不一致 %s", key)
			}
		case "invalid_judgement":
			_, err := Decode(r.Raw, in)
			if err == nil || r.Error == "" || r.Judgement != nil {
				return fmt.Errorf("无效评分状态不一致 %s", key)
			}
		case "judge_error":
			if r.Error == "" || r.Judgement != nil {
				return fmt.Errorf("执行错误状态不一致 %s", key)
			}
		default:
			return fmt.Errorf("未知 Judge 状态 %s", key)
		}
	}
	return nil
}

func Markdown(inputs []Input, records []Record, repeats int) string {
	var b strings.Builder
	b.WriteString("# 解释质量诊断（未经过独立人工校准）\n\n评分只针对保存的理由和非交付说明。它不修改原任务通过率、不构成发布门禁，也不证明模型选择优劣。示例由 AI 编写，未与独立人工参考评分对齐。\n\n")
	fmt.Fprintf(&b, "%d 个固定样本，每个 %d 次诊断；空解释由程序标记、记 0 档，不调用模型，不等于证明存在虚假事实。错误评分不进入均值，单独报告。\n\n| 用例 | 依据表达 | 需求关联 | 限制与下一步 | 有效/空白/错误 |\n|---|---|---|---|---|\n", len(inputs), repeats)
	for _, in := range inputs {
		var sum [3]int
		valid, missing, failed := 0, 0, 0
		for _, r := range records {
			if r.CaseID != in.CaseID {
				continue
			}
			switch r.Status {
			case "missing_explanation":
				missing++
			case "scored":
				valid++
				for i, k := range Dimensions {
					sum[i] += r.Judgement.Ratings[k].Score
				}
			default:
				failed++
			}
		}
		cells := [3]string{"未评分", "未评分", "未评分"}
		if valid+missing > 0 {
			for i := range cells {
				cells[i] = fmt.Sprintf("%.2f/2", float64(sum[i])/float64(valid+missing))
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d/%d/%d |\n", in.CaseID, cells[0], cells[1], cells[2], valid, missing, failed)
	}
	var usage evalsuite.Usage
	var elapsed int64
	for _, r := range records {
		usage.ModelCalls += r.Usage.ModelCalls
		usage.UsageResponses += r.Usage.UsageResponses
		usage.InputTokens += r.Usage.InputTokens
		usage.OutputTokens += r.Usage.OutputTokens
		usage.TotalTokens += r.Usage.TotalTokens
		elapsed += r.DurationMS
	}
	fmt.Fprintf(&b, "\n逻辑调用 %d；有用量 %d；已知 token：输入 %d、输出 %d、合计 %d；耗时合计 %.3f 秒。没有计价或 Embedding token，未测量适配器内部重试。\n", usage.ModelCalls, usage.UsageResponses, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, float64(elapsed)/1000)
	b.WriteString("\n逐次分数、评分理由与逐字引用在 results.jsonl；原始被评文本与事实索引在 inputs.json。引用存在只证明引用可定位，语义评价仍需人工校准；不得把本小样本均分当总体模型排名。\n")
	return b.String()
}

func ReadRecords(raw []byte) ([]Record, error) {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	var out []Record
	for {
		var r Record
		err := d.Decode(&r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
