// evalfastlane 运行 jev-v2-fastlane-feasibility 实验：零模型机会审计（audit）
// 与有界影子验证（shadow-live）。Jev 只在 shadow-live 下可达，要求显式正数
// 调用上限、超时与新输出目录；plan/provenance 先于首请求落盘。产品链路
// 不受影响，holdout 不参与。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/decision"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
	"github.com/subaru-ye/pc-builder-agent/internal/providers/jev"
)

const defaultDatasetRoot = "internal/planningeval/testdata/requirement-v2"

func main() {
	mode := flag.String("mode", "audit", "audit (zero model), shadow-live (bounded live Jev), or shadow-replay (recompute stats from a frozen jev.jsonl; no network)")
	datasetRoot := flag.String("dataset", defaultDatasetRoot, "frozen requirement-v2 dataset root")
	out := flag.String("out", "", "new output directory")
	screeningBaseline := flag.String("screening-baseline", "artifacts/reqv2/spec3-final-live-20260923", "frozen Screening live run directory (results.jsonl/report.json)")
	jevReplay := flag.String("jev-replay", "", "shadow-replay source directory containing a frozen jev.jsonl")
	jevModel := flag.String("jev-model", jev.ModelPin, "pinned Jev model")
	jevMaxCalls := flag.Int("jev-max-calls", 0, "required positive Jev call ceiling for shadow-live")
	jevTimeout := flag.Duration("jev-timeout", 2*time.Second, "per-call Jev timeout; an evaluation input, recorded in provenance")
	jevInputPrice := flag.Float64("jev-input-price", 0, "recorded CNY rate per million input tokens; zero omits the cash estimate")
	jevOutputPrice := flag.Float64("jev-output-price", 0, "recorded CNY rate per million output tokens; zero omits the cash estimate")
	flag.Parse()

	if *out == "" {
		fail(fmt.Errorf("-out 为必填且必须是不存在的新目录"))
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		fail(fmt.Errorf("output directory must not exist: %s", *out))
	}
	if *mode != "audit" && *mode != "shadow-live" && *mode != "shadow-replay" {
		fail(fmt.Errorf("-mode 只允许 audit、shadow-live 或 shadow-replay"))
	}
	dotenv.Load(".env")
	jevRequested := *mode == "shadow-live"
	replayMode := *mode == "shadow-replay"
	if replayMode && *jevReplay == "" {
		fail(fmt.Errorf("-mode shadow-replay requires -jev-replay pointing at a frozen run directory"))
	}
	if jevRequested && *jevMaxCalls <= 0 {
		fail(fmt.Errorf("Jev calls require a positive -jev-max-calls cap"))
	}
	if !jevRequested && !replayMode && (*jevMaxCalls > 0 || *jevInputPrice > 0 || *jevOutputPrice > 0) {
		fail(fmt.Errorf("audit mode must stay network-free: drop -jev-* flags or use -mode shadow-live"))
	}

	dataset, err := planningeval.LoadRequirementV2(*datasetRoot)
	if err != nil {
		fail(err)
	}
	setb, err := planningeval.LoadFastlaneSetB(*datasetRoot)
	if err != nil {
		fail(err)
	}
	setbRaw, err := os.ReadFile(filepath.Join(*datasetRoot, planningeval.FastlaneSetBPath()))
	if err != nil {
		fail(err)
	}

	var jevJudge decision.FastlaneJudge
	var jevPlan map[string]any
	if replayMode {
		raw, err := os.ReadFile(filepath.Join(*jevReplay, "jev.jsonl"))
		if err != nil {
			fail(fmt.Errorf("jev replay source: %w", err))
		}
		jevPlan = map[string]any{
			"replay_source": *jevReplay, "replay_sha256": planningeval.Hash(raw),
			"note": "stats recomputed from frozen observations; zero network",
		}
	}
	if jevRequested {
		apiKey := os.Getenv("JEV_API_KEY")
		if apiKey == "" {
			fail(fmt.Errorf("JEV_API_KEY is required for Jev calls; export it or add it to .env"))
		}
		base := os.Getenv("JEV_BASE_URL")
		if base == "" {
			base = jev.DefaultBaseURL
		}
		client, err := jev.New(jev.Config{APIKey: apiKey, BaseURL: base, Model: *jevModel, Timeout: *jevTimeout, HTTPClient: &http.Client{}})
		if err != nil {
			fail(err)
		}
		host := client.Host()
		jevPlan = map[string]any{
			"model": *jevModel, "base_url_host": host, "timeout": jevTimeout.String(),
			"question_sha256": decision.FastlaneQuestionHash(), "max_calls": *jevMaxCalls,
			"input_price_per_mtok_cny": *jevInputPrice, "output_price_per_mtok_cny": *jevOutputPrice,
			"wire_state": "current_turn + candidate field/value only",
		}
		jevJudge = planningeval.FastlaneJevJudge{Client: client}
	}

	if err = os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	// plan.json 在任何 Jev 请求之前落盘，记录可复核的运行身份。
	baselineHash := ""
	if *screeningBaseline != "" {
		raw, err := os.ReadFile(filepath.Join(*screeningBaseline, "results.jsonl"))
		if err != nil {
			fail(fmt.Errorf("screening baseline: %w", err))
		}
		baselineHash = planningeval.Hash(raw)
	}
	manifestRaw, err := json.MarshalIndent(map[string]any{
		"dataset": dataset.Manifest, "setb_sha256": planningeval.Hash(setbRaw),
		"screening_baseline_dir": *screeningBaseline, "screening_baseline_results_sha256": baselineHash,
		"go_version": runtime.Version(), "binary_sha256": binaryHash(),
	}, "", "  ")
	if err != nil {
		fail(err)
	}
	plan := map[string]any{
		"mode": *mode, "created_at": time.Now().UTC().Format(time.RFC3339),
		"dataset_root": *datasetRoot, "grader_version": dataset.Manifest.GraderVersion,
		"dataset_frozen_at": dataset.Manifest.FrozenAt,
		"inputs_sha256":     planningeval.Hash(manifestRaw),
		"setb_path":         planningeval.FastlaneSetBPath(),
		"scope":             "single-field budget_cny fastlane feasibility; shadow only; holdout excluded",
	}
	if jevRequested {
		plan["jev"] = jevPlan
	}
	if err = writeJSONOut(*out, "plan.json", plan, 0o600); err != nil {
		fail(err)
	}
	if err = writeJSONOut(*out, "dataset-manifest.json", json.RawMessage(dataset.ProvenanceRaw), 0o600); err != nil {
		fail(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	report, err := planningeval.RunFastlane(ctx, dataset, setb, planningeval.FastlaneOptions{
		OutDir:                *out,
		ScreeningBaselineDir:  *screeningBaseline,
		JevJudge:              jevJudge,
		JevReplayPath:         *jevReplay,
		JevMaxCalls:           *jevMaxCalls,
		JevInputPricePerMTok:  *jevInputPrice,
		JevOutputPricePerMTok: *jevOutputPrice,
	})
	if err != nil {
		fail(err)
	}
	printSummary(report)
}

func printSummary(report *planningeval.FastlaneReport) {
	fmt.Printf("mode=%s corpus_turns=%d label_fast=%d theoretical_bypass=%.3f\n",
		report.Mode, report.Audit.CorpusTurns, report.Audit.CorpusLabelFast, report.Audit.TheoreticalBypassRatio)
	fmt.Printf("corpus rules: fast=%d correct=%d false_fast=%d false_fallback=%d\n",
		report.Corpus["rules"].FastRouted, report.Corpus["rules"].FastCorrect, report.Corpus["rules"].FalseFast, report.Corpus["rules"].FalseFallback)
	if report.Jev != nil {
		fmt.Printf("jev: requested=%d success=%d failures=%d p50=%dms p95=%dms verdicts=%v threshold=%.2f\n",
			report.Jev.Requested, report.Jev.Success, report.Jev.Failures, report.Jev.LatencyP50MS, report.Jev.LatencyP95MS,
			report.Jev.Verdicts, report.Threshold.Threshold)
		fmt.Printf("corpus rules+jev: fast=%d correct=%d false_fast=%d false_fallback=%d\n",
			report.Corpus["rules+jev"].FastRouted, report.Corpus["rules+jev"].FastCorrect, report.Corpus["rules+jev"].FalseFast, report.Corpus["rules+jev"].FalseFallback)
		fmt.Printf("setb rules: fast=%d correct=%d false_fast=%d | rules+jev: fast=%d correct=%d false_fast=%d\n",
			report.SetB["rules"].FastRouted, report.SetB["rules"].FastCorrect, report.SetB["rules"].FalseFast,
			report.SetB["rules+jev"].FastRouted, report.SetB["rules+jev"].FastCorrect, report.SetB["rules+jev"].FalseFast)
	}
	if report.Predicted != nil {
		fmt.Printf("predicted (NOT measured): saved_calls=%d per_turn_p50=%dms total_p50=%dms\n",
			report.Predicted.SavedScreeningCalls, report.Predicted.PerTurnSavingP50MS, report.Predicted.TotalSavingP50MS)
	}
}

func writeJSONOut(dir, name string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, mode); err != nil {
		return err
	}
	return nil
}

func binaryHash() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		return ""
	}
	return planningeval.Hash(binary)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "evalfastlane:", err)
	os.Exit(1)
}
