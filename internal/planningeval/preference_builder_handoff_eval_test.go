package planningeval

import (
	"context"
	"strings"
	"testing"
)

// TestPrefHandoffCheckRegistry 验证 handoff-check 模式的注册表自检:
// 四类交接断言维度覆盖、ID 唯一。
func TestPrefHandoffCheckRegistry(t *testing.T) {
	if err := CheckPreferenceHandoffEval(); err != nil {
		t.Fatalf("handoff-check: %v", err)
	}
	orig := prefHandoffCases
	defer func() { prefHandoffCases = orig }()
	prefHandoffCases = append(prefHandoffCases, prefHandoffCases[0])
	if err := CheckPreferenceHandoffEval(); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 ID 未被拒绝: %v", err)
	}
	prefHandoffCases = orig[:len(orig)-1] // 摘掉 PM-BH-04
	if err := CheckPreferenceHandoffEval(); err == nil || !strings.Contains(err.Error(), "owner-conflict") {
		t.Fatalf("缺失分类未被拒绝: %v", err)
	}
}

// TestPrefHandoffRunRejectsRemoteDSN 验证 handoff-run 只接受 localhost
// 服务器,远程 DSN 在触网前被拒绝。
func TestPrefHandoffRunRejectsRemoteDSN(t *testing.T) {
	for _, dsn := range []string{"postgres://remote.example/db", "not-a-dsn", ""} {
		if _, err := RunPreferenceHandoffEval(context.Background(), dsn); err == nil {
			t.Fatalf("DSN %q 应被拒绝", dsn)
		}
	}
}
