package buildharness

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestExplicitSoftBrandKeepsAlternativesAndRanksPreferredFirst(t *testing.T) {
	embedder := &countingEmbedder{}
	planner, _ := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, embedder)
	spec := fixtureRequirement()
	spec.BrandPref.CPU, spec.BrandPref.GPU = schemas.CPUBrandIntel, schemas.GPUBrandNvidia
	spec.ConstraintStrengths = map[string]string{"brand_pref.cpu": "prefer", "brand_pref.gpu": "prefer"}
	bundle, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec})
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range []schemas.Category{schemas.CategoryCPU, schemas.CategoryGPU} {
		group := bundleGroup(&bundle, category)
		if len(group.Candidates) < 2 || !softConstraintMatch(spec, category, group.Candidates[0]) {
			t.Fatalf("soft preference was not ranked first: %+v", group)
		}
		fallback := false
		for _, candidate := range group.Candidates {
			fallback = fallback || !softConstraintMatch(spec, category, candidate)
		}
		if !fallback {
			t.Fatalf("soft %s brand became a hard filter", category)
		}
	}
	if embedder.calls != 0 {
		t.Fatal("deterministic brand ranking added an embedding call")
	}
	// An explicit must restores the historical hard constraint on the same value.
	spec.ConstraintStrengths["brand_pref.gpu"] = "must"
	bundle, err = planner.Prepare(context.Background(), BuildInput{Requirement: spec})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range bundleGroup(&bundle, schemas.CategoryGPU).Candidates {
		if !strings.Contains(candidate.SKU, "nvidia") {
			t.Fatalf("must brand retained a conflicting candidate: %s", candidate.SKU)
		}
	}
}

func TestSoftSizeCanUseAvailableBoardButMustSizeCannot(t *testing.T) {
	planner, _ := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, &countingEmbedder{})
	spec := fixtureRequirement()
	spec.SizePref = schemas.SizePrefITX
	spec.ConstraintStrengths = map[string]string{"size_pref": "prefer"}
	if _, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec}); err != nil {
		t.Fatalf("soft ITX preference incorrectly rejected available MATX boards: %v", err)
	}
	spec.ConstraintStrengths["size_pref"] = "must"
	if _, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec}); err == nil {
		t.Fatal("must ITX accepted a catalog with only MATX boards")
	}
}

func TestMandatoryBrandConflictWithOwnedGPURequiresClarification(t *testing.T) {
	planner, _ := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, &countingEmbedder{})
	spec := fixtureRequirement()
	spec.BrandPref.GPU = schemas.GPUBrandAMD
	spec.OwnedParts = []schemas.OwnedPart{{Category: schemas.CategoryGPU, Model: "GeForce RTX 4060", Quantity: 1}}
	spec.BudgetBasis = "new_purchase"
	spec.ConstraintStrengths = map[string]string{"brand_pref.gpu": "must"}
	_, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec})
	decision, ok := err.(*Decision)
	if !ok || decision.Kind != "clarify" || decision.Reason != "mandatory_locked_conflict" {
		t.Fatalf("owned NVIDIA card silently defeated mandatory AMD condition: %v", err)
	}
	spec.ConstraintStrengths["brand_pref.gpu"] = "prefer"
	if _, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec}); err != nil {
		t.Fatalf("soft preference should allow retaining the owned card: %v", err)
	}
}

func TestMandatoryUnverifiablePreferenceDoesNotClaimSuccessOrCallBuilder(t *testing.T) {
	for _, key := range []string{"noise_pref", "appearance", "notes"} {
		t.Run(key, func(t *testing.T) {
			model := &fakeModel{}
			harness := newTestHarness(t, model, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
				t.Fatal("unverified mandatory requirement reached final compatibility evaluation")
				return validate.Result{}, nil
			})
			spec := fixtureRequirement()
			spec.NoisePref = schemas.NoisePrefSilent
			spec.Notes = "白色海景房"
			spec.RequirementDetails = map[string]json.RawMessage{"appearance": json.RawMessage(`"白色海景房"`)}
			spec.ConstraintStrengths = map[string]string{key: "must"}
			spec.RequirementSemantics = map[string]string{key: "constraint"}
			result, err := harness.Run(context.Background(), BuildInput{Requirement: spec})
			if err != nil || result.Succeeded || result.Decision == nil || result.Decision.Reason != "requirement_evidence_missing" || len(model.requests) != 0 {
				t.Fatalf("mandatory unsupported condition escaped: %+v err=%v calls=%d", result, err, len(model.requests))
			}
			if len(result.Decision.Fields) != 1 || result.Decision.Fields[0] != key || !strings.Contains(result.Message, "尚待核验") {
				t.Fatalf("unhelpful evidence request: %+v", result.Decision)
			}
			spec.ConstraintStrengths[key] = "prefer"
			if unverifiedMandatoryRequirements(spec) != nil {
				t.Fatal("soft preference incorrectly blocked generation")
			}
		})
	}
}

func TestUsageFactsAndContextReachBuilderWithoutWeakeningStrength(t *testing.T) {
	for _, kind := range []string{"fact", "context"} {
		t.Run(kind, func(t *testing.T) {
			m := &fakeModel{outputs: []string{draftJSON("psu-a", "build-1")}}
			h := newTestHarness(t, m, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
				return passingResult("8000.00"), nil
			})
			spec := fixtureRequirement()
			spec.UseCase = schemas.UseCase{Type: schemas.UseCaseProductivity, Titles: []string{"本地工作流"}}
			spec.Notes = "白天做客户的片子，晚上跑几份本地模型；素材常从两台机器拷过来"
			spec.ConstraintStrengths = map[string]string{"notes": "must", "use_case.type": "must"}
			spec.RequirementSemantics = map[string]string{"notes": kind, "use_case.type": "fact"}
			spec.RequirementObservations = []schemas.RequirementObservation{{Field: "notes", Text: "旁边还会放一台旧设备，具体接口晚点看", Reason: "接口信息待确认",
				Source: schemas.RequirementSource{Kind: "chat", MessageID: "current-message", Quote: "旁边还会放一台旧设备，具体接口晚点看"}}}
			before, err := schemas.EncodeRequirementSpec(spec)
			if err != nil {
				t.Fatal(err)
			}
			result, err := h.Run(context.Background(), BuildInput{Requirement: spec})
			if err != nil || !result.Succeeded || len(m.requests) != 1 {
				t.Fatalf("usage information blocked selection: %+v err=%v calls=%d", result, err, len(m.requests))
			}
			var envelope promptEnvelope
			if err := json.Unmarshal([]byte(m.requests[0].Contents[0].Parts[0].Text), &envelope); err != nil {
				t.Fatal(err)
			}
			projected, err := schemas.DecodeRequirementSpec(envelope.Requirement)
			if err != nil || !reflect.DeepEqual(projected.ConstraintStrengths, spec.ConstraintStrengths) ||
				projected.Notes != spec.Notes || projected.RequirementSemantics["notes"] != kind ||
				!reflect.DeepEqual(projected.RequirementObservations, spec.RequirementObservations) {
				t.Fatalf("fact/context was omitted or softened: %+v err=%v", projected, err)
			}
			after, _ := schemas.EncodeRequirementSpec(spec)
			if string(before) != string(after) {
				t.Fatal("generation mutated the confirmed input")
			}
		})
	}
}

func TestUnclassifiedLegacyMandatoryNoteNeedsMeaningNotDowngrade(t *testing.T) {
	spec := fixtureRequirement()
	spec.Notes = "必须有两个雷电接口，接现有的两块采集设备"
	spec.UseCase.Titles = []string{"视频采集"}
	spec.ConstraintStrengths = map[string]string{"notes": "must"}
	d := unverifiedMandatoryRequirements(spec)
	if d == nil || d.Kind != "clarify" || d.Reason != "requirement_semantics_missing" ||
		!reflect.DeepEqual(d.Fields, []string{"notes"}) || !strings.Contains(d.Message, spec.Notes) || strings.Contains(d.Message, "尽量满足") {
		t.Fatalf("legacy unknown must silently softened or misclassified: %+v", d)
	}
	// A lexical resemblance to a workload is insufficient evidence for ignoring a condition.
	spec.Notes = "视频采集，但必须有两个雷电接口"
	if d := unverifiedMandatoryRequirements(spec); d == nil || d.Reason != "requirement_semantics_missing" {
		t.Fatalf("partial workload overlap defeated legacy protection: %+v", d)
	}
	// Explicit classification remains authoritative even if a title duplicates the text.
	spec.RequirementSemantics = map[string]string{"notes": "constraint"}
	spec.UseCase.Titles = []string{spec.Notes}
	d = unverifiedMandatoryRequirements(spec)
	if d == nil || d.Reason != "requirement_evidence_missing" || !strings.Contains(d.Message, spec.Notes) {
		t.Fatalf("a real hard condition disappeared through title matching: %+v", d)
	}
}

func TestSavedVideoEditingFailureRequirementReachesConfiguration(t *testing.T) {
	raw, err := os.ReadFile("testdata/video_editing_failure_requirement.json")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := schemas.DecodeRequirementSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if spec.ConstraintStrengths["notes"] != "must" || spec.RequirementSemantics["notes"] != "" || spec.ConstraintStrengths["budget_cny"] != "prefer" {
		t.Fatal("saved failure fixture no longer exercises the original mandatory note and budget edit")
	}
	m := &fakeModel{outputs: []string{draftJSON("psu-a", "build-video-offline")}}
	h := newTestHarness(t, m, func(context.Context, schemas.BuildSelection) (validate.Result, error) {
		return passingResult("6000.00"), nil
	})
	result, err := h.Run(context.Background(), BuildInput{Requirement: spec})
	if err != nil || !result.Succeeded || result.Attempts != 1 || result.Result.Report.OverallStatus != schemas.OverallPass {
		t.Fatalf("saved 4K editing failure still blocks configuration: %+v err=%v", result, err)
	}
	var envelope promptEnvelope
	if err := json.Unmarshal([]byte(m.requests[0].Contents[0].Parts[0].Text), &envelope); err != nil {
		t.Fatal(err)
	}
	forwarded, err := schemas.DecodeRequirementSpec(envelope.Requirement)
	if err != nil || forwarded.Notes != "剪4K视频" || forwarded.UseCase.Type != schemas.UseCaseProductivity ||
		!reflect.DeepEqual(forwarded.UseCase.Titles, []string{"剪4K视频"}) || forwarded.NoisePref != schemas.NoisePrefSilent ||
		forwarded.ConstraintStrengths["notes"] != "must" || forwarded.ConstraintStrengths["budget_cny"] != "prefer" {
		t.Fatalf("saved failure requirements did not reach generation intact: %+v err=%v", forwarded, err)
	}
	if envelope.BudgetWindow.FlexSource != "execution_default" || envelope.BudgetWindow.TargetCNY != "6000.00" ||
		envelope.BudgetWindow.UpperCNY != "6600.00" || forwarded.ConstraintStrengths["budget_flex"] != "" {
		t.Fatalf("default budget flex was passed off as a user preference: %+v", envelope.BudgetWindow)
	}
}

func TestBudgetWindowPreservesExecutionLimitAndPreferenceProvenance(t *testing.T) {
	spec := fixtureRequirement()
	if got := requirementBudgetWindow(spec); got.FlexSource != "legacy_unspecified" {
		t.Fatalf("legacy source invented: %+v", got)
	}
	spec.ConstraintStrengths = map[string]string{"budget_cny": "prefer"}
	if got := requirementBudgetWindow(spec); got.FlexSource != "execution_default" || got.LowerCNY != "7200.00" || got.UpperCNY != "8800.00" {
		t.Fatalf("soft budget changed execution limits or source: %+v", got)
	}
	if inBudgetWindow(spec, testQuote("8800.01")) {
		t.Fatal("soft budget silently removed execution bound")
	}
	spec.ConstraintStrengths["budget_flex"] = "must"
	if got := requirementBudgetWindow(spec); got.FlexSource != "user_requirement" {
		t.Fatalf("explicit flex lost its provenance: %+v", got)
	}
}

func TestMandatoryCandidateShortageIsSpecificBusinessResultBeforeModel(t *testing.T) {
	for _, field := range []string{"size_pref", "brand_pref.cpu", "brand_pref.gpu"} {
		t.Run(field, func(t *testing.T) {
			catalog := fixtureCatalog()
			spec := fixtureRequirement()
			spec.UseCase = schemas.UseCase{Type: schemas.UseCaseProductivity}
			spec.ConstraintStrengths = map[string]string{field: "must"}
			switch field {
			case "size_pref":
				spec.SizePref = schemas.SizePrefITX
			case "brand_pref.cpu", "brand_pref.gpu":
				spec.BrandPref = schemas.BrandPref{CPU: schemas.CPUBrandIntel, GPU: schemas.GPUBrandNvidia}
				var candidates []store.Candidate
				for _, c := range catalog.Candidates {
					if field == "brand_pref.cpu" && c.Category == schemas.CategoryCPU && cpuVendor(c) == "intel" ||
						field == "brand_pref.gpu" && c.Category == schemas.CategoryGPU && gpuFamily(c) == "nvidia" {
						continue
					}
					candidates = append(candidates, c)
				}
				catalog.Candidates = candidates
			}
			planner, err := NewCandidatePlanner(&fakeCatalogSource{catalog: catalog}, &countingEmbedder{})
			if err != nil {
				t.Fatal(err)
			}
			m := &fakeModel{}
			h, err := New(Config{Model: m, Planner: planner, Repairer: NewRepairPlanner(), Eval: evalFunc(func(context.Context, schemas.BuildSelection) (validate.Result, error) {
				t.Fatal("missing hard candidates reached evaluation")
				return validate.Result{}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			result, err := h.Run(context.Background(), BuildInput{Requirement: spec})
			if err != nil || result.Succeeded || len(m.requests) != 0 || result.Decision == nil ||
				result.Decision.Kind != "data_unavailable" || result.Decision.Reason != "required_candidates_missing" ||
				!reflect.DeepEqual(result.Decision.Fields, []string{field}) {
				t.Fatalf("hard requirement became a service error or disappeared: %+v err=%v calls=%d", result, err, len(m.requests))
			}
		})
	}
}

func TestMandatoryGPUCannotBeOmittedFromProductivityConfiguration(t *testing.T) {
	spec := fixtureRequirement()
	spec.UseCase = schemas.UseCase{Type: schemas.UseCaseProductivity}
	spec.BrandPref.GPU = schemas.GPUBrandNvidia
	spec.ConstraintStrengths = map[string]string{"brand_pref.gpu": "must"}
	omitted := strings.Replace(draftJSON("psu-a", "build-1"), `"gpu":"gpu-a"`, `"gpu":null`, 1)
	m := &fakeModel{outputs: []string{omitted, draftJSON("psu-a", "build-2")}}
	evaluated := 0
	h := newTestHarness(t, m, func(_ context.Context, selection schemas.BuildSelection) (validate.Result, error) {
		evaluated++
		if selection.GPU == nil {
			t.Fatal("missing mandatory GPU reached compatibility pass")
		}
		return passingResult("8000.00"), nil
	})
	result, err := h.Run(context.Background(), BuildInput{Requirement: spec})
	if err != nil || !result.Succeeded || result.Attempts != 2 || evaluated != 1 {
		t.Fatalf("mandatory GPU omission was not repaired: %+v err=%v evaluations=%d", result, err, evaluated)
	}
	base := testDraft("psu-a").Selection
	base.GPU = nil
	planner, _ := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, &countingEmbedder{})
	_, err = planner.Prepare(context.Background(), BuildInput{Requirement: spec, BaseSelection: &base, Locked: []schemas.Category{schemas.CategoryGPU}})
	d, ok := err.(*Decision)
	if !ok || d.Reason != "mandatory_locked_conflict" || !reflect.DeepEqual(d.Fields, []string{"brand_pref.gpu"}) {
		t.Fatalf("locked GPU omission bypassed requirement: %v", err)
	}
}

func TestBudgetRepairCannotDropMandatoryGPU(t *testing.T) {
	for _, strength := range []string{"must", "prefer"} {
		t.Run(strength, func(t *testing.T) {
			current := testDraft("psu-a").Selection
			spec := fixtureRequirement()
			spec.UseCase = schemas.UseCase{Type: schemas.UseCaseProductivity}
			spec.BudgetCNY, spec.BudgetFlex = 7000, 0
			spec.BrandPref.GPU = schemas.GPUBrandNvidia
			spec.ConstraintStrengths = map[string]string{"brand_pref.gpu": strength}
			input := BuildInput{Requirement: spec, BaseSelection: &current}
			for _, category := range schemas.AllCategories {
				if category != schemas.CategoryGPU {
					input.Locked = append(input.Locked, category)
				}
			}
			h := runner{eval: pricedEvaluator(2)}
			plan, _, err := h.budgetRepair(context.Background(), input, current, testQuote("8000.00"), testBundle(), RepairPlan{Reason: "budget_over"})
			if err != nil || plan.DropGPU != (strength == "prefer") {
				t.Fatalf("budget repair mishandled GPU strength: %+v err=%v", plan, err)
			}
		})
	}
}

type capturedPreferenceEmbedder struct{ queries []string }

func (e *capturedPreferenceEmbedder) EmbedOne(_ context.Context, text string) ([]float32, error) {
	e.queries = append(e.queries, text)
	return make([]float32, store.EmbeddingDims), nil
}

func TestAppearanceDetailsDoNotTurnLegacyWorkloadIntoMandatoryProperty(t *testing.T) {
	for _, appearance := range []string{"白色", "像一件不抢眼的录音室器材"} {
		t.Run(appearance, func(t *testing.T) {
			state := schemas.NewRequirementState()
			for field, value := range map[string]string{
				"budget_cny": `6000`, "use_case.type": `"productivity"`, "use_case.titles": `["剪4K视频"]`, "notes": `"剪4K视频"`,
			} {
				state.Fields[field] = schemas.RequirementField{Status: "active", Value: json.RawMessage(value), Strength: "must", Scope: "session"}
			}
			state.Fields["noise_pref"] = schemas.RequirementField{Status: "active", Value: json.RawMessage(`"silent"`), Strength: "prefer", Scope: "session"}
			state.Fields["appearance"] = schemas.RequirementField{Status: "active", Value: rawJSON(appearance), Strength: "prefer", Kind: "constraint", Scope: "session"}
			raw, missing, err := schemas.RequirementStateSpec(state)
			if err != nil || len(missing) > 0 {
				t.Fatalf("projecting historical workload failed: %v missing=%v", err, missing)
			}
			spec, err := schemas.DecodeRequirementSpec(raw)
			if err != nil || spec.Notes != "剪4K视频" || appearanceRequirementText(spec) != appearance || spec.ConstraintStrengths["notes"] != "must" {
				t.Fatalf("appearance contaminated workload or weakened its strength: %+v err=%v", spec, err)
			}
			e := &capturedPreferenceEmbedder{}
			planner, err := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, e)
			if err != nil {
				t.Fatal(err)
			}
			output := strings.NewReplacer(`"cpu-a"`, `"cpu-amd-a"`, `"gpu-a"`, `"gpu-nvidia"`, `"motherboard-a"`, `"mb-am5"`,
				`"memory-a"`, `"mem"`, `"ssd-a"`, `"ssd"`, `"psu-a"`, `"psu"`, `"case-a"`, `"case"`, `"cooler-a"`, `"cooler"`).Replace(draftJSON("psu-a", "appearance-offline"))
			m := &fakeModel{outputs: []string{output}}
			h, err := New(Config{Model: m, Planner: planner, Repairer: NewRepairPlanner(), Eval: evalFunc(func(context.Context, schemas.BuildSelection) (validate.Result, error) {
				return passingResult("6400.00"), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			result, err := h.Run(context.Background(), BuildInput{Requirement: spec})
			if err != nil || !result.Succeeded || len(m.requests) != 1 || len(e.queries) != 1 ||
				!strings.Contains(e.queries[0], appearance) || !strings.Contains(e.queries[0], "安静") {
				t.Fatalf("workload or appearance lost, blocked, or added retrieval calls: %+v err=%v model=%d queries=%v", result, err, len(m.requests), e.queries)
			}
			if appearance == "白色" && !strings.Contains(result.Draft.Rationale["case"], "尚未核验") {
				t.Fatalf("typed appearance lost evidence warning: %+v", result.Draft.Rationale)
			}
		})
	}
}
