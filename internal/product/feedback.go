package product

import (
	"context"
	"encoding/json"
	"log"

	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type feedbackStore interface {
	SubmitFeedback(context.Context, string, string, string, string) (store.Feedback, error)
	FeedbackByOwner(context.Context, string, string) (*store.Feedback, error)
}

func (s *Service) SubmitFeedback(ctx context.Context, ownerID, runID, reason, comment string) (store.Feedback, error) {
	f, ok := s.store.(feedbackStore)
	if !ok {
		return store.Feedback{}, NewProblem("feedback_unavailable", "反馈暂不可用", 503, "请稍后重试。", "")
	}
	return f.SubmitFeedback(ctx, ownerID, runID, reason, comment)
}
func (s *Service) GetFeedback(ctx context.Context, ownerID, runID string) (*store.Feedback, error) {
	f, ok := s.store.(feedbackStore)
	if !ok {
		return nil, NewProblem("feedback_unavailable", "反馈暂不可用", 503, "请稍后重试。", "")
	}
	return f.FeedbackByOwner(ctx, ownerID, runID)
}

// 证据是旁路记录；缺失时明确标缺，不让反馈采集故障破坏正常装机。
func (s *Service) captureEvidence(ctx context.Context, runID, slot string, value any) {
	st, ok := s.store.(interface {
		CaptureRunEvidence(context.Context, string, string, json.RawMessage) error
	})
	if !ok {
		return
	}
	raw, err := json.Marshal(value)
	if err == nil {
		err = st.CaptureRunEvidence(ctx, runID, slot, raw)
	}
	if err != nil {
		log.Printf("[api] run %s feedback evidence slot %s unavailable", runID, slot)
	}
}

func screeningEvidence(input ScreenInput) map[string]any {
	var configured any
	if cfg, err := modelprovider.Load(modelprovider.RoleScreening); err == nil {
		profile := cfg.Redacted()
		profile["model_chain"] = append([]string{}, cfg.ModelChain...)
		configured = profile
	}
	return map[string]any{"text": input.Text, "user_sources": input.UserSources, "context": input.Context, "has_build": input.HasBuild,
		"requirement_state": input.RequirementState, "conversation": input.Conversation,
		"configured_model": configured, "model_evidence_source": "api_environment_at_execution_not_per_request_identity"}
}
