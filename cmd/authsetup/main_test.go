package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateEnvPreservesExistingValuesAndUsesLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	input := "DASHSCOPE_API_KEY=keep-me\r\nSUPABASE_JWT_SECRET=existing-secret\r\n"
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := updateEnv(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "\r") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("expected LF and final newline: %q", got)
	}
	for _, want := range []string{
		"DASHSCOPE_API_KEY=keep-me",
		"SUPABASE_JWT_SECRET=existing-secret",
		"AUTH_ENABLED=true",
		"AUTH_SESSION_SECRET=",
		"AUTH_SESSION_TTL=720h",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Count(got, "SUPABASE_JWT_SECRET=") != 1 {
		t.Fatal("existing secret was duplicated")
	}
}
