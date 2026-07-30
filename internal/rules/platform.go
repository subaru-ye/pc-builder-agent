package rules

import (
	"fmt"
	"slices"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// socketMatchRule 规则 #1 SOCKET_MATCH(错误级):CPU 插槽 == 主板插槽。
type socketMatchRule struct{}

func (socketMatchRule) ID() schemas.RuleID { return schemas.RuleSocketMatch }

func (socketMatchRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	observed := map[string]any{}
	var missing []string
	if b.CPU.Socket == nil {
		missing = append(missing, "cpu.socket")
	} else {
		observed["cpu_socket"] = *b.CPU.Socket
	}
	if b.Motherboard.Socket == nil {
		missing = append(missing, "motherboard.socket")
	} else {
		observed["motherboard_socket"] = *b.Motherboard.Socket
	}
	if len(missing) > 0 {
		return unknown(schemas.RuleSocketMatch, observed, missing, "插槽字段缺失,无法判定")
	}
	if *b.CPU.Socket == *b.Motherboard.Socket {
		return pass(schemas.RuleSocketMatch, observed, "CPU 与主板插槽一致")
	}
	return fail(schemas.RuleSocketMatch, schemas.SeverityError, observed,
		fmt.Sprintf("CPU 插槽 %s 与主板插槽 %s 不匹配", *b.CPU.Socket, *b.Motherboard.Socket))
}

// chipsetSupportRule 规则 #2 CHIPSET_SUPPORT(错误级):主板芯片组 ∈ CPU 支持列表。
// CPU supported_chipsets 为 nil(未知)→ unknown;空集合(已知为空)→ fail。
type chipsetSupportRule struct{}

func (chipsetSupportRule) ID() schemas.RuleID { return schemas.RuleChipsetSupport }

func (chipsetSupportRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	observed := map[string]any{}
	var missing []string
	if b.CPU.SupportedChipsets == nil {
		missing = append(missing, "cpu.supported_chipsets")
	} else {
		observed["cpu_supported_chipsets"] = b.CPU.SupportedChipsets
	}
	if b.Motherboard.Chipset == nil {
		missing = append(missing, "motherboard.chipset")
	} else {
		observed["motherboard_chipset"] = *b.Motherboard.Chipset
	}
	if len(missing) > 0 {
		return unknown(schemas.RuleChipsetSupport, observed, missing, "芯片组字段缺失,无法判定")
	}
	if slices.Contains(b.CPU.SupportedChipsets, *b.Motherboard.Chipset) {
		return pass(schemas.RuleChipsetSupport, observed, "主板芯片组在 CPU 支持列表内")
	}
	return fail(schemas.RuleChipsetSupport, schemas.SeverityError, observed,
		fmt.Sprintf("主板芯片组 %s 不在 CPU 支持列表 %v 内", *b.Motherboard.Chipset, b.CPU.SupportedChipsets))
}
