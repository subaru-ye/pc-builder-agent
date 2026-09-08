// Package schemas 定义 P1 校验契约的全部数据类型:
// BuildSelection(CLI 输入)/ ResolvedBuild(规则引擎输入)/ ValidationReport(输出)
// 及各品类 canonical 规格。唯一权威出处为 docs/tech/系统架构.md §四.1,
// 字段变更先改文档再改本包,不在代码里静默漂移(CLAUDE.md 工程纪律)。
//
// null 语义:标量指针 nil = 未知;集合 nil = 未知,空集合 = 已知为空。
// 单位统一为整数 mm / W / MT/s;未知字段、非法枚举、非正数量均为输入错误。
package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Category 零件八大类(固定枚举,与 parts.category CHECK 约束一致)。
type Category string

const (
	CategoryCPU         Category = "cpu"
	CategoryGPU         Category = "gpu"
	CategoryMotherboard Category = "motherboard"
	CategoryMemory      Category = "memory"
	CategorySSD         Category = "ssd"
	CategoryPSU         Category = "psu"
	CategoryCase        Category = "case"
	CategoryCooler      Category = "cooler"
)

// AllCategories 八大类固定顺序(文档表序)。
var AllCategories = []Category{
	CategoryCPU, CategoryGPU, CategoryMotherboard, CategoryMemory,
	CategorySSD, CategoryPSU, CategoryCase, CategoryCooler,
}

// FormFactor 主板板型 / 机箱支持板型。
type FormFactor string

const (
	FormFactorATX  FormFactor = "atx"
	FormFactorMATX FormFactor = "matx"
	FormFactorITX  FormFactor = "itx"
)

func (f *FormFactor) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("form_factor 必须为字符串: %w", err)
	}
	switch FormFactor(s) {
	case FormFactorATX, FormFactorMATX, FormFactorITX:
		*f = FormFactor(s)
		return nil
	}
	return fmt.Errorf("非法板型枚举 %q(仅 atx|matx|itx)", s)
}

// SSDFormFactor SSD 形态。
type SSDFormFactor string

const (
	SSDFormFactorM2     SSDFormFactor = "m2"
	SSDFormFactorSATA25 SSDFormFactor = "sata_2_5"
)

func (f *SSDFormFactor) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("ssd form_factor 必须为字符串: %w", err)
	}
	switch SSDFormFactor(s) {
	case SSDFormFactorM2, SSDFormFactorSATA25:
		*f = SSDFormFactor(s)
		return nil
	}
	return fmt.Errorf("非法 SSD 形态枚举 %q(仅 m2|sata_2_5)", s)
}

// CoolerType 散热器类型。
type CoolerType string

const (
	CoolerTypeAir CoolerType = "air"
	CoolerTypeAIO CoolerType = "aio"
)

func (t *CoolerType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("cooler type 必须为字符串: %w", err)
	}
	switch CoolerType(s) {
	case CoolerTypeAir, CoolerTypeAIO:
		*t = CoolerType(s)
		return nil
	}
	return fmt.Errorf("非法散热器类型枚举 %q(仅 air|aio)", s)
}

// PowerConnector 供电接口枚举:只保留两种;
// 12VHPWR / 12V-2×6 由数据管道归一为 pcie_16pin,本层不做别名兼容。
type PowerConnector string

const (
	ConnectorPCIe8Pin  PowerConnector = "pcie_8pin"
	ConnectorPCIe16Pin PowerConnector = "pcie_16pin"
)

func (c *PowerConnector) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("power_connector 必须为字符串: %w", err)
	}
	switch PowerConnector(s) {
	case ConnectorPCIe8Pin, ConnectorPCIe16Pin:
		*c = PowerConnector(s)
		return nil
	}
	return fmt.Errorf("非法供电接口枚举 %q(仅 pcie_8pin|pcie_16pin)", s)
}

// ---- 各品类 canonical 规格(字段字典见设计方案 §四.1)----

// CPUSpec CPU 规格。
type CPUSpec struct {
	Socket            *string  `json:"socket"`
	SupportedChipsets []string `json:"supported_chipsets"`
	HasIGPU           *bool    `json:"has_igpu"`
	TDPW              *int     `json:"tdp_w"`
}

// GPUSpec 显卡规格。
type GPUSpec struct {
	LengthMM        *int             `json:"length_mm"`
	TDPW            *int             `json:"tdp_w"`
	PowerConnectors []PowerConnector `json:"power_connectors"`
}

// MotherboardSpec 主板规格。
type MotherboardSpec struct {
	Socket            *string     `json:"socket"`
	Chipset           *string     `json:"chipset"`
	MemoryGeneration  *string     `json:"memory_generation"`
	MemorySpeedMaxMTS *int        `json:"memory_speed_max_mts"`
	FormFactor        *FormFactor `json:"form_factor"`
	M2Slots           *int        `json:"m2_slots"`
}

// MemorySpec 内存规格。
type MemorySpec struct {
	Generation *string `json:"generation"`
	SpeedMTS   *int    `json:"speed_mts"`
}

// SSDSpec SSD 规格(数量属于选择层,不在规格内)。
type SSDSpec struct {
	FormFactor *SSDFormFactor `json:"form_factor"`
}

// PSUSpec 电源规格。
type PSUSpec struct {
	WattageW        *int             `json:"wattage_w"`
	PowerConnectors []PowerConnector `json:"power_connectors"`
}

// CaseSpec 机箱规格。
type CaseSpec struct {
	GPULengthMaxMM       *int         `json:"gpu_length_max_mm"`
	CoolerHeightMaxMM    *int         `json:"cooler_height_max_mm"`
	SupportedFormFactors []FormFactor `json:"supported_form_factors"`
	RadiatorSizesMM      []int        `json:"radiator_sizes_mm"`
}

// CoolerSpec 散热器规格。
type CoolerSpec struct {
	Type             *CoolerType `json:"type"`
	HeightMM         *int        `json:"height_mm"`
	RadiatorSizeMM   *int        `json:"radiator_size_mm"`
	CoolingCapacityW *int        `json:"cooling_capacity_w"`
}

// decodeStrict 严格 JSON 解码:未知字段、尾部多余内容均报错。
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("JSON 尾部存在多余内容")
	}
	return nil
}

// unmarshalString 将 JSON 值解码为字符串,失败时用 field 名产出可读错误;
// 供各枚举类型的 UnmarshalJSON 复用。
func unmarshalString(b []byte, field string) (string, error) {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return "", fmt.Errorf("%s 必须为字符串: %w", field, err)
	}
	return s, nil
}

// requirePositive 校验标量存在时必须为正整数(单位 mm/W/MT/s 均为正数)。
func requirePositive(field string, v *int) error {
	if v != nil && *v <= 0 {
		return fmt.Errorf("字段 %s 必须为正整数,得到 %d", field, *v)
	}
	return nil
}

// requireNonNegative 校验计数字段存在时必须 ≥0(如 m2_slots)。
func requireNonNegative(field string, v *int) error {
	if v != nil && *v < 0 {
		return fmt.Errorf("字段 %s 不得为负数,得到 %d", field, *v)
	}
	return nil
}

// requireNonEmpty 校验字符串存在时不得为空串(空串不是"未知",是脏数据)。
func requireNonEmpty(field string, v *string) error {
	if v != nil && *v == "" {
		return fmt.Errorf("字段 %s 不得为空字符串(未知请用 null)", field)
	}
	return nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// DecodeCPUSpec 严格解码并校验 CPU 规格。
func DecodeCPUSpec(data []byte) (CPUSpec, error) {
	var s CPUSpec
	if err := decodeStrict(data, &s); err != nil {
		return CPUSpec{}, fmt.Errorf("cpu specs: %w", err)
	}
	for i, c := range s.SupportedChipsets {
		if c == "" {
			return CPUSpec{}, fmt.Errorf("cpu specs: supported_chipsets[%d] 不得为空字符串", i)
		}
	}
	if err := firstErr(
		requireNonEmpty("socket", s.Socket),
		requirePositive("tdp_w", s.TDPW),
	); err != nil {
		return CPUSpec{}, fmt.Errorf("cpu specs: %w", err)
	}
	return s, nil
}

// DecodeGPUSpec 严格解码并校验显卡规格。
func DecodeGPUSpec(data []byte) (GPUSpec, error) {
	var s GPUSpec
	if err := decodeStrict(data, &s); err != nil {
		return GPUSpec{}, fmt.Errorf("gpu specs: %w", err)
	}
	if err := firstErr(
		requirePositive("length_mm", s.LengthMM),
		requirePositive("tdp_w", s.TDPW),
	); err != nil {
		return GPUSpec{}, fmt.Errorf("gpu specs: %w", err)
	}
	return s, nil
}

// DecodeMotherboardSpec 严格解码并校验主板规格。
func DecodeMotherboardSpec(data []byte) (MotherboardSpec, error) {
	var s MotherboardSpec
	if err := decodeStrict(data, &s); err != nil {
		return MotherboardSpec{}, fmt.Errorf("motherboard specs: %w", err)
	}
	if err := firstErr(
		requireNonEmpty("socket", s.Socket),
		requireNonEmpty("chipset", s.Chipset),
		requireNonEmpty("memory_generation", s.MemoryGeneration),
		requirePositive("memory_speed_max_mts", s.MemorySpeedMaxMTS),
		requireNonNegative("m2_slots", s.M2Slots),
	); err != nil {
		return MotherboardSpec{}, fmt.Errorf("motherboard specs: %w", err)
	}
	return s, nil
}

// DecodeMemorySpec 严格解码并校验内存规格。
func DecodeMemorySpec(data []byte) (MemorySpec, error) {
	var s MemorySpec
	if err := decodeStrict(data, &s); err != nil {
		return MemorySpec{}, fmt.Errorf("memory specs: %w", err)
	}
	if err := firstErr(
		requireNonEmpty("generation", s.Generation),
		requirePositive("speed_mts", s.SpeedMTS),
	); err != nil {
		return MemorySpec{}, fmt.Errorf("memory specs: %w", err)
	}
	return s, nil
}

// DecodeSSDSpec 严格解码并校验 SSD 规格。
func DecodeSSDSpec(data []byte) (SSDSpec, error) {
	var s SSDSpec
	if err := decodeStrict(data, &s); err != nil {
		return SSDSpec{}, fmt.Errorf("ssd specs: %w", err)
	}
	return s, nil
}

// DecodePSUSpec 严格解码并校验电源规格。
func DecodePSUSpec(data []byte) (PSUSpec, error) {
	var s PSUSpec
	if err := decodeStrict(data, &s); err != nil {
		return PSUSpec{}, fmt.Errorf("psu specs: %w", err)
	}
	if err := requirePositive("wattage_w", s.WattageW); err != nil {
		return PSUSpec{}, fmt.Errorf("psu specs: %w", err)
	}
	return s, nil
}

// DecodeCaseSpec 严格解码并校验机箱规格。
func DecodeCaseSpec(data []byte) (CaseSpec, error) {
	var s CaseSpec
	if err := decodeStrict(data, &s); err != nil {
		return CaseSpec{}, fmt.Errorf("case specs: %w", err)
	}
	for i, r := range s.RadiatorSizesMM {
		if r <= 0 {
			return CaseSpec{}, fmt.Errorf("case specs: radiator_sizes_mm[%d] 必须为正整数,得到 %d", i, r)
		}
	}
	if err := firstErr(
		requirePositive("gpu_length_max_mm", s.GPULengthMaxMM),
		requirePositive("cooler_height_max_mm", s.CoolerHeightMaxMM),
	); err != nil {
		return CaseSpec{}, fmt.Errorf("case specs: %w", err)
	}
	return s, nil
}

// DecodeCoolerSpec 严格解码并校验散热器规格。
func DecodeCoolerSpec(data []byte) (CoolerSpec, error) {
	var s CoolerSpec
	if err := decodeStrict(data, &s); err != nil {
		return CoolerSpec{}, fmt.Errorf("cooler specs: %w", err)
	}
	if err := firstErr(
		requirePositive("height_mm", s.HeightMM),
		requirePositive("radiator_size_mm", s.RadiatorSizeMM),
		requirePositive("cooling_capacity_w", s.CoolingCapacityW),
	); err != nil {
		return CoolerSpec{}, fmt.Errorf("cooler specs: %w", err)
	}
	return s, nil
}
