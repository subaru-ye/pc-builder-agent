package rules

import (
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// 本文件为规则测试共享夹具:一套全字段齐备、12 条规则全 pass 的风冷基线配置。
// 各规则测试在此基线上做最小改动构造 fail/unknown 分支。

func ip(v int) *int                                { return &v }
func sp(v string) *string                          { return &v }
func bp(v bool) *bool                              { return &v }
func ffp(v schemas.FormFactor) *schemas.FormFactor { return &v }
func ctp(v schemas.CoolerType) *schemas.CoolerType { return &v }
func sfp(v schemas.SSDFormFactor) *schemas.SSDFormFactor {
	return &v
}

// validBuild 全 pass 风冷基线:负载 = 65 + 220 + 100 = 385W,750W 电源余量 ≈1.95。
func validBuild() schemas.ResolvedBuild {
	return schemas.ResolvedBuild{
		BuildRef: "build_test",
		CPU: schemas.CPUSpec{
			Socket:            sp("AM5"),
			SupportedChipsets: []string{"B650", "X670"},
			HasIGPU:           bp(true),
			TDPW:              ip(65),
		},
		GPU: &schemas.GPUSpec{
			LengthMM:        ip(300),
			TDPW:            ip(220),
			PowerConnectors: []schemas.PowerConnector{schemas.ConnectorPCIe16Pin},
		},
		Motherboard: schemas.MotherboardSpec{
			Socket:            sp("AM5"),
			Chipset:           sp("B650"),
			MemoryGeneration:  sp("ddr5"),
			MemorySpeedMaxMTS: ip(6000),
			FormFactor:        ffp(schemas.FormFactorMATX),
			M2Slots:           ip(2),
		},
		Memory: schemas.MemorySpec{
			Generation: sp("ddr5"),
			SpeedMTS:   ip(6000),
		},
		SSDs: []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 1},
		},
		PSU: schemas.PSUSpec{
			WattageW: ip(750),
			PowerConnectors: []schemas.PowerConnector{
				schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe16Pin,
			},
		},
		Case: schemas.CaseSpec{
			GPULengthMaxMM:    ip(300),
			CoolerHeightMaxMM: ip(160),
			SupportedFormFactors: []schemas.FormFactor{
				schemas.FormFactorATX, schemas.FormFactorMATX, schemas.FormFactorITX,
			},
			RadiatorSizesMM: []int{240, 360},
		},
		Cooler: schemas.CoolerSpec{
			Type:             ctp(schemas.CoolerTypeAir),
			HeightMM:         ip(155),
			CoolingCapacityW: ip(220),
		},
	}
}

// checkByID 从报告中取指定规则的结果;不存在时返回零值。
func checkByID(rep schemas.ValidationReport, id schemas.RuleID) (schemas.CheckResult, bool) {
	for _, c := range rep.Checks {
		if c.RuleID == id {
			return c, true
		}
	}
	return schemas.CheckResult{}, false
}
