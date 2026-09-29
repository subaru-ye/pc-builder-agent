package planningeval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func candidateFixture(t *testing.T) (schemas.RequirementState, []preferenceCandidateMessage) {
	t.Helper()
	quote := "小王一直喜欢安静。"
	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{
		Operations: []schemas.RequirementOperation{{Op: "set", Field: "noise_pref", Value: json.RawMessage(`"silent"`), Strength: "must", Scope: "session", Evidence: "stated", Quote: quote}},
	}, schemas.RequirementSource{Kind: "chat", MessageID: "m1", Quote: quote})
	if err != nil {
		t.Fatal(err)
	}
	return state, []preferenceCandidateMessage{{ID: "m1", Role: "user", Content: quote}}
}

func TestCandidateRequiresSubjectSelectionAndPreservesState(t *testing.T) {
	state, messages := candidateFixture(t)
	before, _ := json.Marshal(state)
	got := preferenceCandidates("s1", state, messages, true)
	if len(got) != 1 || got[0].Candidate == nil {
		t.Fatalf("expected candidate: %+v", got)
	}
	candidate := got[0].Candidate
	if candidate.SubjectStatus != "requires_user_selection" || candidate.Strength != "must" || candidate.Evidence != "stated" || candidate.Source.SessionID != "s1" || candidate.Source.MessageID != "m1" || candidate.Source.Quote != messages[0].Content {
		t.Fatalf("归属/强度/来源不符: %+v", candidate)
	}
	// 改动返回值也不能修改会话状态。
	candidate.Value[1] = 'x'
	after, _ := json.Marshal(state)
	if string(after) != string(before) {
		t.Fatal("候选提取修改了输入状态")
	}
}

func TestCandidateRejectsUntrustedState(t *testing.T) {
	for _, name := range []string{"temporary", "missing_scope", "inferred", "uncertain", "accepted_proposal", "missing_source", "assistant_source", "missing_message", "duplicate_message", "forged_quote", "invalid_value", "invalid_strength", "edit_source"} {
		t.Run(name, func(t *testing.T) {
			state, messages := candidateFixture(t)
			f := state.Fields["noise_pref"]
			switch name {
			case "temporary":
				f.Scope = "temporary"
			case "missing_scope":
				f.Scope = ""
			case "inferred", "uncertain", "accepted_proposal":
				f.Evidence = name
			case "missing_source":
				f.Source = nil
			case "assistant_source":
				messages[0].Role = "assistant"
			case "missing_message":
				messages = nil
			case "duplicate_message":
				messages = append(messages, messages[0])
			case "forged_quote":
				f.Source.Quote = "用户没有说的话"
			case "invalid_value":
				f.Value = json.RawMessage(`"not-a-noise-value"`)
			case "invalid_strength":
				f.Strength = "mandatory"
			case "edit_source":
				f.Source.Kind = "edit"
			}
			state.Fields["noise_pref"] = f
			for _, lifetime := range []bool{false, true} {
				got := preferenceCandidates("s1", state, messages, lifetime)
				if len(got) != 1 || got[0].Candidate != nil {
					t.Fatalf("不可信状态被接受: %+v", got)
				}
			}
		})
	}
}

func TestCandidateLifetimeUsesWholeMessageAndBoundClause(t *testing.T) {
	state, messages := candidateFixture(t)
	messages[0].Content = "不要记住：" + messages[0].Content
	if got := preferenceCandidates("s1", state, messages, true); got[0].Candidate != nil || got[0].Reason != "negated_language" {
		t.Fatalf("截断 quote 绕过消息否定: %+v", got)
	}
	if got := preferenceCandidates("s1", state, messages, false); got[0].Candidate == nil {
		t.Fatal("对照策略应只核对结构来源，不检查长期意图")
	}
	if reason := preferenceLifetimeReason("brand_pref.gpu", json.RawMessage(`"nvidia"`), "我一直喜欢安静，显卡用N卡。", "我一直喜欢安静，显卡用N卡。"); reason != "no_bound_lifetime_evidence" {
		t.Fatalf("长期意图跨分句扩散: %s", reason)
	}
	if preferenceValueGrounded("brand_pref.cpu", json.RawMessage(`"intel"`), "我一直喜欢intelligent设计") {
		t.Fatal("英文单词的品牌子串不得当来源")
	}
	if !preferenceValueGrounded("brand_pref.cpu", json.RawMessage(`"intel"`), "我一向优先选择英特尔CPU") {
		t.Fatal("中文品牌边界不能因为后接英文类别名而被拒绝")
	}
}

func TestCandidateMetricsCountWrongStrengthAsFPAndFN(t *testing.T) {
	state, messages := candidateFixture(t)
	r := CandidateEvalResult{ID: "s1", Expected: []candidateGold{{Field: "noise_pref", Value: json.RawMessage(`"silent"`), Strength: "prefer", Evidence: "stated", MessageID: "m1", Quote: messages[0].Content}}, Decisions: preferenceCandidates("s1", state, messages, true)}
	var metrics CandidateMetrics
	gradePreferenceCandidates(&r, &metrics)
	if metrics.FalsePositives != 1 || metrics.FalseNegatives != 1 || metrics.TruePositives != 0 || metrics.ExactCases != 0 || len(r.Errors) != 2 {
		t.Fatalf("错强度必须同时 FP/FN: %+v %+v", metrics, r.Errors)
	}
	if candidateRatio(0, 0) != nil {
		t.Fatal("零分母不能标成 100%")
	}
}

func TestCandidateCorpusAndReport(t *testing.T) {
	if err := CheckPreferenceCandidateEval(); err != nil {
		t.Fatal(err)
	}
	// 仅执行 dev，不用单测对验证集输出做调优或要求暂定金标全绿。
	report, err := RunPreferenceCandidateEval("dev")
	if err != nil {
		t.Fatal(err)
	}
	if report.ReadyForProduct || report.GoldStatus != "provisional" || len(report.PolicySHA256) != 64 || len(report.CorpusSHA256) != 64 {
		t.Fatalf("实验元数据错误: %+v", report)
	}
	for _, c := range report.Cases {
		if c.Split != "dev" {
			t.Fatal("分组泄漏")
		}
	}
	dir := filepath.Join(t.TempDir(), "result")
	if err := WritePreferenceCandidateReport(dir, report); err != nil {
		t.Fatal(err)
	}
	if err := WritePreferenceCandidateReport(dir, report); err == nil {
		t.Fatal("不能覆盖已有报告")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil || !strings.Contains(string(raw), "暂定金标") || !strings.Contains(string(raw), "FP:") || !strings.Contains(string(raw), "FN:") {
		t.Fatalf("报告缺少失败/限制: %v", err)
	}
	if _, err := RunPreferenceCandidateEval("holdout-unreviewed"); err == nil {
		t.Fatal("无效 split 未拒绝")
	}
}

func TestCandidateFixtureRejectsUnexpectedReducerFailure(t *testing.T) {
	c := candidateEvalCase{ID: "broken", Turns: []candidateEvalTurn{{preferenceCandidateMessage: preferenceCandidateMessage{ID: "m1", Role: "user", Content: "没有这个证据"}, Update: schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{{Op: "set", Field: "noise_pref", Value: json.RawMessage(`"silent"`), Quote: "伪造的原话"}}}}}}
	if _, _, err := candidateCaseState(c); err == nil {
		t.Fatal("夹具技术错误不能伪装成正确拒绝")
	}
	c.Turns[0].ExpectRejected = true
	if _, _, err := candidateCaseState(c); err != nil {
		t.Fatalf("声明过的防御反例应允许: %v", err)
	}
}
