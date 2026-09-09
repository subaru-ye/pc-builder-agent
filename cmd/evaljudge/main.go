// evaljudge 对保存的解释做可重放诊断；不改变原任务分数或产品模型配置。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/evaljudge"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
)

type manifest struct {
	SchemaVersion     int                  `json:"schema_version"`
	SourceDir         string               `json:"source_dir"`
	SourceMeta        evalsuite.ReportMeta `json:"source_meta"`
	SourceSHA256      string               `json:"source_results_sha256"`
	InputsSHA256      string               `json:"inputs_sha256"`
	RubricSHA256      string               `json:"rubric_sha256"`
	SelectionSHA256   string               `json:"selection_sha256"`
	JudgeBinarySHA256 string               `json:"judge_binary_sha256"`
	JudgeConfig       map[string]any       `json:"judge_config"`
	Repeats           int                  `json:"repeats"`
	Calibration       string               `json:"calibration"`
	CreatedAt         time.Time            `json:"created_at"`
}

func digest(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0600)
}
func fail(err error) int { fmt.Fprintln(os.Stderr, err); return 2 }
func main()              { os.Exit(run()) }
func run() int {
	mode := flag.String("mode", "run", "run/replay")
	source := flag.String("source", "", "已完成的 eval 原产物目录")
	dir := flag.String("dir", "", "replay 的 Judge 产物目录")
	out := flag.String("out", "artifacts/evaljudge", "新产物的根目录")
	rubricPath := flag.String("rubric", "docs/eval/judge/rubric-v1.md", "冻结评分规则")
	selectionPath := flag.String("selection", "docs/eval/judge/selection-v1.json", "固定诊断样本")
	modelName := flag.String("model", "deepseek-v4-flash-0731", "显式 Judge 模型，不改产品配置")
	repeats := flag.Int("repeats", 3, "每个样本诊断次数，1–3")
	flag.Parse()
	if *mode == "replay" {
		if err := replay(*dir); err != nil {
			return fail(err)
		}
		return 0
	}
	if *mode != "run" || *source == "" || *repeats < 1 || *repeats > 3 {
		return fail(fmt.Errorf("请指定原运行目录和合法模式/重复次数"))
	}
	meta, records, err := evalsuite.ReadVerifiedRun(*source)
	if err != nil {
		return fail(err)
	}
	selectionRaw, err := os.ReadFile(*selectionPath)
	if err != nil {
		return fail(err)
	}
	var selection evaljudge.Selection
	if err := json.Unmarshal(selectionRaw, &selection); err != nil {
		return fail(err)
	}
	inputs, err := evaljudge.Prepare(records, selection)
	if err != nil {
		return fail(err)
	}
	rubric, err := os.ReadFile(*rubricPath)
	if err != nil {
		return fail(err)
	}
	if len(rubric) == 0 {
		return fail(fmt.Errorf("评分规则为空"))
	}
	dotenv.Load(".env")
	cfg, err := modelprovider.Load(modelprovider.RoleScreening)
	if err != nil {
		return fail(err)
	}
	if len(cfg.ModelChain) > 0 {
		return fail(fmt.Errorf("judge 不允许自动切换模型链"))
	}
	cfg.Model = *modelName
	if err := evaljudge.CheckJudgeFamily(meta, *modelName); err != nil {
		return fail(err)
	}
	llm, err := modelprovider.NewChat(context.Background(), cfg, "evaljudge")
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return fail(err)
	}
	created, err := os.MkdirTemp(*out, time.Now().Format("20060102-150405")+"-")
	if err != nil {
		return fail(err)
	}
	if err := writeJSON(filepath.Join(created, "inputs.json"), inputs); err != nil {
		return fail(err)
	}
	inputRaw, err := os.ReadFile(filepath.Join(created, "inputs.json"))
	if err != nil {
		return fail(err)
	}
	sourceRaw, err := os.ReadFile(filepath.Join(*source, "results.jsonl"))
	if err != nil {
		return fail(err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	exeRaw, err := os.ReadFile(exe)
	if err != nil {
		return fail(err)
	}
	absSource, err := filepath.Abs(*source)
	if err != nil {
		return fail(err)
	}
	m := manifest{SchemaVersion: 1, SourceDir: absSource, SourceMeta: meta, SourceSHA256: digest(sourceRaw), InputsSHA256: digest(inputRaw), RubricSHA256: digest(rubric), SelectionSHA256: digest(selectionRaw), JudgeBinarySHA256: digest(exeRaw), JudgeConfig: cfg.Redacted(), Repeats: *repeats, Calibration: "not_human_calibrated_ai_authored_anchors", CreatedAt: time.Now()}
	if err := os.WriteFile(filepath.Join(created, "rubric.md"), rubric, 0600); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(created, "selection.json"), selectionRaw, 0600); err != nil {
		return fail(err)
	}
	if err := writeJSON(filepath.Join(created, "meta.json"), m); err != nil {
		return fail(err)
	}
	f, err := os.OpenFile(filepath.Join(created, "results.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = f.Close() }()
	encoder := json.NewEncoder(f)
	var results []evaljudge.Record
	failed := false
	for _, in := range inputs {
		for repeat := 1; repeat <= *repeats; repeat++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			r := evaljudge.Evaluate(ctx, llm, string(rubric), in, repeat)
			cancel()
			if err := encoder.Encode(r); err != nil {
				return fail(err)
			}
			results = append(results, r)
			if r.Status == "judge_error" || r.Status == "invalid_judgement" {
				failed = true
			}
			fmt.Printf("%s repeat=%d status=%s\n", r.CaseID, r.Repeat, r.Status)
		}
	}
	if err := os.WriteFile(filepath.Join(created, "report.md"), []byte(evaljudge.Markdown(inputs, results, *repeats)), 0600); err != nil {
		return fail(err)
	}
	fmt.Println("解释诊断已保存：", created)
	if failed {
		return 1
	}
	return 0
}

func replay(dir string) error {
	if dir == "" {
		return fmt.Errorf("-dir 必填")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m.SchemaVersion != 1 || m.Calibration != "not_human_calibrated_ai_authored_anchors" || m.Repeats < 1 || m.Repeats > 3 {
		return fmt.Errorf("未知诊断版本或校准状态")
	}
	judgeModel, _ := m.JudgeConfig["model"].(string)
	if err := evaljudge.CheckJudgeFamily(m.SourceMeta, judgeModel); err != nil {
		return err
	}
	var inputs []evaljudge.Input
	var selection evaljudge.Selection
	for name, hash := range map[string]string{"inputs.json": m.InputsSHA256, "rubric.md": m.RubricSHA256, "selection.json": m.SelectionSHA256} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if hash == "" || digest(raw) != hash {
			return fmt.Errorf("%s 哈希不一致", name)
		}
		if name == "inputs.json" {
			if err := json.Unmarshal(raw, &inputs); err != nil {
				return err
			}
		}
		if name == "selection.json" {
			if err := json.Unmarshal(raw, &selection); err != nil {
				return err
			}
		}
	}
	sourceRaw, err := os.ReadFile(filepath.Join(m.SourceDir, "results.jsonl"))
	if err != nil {
		return err
	}
	if digest(sourceRaw) != m.SourceSHA256 {
		return fmt.Errorf("原运行结果已变化")
	}
	meta, source, err := evalsuite.ReadVerifiedRun(m.SourceDir)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(meta, m.SourceMeta) {
		return fmt.Errorf("原运行元数据不一致")
	}
	want, err := evaljudge.Prepare(source, selection)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(want, inputs) {
		return fmt.Errorf("诊断输入不来自保存的原始解释")
	}
	raw, err = os.ReadFile(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		return err
	}
	records, err := evaljudge.ReadRecords(raw)
	if err != nil {
		return err
	}
	if err := evaljudge.VerifyRecords(inputs, records, m.Repeats); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "replay.md"), []byte(evaljudge.Markdown(inputs, records, m.Repeats)), 0600); err != nil {
		return err
	}
	fmt.Println("解释诊断重放一致（零模型调用）：", dir)
	return nil
}
