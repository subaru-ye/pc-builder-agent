package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
)

// Binary hash identifies the actual runner/grader, including uncommitted code.
// Git describes the launch checkout; it is not claimed to be the binary's source.
func codeIdentity() evalsuite.CodeIdentity {
	id := evalsuite.CodeIdentity{GoVersion: runtime.Version()}
	if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		id.CheckoutCommit = strings.TrimSpace(string(out))
		if out, err := exec.Command("git", "status", "--porcelain", "--untracked-files=normal").Output(); err == nil {
			dirty := len(strings.TrimSpace(string(out))) != 0
			id.CheckoutDirty = &dirty
		}
	}
	if path, err := os.Executable(); err == nil {
		if f, err := os.Open(path); err == nil {
			h := sha256.New()
			_, readErr := io.Copy(h, f)
			closeErr := f.Close()
			if readErr == nil && closeErr == nil {
				id.BinarySHA256 = fmt.Sprintf("%x", h.Sum(nil))
			}
		}
	}
	return id
}

func modelIdentity(cfg modelprovider.Config) map[string]any {
	value := cfg.Redacted()
	value["model_chain"] = append([]string{}, cfg.ModelChain...)
	value["timeout"] = cfg.Timeout.String()
	value["dimensions"] = cfg.Dimensions
	return value
}

func checkRecords(records []evalsuite.CaseRecord, cases map[string]evalsuite.Case, repeats int) error {
	if repeats < 1 || len(records) != len(cases)*repeats {
		return fmt.Errorf("incomplete run records")
	}
	seen := map[string]map[int]bool{}
	for _, r := range records {
		c, ok := cases[r.CaseID]
		if !ok || r.Stage != c.Stage || r.Seed < 1 || r.Seed > repeats {
			return fmt.Errorf("unexpected case/repeat: %s/%d", r.CaseID, r.Seed)
		}
		if seen[r.CaseID] == nil {
			seen[r.CaseID] = map[int]bool{}
		}
		if seen[r.CaseID][r.Seed] {
			return fmt.Errorf("duplicate case/repeat: %s/%d", r.CaseID, r.Seed)
		}
		seen[r.CaseID][r.Seed] = true
	}
	return nil
}
