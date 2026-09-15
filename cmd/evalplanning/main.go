// evalplanning runs the current product flow in a fresh isolated database.
// It does not load project credentials; the default model is an offline oracle.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
	"os"
	"path/filepath"
	"time"
)

func main() {
	suitePath := flag.String("suite", "internal/planningeval/testdata/v2.0.json", "frozen planning suite")
	out := flag.String("out", "", "new output directory")
	mode := flag.String("mode", "check", "check, replay, plan-live (zero calls), or live (explicit bounded provider calls)")
	maxCalls := flag.Int("max-calls", 0, "required positive shared model request ceiling for live")
	modelPin := flag.String("model-pin", "docs/eval/planning-v2/baseline-20260915.json", "pinned redacted model settings")
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
		fmt.Printf("%s: %d cases, sha256=%s\n", suite.Version, len(suite.Cases), planningeval.Hash(raw))
		return
	}
	isLive := *mode == "live" || *mode == "plan-live"
	if (*mode != "replay" && !isLive) || *out == "" || (isLive && (!suite.Live || *maxCalls <= 0)) || (!isLive && suite.Live) {
		fail(fmt.Errorf("use a matching replay/live suite, new output directory and positive live max-calls"))
	}
	if _, err = os.Stat(*out); !os.IsNotExist(err) {
		fail(fmt.Errorf("output directory must not exist"))
	}
	if err = os.MkdirAll(*out, 0755); err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	models := planningeval.Models{}
	if isLive {
		configs, redacted, err := fixedConfigs(*modelPin)
		if err != nil {
			fail(err)
		}
		exe, err := os.Executable()
		if err != nil {
			fail(err)
		}
		binary, err := os.ReadFile(exe)
		if err != nil {
			fail(err)
		}
		catalog, _ := json.Marshal(suite.Catalog)
		plan := map[string]any{"mode": *mode, "created_at": time.Now().UTC(), "suite_sha256": planningeval.Hash(raw), "catalog_sha256": planningeval.Hash(catalog), "models": redacted, "max_model_requests": *maxCalls, "external_requests_limit": 0, "embedding_requests_limit": 0, "binary_sha256": planningeval.Hash(binary)}
		planRaw, _ := json.MarshalIndent(plan, "", "  ")
		if err = os.WriteFile(filepath.Join(*out, "plan.json"), planRaw, 0600); err != nil {
			fail(err)
		}
		if *mode == "plan-live" {
			fmt.Println("Live plan saved; no model or database call.")
			return
		}
		models, err = liveModels(ctx, configs, *maxCalls)
		if err != nil {
			fail(err)
		}
		journal, err := os.OpenFile(filepath.Join(*out, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fail(err)
		}
		defer journal.Close()
		models.Journal = func(value any) error {
			if err := json.NewEncoder(journal).Encode(value); err != nil {
				return err
			}
			return journal.Sync()
		}
	}
	// Persist exact inputs before evaluation so an interrupted process is auditable.
	if err = os.WriteFile(filepath.Join(*out, "suite.json"), raw, 0600); err != nil {
		fail(err)
	}
	if err = os.WriteFile(filepath.Join(*out, "provenance.json"), provenance, 0600); err != nil {
		fail(err)
	}
	report, runErr := planningeval.Run(ctx, os.Getenv("PLANNING_EVAL_DSN"), suite, raw, models)
	data, _ := json.MarshalIndent(report, "", "  ")
	if err = os.WriteFile(filepath.Join(*out, "report.json"), data, 0644); err != nil {
		fail(err)
	}
	if err = os.WriteFile(filepath.Join(*out, "suite.json"), raw, 0644); err != nil {
		fail(err)
	}
	if err = os.WriteFile(filepath.Join(*out, "provenance.json"), provenance, 0644); err != nil {
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
	if runErr != nil {
		fail(runErr)
	}
	if report.Passed != len(suite.Cases) {
		os.Exit(1)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
