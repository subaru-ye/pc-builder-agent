package evalsuite

import "testing"

func TestComparePairsCasesAndRejectsConfounds(t *testing.T) {
	a := ReportMeta{SuiteSHA256: "suite", SnapshotDate: "date", RequestedSeeds: 3, Code: &CodeIdentity{BinarySHA256: "binary"}, HarnessProfile: &HarnessProfile{AttemptLimit: 3, Semantic: true}, Models: map[string]map[string]any{"builder": {"model": "qwen"}}}
	b := a
	b.HarnessProfile = &HarnessProfile{AttemptLimit: 1, Semantic: true}
	var ar, br []CaseRecord
	for seed := 1; seed <= 3; seed++ {
		ar = append(ar, CaseRecord{CaseID: "case", Stage: StageBuild, Seed: seed, Verdict: Verdict{Passed: true}})
		br = append(br, CaseRecord{CaseID: "case", Stage: StageBuild, Seed: seed, Verdict: Verdict{Passed: seed == 1}})
	}
	out, err := Compare(a, ar, b, br, "repair")
	if err != nil || len(out.Cases) != 1 || out.Cases[0].AOnly != 2 || out.Cases[0].BothPass != 1 || out.Cases[0].UsageMeasured {
		t.Fatalf("comparison=%+v err=%v", out, err)
	}
	if _, err := Compare(a, ar, b, br[:2], "repair"); err == nil {
		t.Fatal("incomplete trials accepted")
	}
	b.HarnessProfile = &HarnessProfile{AttemptLimit: 1, Semantic: false}
	if _, err := Compare(a, ar, b, br, "repair"); err == nil {
		t.Fatal("two factors accepted")
	}
	b = a
	b.Models = map[string]map[string]any{"builder": {"model": "deepseek"}}
	if _, err := Compare(a, ar, b, br, "builder"); err != nil {
		t.Fatal(err)
	}
	if a.Models["builder"]["model"] != "qwen" {
		t.Fatal("comparison mutated source metadata")
	}
	for seed := 1; seed <= 3; seed++ {
		ar = append(ar, CaseRecord{CaseID: "screen", Stage: StageScreening, Seed: seed, Verdict: Verdict{Passed: false}})
		br = append(br, CaseRecord{CaseID: "screen", Stage: StageScreening, Seed: seed, Verdict: Verdict{Passed: true}})
	}
	out, err = Compare(a, ar, b, br, "builder")
	if err != nil || len(out.Cases) != 1 || len(out.ExcludedCaseIDs) != 1 || out.Stage != StageBuild || out.MeanPassRateDelta != -2.0/3 {
		t.Fatalf("unrelated stage changed measured effect: %+v %v", out, err)
	}
	b.Code = &CodeIdentity{BinarySHA256: "other"}
	if _, err := Compare(a, ar, b, br, "builder"); err == nil {
		t.Fatal("different code accepted")
	}
}
