package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFeedbackValidation(t *testing.T) {
	if ValidFeedback("unknown", "text") || ValidFeedback("other", "  ") || ValidFeedback("price_issue", strings.Repeat("字", 2001)) || !ValidFeedback("price_issue", strings.Repeat("字", 2000)) {
		t.Fatal("feedback validation boundary")
	}
}

func TestFeedbackEvidenceLifecycle(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	owner, sessionID, runID := "feedback-owner", uuid.NewString(), uuid.NewString()
	if _, err := s.CreateWebSession(ctx, sessionID, owner, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StartMessageRun(ctx, StartMessageRunParams{OwnerID: owner, SessionID: sessionID, RequestID: uuid.NewString(), RunID: runID, MessageID: uuid.NewString(), Text: "预算8000元", Title: "测试"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitFeedback(ctx, owner, runID, "price_issue", ""); !errors.Is(err, ErrFeedbackRunActive) {
		t.Fatalf("active: %v", err)
	}
	if _, err := s.SubmitFeedback(ctx, "other-owner", runID, "price_issue", ""); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("ownership: %v", err)
	}
	if _, err := s.FeedbackByOwner(ctx, "other-owner", runID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("read ownership: %v", err)
	}
	if f, err := s.FeedbackByOwner(ctx, owner, runID); err != nil || f != nil {
		t.Fatalf("initial: %+v %v", f, err)
	}
	for _, value := range []string{`{"text":"original"}`, `{"text":"must not overwrite"}`} {
		if err := s.CaptureRunEvidence(ctx, runID, "screening_input", json.RawMessage(value)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CompleteRun(ctx, CompleteRunParams{RunID: runID, SessionID: sessionID, AssistantMessageID: uuid.NewString(), AssistantContent: "请说明预算", Status: RunSucceeded, Phase: PhaseCollecting}); err != nil {
		t.Fatal(err)
	}
	f, err := s.SubmitFeedback(ctx, owner, runID, "unnecessary_question", " 已经说过了 ")
	if err != nil {
		t.Fatal(err)
	}
	if f.Comment != "已经说过了" || len(f.Fingerprint) != 64 {
		t.Fatalf("feedback: %+v", f)
	}
	repeated, err := s.SubmitFeedback(ctx, owner, runID, f.Reason, f.Comment)
	if err != nil || repeated != f {
		t.Fatalf("not idempotent: %+v %v", repeated, err)
	}
	before, err := s.FeedbackCandidate(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 晚到的证据和后续修改不能改写首次提交时冻结的场景。
	if err := s.CaptureRunEvidence(ctx, runID, "screening_output", json.RawMessage(`{"text":"late output"}`)); err != nil {
		t.Fatal(err)
	}
	changed, err := s.SubmitFeedback(ctx, owner, runID, "unclear_explanation", "修改说明")
	if err != nil || changed.ID != f.ID || changed.Fingerprint != f.Fingerprint {
		t.Fatalf("evidence identity drift: %+v %v", changed, err)
	}
	after, err := s.FeedbackCandidate(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	var first, last map[string]json.RawMessage
	if json.Unmarshal(before, &first) != nil || json.Unmarshal(after, &last) != nil {
		t.Fatal("invalid export")
	}
	if string(first["evidence"]) != string(last["evidence"]) || string(last["expected"]) != "null" || string(last["review_status"]) != `"pending_review"` {
		t.Fatalf("export contract: %s", after)
	}
	if !strings.Contains(string(after), "original") || strings.Contains(string(after), "must not overwrite") || strings.Contains(string(after), "late output") || !strings.Contains(string(after), "missing_evidence") {
		t.Fatalf("frozen evidence: %s", after)
	}
	if _, err := s.FeedbackCandidate(ctx, uuid.NewString()); !errors.Is(err, ErrFeedbackNotFound) {
		t.Fatalf("missing: %v", err)
	}
}
