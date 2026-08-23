package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `{
  "error": null,
  "finished_at": "2026-08-24T00:00:01Z",
  "model_used": false,
  "profile": "weekly",
  "run_id": "1130de13-2f6a-56c4-bec8-1e034e7771ca",
  "scheduled_for": "2026-08-24T00:00:00Z",
  "schema_version": 1,
  "sources": [],
  "started_at": "2026-08-24T00:00:00Z",
  "status": "no_change",
  "summary": {"changes": 0},
  "trigger": "schedule"
}
`

func writeManifest(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRunManifest(t *testing.T) {
	manifest, err := loadRunManifest(writeManifest(t, validManifest))
	if err != nil {
		t.Fatalf("合法 manifest 应通过: %v", err)
	}
	if manifest.Status != "no_change" || len(manifest.ManifestSHA) != 64 {
		t.Fatalf("解码结果错误: %+v", manifest)
	}
}

func TestLoadRunManifestRejectsModelAndUnknownField(t *testing.T) {
	model := strings.Replace(validManifest, `"model_used": false`, `"model_used": true`, 1)
	if _, err := loadRunManifest(writeManifest(t, model)); err == nil || !strings.Contains(err.Error(), "禁止") {
		t.Fatalf("model_used=true 应失败,得到 %v", err)
	}
	unknown := strings.Replace(validManifest, `"error": null,`, `"error": null, "api_key": "x",`, 1)
	if _, err := loadRunManifest(writeManifest(t, unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("未知字段应失败,得到 %v", err)
	}
}
