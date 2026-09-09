package evalsuite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func changeFixture(t *testing.T) (AuditedRun, AuditedRun) {
	t.Helper()
	makeRun := func() AuditedRun {
		a := AuditedRun{Meta: ReportMeta{SuiteSHA256: "frozen", SnapshotDate: "2026-09-08", RequestedSeeds: 3, Code: &CodeIdentity{BinarySHA256: "binary"}, HarnessProfile: &HarnessProfile{AttemptLimit: 3, Semantic: true}, Models: map[string]map[string]any{}}}
		for _, role := range []string{"builder", "screening", "embedding"} {
			a.Meta.Models[role] = map[string]any{"model": "fixed", "model_chain": []string{}, "provider": "test", "base_host": "example.invalid"}
		}
		for seed := 1; seed <= 3; seed++ {
			_, r := deliveredFixture(t)
			r.Seed = seed
			r.Usage = &Usage{ModelCalls: 2, UsageResponses: 2, TotalTokens: 100}
			a.Original = append(a.Original, r)
			a.Current = append(a.Current, r)
		}
		return a
	}
	return makeRun(), makeRun()
}

func TestChangeReportsRegressionsAndObservedEvidence(t *testing.T) {
	a, b := changeFixture(t)
	b.Meta.Code.BinarySHA256 = "changed-binary"
	b.Current[0].Verdict = Verdict{Failures: []AssertionFailure{{ID: "A6", Detail: "未给模型的 SKU", Veto: true}}}
	b.Current[0].Result.Message = "changed output"
	b.Current[0].Usage.ModelCalls++
	r, err := compareChanges(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.GatePassed || r.Regressed != 1 || r.RemainingFailures != 1 || r.Cases[0].APasses != 3 || r.Cases[0].BPasses != 2 || r.UsageDelta.ModelCalls != 1 || r.Cases[0].OutputChanges != 1 {
		t.Fatalf("%+v", r)
	}
	dir := t.TempDir()
	if err := WriteChangeReport(dir, r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "未给模型的 SKU") || !strings.Contains(string(raw), "不自动证明") {
		t.Fatalf("missing evidence: %s", raw)
	}
	r, err = compareChanges(b, a)
	if err != nil || !r.GatePassed || r.Improved != 1 {
		t.Fatalf("recovery: %+v %v", r, err)
	}
}

func TestChangeDoesNotConfuseGraderAndProductChanges(t *testing.T) {
	a, b := changeFixture(t)
	v := Verdict{Failures: []AssertionFailure{{ID: "A10", Detail: "共同规则复核失败"}}}
	a.Current[0].Verdict = v
	b.Current[0].Verdict = v
	r, err := compareChanges(a, b)
	if err != nil || r.Regressed != 0 || r.GatePassed || r.Cases[0].Status != "persistent_failure" || r.Cases[0].OriginalAPasses != 3 || r.Cases[0].APasses != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestChangeRefusesConfoundersAndMissingExecutions(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*AuditedRun)
	}{
		{"suite", func(b *AuditedRun) { b.Meta.SuiteSHA256 = "other" }},
		{"price", func(b *AuditedRun) { b.Meta.SnapshotDate = "2000-01-01" }},
		{"model", func(b *AuditedRun) { b.Meta.Models["builder"]["model"] = "other" }},
		{"chain", func(b *AuditedRun) { b.Meta.Models["builder"]["model_chain"] = []string{"other"} }},
		{"profile", func(b *AuditedRun) { b.Meta.HarnessProfile.AttemptLimit = 1 }},
		{"missing repeat", func(b *AuditedRun) { b.Current = b.Current[:2] }},
		{"duplicate repeat", func(b *AuditedRun) { b.Current[1].Seed = 1 }},
		{"catalog", func(b *AuditedRun) { b.Current[0].Snapshot.Catalog.Candidates[0].Model = "different" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, b := changeFixture(t)
			tt.mutate(&b)
			if _, err := compareChanges(a, b); err == nil {
				t.Fatal("invalid comparison accepted")
			}
		})
	}
}

func TestChangeMissingUsageIsUnknown(t *testing.T) {
	a, b := changeFixture(t)
	b.Current[0].Usage = nil
	r, err := compareChanges(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.UsageDelta != nil || r.Cases[0].UsageDelta != nil {
		t.Fatal("missing cost treated as zero")
	}
	a, b = changeFixture(t)
	b.Current[0].Usage.UsageResponses = 0
	r, err = compareChanges(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.BUsage.UsageResponses == r.BUsage.ModelCalls {
		t.Fatal("missing token coverage hidden")
	}
}
