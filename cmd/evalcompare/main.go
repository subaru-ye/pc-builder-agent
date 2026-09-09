// evalcompare 零模型读取两轮已冻结产物，复验逐题判卷后生成受控配对报告。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	return evalsuite.ReadVerifiedRun(dir)
}
