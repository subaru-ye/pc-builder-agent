package main

import (
	"context"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
)

type evidencePlannerFunc func(context.Context, buildharness.BuildInput) (buildharness.CandidateBundle, error)

func (f evidencePlannerFunc) Prepare(c context.Context, i buildharness.BuildInput) (buildharness.CandidateBundle, error) {
	return f(c, i)
}

type evidenceHarnessFunc func(context.Context, buildharness.BuildInput) (buildharness.BuildResult, error)

func (f evidenceHarnessFunc) Run(c context.Context, i buildharness.BuildInput) (buildharness.BuildResult, error) {
	return f(c, i)
}

func TestCandidateEvidenceIsIndependentAndClearedForEarlyDecision(t *testing.T) {
	bundle := buildharness.CandidateBundle{Groups: []buildharness.CandidateGroup{{Candidates: []buildharness.Candidate{{SKU: "sku", MatchText: "低噪说明"}}}}}
	p := &recordingPlanner{CandidatePlanner: evidencePlannerFunc(func(context.Context, buildharness.BuildInput) (buildharness.CandidateBundle, error) {
		return bundle, nil
	})}
	h := recordingHarness{planner: p, Harness: evidenceHarnessFunc(func(ctx context.Context, i buildharness.BuildInput) (buildharness.BuildResult, error) {
		_, err := p.Prepare(ctx, i)
		return buildharness.BuildResult{}, err
	})}
	if _, err := h.Run(context.Background(), buildharness.BuildInput{}); err != nil {
		t.Fatal(err)
	}
	saved := h.CandidateBundle()
	if saved == nil {
		t.Fatal("missing evidence")
	}
	bundle.Groups[0].Candidates[0].MatchText = "modified"
	if saved.Groups[0].Candidates[0].MatchText != "低噪说明" {
		t.Fatal("evidence shares mutable storage")
	}
	h.Harness = evidenceHarnessFunc(func(context.Context, buildharness.BuildInput) (buildharness.BuildResult, error) {
		return buildharness.BuildResult{}, nil
	})
	if _, err := h.Run(context.Background(), buildharness.BuildInput{}); err != nil {
		t.Fatal(err)
	}
	if h.CandidateBundle() != nil {
		t.Fatal("previous case candidates leaked to early decision")
	}
	if saved.Groups[0].Candidates[0].SKU != "sku" {
		t.Fatal("previous record was lost")
	}
}
