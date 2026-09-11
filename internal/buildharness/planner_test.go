package buildharness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestPlannerPreservesCheapIGPUAndKnownCoolerOutsideQuantiles(t *testing.T) {
	catalog := fixtureCatalog()
	var candidates []store.Candidate
	for _, candidate := range catalog.Candidates {
		if candidate.Category == schemas.CategoryCooler {
			continue
		}
		if candidate.Category == schemas.CategoryCPU && strings.HasPrefix(candidate.SKU, "cpu-amd-") {
			var specs map[string]any
			if err := json.Unmarshal(candidate.Specs, &specs); err != nil {
				t.Fatal(err)
			}
			specs["has_igpu"] = candidate.SKU == "cpu-amd-c"
			candidate.Specs = rawJSON(specs)
		}
		candidates = append(candidates, candidate)
	}
	for i := 0; i < 7; i++ {
		price := fmt.Sprintf("%d.00", 100+i*10)
		specs := map[string]any{"type": "air", "height_mm": 150}
		if i == 2 {
			specs["cooling_capacity_w"] = 220
		}
		candidates = append(candidates, store.Candidate{SKU: fmt.Sprintf("cooler-%d", i), Category: schemas.CategoryCooler, PriceCNY: &price, Specs: rawJSON(specs)})
	}
	catalog.Candidates = candidates
	planner, err := NewCandidatePlanner(&fakeCatalogSource{catalog: catalog}, &countingEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	requirement := fixtureRequirement()
	requirement.UseCase.Type = schemas.UseCaseGeneral
	bundle, err := planner.Prepare(context.Background(), BuildInput{Requirement: requirement})
	if err != nil {
		t.Fatal(err)
	}
	for category, want := range map[schemas.Category]string{schemas.CategoryCPU: "cpu-amd-c", schemas.CategoryCooler: "cooler-2"} {
		found := false
		for _, candidate := range bundleGroup(&bundle, category).Candidates {
			if candidate.SKU == want {
				found = candidate.protected
			}
		}
		if !found {
			t.Fatalf("lost protected feasible path %s", want)
		}
	}
}

type fakeCatalogSource struct {
	catalog  store.CatalogSnapshot
	semantic store.SemanticResult
}

func TestPlannerKeepsEligibleBaseWithoutDefeatingSwapBrandFilter(t *testing.T) {
	planner, err := NewCandidatePlanner(&fakeCatalogSource{catalog: fixtureCatalog()}, &countingEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	gpu := "gpu-nvidia"
	base := schemas.BuildSelection{CPU: "cpu-amd-c", Motherboard: "mb-am5", Memory: "mem", GPU: &gpu, SSDs: []schemas.SSDSelection{{SKU: "ssd", Quantity: 1}}, PSU: "psu", Case: "case", Cooler: "cooler"}
	input := BuildInput{Requirement: fixtureRequirement(), BaseSelection: &base}
	bundle, err := planner.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range schemas.AllCategories {
		for _, sku := range selectionSKUs(base, category) {
			found := false
			for _, candidate := range bundleGroup(&bundle, category).Candidates {
				if candidate.SKU == sku {
					found = candidate.protected
				}
			}
			if !found {
				t.Fatalf("base %s lost or unprotected", sku)
			}
		}
	}
	input.Change = &schemas.ChangeRequest{Intent: schemas.IntentSwapPart, Swap: &schemas.SwapSpec{Category: schemas.CategoryGPU, TargetHint: "换 AMD 显卡"}}
	bundle, err = planner.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range bundleGroup(&bundle, schemas.CategoryGPU).Candidates {
		if candidate.SKU == gpu {
			t.Fatal("base retention defeated AMD swap constraint")
		}
	}
}

func (f *fakeCatalogSource) ActiveCatalogSnapshot(context.Context) (store.CatalogSnapshot, error) {
	return f.catalog, nil
}

func (f *fakeCatalogSource) SemanticCandidates(context.Context, store.SemanticQuery) (store.SemanticResult, error) {
	return f.semantic, nil
}

type countingEmbedder struct{ calls int }

func (e *countingEmbedder) EmbedOne(context.Context, string) ([]float32, error) {
	e.calls++
	return make([]float32, store.EmbeddingDims), nil
}

func TestParseMode(t *testing.T) {
	for input, want := range map[string]Mode{"": ModePlanning, "planning": ModePlanning, "legacy": ModeLegacy, " V2 ": ModeV2} {
		got, err := ParseMode(input)
		if err != nil || got != want {
			t.Fatalf("ParseMode(%q)=(%q,%v), want %q", input, got, err, want)
		}
	}
	if _, err := ParseMode("shadow"); err == nil {
		t.Fatal("非法 mode 应失败")
	}
}

func TestPlannerBuildsPlatformLanesWithoutUnneededEmbedding(t *testing.T) {
	source := &fakeCatalogSource{catalog: fixtureCatalog()}
	embedder := &countingEmbedder{}
	planner, err := NewCandidatePlanner(source, embedder)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := planner.Prepare(context.Background(), BuildInput{Requirement: fixtureRequirement()})
	if err != nil {
		t.Fatal(err)
	}
	if embedder.calls != 0 {
		t.Fatalf("无软偏好不应调用 embedding，得到 %d", embedder.calls)
	}
	if bundle.SnapshotDate != "2026-08-27" {
		t.Fatalf("snapshot=%q", bundle.SnapshotDate)
	}
	if got := len(bundleGroup(&bundle, schemas.CategoryCPU).Candidates); got != 6 {
		t.Fatalf("CPU 应为 Intel/AMD 各 3 个，得到 %d", got)
	}
	boards := bundleGroup(&bundle, schemas.CategoryMotherboard).Candidates
	seenSocket := map[string]bool{}
	for _, board := range boards {
		seenSocket[board.Specs["socket"].(string)] = true
	}
	if !seenSocket["AM5"] || !seenSocket["LGA1700"] {
		t.Fatalf("主板平台通道不完整:%v", seenSocket)
	}
	encoded, _ := json.Marshal(bundle)
	if strings.Contains(string(encoded), "internal_field") {
		t.Fatal("候选包泄漏了非白名单 specs")
	}
}

func TestPlannerGPUFamilyAndSoftPreference(t *testing.T) {
	catalog := fixtureCatalog()
	semanticGPU := catalog.Candidates[8]
	source := &fakeCatalogSource{catalog: catalog, semantic: store.SemanticResult{Candidates: []store.SemanticCandidate{{
		Candidate: semanticGPU, MatchText: "白色外观 安静低噪", Similarity: 0.9,
	}}}}
	embedder := &countingEmbedder{}
	planner, _ := NewCandidatePlanner(source, embedder)
	spec := fixtureRequirement()
	spec.BrandPref.GPU = schemas.GPUBrandAMD
	spec.NoisePref = schemas.NoisePrefSilent
	bundle, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec})
	if err != nil {
		t.Fatal(err)
	}
	if embedder.calls != 1 {
		t.Fatalf("软偏好应只调用一次 embedding，得到 %d", embedder.calls)
	}
	for _, candidate := range bundleGroup(&bundle, schemas.CategoryGPU).Candidates {
		if gpuFamily(store.Candidate{SKU: candidate.SKU, Brand: candidate.Brand, Model: candidate.Model}) != "amd" {
			t.Fatalf("AMD 偏好混入其他阵营:%+v", candidate)
		}
	}
}

func TestPlannerSwapHintOverridesOldGPUPreference(t *testing.T) {
	source := &fakeCatalogSource{catalog: fixtureCatalog()}
	planner, _ := NewCandidatePlanner(source, &countingEmbedder{})
	spec := fixtureRequirement()
	spec.BrandPref.GPU = schemas.GPUBrandNvidia
	change := &schemas.ChangeRequest{Intent: schemas.IntentSwapPart,
		Swap: &schemas.SwapSpec{Category: schemas.CategoryGPU, TargetHint: "换成 A 卡"}}
	bundle, err := planner.Prepare(context.Background(), BuildInput{Requirement: spec, Change: change})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range bundleGroup(&bundle, schemas.CategoryGPU).Candidates {
		if gpuFamily(store.Candidate{SKU: candidate.SKU, Brand: candidate.Brand, Model: candidate.Model}) != "amd" {
			t.Fatalf("A 卡改单仍混入非 AMD 候选:%+v", candidate)
		}
	}
}

func TestTrimBundleRemovesSemanticBeforeCore(t *testing.T) {
	price := "100.00"
	bundle := CandidateBundle{SchemaVersion: 1, Groups: []CandidateGroup{{Category: schemas.CategoryCPU}}}
	bundle.Groups[0].Candidates = append(bundle.Groups[0].Candidates, Candidate{
		SKU: "protected", Model: "core", PriceCNY: &price, Specs: map[string]any{}, origin: originCore, protected: true,
	})
	for index := 0; index < 8; index++ {
		bundle.Groups[0].Candidates = append(bundle.Groups[0].Candidates, Candidate{
			SKU: "semantic-" + strings.Repeat("x", 4_000), Model: "soft", PriceCNY: &price,
			Specs: map[string]any{}, MatchText: strings.Repeat("白", 4_000), origin: originSemantic,
		})
	}
	if err := trimBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Trimmed == 0 || bundle.Groups[0].Candidates[0].SKU != "protected" {
		t.Fatalf("裁剪未优先保留 core:%+v", bundle)
	}
}

func TestPlannerPostgreSQLCatalogIntegration(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN 未设置，跳过真实目录只读集成测试")
	}
	data, err := store.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("PG_TEST_DSN 已设置但不可达:%v", err)
	}
	defer data.Close()
	planner, err := NewCandidatePlanner(data, &countingEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := planner.Prepare(context.Background(), BuildInput{Requirement: fixtureRequirement()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Groups) != len(schemas.AllCategories) || len(encoded) == 0 {
		t.Fatalf("真实候选包不完整:groups=%d bytes=%d", len(bundle.Groups), len(encoded))
	}
	for _, group := range bundle.Groups {
		if group.Category != schemas.CategoryGPU && len(group.Candidates) == 0 {
			t.Fatalf("真实目录品类 %s 无候选", group.Category)
		}
	}
}

func fixtureRequirement() schemas.RequirementSpec {
	return schemas.RequirementSpec{
		SchemaVersion: 1, BudgetCNY: 8000, BudgetFlex: 0.1,
		UseCase:  schemas.UseCase{Type: schemas.UseCaseGaming, Resolution: schemas.Resolution2K},
		SizePref: schemas.SizePrefAny, NoisePref: schemas.NoisePrefAny,
		BrandPref: schemas.BrandPref{CPU: schemas.CPUBrandAny, GPU: schemas.GPUBrandAny},
	}
}

func fixtureCatalog() store.CatalogSnapshot {
	var candidates []store.Candidate
	price := func(value string) *string { return &value }
	part := func(sku, brand, model string, category schemas.Category, amount string, specs string) store.Candidate {
		return store.Candidate{SKU: sku, Brand: brand, Model: model, Category: category,
			PriceCNY: price(amount), Specs: json.RawMessage(specs)}
	}
	for index, amount := range []string{"600.00", "900.00", "1300.00", "1700.00"} {
		candidates = append(candidates, part("cpu-amd-"+string(rune('a'+index)), "AMD", "Ryzen", schemas.CategoryCPU, amount,
			`{"socket":"AM5","supported_chipsets":["B650"],"has_igpu":true,"tdp_w":65,"internal_field":"x"}`))
		candidates = append(candidates, part("cpu-intel-"+string(rune('a'+index)), "Intel", "Core i5", schemas.CategoryCPU, amount,
			`{"socket":"LGA1700","supported_chipsets":["B760"],"has_igpu":true,"tdp_w":65}`))
	}
	candidates = append(candidates,
		part("gpu-amd", "Sapphire", "Radeon RX 7600", schemas.CategoryGPU, "2000.00", `{"length_mm":240,"tdp_w":180,"power_connectors":["pcie_8pin"]}`),
		part("gpu-nvidia", "MSI", "GeForce RTX 4060", schemas.CategoryGPU, "2200.00", `{"length_mm":240,"tdp_w":120,"power_connectors":["pcie_8pin"]}`),
		part("gpu-intel", "Intel", "Arc B580", schemas.CategoryGPU, "2100.00", `{"length_mm":250,"tdp_w":190,"power_connectors":["pcie_8pin"]}`),
	)
	for _, item := range []struct{ socket, chipset, sku string }{{"AM5", "B650", "mb-am5"}, {"LGA1700", "B760", "mb-intel"}} {
		candidates = append(candidates, part(item.sku, "MSI", item.chipset, schemas.CategoryMotherboard, "1000.00",
			`{"socket":"`+item.socket+`","chipset":"`+item.chipset+`","memory_generation":"ddr5","memory_speed_max_mts":6000,"form_factor":"matx","m2_slots":2}`))
	}
	candidates = append(candidates,
		part("mem", "Kingston", "DDR5 6000", schemas.CategoryMemory, "700.00", `{"generation":"ddr5","speed_mts":6000}`),
		part("ssd", "WD", "SN770", schemas.CategorySSD, "500.00", `{"form_factor":"m2"}`),
		part("psu", "MSI", "750W", schemas.CategoryPSU, "600.00", `{"wattage_w":750,"power_connectors":["pcie_8pin","pcie_16pin"]}`),
		part("case", "LianLi", "A3", schemas.CategoryCase, "500.00", `{"gpu_length_max_mm":350,"cooler_height_max_mm":170,"supported_form_factors":["atx","matx","itx"],"radiator_sizes_mm":[240,360]}`),
		part("cooler", "Thermalright", "PA120", schemas.CategoryCooler, "300.00", `{"type":"air","height_mm":155,"cooling_capacity_w":220}`),
	)
	return store.CatalogSnapshot{Snapshot: store.Snapshot{ID: 1, SnapshotDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}, Candidates: candidates}
}
