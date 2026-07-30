package rules

import (
	"fmt"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// psuHeadroomRule 规则 #7 PSU_HEADROOM:
// 负载(冻结)= CPU TDP + GPU TDP(无独显为 0)+ 100W。
// 余量 ≥1.30 pass;[1.15,1.30) warning;<1.15 error。
// 全程整数交叉相乘,不引入浮点:psu×100 与 load×130 / load×115 比较。
type psuHeadroomRule struct{}

// systemOverheadW 冻结的整机固定开销(主板/内存/存储/风扇等)。
const systemOverheadW = 100

func (psuHeadroomRule) ID() schemas.RuleID { return schemas.RulePSUHeadroom }

func (psuHeadroomRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	observed := map[string]any{}
	var missing []string

	if b.CPU.TDPW == nil {
		missing = append(missing, "cpu.tdp_w")
	} else {
		observed["cpu_tdp_w"] = *b.CPU.TDPW
	}

	gpuTDP := 0
	if b.GPU == nil {
		observed["gpu_present"] = false
	} else {
		observed["gpu_present"] = true
		if b.GPU.TDPW == nil {
			missing = append(missing, "gpu.tdp_w")
		} else {
			gpuTDP = *b.GPU.TDPW
		}
	}

	if b.PSU.WattageW == nil {
		missing = append(missing, "psu.wattage_w")
	} else {
		observed["psu_wattage_w"] = *b.PSU.WattageW
	}

	if len(missing) > 0 {
		return unknown(schemas.RulePSUHeadroom, observed, missing, "功耗字段缺失,无法判定电源余量")
	}

	loadW := *b.CPU.TDPW + gpuTDP + systemOverheadW
	observed["gpu_tdp_w"] = gpuTDP
	observed["load_w"] = loadW
	psuW := *b.PSU.WattageW

	switch {
	case psuW*100 >= loadW*130:
		return pass(schemas.RulePSUHeadroom, observed,
			fmt.Sprintf("电源 %dW 对估算负载 %dW 余量不低于 1.30", psuW, loadW))
	case psuW*100 >= loadW*115:
		return fail(schemas.RulePSUHeadroom, schemas.SeverityWarning, observed,
			fmt.Sprintf("电源 %dW 对估算负载 %dW 余量在 [1.15,1.30) 区间,建议加大电源", psuW, loadW))
	default:
		return fail(schemas.RulePSUHeadroom, schemas.SeverityError, observed,
			fmt.Sprintf("电源 %dW 对估算负载 %dW 余量低于 1.15,存在断电/降频风险", psuW, loadW))
	}
}

// gpuPowerConnectorsRule 规则 #10 GPU_POWER_CONNECTORS(错误级):
// 显卡所需供电接口 multiset ⊆ 电源提供的接口 multiset,逐类型计数比较;
// 枚举只有 pcie_8pin/pcie_16pin(冻结),不推断转接线或一分二。
// gpu 显式为 null 时无物可校,视为 pass;nil 集合为未知,空集合为已知为空。
type gpuPowerConnectorsRule struct{}

func (gpuPowerConnectorsRule) ID() schemas.RuleID { return schemas.RuleGPUPowerConnectors }

func (gpuPowerConnectorsRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	if b.GPU == nil {
		return pass(schemas.RuleGPUPowerConnectors, map[string]any{"gpu_present": false}, "无独显,无需校验供电接口")
	}
	observed := map[string]any{"gpu_present": true}
	var missing []string
	if b.GPU.PowerConnectors == nil {
		missing = append(missing, "gpu.power_connectors")
	}
	if b.PSU.PowerConnectors == nil {
		missing = append(missing, "psu.power_connectors")
	}
	if len(missing) > 0 {
		return unknown(schemas.RuleGPUPowerConnectors, observed, missing, "供电接口字段缺失,无法判定")
	}

	required := countConnectors(b.GPU.PowerConnectors)
	available := countConnectors(b.PSU.PowerConnectors)
	observed["gpu_required_pcie_8pin"] = required[schemas.ConnectorPCIe8Pin]
	observed["gpu_required_pcie_16pin"] = required[schemas.ConnectorPCIe16Pin]
	observed["psu_available_pcie_8pin"] = available[schemas.ConnectorPCIe8Pin]
	observed["psu_available_pcie_16pin"] = available[schemas.ConnectorPCIe16Pin]

	for _, conn := range []schemas.PowerConnector{schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe16Pin} {
		if required[conn] > available[conn] {
			return fail(schemas.RuleGPUPowerConnectors, schemas.SeverityError, observed,
				fmt.Sprintf("显卡需要 %d 个 %s,电源仅提供 %d 个", required[conn], conn, available[conn]))
		}
	}
	return pass(schemas.RuleGPUPowerConnectors, observed, "电源供电接口满足显卡需求")
}

// countConnectors 按接口类型计数;入参应为非 nil(nil 语义由调用方先判)。
func countConnectors(conns []schemas.PowerConnector) map[schemas.PowerConnector]int {
	counts := make(map[schemas.PowerConnector]int, 2)
	for _, c := range conns {
		counts[c]++
	}
	return counts
}
