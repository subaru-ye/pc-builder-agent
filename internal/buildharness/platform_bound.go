package buildharness

import (
	"encoding/json"
	"math"
	"slices"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// generalPlatformLowerBound 是完整目录上办公新整机的乐观下限,不是可交付报价。
// 仅排除已知的插槽/芯片组/内存代际冲突,无核显者计最低独显价;
// 其他品类各取最低价。忽略未知规格、尺寸、功耗、品牌等约束只会扩大可行集合。
// 现阶段不处理已有件和改单;价格未知或枚举过大时放弃证明,继续正常选配。
func generalPlatformLowerBound(input BuildInput, catalog store.CatalogSnapshot) (int64, bool) {
	if input.Requirement.UseCase.Type != schemas.UseCaseGeneral || len(input.Requirement.OwnedParts) != 0 || len(input.Requirement.ExistingParts) != 0 || input.Change != nil || input.BaseSelection != nil || len(input.Locked) != 0 {
		return 0, false
	}
	byCategory := map[schemas.Category][]store.Candidate{}
	prices := map[string]int64{}
	minima := map[schemas.Category]int64{}
	for _, c := range catalog.Candidates {
		price, ok := parsePriceFen(c.PriceCNY)
		if !ok || price < 0 {
			return 0, false
		}
		prices[c.SKU] = price
		byCategory[c.Category] = append(byCategory[c.Category], c)
		if value, exists := minima[c.Category]; !exists || price < value {
			minima[c.Category] = price
		}
	}
	var extra int64
	for _, category := range []schemas.Category{schemas.CategorySSD, schemas.CategoryPSU, schemas.CategoryCase, schemas.CategoryCooler} {
		value, ok := minima[category]
		if !ok {
			return 0, false
		}
		extra += value
	}
	cpus, boards, memory := byCategory[schemas.CategoryCPU], byCategory[schemas.CategoryMotherboard], byCategory[schemas.CategoryMemory]
	if float64(len(cpus))*float64(len(boards))*float64(len(memory)) > 500000 {
		return 0, false
	}
	cpuSpecs := make([]schemas.CPUSpec, len(cpus))
	boardSpecs := make([]schemas.MotherboardSpec, len(boards))
	memorySpecs := make([]schemas.MemorySpec, len(memory))
	for i, c := range cpus {
		if json.Unmarshal(c.Specs, &cpuSpecs[i]) != nil {
			return 0, false
		}
	}
	for i, c := range boards {
		if json.Unmarshal(c.Specs, &boardSpecs[i]) != nil {
			return 0, false
		}
	}
	for i, c := range memory {
		if json.Unmarshal(c.Specs, &memorySpecs[i]) != nil {
			return 0, false
		}
	}
	minimum := int64(math.MaxInt64)
	for i, cpu := range cpus {
		cs := cpuSpecs[i]
		var gpu int64
		if cs.HasIGPU != nil && !*cs.HasIGPU {
			// 无显卡目录时按零价放宽,不能凭缺少该品类抬高数值下限。
			gpu = minima[schemas.CategoryGPU]
		}
		for j, board := range boards {
			bs := boardSpecs[j]
			if knownDifferent(cs.Socket, bs.Socket) || (cs.SupportedChipsets != nil && bs.Chipset != nil && !slices.Contains(cs.SupportedChipsets, *bs.Chipset)) {
				continue
			}
			for k, mem := range memory {
				if knownDifferent(bs.MemoryGeneration, memorySpecs[k].Generation) {
					continue
				}
				total := prices[cpu.SKU] + prices[board.SKU] + prices[mem.SKU] + gpu + extra
				if total < minimum {
					minimum = total
				}
			}
		}
	}
	return minimum, minimum != math.MaxInt64
}

func knownDifferent(a, b *string) bool {
	return a != nil && b != nil && *a != *b
}
