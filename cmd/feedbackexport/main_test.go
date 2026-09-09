package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportNeverOverwritesReviewedFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pending")
	before := []byte(`{"review_status":"pending_review","expected":null}`)
	path, err := writeCandidate(out, "test-feedback", before)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeCandidate(out, "test-feedback", []byte(`{"expected":"invented"}`)); err == nil {
		t.Fatal("existing candidate overwritten")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != string(before)+"\n" {
		t.Fatalf("export modified: %s %v", raw, err)
	}
}
