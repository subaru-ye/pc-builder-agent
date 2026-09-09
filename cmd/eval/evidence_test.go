package main

import (
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func TestRunPromptEvidenceOnlyIncludesUsedRoles(t *testing.T) {
	for _, stage := range []evalsuite.Stage{evalsuite.StageBuild, evalsuite.StageScreening} {
		snapshot, id, err := runPromptEvidence([]evalsuite.Case{{Stage: stage}})
		if err != nil || len(snapshot.Components) == 0 || id.SHA256 == "" {
			t.Fatal("missing compiled prompt evidence", err)
		}
		role := "builder"
		if stage == evalsuite.StageScreening {
			role = "screening"
		}
		for _, component := range snapshot.Components {
			if component.Role != role {
				t.Fatal("unused model role was presented as evaluated")
			}
		}
	}
}

func TestLimitCompletionDistinguishesReachingBudgetFromDeniedWork(t *testing.T) {
	u := &usageCounter{maxCalls: 1}
	if err := u.reserve(false); err != nil || u.limitDenied() {
		t.Fatal("finishing exactly at the budget became an incomplete run")
	}
	if err := u.reserve(false); err == nil || !u.limitDenied() {
		t.Fatal("denied request was not marked incomplete")
	}
	u = &usageCounter{maxCalls: 1}
	_ = u.reserve(false)
	if err := u.stopReason(); err == nil || !u.limitDenied() {
		t.Fatal("unexecuted remaining case was not marked incomplete")
	}
}
