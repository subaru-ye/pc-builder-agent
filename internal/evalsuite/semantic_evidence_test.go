package evalsuite

import (
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"testing"
)

func TestSemanticEvidenceSeparatesRetrievalFromDeliveryAndUnknown(t *testing.T) {
	a := CaseRecord{Candidates: &buildharness.CandidateBundle{Groups: []buildharness.CandidateGroup{{Category: schemas.CategoryCase, Candidates: []buildharness.Candidate{{SKU: "case-a", MatchText: "外观白色，需要核对具体版本"}, {SKU: "case-b"}}}}}, Result: &buildharness.BuildResult{Succeeded: true, Draft: schemas.BuildDraft{Selection: schemas.BuildSelection{Case: "case-a"}}}}
	b := a
	b.Candidates = &buildharness.CandidateBundle{Groups: []buildharness.CandidateGroup{{Category: schemas.CategoryCase, Candidates: []buildharness.Candidate{{SKU: "case-b"}}}}}
	out := semanticTrial(a, b)
	if len(out.RemovedInB) != 1 || out.RemovedInB[0] != "case-a" || len(out.A.Matches) != 1 || !out.A.Matches[0].Selected || len(out.B.Matches) != 0 {
		t.Fatalf("wrong evidence: %+v", out)
	}
	a.Result.Succeeded = false
	if candidateEvidence(a).Matches[0].Selected {
		t.Fatal("failed draft counted as delivered preference evidence")
	}
	if candidateEvidence(CaseRecord{}).Recorded {
		t.Fatal("missing evidence treated as empty measurement")
	}
}
