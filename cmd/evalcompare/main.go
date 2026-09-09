// evalcompare 零模型读取两轮已冻结产物，复验逐题判卷后生成受控配对报告。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func main() { os.Exit(run()) }
func run() int {
	a := flag.String("a", "", "A 首跑产物目录")
	b := flag.String("b", "", "B 首跑产物目录")
	factor := flag.String("factor", "", "repair/semantic/builder/screening")
	out := flag.String("out", "", "新的对照报告目录，必须不存在")
	flag.Parse()
	if *a == "" || *b == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "-a/-b/-out 必填")
		return 2
	}
	am, ar, err := readVerifiedRun(*a)
	if err != nil {
		fmt.Fprintln(os.Stderr, "A:", err)
		return 2
	}
	bm, br, err := readVerifiedRun(*b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "B:", err)
		return 2
	}
	c, err := evalsuite.Compare(am, ar, bm, br, *factor)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := os.Mkdir(*out, 0755); err != nil {
		fmt.Fprintln(os.Stderr, "输出目录必须不存在:", err)
		return 2
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := os.WriteFile(filepath.Join(*out, "comparison.json"), append(raw, '\n'), 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var report strings.Builder
	fmt.Fprintf(&report, "评估范围：%s 阶段；另有 %d 个其他阶段用例经过证据复验，但不计入该变量的效果。\n\n", c.Stage, len(c.ExcludedCaseIDs))
	fmt.Fprintf(&report, "# 评估配对对照：%s\n\nA：%s\n\nB：%s\n\n- 同一清单、程序、快照，唯一改变项：%s。运行先后见 JSON 的时间戳，未声称随机化执行顺序。\n- B−A 平均题目通过率差：%.2f 个百分点；按题目聚类 bootstrap 95%% 区间 [%.2f, %.2f]。\n- 每题重复 %d 次，重复编号不是共享随机种子。小样本及零分差不能证明等效，也不支持自动替换生产模型。\n- 生成、合理非交付和初筛的整体拆分见各自原报告；下表逐题比较，零模型场景不能用来证明修复环收益。\n\n| 用例 | A 通过 | B 通过 | 仅 A / 仅 B | A/B 模型调用 | A/B Embedding 调用 | A/B 耗时 ms |\n|---|---:|---:|---|---|---|---|\n", *factor, *a, *b, *factor, c.MeanPassRateDelta*100, c.Bootstrap95[0]*100, c.Bootstrap95[1]*100, am.RequestedSeeds)
	for _, p := range c.Cases {
		calls, embeds := "未完整测量", "未完整测量"
		if p.UsageMeasured {
			calls = fmt.Sprintf("%d / %d", p.AUsage.ModelCalls, p.BUsage.ModelCalls)
			embeds = fmt.Sprintf("%d / %d", p.AUsage.EmbeddingCalls, p.BUsage.EmbeddingCalls)
		}
		fmt.Fprintf(&report, "| %s | %d/%d | %d/%d | %d / %d | %s | %s | %d / %d |\n", p.ID, p.APasses, p.Repeats, p.BPasses, p.Repeats, p.AOnly, p.BOnly, calls, embeds, p.ADurationMS, p.BDurationMS)
	}
	report.WriteString("\nToken 用量见 comparison.json，usage_responses 小于 model_calls 时表示用量不完整；逻辑调用不含内部 HTTP 重试，未换算费用。\n")
	if *factor == "semantic" {
		report.WriteString("\n逐次候选差异、需求原文、实际选中 SKU 和完整匹配文本见 comparison.json 的 semantic_trials。recorded=false 表示未保存或未准备候选，不能按零命中计分。匹配文本和选中标记只是检索诊断，不是噪声、颜色或品质已被验证的结论；硬规则同分也不能证明偏好质量等效。\n")
	}
	if err := os.WriteFile(filepath.Join(*out, "report.md"), []byte(report.String()), 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Printf("配对报告已保存：%s；%d 题，B−A=%.2f 个百分点\n", *out, len(c.Cases), c.MeanPassRateDelta*100)
	return 0
}

func readVerifiedRun(dir string) (evalsuite.ReportMeta, []evalsuite.CaseRecord, error) {
	var meta evalsuite.ReportMeta
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return meta, nil, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, nil, err
	}
	_, cases, err := evalsuite.ReadSuiteSnapshot(filepath.Join(dir, "cases.json"), meta.SuiteSHA256)
	if err != nil {
		return meta, nil, err
	}
	records, err := evalsuite.ReadRecords(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		return meta, nil, err
	}
	if len(records) != len(cases)*meta.RequestedSeeds {
		return meta, nil, fmt.Errorf("首跑记录数量不完整")
	}
	byID := map[string]evalsuite.Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	seen := map[string]bool{}
	for _, r := range records {
		c, ok := byID[r.CaseID]
		key := fmt.Sprintf("%s/%d", r.CaseID, r.Seed)
		if !ok || r.Stage != c.Stage || r.Seed < 1 || r.Seed > meta.RequestedSeeds || seen[key] {
			return meta, nil, fmt.Errorf("不匹配或重复记录 %s", key)
		}
		seen[key] = true
		if meta.HarnessProfile != nil {
			if r.Result != nil && (r.Result.Attempts < 0 || r.Result.Attempts > meta.HarnessProfile.AttemptLimit) {
				return meta, nil, fmt.Errorf("%s 实际尝试次数超出声明配置", key)
			}
			if !meta.HarnessProfile.Semantic && r.Usage != nil && r.Usage.EmbeddingCalls != 0 {
				return meta, nil, fmt.Errorf("%s 声明关闭语义检索但仍调用 Embedding", key)
			}
		}
		if r.RunErr != "" {
			v := evalsuite.Verdict{Failures: []evalsuite.AssertionFailure{{ID: "RUN", Name: "执行错误", Detail: r.RunErr}}}
			if !reflect.DeepEqual(v, r.Verdict) || !reflect.DeepEqual(evalsuite.Attribute(v.Failures), r.Attribution) {
				return meta, nil, fmt.Errorf("%s 执行错误判分或归因不一致", key)
			}
			continue
		}
		var v evalsuite.Verdict
		if c.Stage == evalsuite.StageScreening {
			if r.Screening == nil {
				return meta, nil, fmt.Errorf("缺少初筛原文")
			}
			if err := evalsuite.CheckDialogueEvidence(c, *r.Screening); err != nil {
				return meta, nil, fmt.Errorf("%s: %w", key, err)
			}
			v = evalsuite.AssertScreeningOutput(c, *r.Screening)
		} else {
			if r.Result == nil {
				return meta, nil, fmt.Errorf("缺少构建轨迹")
			}
			v = evalsuite.AssertCase(c, *r.Result, r.Snapshot)
		}
		if !reflect.DeepEqual(v, r.Verdict) || !reflect.DeepEqual(evalsuite.Attribute(v.Failures), r.Attribution) {
			return meta, nil, fmt.Errorf("%s 判卷与保存结果不一致", key)
		}
	}
	return meta, records, nil
}
