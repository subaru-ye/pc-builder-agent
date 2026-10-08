package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func TestSaveRequirementPromptEvidence(t *testing.T) {
	dir := t.TempDir()
	identity, err := savePromptEvidence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(identity.Components) != 2 || identity.Components["screening/system"] == "" || identity.Components["screening/format_retry"] == "" {
		t.Fatal("incorrect active protocol components")
	}
	if err := evalsuite.VerifyRunEvidence(dir, evalsuite.ReportMeta{Prompts: &identity}, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "prompts.json"))
	if _, err := savePromptEvidence(dir); err == nil {
		t.Fatal("existing evidence overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "prompts.json"))
	if string(before) != string(after) {
		t.Fatal("original evidence changed")
	}
}
