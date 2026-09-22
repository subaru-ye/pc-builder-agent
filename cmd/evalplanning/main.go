// evalplanning runs the current product flow in a fresh isolated database.
// Offline is the default; only explicit live modes make provider calls, and
// Jev is reachable only through shadow-live or an explicit live opt-in.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
	"github.com/subaru-ye/pc-builder-agent/internal/providers/jev"
)

func main() {
	suitePath := flag.String("suite", "internal/planningeval/testdata/current-123-20260915-r2/mechanisms/suite.json", "frozen planning suite")
	out := flag.String("out", "", "new output directory")
	mode := flag.String("mode", "check", "check, replay, plan-live (zero calls), live (explicit bounded provider calls), or shadow-live (recorded product + live Jev)")
	maxCalls := flag.Int("max-calls", 0, "required positive shared model request ceiling for live")
	modelPin := flag.String("model-pin", "docs/eval/planning-v2/baseline-20260915.json", "pinned redacted model settings")
	jevModel := flag.String("jev-model", jev.ModelPin, "pinned Jev model")
	jevMaxCalls := flag.Int("jev-max-calls", 0, "required positive Jev call ceiling for shadow-live and jev-enabled live runs")
	jevTimeout := flag.Duration("jev-timeout", 2*time.Second, "per-call Jev timeout; an evaluation input, recorded in provenance")
	jevOptIn := flag.Bool("jev", false, "explicit opt-in attaching Jev shadow calls to a live run")
	jevInputPrice := flag.Float64("jev-input-price", 0, "recorded CNY rate per million input tokens; zero omits the cash estimate")
	jevOutputPrice := flag.Float64("jev-output-price", 0, "recorded CNY rate per million output tokens; zero omits the cash estimate")
	flag.Parse()
	raw, err := os.ReadFile(*suitePath)
	if err != nil {
		fail(err)
	}
	suite, err := planningeval.Load(raw)
	if err != nil {
		fail(err)
	}
	provenance, err := os.ReadFile(filepath.Join(filepath.Dir(*suitePath), "provenance.json"))
	if err != nil {
		fail(err)
	}
	if err = planningeval.VerifyProvenance(raw, provenance); err != nil {
		fail(err)
	}
	if *mode == "check" {
		labelled := 0
		for _, c := range suite.Cases {
			for _, s := range c.Steps {
				if s.Kind == "message" && s.Expect.NextAction != "" {
					labelled++
				}
			}
		}
		split := "none"
		if suite.IntentSplit != nil {
			split = fmt.Sprintf("calibration=%d holdout=%d", len(suite.IntentSplit.Calibration), len(suite.IntentSplit.Holdout))
		}
		fmt.Printf("%s: %d cases, %d labelled intent steps, intent_split %s, sha256=%s\n", suite.Version, len(suite.Cases), labelled, split, planningeval.Hash(raw))
		return
	}
	isLive := *mode == "live" || *mode == "plan-live"
	if *mode != "replay" && !isLive && *mode != "shadow-live" {
		fail(fmt.Errorf("unknown mode %q", *mode))
	}
	jevRequested := *mode == "shadow-live" || (isLive && *jevOptIn)
	if *mode == "replay" && (jevRequested || *jevMaxCalls > 0) {
		fail(fmt.Errorf("replay must stay network-free with respect to Jev"))
	}
	if jevRequested && *jevMaxCalls <= 0 {
		fail(fmt.Errorf("Jev calls require a positive -jev-max-calls cap"))
	}
	if (isLive && (!suite.Live || *maxCalls <= 0)) || (!isLive && suite.Live) {
		fail(fmt.Errorf("use a matching replay/live/shadow suite, new output directory and positive live max-calls"))
	}
	models := planningeval.Models{}
	jevPlan := map[string]any{}
	if jevRequested {
		// Resolve credentials and build the only Jev-reaching object before
		// creating the output directory, so a missing key fails cleanly. The
		// key stays in the environment; it is never copied into the plan, the
		// journal or the report.
		dotenv.Load(".env")
		apiKey := os.Getenv("JEV_API_KEY")
		if apiKey == "" {
			fail(fmt.Errorf("JEV_API_KEY is required for Jev calls; export it or add it to .env"))
		}
		base := os.Getenv("JEV_BASE_URL")
		if base == "" {
			base = jev.DefaultBaseURL
		}
		host := base
		if u, err := url.Parse(base); err == nil {
			host = u.Host
		}
		jevPlan = map[string]any{
			"model": *jevModel, "base_url_host": host, "timeout": jevTimeout.String(),
			"question_sha256": jev.QuestionHash(), "max_calls": *jevMaxCalls,
			"input_price_per_mtok_cny": *jevInputPrice, "output_price_per_mtok_cny": *jevOutputPrice,
		}
		models.Intent, err = jev.New(jev.Config{APIKey: apiKey, BaseURL: base, Model: *jevModel, Timeout: *jevTimeout, HTTPClient: &http.Client{}})
		if err != nil {
			fail(err)
		}
		models.MaxIntentCalls = *jevMaxCalls
		models.IntentInputPricePerMTok, models.IntentOutputPricePerMTok = *jevInputPrice, *jevOutputPrice
	}
	if _, err = os.Stat(*out); !os.IsNotExist(err) {
		fail(fmt.Errorf("output directory must not exist"))
	}
	if err = os.MkdirAll(*out, 0755); err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	// Persist exact inputs before evaluation so an interrupted process is auditable.
	if err = writeOut(*out, "suite.json", raw, 0600); err != nil {
		fail(err)
	}
	if err = writeOut(*out, "provenance.json", provenance, 0600); err != nil {
		fail(err)
	}
	plan := map[string]any{"mode": *mode, "created_at": time.Now().UTC(), "suite_sha256": planningeval.Hash(raw), "catalog_sha256": planningeval.Hash(mustJSON(suite.Catalog)), "binary_sha256": binaryHash()}
	if isLive {
		configs, redacted, err := fixedConfigs(*modelPin)
		if err != nil {
			fail(err)
		}
		plan["models"], plan["max_model_requests"] = redacted, *maxCalls
		plan["external_requests_limit"], plan["embedding_requests_limit"] = 0, 0
		if jevRequested {
			plan["jev"] = jevPlan
		}
		if *mode == "plan-live" {
			if err = writeJSONOut(*out, "plan.json", plan, 0600); err != nil {
				fail(err)
			}
			fmt.Println("Live plan saved; no model or database call.")
			return
		}
		intent := models.Intent
		models, err = liveModels(ctx, configs, *maxCalls)
		if err != nil {
			fail(err)
		}
		models.Intent = intent
	}
	if jevRequested {
		plan["jev"] = jevPlan
		if !isLive {
			plan["max_model_requests"] = 0
			plan["external_requests_limit"], plan["embedding_requests_limit"] = 0, 0
		}
		if err = writeJSONOut(*out, "plan.json", plan, 0600); err != nil {
			fail(err)
		}
	}
	if models.Journal == nil && (jevRequested || isLive) {
		journal, err := os.OpenFile(filepath.Join(*out, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fail(err)
		}
		defer func() { _ = journal.Close() }()
		models.Journal = func(value any) error {
			if err := json.NewEncoder(journal).Encode(value); err != nil {
				return err
			}
			return journal.Sync()
		}
	}
	report, runErr := planningeval.Run(ctx, os.Getenv("PLANNING_EVAL_DSN"), suite, raw, models)
	data, _ := json.MarshalIndent(report, "", "  ")
	if err = writeOut(*out, "report.json", data, 0644); err != nil {
		fail(err)
	}
	if err = writeOut(*out, "suite.json", raw, 0644); err != nil {
		fail(err)
	}
	if err = writeOut(*out, "provenance.json", provenance, 0644); err != nil {
		fail(err)
	}
	fmt.Printf("%s: %d/%d cases; Screening=%d Builder=%d tools=%d; external requests=0\n", report.Mode, report.Passed, len(report.Cases), report.ScreeningCalls, report.BuilderCalls, report.ToolCalls)
	for _, c := range report.Cases {
		if !c.Pass {
			for i, s := range c.Steps {
				for _, check := range s.Checks {
					if !check.Pass {
						fmt.Printf("FAIL %s step %d %s: %.300s\n", c.ID, i+1, check.Name, check.Detail)
					}
				}
			}
		}
	}
	if report.Intent != nil {
		fmt.Printf("Jev: calls=%d success=%d failure=%d labelled=%d accuracy=%s agreement=%d/%d\n",
			report.Intent.Calls, report.Intent.Successes, report.Intent.Failures, report.Intent.Labelled,
			formatRate(report.Intent.Accuracy), report.Intent.AgreementCount, report.Intent.AgreementTotal)
	}
	if runErr != nil {
		fail(runErr)
	}
	if report.Passed != len(suite.Cases) {
		os.Exit(1)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(2) }

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

func writeOut(dir, name string, raw []byte, mode os.FileMode) error {
	return os.WriteFile(filepath.Join(dir, name), raw, mode)
}

func writeJSONOut(dir, name string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeOut(dir, name, raw, mode)
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

func formatRate(rate *float64) string {
	if rate == nil {
		return "undefined"
	}
	return fmt.Sprintf("%.3f", *rate)
}
