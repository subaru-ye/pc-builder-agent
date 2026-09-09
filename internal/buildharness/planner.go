package buildharness

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const (
	originCore     = "core"
	originSemantic = "semantic"
)

// Planner 确定性准备 Candidate Bundle，不调用 chat model。
type Planner struct {
	source          CatalogSource
	embedder        QueryEmbedder
	disableSemantic bool
}

type PlannerOptions struct{ DisableSemantic bool }

func NewCandidatePlanner(source CatalogSource, embedder QueryEmbedder) (*Planner, error) {
	return NewCandidatePlannerWithOptions(source, embedder, PlannerOptions{})
}

func NewCandidatePlannerWithOptions(source CatalogSource, embedder QueryEmbedder, options PlannerOptions) (*Planner, error) {
	if source == nil {
		return nil, fmt.Errorf("buildharness: CatalogSource 不能为空")
	}
	if embedder == nil && !options.DisableSemantic {
		return nil, fmt.Errorf("buildharness: QueryEmbedder 不能为空")
	}
	return &Planner{source: source, embedder: embedder, disableSemantic: options.DisableSemantic}, nil
}

func (p *Planner) Prepare(ctx context.Context, input BuildInput) (CandidateBundle, error) {
	catalog, err := p.source.ActiveCatalogSnapshot(ctx)
	if err != nil {
		return CandidateBundle{}, fmt.Errorf("buildharness: 读取候选快照失败: %w", err)
	}
	if d := AssessCatalog(input, catalog); d != nil {
		return CandidateBundle{}, d
	}
	input, _ = bindOwned(input, catalog.Candidates)
	byCategory := make(map[schemas.Category][]store.Candidate, len(schemas.AllCategories))
	bySKU := make(map[string]store.Candidate, len(catalog.Candidates))
	for _, candidate := range catalog.Candidates {
		byCategory[candidate.Category] = append(byCategory[candidate.Category], candidate)
		bySKU[candidate.SKU] = candidate
	}

	locked := categorySet(input.Locked)
	selected := make(map[schemas.Category][]store.Candidate, len(schemas.AllCategories))
	eligible := make(map[schemas.Category][]store.Candidate, len(schemas.AllCategories))
	retainedBase := map[string]bool{}
	protectedCooler := ""
	// 改单必须允许沿用仍满足本轮硬约束的基版本零件,否则裁剪会制造额外改动。
	retainBase := func(category schemas.Category) {
		if input.BaseSelection == nil || locked[category] {
			return
		}
		for _, sku := range selectionSKUs(*input.BaseSelection, category) {
			for _, candidate := range eligible[category] {
				if candidate.SKU == sku {
					selected[category] = appendCandidateOnce(selected[category], candidate)
					retainedBase[sku] = true
					break
				}
			}
		}
	}

	// 锁定项先固定；GPU 显式为 null 时没有候选行，但仍由 prompt 固定为 null。
	for category := range locked {
		if input.BaseSelection == nil {
			return CandidateBundle{}, fmt.Errorf("buildharness: 品类 %s 已锁定但缺少基版本 selection", category)
		}
		for _, sku := range selectionSKUs(*input.BaseSelection, category) {
			candidate, ok := bySKU[sku]
			if !ok || candidate.Category != category {
				return CandidateBundle{}, fmt.Errorf("buildharness: 锁定 SKU %q 不属于当前 active_core 候选", sku)
			}
			selected[category] = append(selected[category], candidate)
			eligible[category] = append(eligible[category], candidate)
		}
	}

	if !locked[schemas.CategoryCPU] {
		cpuPreference := input.Requirement.BrandPref.CPU
		if hinted := swapCPUPreference(input.Change); hinted != schemas.CPUBrandAny {
			cpuPreference = hinted
		}
		cpus := filterCPU(byCategory[schemas.CategoryCPU], cpuPreference)
		eligible[schemas.CategoryCPU] = cpus
		selected[schemas.CategoryCPU] = selectByLane(cpus, cpuVendor, 3)
		// 非游戏整机需保留各厂商最便宜的核显路径,避免分位抽样只留下贵核显。
		if input.Requirement.UseCase.Type != schemas.UseCaseGaming {
			seen := map[string]bool{}
			for _, cpu := range cpus {
				if candidateHasField(cpu, "has_igpu", true) && !seen[cpuVendor(cpu)] {
					selected[schemas.CategoryCPU] = appendCandidateOnce(selected[schemas.CategoryCPU], cpu)
					seen[cpuVendor(cpu)] = true
				}
			}
		}
	}
	retainBase(schemas.CategoryCPU)
	if len(selected[schemas.CategoryCPU]) == 0 {
		return CandidateBundle{}, fmt.Errorf("buildharness: CPU 硬约束下没有候选")
	}

	if !locked[schemas.CategoryGPU] {
		gpuPreference := input.Requirement.BrandPref.GPU
		if hinted := swapGPUPreference(input.Change); hinted != schemas.GPUBrandAny {
			gpuPreference = hinted
		}
		gpus := filterGPU(byCategory[schemas.CategoryGPU], gpuPreference)
		eligible[schemas.CategoryGPU] = gpus
		selected[schemas.CategoryGPU] = selectByLane(gpus, gpuFamily, 3)
	}
	retainBase(schemas.CategoryGPU)
	if input.Requirement.UseCase.Type == schemas.UseCaseGaming && len(selected[schemas.CategoryGPU]) == 0 {
		return CandidateBundle{}, fmt.Errorf("buildharness: 游戏需求的 GPU 硬约束下没有候选")
	}

	if !locked[schemas.CategoryMotherboard] {
		boards := filterMotherboards(byCategory[schemas.CategoryMotherboard], selected[schemas.CategoryCPU], input.Requirement.SizePref)
		eligible[schemas.CategoryMotherboard] = boards
		selected[schemas.CategoryMotherboard] = selectByLane(boards, candidateSocket, 2)
	}
	retainBase(schemas.CategoryMotherboard)
	if len(selected[schemas.CategoryMotherboard]) == 0 {
		return CandidateBundle{}, fmt.Errorf("buildharness: 主板平台约束下没有候选")
	}

	if !locked[schemas.CategoryMemory] {
		memory := filterMemory(byCategory[schemas.CategoryMemory], selected[schemas.CategoryMotherboard])
		eligible[schemas.CategoryMemory] = memory
		selected[schemas.CategoryMemory] = selectByLane(memory, func(c store.Candidate) string {
			var specs struct {
				Generation string `json:"generation"`
			}
			_ = json.Unmarshal(c.Specs, &specs)
			return specs.Generation
		}, 3)
	}
	retainBase(schemas.CategoryMemory)

	for _, category := range []schemas.Category{
		schemas.CategorySSD, schemas.CategoryPSU, schemas.CategoryCase, schemas.CategoryCooler,
	} {
		if locked[category] {
			continue
		}
		items := byCategory[category]
		if category == schemas.CategoryCase {
			items = filterCases(items, input.Requirement.SizePref)
		}
		eligible[category] = items
		selected[category] = selectQuantiles(items, 5)
		if category == schemas.CategoryCooler {
			for _, item := range items {
				var specs map[string]any
				if json.Unmarshal(item.Specs, &specs) == nil && specs["cooling_capacity_w"] != nil {
					selected[category] = appendCandidateOnce(selected[category], item)
					protectedCooler = item.SKU
					break
				}
			}
		}
		retainBase(category)
	}

	for _, category := range schemas.AllCategories {
		if category == schemas.CategoryGPU && input.Requirement.UseCase.Type != schemas.UseCaseGaming {
			continue
		}
		if len(selected[category]) == 0 {
			return CandidateBundle{}, fmt.Errorf("buildharness: 品类 %s 没有可用候选", category)
		}
	}

	bundle := CandidateBundle{OwnedInput: &input, SchemaVersion: 1, SnapshotDate: catalog.Snapshot.SnapshotDate.Format("2006-01-02")}
	for _, category := range schemas.AllCategories {
		group := CandidateGroup{Category: category}
		protectedLanes := map[string]bool{}
		for i, candidate := range selected[category] {
			view, err := candidateView(candidate, "")
			if err != nil {
				return CandidateBundle{}, err
			}
			view.origin = originCore
			lane := plannerLane(category, candidate)
			view.protected = i == 0 || locked[category] || retainedBase[candidate.SKU] || (lane != "" && !protectedLanes[lane]) ||
				(category == schemas.CategoryCPU && candidateHasField(candidate, "has_igpu", true)) ||
				(category == schemas.CategoryCooler && candidate.SKU == protectedCooler)
			protectedLanes[lane] = true
			group.Candidates = append(group.Candidates, view)
		}
		bundle.Groups = append(bundle.Groups, group)
	}

	if softQuery := softPreferenceQuery(input.Requirement); softQuery != "" && !p.disableSemantic {
		if err := p.enrichSemantic(ctx, softQuery, eligible, &bundle); err != nil {
			return CandidateBundle{}, err
		}
	}
	if err := trimBundle(&bundle); err != nil {
		return CandidateBundle{}, err
	}
	return bundle, nil
}

func appendCandidateOnce(items []store.Candidate, candidate store.Candidate) []store.Candidate {
	for _, item := range items {
		if item.SKU == candidate.SKU {
			return items
		}
	}
	return append(items, candidate)
}

func candidateHasField(candidate store.Candidate, field string, value any) bool {
	var specs map[string]any
	return json.Unmarshal(candidate.Specs, &specs) == nil && specs[field] == value
}

func swapCPUPreference(change *schemas.ChangeRequest) schemas.CPUBrand {
	if change == nil || change.Intent != schemas.IntentSwapPart || change.Swap == nil || change.Swap.Category != schemas.CategoryCPU {
		return schemas.CPUBrandAny
	}
	text := strings.ToUpper(change.Swap.TargetHint)
	if strings.Contains(text, "INTEL") || strings.Contains(text, "英特尔") {
		return schemas.CPUBrandIntel
	}
	if strings.Contains(text, "AMD") || strings.Contains(text, "锐龙") {
		return schemas.CPUBrandAMD
	}
	return schemas.CPUBrandAny
}

func swapGPUPreference(change *schemas.ChangeRequest) schemas.GPUBrand {
	if change == nil || change.Intent != schemas.IntentSwapPart || change.Swap == nil || change.Swap.Category != schemas.CategoryGPU {
		return schemas.GPUBrandAny
	}
	text := strings.ToUpper(strings.TrimSpace(change.Swap.TargetHint))
	if strings.Contains(text, "NVIDIA") || strings.Contains(text, "英伟达") || strings.Contains(text, "N卡") || strings.Contains(text, "N 卡") {
		return schemas.GPUBrandNvidia
	}
	if strings.Contains(text, "AMD") || strings.Contains(text, "RADEON") || strings.Contains(text, "A卡") || strings.Contains(text, "A 卡") {
		return schemas.GPUBrandAMD
	}
	return schemas.GPUBrandAny
}

func plannerLane(category schemas.Category, candidate store.Candidate) string {
	switch category {
	case schemas.CategoryCPU:
		return cpuVendor(candidate)
	case schemas.CategoryGPU:
		return gpuFamily(candidate)
	case schemas.CategoryMotherboard:
		return candidateSocket(candidate)
	case schemas.CategoryMemory:
		var specs struct {
			Generation string `json:"generation"`
		}
		_ = json.Unmarshal(candidate.Specs, &specs)
		return specs.Generation
	default:
		return ""
	}
}

func (p *Planner) enrichSemantic(ctx context.Context, query string, eligible map[schemas.Category][]store.Candidate, bundle *CandidateBundle) error {
	vector, err := p.embedder.EmbedOne(ctx, query)
	if err != nil {
		return fmt.Errorf("buildharness: 软偏好向量化失败: %w", err)
	}
	result, err := p.source.SemanticCandidates(ctx, store.SemanticQuery{QueryEmbedding: vector, TopN: 32})
	if err != nil {
		return fmt.Errorf("buildharness: 软偏好检索失败: %w", err)
	}
	allowed := make(map[schemas.Category]map[string]store.Candidate, len(eligible))
	for category, items := range eligible {
		allowed[category] = make(map[string]store.Candidate, len(items))
		for _, item := range items {
			allowed[category][item.SKU] = item
		}
	}
	added := make(map[schemas.Category]int)
	for _, semantic := range result.Candidates {
		candidate, ok := allowed[semantic.Category][semantic.SKU]
		if !ok || added[semantic.Category] >= 2 {
			continue
		}
		group := bundleGroup(bundle, semantic.Category)
		if group == nil {
			continue
		}
		found := false
		for i := range group.Candidates {
			if group.Candidates[i].SKU == semantic.SKU {
				group.Candidates[i].MatchText = semantic.MatchText
				found = true
				break
			}
		}
		if found {
			added[semantic.Category]++
			continue
		}
		if len(group.Candidates) >= 8 {
			continue
		}
		view, err := candidateView(candidate, semantic.MatchText)
		if err != nil {
			return err
		}
		view.origin = originSemantic
		group.Candidates = append(group.Candidates, view)
		added[semantic.Category]++
	}
	return nil
}

func filterCPU(items []store.Candidate, pref schemas.CPUBrand) []store.Candidate {
	if pref == schemas.CPUBrandAny {
		return append([]store.Candidate(nil), items...)
	}
	want := string(pref)
	return filterCandidates(items, func(candidate store.Candidate) bool { return cpuVendor(candidate) == want })
}

func filterGPU(items []store.Candidate, pref schemas.GPUBrand) []store.Candidate {
	return filterCandidates(items, func(candidate store.Candidate) bool {
		family := gpuFamily(candidate)
		if family == "" {
			return false
		}
		return pref == schemas.GPUBrandAny || family == string(pref)
	})
}

func filterMotherboards(items, cpus []store.Candidate, size schemas.SizePref) []store.Candidate {
	platforms := make(map[string]map[string]bool)
	for _, cpu := range cpus {
		var specs map[string]any
		if json.Unmarshal(cpu.Specs, &specs) != nil {
			continue
		}
		socket, _ := specs["socket"].(string)
		if socket == "" {
			continue
		}
		if platforms[socket] == nil {
			platforms[socket] = map[string]bool{}
		}
		for _, chipset := range stringSlice(specs["supported_chipsets"]) {
			platforms[socket][strings.ToUpper(chipset)] = true
		}
	}
	wantForm := sizeFormFactor(size)
	return filterCandidates(items, func(candidate store.Candidate) bool {
		var specs map[string]any
		if json.Unmarshal(candidate.Specs, &specs) != nil {
			return false
		}
		socket, _ := specs["socket"].(string)
		chipset, _ := specs["chipset"].(string)
		allowed, ok := platforms[socket]
		if !ok || chipset == "" || !allowed[strings.ToUpper(chipset)] {
			return false
		}
		if wantForm == "" {
			return true
		}
		form, _ := specs["form_factor"].(string)
		return form == wantForm
	})
}

func filterMemory(items, boards []store.Candidate) []store.Candidate {
	generations := map[string]bool{}
	for _, board := range boards {
		var specs map[string]any
		if json.Unmarshal(board.Specs, &specs) == nil {
			if generation, _ := specs["memory_generation"].(string); generation != "" {
				generations[generation] = true
			}
		}
	}
	return filterCandidates(items, func(candidate store.Candidate) bool {
		var specs map[string]any
		if json.Unmarshal(candidate.Specs, &specs) != nil {
			return false
		}
		generation, _ := specs["generation"].(string)
		return generation != "" && generations[generation]
	})
}

func filterCases(items []store.Candidate, size schemas.SizePref) []store.Candidate {
	want := sizeFormFactor(size)
	if want == "" {
		return append([]store.Candidate(nil), items...)
	}
	return filterCandidates(items, func(candidate store.Candidate) bool {
		var specs map[string]any
		if json.Unmarshal(candidate.Specs, &specs) != nil {
			return false
		}
		for _, form := range stringSlice(specs["supported_form_factors"]) {
			if form == want {
				return true
			}
		}
		return false
	})
}

func filterCandidates(items []store.Candidate, keep func(store.Candidate) bool) []store.Candidate {
	out := make([]store.Candidate, 0, len(items))
	for _, item := range items {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}

func selectByLane(items []store.Candidate, lane func(store.Candidate) string, perLane int) []store.Candidate {
	lanes := map[string][]store.Candidate{}
	for _, item := range items {
		key := lane(item)
		if key != "" {
			lanes[key] = append(lanes[key], item)
		}
	}
	keys := make([]string, 0, len(lanes))
	for key := range lanes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []store.Candidate
	for _, key := range keys {
		out = append(out, selectQuantiles(lanes[key], perLane)...)
	}
	return out
}

func selectQuantiles(items []store.Candidate, limit int) []store.Candidate {
	if len(items) <= limit {
		return append([]store.Candidate(nil), items...)
	}
	indices := make([]int, 0, limit)
	for i := 0; i < limit; i++ {
		indices = append(indices, i*(len(items)-1)/(limit-1))
	}
	out := make([]store.Candidate, 0, limit)
	seen := map[string]bool{}
	for _, index := range indices {
		if !seen[items[index].SKU] {
			seen[items[index].SKU] = true
			out = append(out, items[index])
		}
	}
	return out
}

func candidateView(candidate store.Candidate, matchText string) (Candidate, error) {
	var raw map[string]any
	if err := json.Unmarshal(candidate.Specs, &raw); err != nil {
		return Candidate{}, fmt.Errorf("buildharness: SKU %q specs 非法: %w", candidate.SKU, err)
	}
	allowed := specFields[candidate.Category]
	specs := make(map[string]any, len(allowed))
	for _, key := range allowed {
		if value, ok := raw[key]; ok {
			specs[key] = value
		}
	}
	return Candidate{SKU: candidate.SKU, Brand: candidate.Brand, Model: candidate.Model,
		PriceCNY: candidate.PriceCNY, Specs: specs, MatchText: matchText}, nil
}

var specFields = map[schemas.Category][]string{
	schemas.CategoryCPU:         {"socket", "supported_chipsets", "has_igpu", "tdp_w"},
	schemas.CategoryGPU:         {"length_mm", "tdp_w", "power_connectors"},
	schemas.CategoryMotherboard: {"socket", "chipset", "memory_generation", "memory_speed_max_mts", "form_factor", "m2_slots"},
	schemas.CategoryMemory:      {"generation", "speed_mts"},
	schemas.CategorySSD:         {"form_factor"},
	schemas.CategoryPSU:         {"wattage_w", "power_connectors"},
	schemas.CategoryCase:        {"gpu_length_max_mm", "cooler_height_max_mm", "supported_form_factors", "radiator_sizes_mm"},
	schemas.CategoryCooler:      {"type", "height_mm", "radiator_size_mm", "cooling_capacity_w"},
}

func trimBundle(bundle *CandidateBundle) error {
	for {
		encoded, err := json.Marshal(bundle)
		if err != nil {
			return fmt.Errorf("buildharness: 序列化候选包失败: %w", err)
		}
		if utf8.RuneCount(encoded) <= MaxCandidateRunes {
			return nil
		}
		if removeCandidate(bundle, func(candidate Candidate) bool { return candidate.origin == originSemantic && !candidate.protected }) {
			bundle.Trimmed++
			continue
		}
		if removeCandidate(bundle, func(candidate Candidate) bool { return !candidate.protected }) {
			bundle.Trimmed++
			continue
		}
		return fmt.Errorf("buildharness: 候选包超过 %d 字符且只剩受保护平台候选", MaxCandidateRunes)
	}
}

func removeCandidate(bundle *CandidateBundle, match func(Candidate) bool) bool {
	for gi := len(bundle.Groups) - 1; gi >= 0; gi-- {
		items := bundle.Groups[gi].Candidates
		for ci := len(items) - 1; ci >= 0; ci-- {
			if match(items[ci]) {
				bundle.Groups[gi].Candidates = append(items[:ci], items[ci+1:]...)
				return true
			}
		}
	}
	return false
}

func bundleGroup(bundle *CandidateBundle, category schemas.Category) *CandidateGroup {
	for i := range bundle.Groups {
		if bundle.Groups[i].Category == category {
			return &bundle.Groups[i]
		}
	}
	return nil
}

func selectionSKUs(selection schemas.BuildSelection, category schemas.Category) []string {
	switch category {
	case schemas.CategoryCPU:
		return []string{selection.CPU}
	case schemas.CategoryGPU:
		if selection.GPU != nil {
			return []string{*selection.GPU}
		}
	case schemas.CategoryMotherboard:
		return []string{selection.Motherboard}
	case schemas.CategoryMemory:
		return []string{selection.Memory}
	case schemas.CategorySSD:
		out := make([]string, 0, len(selection.SSDs))
		for _, item := range selection.SSDs {
			out = append(out, item.SKU)
		}
		return out
	case schemas.CategoryPSU:
		return []string{selection.PSU}
	case schemas.CategoryCase:
		return []string{selection.Case}
	case schemas.CategoryCooler:
		return []string{selection.Cooler}
	}
	return nil
}

func categorySet(categories []schemas.Category) map[schemas.Category]bool {
	out := make(map[schemas.Category]bool, len(categories))
	for _, category := range categories {
		out[category] = true
	}
	return out
}

func sizeFormFactor(size schemas.SizePref) string {
	switch size {
	case schemas.SizePrefATX:
		return string(schemas.FormFactorATX)
	case schemas.SizePrefMATX:
		return string(schemas.FormFactorMATX)
	case schemas.SizePrefITX:
		return string(schemas.FormFactorITX)
	default:
		return ""
	}
}

func cpuVendor(candidate store.Candidate) string {
	text := strings.ToUpper(candidate.Brand + " " + candidate.Model + " " + candidate.SKU)
	switch {
	case strings.Contains(text, "INTEL") || strings.Contains(text, "CORE I") || strings.Contains(text, "CORE ULTRA"):
		return "intel"
	case strings.Contains(text, "AMD") || strings.Contains(text, "RYZEN"):
		return "amd"
	default:
		return ""
	}
}

var (
	amdGPU   = regexp.MustCompile(`(?i)(RADEON|(^|[^A-Z0-9])RX[ -]?[0-9])`)
	intelGPU = regexp.MustCompile(`(?i)(INTEL[ -]?ARC|(^|[^A-Z0-9])ARC[ -]?[AB][0-9])`)
)

func gpuFamily(candidate store.Candidate) string {
	text := candidate.Brand + " " + candidate.Model + " " + candidate.SKU
	upper := strings.ToUpper(text)
	switch {
	case strings.Contains(upper, "GEFORCE") || strings.Contains(upper, "RTX") || strings.Contains(upper, "GTX"):
		return "nvidia"
	case amdGPU.MatchString(text):
		return "amd"
	case intelGPU.MatchString(text):
		return "intel"
	default:
		return ""
	}
}

func candidateSocket(candidate store.Candidate) string {
	var specs map[string]any
	if json.Unmarshal(candidate.Specs, &specs) != nil {
		return ""
	}
	value, _ := specs["socket"].(string)
	return value
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if direct, ok := value.([]string); ok {
			return direct
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func softPreferenceQuery(spec schemas.RequirementSpec) string {
	parts := make([]string, 0, 2)
	if spec.NoisePref == schemas.NoisePrefSilent {
		parts = append(parts, "安静 低噪")
	}
	notes := strings.TrimSpace(spec.Notes)
	upper := strings.ToUpper(notes)
	for _, keyword := range []string{"白色", "黑色", "海景房", "无光", "RGB", "颜值", "静音", "安静", "低噪", "小巧", "简约"} {
		if strings.Contains(upper, strings.ToUpper(keyword)) {
			parts = append(parts, notes)
			break
		}
	}
	return strings.Join(parts, " ")
}

func parsePriceFen(value *string) (int64, bool) {
	if value == nil {
		return 0, false
	}
	parts := strings.Split(*value, ".")
	if len(parts) > 2 || len(parts) == 0 {
		return 0, false
	}
	yuan, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || yuan < 0 {
		return 0, false
	}
	fen := int64(0)
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		if len(fraction) != 2 {
			return 0, false
		}
		fen, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, false
		}
	}
	return yuan*100 + fen, true
}

// CPUVendor 从品牌+型号+SKU 推断 CPU 厂商(intel/amd);空串 = 无法判定。
// filterCPU 与评估断言(P13 A5)共用此口径,避免两处推断漂移。
func CPUVendor(candidate store.Candidate) string { return cpuVendor(candidate) }

// GPUFamily 从品牌+型号+SKU 推断显卡芯片家族(nvidia/amd/intel);空串 = 无法判定。
// filterGPU 与评估断言(P13 A5)共用此口径,避免两处推断漂移。
func GPUFamily(candidate store.Candidate) string { return gpuFamily(candidate) }
