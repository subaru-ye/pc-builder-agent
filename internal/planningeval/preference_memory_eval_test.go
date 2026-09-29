package planningeval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPrefEvalCheckRegistry 验证 check 模式的注册表自检:六类覆盖、ID 唯一。
func TestPrefEvalCheckRegistry(t *testing.T) {
	if err := CheckPreferenceMemoryEval(); err != nil {
		t.Fatalf("check: %v", err)
	}
	orig := prefEvalCases
	defer func() { prefEvalCases = orig }()
	prefEvalCases = append(prefEvalCases, prefEvalCases[0])
	if err := CheckPreferenceMemoryEval(); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 ID 未被拒绝: %v", err)
	}
	prefEvalCases = orig[:len(orig)-1] // 摘掉 PM-CONF-01
	if err := CheckPreferenceMemoryEval(); err == nil || !strings.Contains(err.Error(), "conf") {
		t.Fatalf("缺失分类未被拒绝: %v", err)
	}
}

// TestPrefEvalRunRejectsRemoteDSN 验证 run 模式只接受 localhost 服务器,
// 远程 DSN 在触网前被拒绝。
func TestPrefEvalRunRejectsRemoteDSN(t *testing.T) {
	for _, dsn := range []string{"postgres://remote.example/db", "not-a-dsn", ""} {
		if _, err := RunPreferenceMemoryEval(context.Background(), dsn); err == nil {
			t.Fatalf("DSN %q 应被拒绝", dsn)
		}
	}
}

// TestPrefEvalWriteReport 验证报告落盘形状:门禁、场景表与失败明细。
func TestPrefEvalWriteReport(t *testing.T) {
	dir := t.TempDir()
	report := &PrefEvalReport{
		SchemaVersion: 1, GeneratedAt: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
		Mode: "deterministic", Database: "peval_prefeval_test", GatePassed: true,
		Limitations: []string{"PM-STALE-01 仅验证拒绝写入;易失事实新鲜度窗口不是可保存产品能力。"},
		Cases: []PrefEvalCaseResult{{
			ID: "PM-CONF-01", Category: "conf", Summary: "确认前不生效",
			Assertions: []PrefEvalAssertion{{Name: "unconfirmed-not-applied", Pass: true}},
		}, {
			ID: "PM-DEL-01", Category: "del", Summary: "删除后不召回",
			Assertions: []PrefEvalAssertion{
				{Name: "storage-physically-deleted", Pass: true},
				{Name: "recall-after-delete-empty", Pass: false, Detail: "got 1, want 0"},
			},
		}},
	}
	if err := WritePrefEvalReport(dir, report, "PG_TEST_DSN=*** go run ./cmd/evalpreference -mode run -out x"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded PrefEvalReport
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Database != "peval_prefeval_test" {
		t.Fatalf("report.json 形状不符: %v %v", err, decoded.Database)
	}
	md, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(md)
	for _, want := range []string{"PM-CONF-01", "PM-DEL-01", "recall-after-delete-empty", "复现命令", "易失事实新鲜度窗口"} {
		if !strings.Contains(text, want) {
			t.Errorf("report.md 缺少 %q", want)
		}
	}
	if !strings.Contains(text, "pcbuilder:***@") && strings.Contains(text, "复现命令:`PG_TEST_DSN=postgres://") {
		t.Error("报告里的复现命令不应包含未脱敏 DSN")
	}
}

func TestPrefEvalProblemCode(t *testing.T) {
	if problemCode(nil) != "" {
		t.Fatal("nil 错误应返回空码")
	}
	if problemCode(context.Canceled) != "" {
		t.Fatal("非 Problem 错误应返回空码")
	}
}
