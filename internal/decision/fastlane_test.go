package decision

import (
	"encoding/json"
	"testing"
)

// 问题定义哈希钉死：criteria 文案漂移会让历史报告无法对回当时的问题。
func TestFastlaneQuestionHashStable(t *testing.T) {
	const pinned = "89f565970575e8bf165b4dcb59a605e146229f99ceefee3163fb3555faad42d8"
	if got := FastlaneQuestionHash(); got != pinned {
		t.Fatalf("FastlaneQuestionHash = %s, want %s（若为有意变更，请同步更新本钉与运行记录）", got, pinned)
	}
}

func TestFastlaneCriteriaCoverAllVerdicts(t *testing.T) {
	criteria := FastlaneCriteria()
	for _, verdict := range []FastlaneVerdict{VerdictDetermine, VerdictAsk, VerdictQuoteReference, VerdictReject, VerdictUncertain} {
		if criteria[verdict] == "" {
			t.Errorf("criteria missing %s", verdict)
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(FastlaneQuestionJSON(), &raw); err != nil {
		t.Fatalf("question json: %v", err)
	}
}
