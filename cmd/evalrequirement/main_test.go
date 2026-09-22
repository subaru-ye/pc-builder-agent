package main

import (
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
)

func TestCompareRejectsGraderMixing(t *testing.T) {
	plan := func(grader string) map[string]any {
		return map[string]any{"manifest_sha256": "m1", "grader_version": grader, "repeats": 1}
	}
	if err := compareIdentityError(plan(planningeval.ReqV2GraderVersion), plan(planningeval.ReqV2GraderVersion)); err != nil {
		t.Fatalf("same-version plans must compare: %v", err)
	}
	err := compareIdentityError(plan("reqv2-grader-v1"), plan(planningeval.ReqV2GraderVersion))
	if err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("mixed grader versions must be rejected, got %v", err)
	}
	err = compareIdentityError(plan("reqv2-grader-v1"), plan("reqv2-grader-v1"))
	if err == nil || !strings.Contains(err.Error(), "regrade") {
		t.Fatalf("two stale v1 runs must be rejected until regraded, got %v", err)
	}
}

func TestReplayRequiresExplicitRegrade(t *testing.T) {
	if err := replaySourceGraderError(planningeval.ReqV2GraderVersion, false); err != nil {
		t.Fatalf("same-version replay must not require -regrade: %v", err)
	}
	err := replaySourceGraderError("reqv2-grader-v1", false)
	if err == nil || !strings.Contains(err.Error(), "-regrade") {
		t.Fatalf("cross-version replay without -regrade must be rejected, got %v", err)
	}
	if err := replaySourceGraderError("reqv2-grader-v1", true); err != nil {
		t.Fatalf("explicit -regrade must be allowed: %v", err)
	}
}
