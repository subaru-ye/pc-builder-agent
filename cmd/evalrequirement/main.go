// evalrequirement runs the Requirement v2 evaluation contract against the
// current product path. check/replay/compare are zero-model; live requires an
// explicit positive call budget, a new output directory and provenance written
// before the first provider request.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
	"google.golang.org/adk/v2/model"
)

func main() {
	dataset := flag.String("dataset", "internal/planningeval/testdata/requirement-v2", "frozen requirement-v2 dataset root")
	mode := flag.String("mode", "check", "check, freeze-manifest, live, replay, compare")
	out := flag.String("out", "", "new output directory (live/replay)")
	splits := flag.String("splits", "development,calibration", "comma-separated splits to evaluate; holdout must be requested explicitly and alone")
	repeats := flag.Int("repeats", 1, "repetitions per case; release metrics use pass^k")
	maxCalls := flag.Int("max-calls", 0, "required positive shared screening call budget for live")
	skipModelLayers := flag.Bool("skip-model-layers", false, "live run without screening model: deterministic layers + DB layers only, model layers recorded as skipped")
	runDir := flag.String("run", "", "run directory to replay")
	regradeOpt := flag.Bool("regrade", false, "explicitly re-grade a run produced by an older grader version")
	baselineDir := flag.String("baseline", "", "baseline run directory (compare)")
	candidateDir := flag.String("candidate", "", "candidate run directory (compare)")
	flag.Parse()

	switch *mode {
	case "freeze-manifest":
		manifest, err := planningeval.FreezeManifest(*dataset)
		if err != nil {
			fail(err)
		}
		raw, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(filepath.Join(*dataset, "manifest.json"), append(raw, '\n'), 0644); err != nil {
			fail(err)
		}
		fmt.Printf("manifest rewritten: %d files; review before freezing\n", len(manifest.Files))
		return
	case "check", "live", "replay", "compare":
	default:
		fail(fmt.Errorf("unknown mode %q", *mode))
	}

	data, err := planningeval.LoadRequirementV2(*dataset)
	if err != nil {
		fail(err)
	}
	if err := planningeval.GradeReqV2Selftest(data.Selftest); err != nil {
		fail(fmt.Errorf("grader selftest: %w", err))
	}
	selected := map[string]bool{}
	for _, s := range strings.Split(*splits, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		selected[s] = true
	}
	if selected["holdout"] && (selected["development"] || selected["calibration"]) {
		fail(fmt.Errorf("holdout 只评最终锁定候选，不能与其他 split 混跑"))
	}
	if *mode == "check" {
		counts := data.CasesPerLayer(map[string]bool{"development": true, "calibration": true, "holdout": true})
		layers := make([]string, 0, len(counts))
		for l := range counts {
			layers = append(layers, l)
		}
		sort.Strings(layers)
		fmt.Printf("%s: grader=%s selftest=%d entries", data.Manifest.Dataset, planningeval.ReqV2GraderVersion, len(data.Selftest))
		for _, l := range layers {
			fmt.Printf(" %s=%d", l, counts[l])
		}
		fmt.Printf("; manifest=%s\n", planningeval.Hash(data.ManifestRaw))
		return
	}
	if *mode == "compare" {
		if *baselineDir == "" || *candidateDir == "" {
			fail(fmt.Errorf("compare requires -baseline and -candidate run directories"))
		}
		base := readResults(*baselineDir)
		cand := readResults(*candidateDir)
		requireSameIdentity(*baselineDir, *candidateDir)
		outcome, err := planningeval.CompareRequirementV2(data.Gates, base, cand)
		if err != nil {
			fail(err)
		}
		raw, _ := json.MarshalIndent(outcome, "", "  ")
		fmt.Println(string(raw))
		return
	}
	if *mode == "replay" {
		if *runDir == "" {
			fail(fmt.Errorf("replay requires -run directory"))
		}
		// 判卷用当前 grader 的冻结数据集；观测来自源产物 results.jsonl。
		// 源 grader 与当前不一致时必须显式 -regrade，产物记录 regrade 身份。
		sourceGrader, sourceManifest := sourceIdentity(*runDir)
		regrade := sourceGrader != "" && sourceGrader != planningeval.ReqV2GraderVersion
		if err := replaySourceGraderError(sourceGrader, *regradeOpt); err != nil {
			fail(err)
		}
		budget := 0
		if raw, err := os.ReadFile(filepath.Join(*runDir, "plan.json")); err == nil {
			var plan struct {
				MaxModelRequests int `json:"max_model_requests"`
			}
			_ = json.Unmarshal(raw, &plan)
			budget = plan.MaxModelRequests
		}
		outcome, err := planningeval.ReplayRequirementV2(data, readResults(*runDir), budget)
		if err != nil {
			fail(err)
		}
		if *out == "" {
			suffix := "replay"
			if regrade {
				suffix = "regrade-" + planningeval.ReqV2GraderVersion
			}
			*out = *runDir + "-" + suffix + "-" + time.Now().UTC().Format("20060102T150405Z")
		}
		if err := os.MkdirAll(*out, 0755); err != nil {
			fail(err)
		}
		// 产物自包含：复制当前数据集并写入 replay/regrade provenance。
		for _, f := range data.Manifest.Files {
			raw, err := os.ReadFile(filepath.Join(*dataset, filepath.FromSlash(f.Path)))
			if err != nil {
				fail(err)
			}
			target := filepath.Join(*out, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				fail(err)
			}
			if err := os.WriteFile(target, raw, 0600); err != nil {
				fail(err)
			}
		}
		for _, name := range []string{"manifest.json", "provenance.json", "gates.json"} {
			raw, err := os.ReadFile(filepath.Join(*dataset, name))
			if err != nil {
				fail(err)
			}
			if err := os.WriteFile(filepath.Join(*out, name), raw, 0600); err != nil {
				fail(err)
			}
		}
		plan := map[string]any{
			"mode": "replay", "created_at": time.Now().UTC(),
			"grader_version":         planningeval.ReqV2GraderVersion,
			"manifest_sha256":        planningeval.Hash(data.ManifestRaw),
			"gates_sha256":           planningeval.Hash(data.GatesRaw),
			"splits":                 []string{"as-recorded"},
			"repeats":                outcome.Report.Repeats,
			"max_model_requests":     budget,
			"source_run":             *runDir,
			"source_grader_version":  sourceGrader,
			"source_manifest_sha256": sourceManifest,
			"zero_model":             true,
			"note":                   "零模型：只重判源产物冻结观测，不重新执行产品路径，不调用 provider。",
		}
		if regrade {
			plan["mode"] = "regrade"
			plan["regrade"] = true
		}
		if raw, err := os.ReadFile(filepath.Join(*runDir, "report.json")); err == nil {
			plan["source_report_sha256"] = planningeval.Hash(raw)
		}
		if err := writeJSON(filepath.Join(*out, "plan.json"), plan); err != nil {
			fail(err)
		}
		resultsFile, err := os.OpenFile(filepath.Join(*out, "results.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fail(err)
		}
		for _, c := range outcome.Report.Cases {
			raw, err := json.Marshal(c)
			if err != nil {
				fail(err)
			}
			if _, err := resultsFile.Write(append(raw, '\n')); err != nil {
				fail(err)
			}
		}
		_ = resultsFile.Close()
		if err := writeArtifact(*out, outcome.Report); err != nil {
			fail(err)
		}
		fmt.Printf("%s: %d cases; verdict changes: %d\n", plan["mode"], len(outcome.Report.Cases), len(outcome.Changed))
		for _, change := range outcome.Changed {
			fmt.Printf("CHANGED %s\n", change)
		}
		return
	}

	// live：显式预算、新目录、请求前 provenance。零模型变体不解析凭据。
	if *maxCalls <= 0 && !*skipModelLayers {
		fail(fmt.Errorf("live requires a positive -max-calls budget"))
	}
	if *out == "" {
		fail(fmt.Errorf("live requires an explicit new -out directory"))
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		fail(fmt.Errorf("output directory must not exist"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	var screening model.LLM
	redactedScreening := map[string]any{"note": "deterministic run; no screening model configured"}
	if !*skipModelLayers {
		dotenv.Load(".env")
		screeningConfig, redacted, err := fixedScreeningConfig()
		if err != nil {
			fail(err)
		}
		redactedScreening = redacted
		screening, err = modelprovider.NewChat(ctx, screeningConfig, "reqv2-eval-screening")
		if err != nil {
			fail(err)
		}
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		fail(err)
	}
	// 复制完整 fixture 清单与 provenance/gates/manifest 到产物目录。
	for _, f := range data.Manifest.Files {
		raw, err := os.ReadFile(filepath.Join(*dataset, filepath.FromSlash(f.Path)))
		if err != nil {
			fail(err)
		}
		if err := os.MkdirAll(filepath.Join(*out, filepath.Dir(filepath.FromSlash(f.Path))), 0755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(filepath.Join(*out, filepath.FromSlash(f.Path)), raw, 0600); err != nil {
			fail(err)
		}
	}
	for _, name := range []string{"manifest.json", "provenance.json", "gates.json"} {
		raw, err := os.ReadFile(filepath.Join(*dataset, name))
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(filepath.Join(*out, name), raw, 0600); err != nil {
			fail(err)
		}
	}
	commit, dirty := codeIdentity()
	plan := map[string]any{
		"mode": "live", "created_at": time.Now().UTC(),
		"grader_version":     planningeval.ReqV2GraderVersion,
		"manifest_sha256":    planningeval.Hash(data.ManifestRaw),
		"gates_sha256":       planningeval.Hash(data.GatesRaw),
		"models":             map[string]any{"screening": redactedScreening},
		"max_model_requests": *maxCalls,
		"splits":             strings.Split(*splits, ","),
		"repeats":            *repeats,
		"builder":            "scripted-error oracle (builder output quality is out of scope for requirement-v2)",
		"code_commit":        commit, "code_dirty": dirty,
		"go_version":    runtime.Version(),
		"binary_sha256": binaryHash(),
		"note":          "prompt SHA256 per request is recorded in events.jsonl model_request events",
	}
	if err := writeJSON(filepath.Join(*out, "plan.json"), plan); err != nil {
		fail(err)
	}
	// plan.json 落盘成功后才允许第一次 provider 请求。
	journalFile, err := os.OpenFile(filepath.Join(*out, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fail(err)
	}
	defer func() { _ = journalFile.Close() }()
	journal := func(value any) error {
		if err := json.NewEncoder(journalFile).Encode(value); err != nil {
			return err
		}
		return journalFile.Sync()
	}
	report, runErr := planningeval.RunRequirementV2(ctx, data, planningeval.ReqV2RunOptions{
		DSN: os.Getenv("PLANNING_EVAL_DSN"), Splits: selected, Repeats: *repeats,
		Screening: screening, MaxCalls: *maxCalls, Journal: journal,
	})
	results, _ := os.OpenFile(filepath.Join(*out, "results.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	for _, c := range report.Cases {
		raw, _ := json.Marshal(c)
		if _, err := results.Write(append(raw, '\n')); err != nil {
			fail(err)
		}
	}
	_ = results.Close()
	if err := writeArtifact(*out, report); err != nil {
		fail(err)
	}
	fmt.Printf("%s: model calls=%d; deterministic layers:\n", report.Mode, report.Usage.ModelCalls)
	for _, layer := range planningeval.ReqV2DeterministicLayers {
		s := report.PerLayer[layer]
		fmt.Printf("  %s: %d/%d passed (vetoes=%d)\n", layer, s.Passed, s.Cases, s.Vetoes)
	}
	for _, layer := range []string{"extraction", "conversations"} {
		s := report.PerLayer[layer]
		fmt.Printf("  %s: %d/%d passed, skipped=%d\n", layer, s.Passed, s.Cases, s.Skipped)
	}
	if runErr != nil {
		fail(runErr)
	}
}

// sourceIdentity 读取源产物的 grader 与 manifest 身份（report.json 优先）。
func sourceIdentity(dir string) (string, string) {
	raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err == nil {
		var report struct {
			GraderVersion  string `json:"grader_version"`
			ManifestSHA256 string `json:"manifest_sha256"`
		}
		if json.Unmarshal(raw, &report) == nil {
			return report.GraderVersion, report.ManifestSHA256
		}
	}
	return "", ""
}

func readResults(dir string) [][]byte {
	raw, err := os.ReadFile(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		fail(fmt.Errorf("%s: %w", dir, err))
	}
	var lines [][]byte
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, []byte(line))
		}
	}
	return lines
}

func requireSameIdentity(a, b string) {
	read := func(dir string) map[string]any {
		raw, err := os.ReadFile(filepath.Join(dir, "plan.json"))
		if err != nil {
			fail(fmt.Errorf("%s: %w", dir, err))
		}
		var plan map[string]any
		if json.Unmarshal(raw, &plan) != nil {
			fail(fmt.Errorf("%s: plan.json undecodable", dir))
		}
		return plan
	}
	if err := compareIdentityError(read(a), read(b)); err != nil {
		fail(err)
	}
}

// compareIdentityError 校验两轮产物可配对：同 manifest、同 grader、且 grader
// 必须是当前版本（混用 v1/v2 一律拒绝，旧产物先 regrade）。
func compareIdentityError(pa, pb map[string]any) error {
	if pa["manifest_sha256"] != pb["manifest_sha256"] || pa["grader_version"] != pb["grader_version"] {
		return fmt.Errorf("两轮产物 manifest/grader 不一致，不能配对比较")
	}
	if pa["grader_version"] != planningeval.ReqV2GraderVersion {
		return fmt.Errorf("产物 grader=%v 与当前 %s 不一致；先用 -mode replay -regrade 重判后再比较", pa["grader_version"], planningeval.ReqV2GraderVersion)
	}
	if pa["repeats"] != pb["repeats"] {
		return fmt.Errorf("两轮重复数不一致")
	}
	return nil
}

// replaySourceGraderError：源产物 grader 与当前不一致且未显式 -regrade 时拒绝。
func replaySourceGraderError(sourceGrader string, regradeOpt bool) error {
	if sourceGrader != "" && sourceGrader != planningeval.ReqV2GraderVersion && !regradeOpt {
		return fmt.Errorf("源产物 grader=%s 与当前 %s 不一致；跨版本重判需显式 -regrade", sourceGrader, planningeval.ReqV2GraderVersion)
	}
	return nil
}

func writeArtifact(dir string, report *planningeval.ReqV2Report) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), raw, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(planningeval.RenderReqV2Markdown(report)), 0644)
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
}

func binaryHash() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		return ""
	}
	return planningeval.Hash(raw)
}

func codeIdentity() (string, bool) {
	commit := strings.TrimSpace(gitOutput("rev-parse", "HEAD"))
	dirty := strings.TrimSpace(gitOutput("status", "--porcelain")) != ""
	return commit, dirty
}

func gitOutput(args ...string) string {
	cmd := exec.Command("git", args...)
	raw, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(raw)
}

// fixedScreeningConfig 读取环境固定 Screening 配置并要求零重试、无模型链，
// 返回脱敏描述供 plan.json 记录；密钥永不进入产物。
func fixedScreeningConfig() (modelprovider.Config, map[string]any, error) {
	cfg, err := modelprovider.Load(modelprovider.RoleScreening)
	if err != nil {
		return cfg, nil, err
	}
	if cfg.MaxRetries != 0 || len(cfg.ModelChain) != 0 {
		return cfg, nil, fmt.Errorf("live evaluation requires zero retries and no model chain")
	}
	description := cfg.Redacted()
	description["model_chain"] = []string{}
	description["timeout"], description["dimensions"] = cfg.Timeout.String(), cfg.Dimensions
	return cfg, description, nil
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
