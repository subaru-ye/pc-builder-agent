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
	mode := flag.String("mode", "check", "check or replay; no live provider calls")
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
	if *mode != "replay" || *out == "" {
		fail(fmt.Errorf("use -mode check, or -mode replay -out NEW_DIRECTORY"))
	}
	if _, err = os.Stat(*out); !os.IsNotExist(err) {
		fail(fmt.Errorf("output directory must not exist"))
	}
	if err = os.MkdirAll(*out, 0755); err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	report, runErr := planningeval.Run(ctx, os.Getenv("PLANNING_EVAL_DSN"), suite, raw, planningeval.Models{})
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
