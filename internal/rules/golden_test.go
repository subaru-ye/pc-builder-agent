package rules

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// B7/P10 golden set:50 组固定 fixture,锁定规则顺序、outcome、severity、
// missing fields、overall_status 与功耗值 load_w;自然语言 detail 不进 golden。
// 更新基线:go test ./internal/rules -run TestGolden -update
var updateGolden = flag.Bool("update", false, "重写 golden 基线文件")

// goldenCheck 单条规则的锁定投影(不含 detail 与非稳定 observed)。
type goldenCheck struct {
	RuleID        schemas.RuleID   `json:"rule_id"`
	Outcome       schemas.Outcome  `json:"outcome"`
	Severity      schemas.Severity `json:"severity"`
	MissingFields []string         `json:"missing_fields"`
}

// goldenReport 整份报告的锁定投影;LoadW 取自 PSU_HEADROOM observed,unknown 时为 null。
type goldenReport struct {
	OverallStatus schemas.OverallStatus `json:"overall_status"`
	LoadW         *int                  `json:"load_w"`
	Checks        []goldenCheck         `json:"checks"`
}

func projectReport(t *testing.T, rep schemas.ValidationReport) goldenReport {
	t.Helper()
	out := goldenReport{OverallStatus: rep.OverallStatus}
	for _, c := range rep.Checks {
		out.Checks = append(out.Checks, goldenCheck{
			RuleID:        c.RuleID,
			Outcome:       c.Outcome,
			Severity:      c.Severity,
			MissingFields: c.MissingFields,
		})
		if c.RuleID == schemas.RulePSUHeadroom {
			if v, ok := c.Observed["load_w"].(int); ok {
				out.LoadW = &v
			}
		}
	}
	return out
}

// boundaryAirBuild 场景 1:风冷全 pass 且多处数值恰等于边界:
// GPU 300=限长 300、内存 6000=上限 6000、风冷 160=限高 160、
// 解热 80=TDP 80、M.2 2 盘=2 槽、PSU 520W 恰为负载 400W 的 1.30。
func boundaryAirBuild() schemas.ResolvedBuild {
	b := validBuild()
	b.CPU.TDPW = ip(80)
	b.PSU.WattageW = ip(520)
	b.Cooler.HeightMM = ip(160)
	b.Cooler.CoolingCapacityW = ip(80)
	b.SSDs = []schemas.ResolvedSSD{
		{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 2},
	}
	return b
}

func goldenCases() []struct {
	Name  string
	Build schemas.ResolvedBuild
} {
	c01 := boundaryAirBuild()

	c02 := aioBuild(360)

	c03 := validBuild()
	c03.Motherboard.Socket = sp("LGA1700")
	c03.Motherboard.Chipset = sp("Z790")

	c04 := validBuild()
	c04.Memory.Generation = sp("ddr4")

	c05 := validBuild()
	c05.Memory.SpeedMTS = ip(6400)

	c06 := validBuild()
	c06.GPU.LengthMM = ip(301)

	c07 := validBuild()
	c07.Cooler.HeightMM = ip(161)

	c08 := aioBuild(280)

	c09 := headroomBuild(460) // 460*100 == 400*115

	c10 := headroomBuild(459)

	c11 := headroomBuild(520) // 520*100 == 400*130

	c12 := validBuild()
	c12.Case.SupportedFormFactors = []schemas.FormFactor{schemas.FormFactorITX}

	c13 := validBuild()
	c13.SSDs = []schemas.ResolvedSSD{
		{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 3},
	}

	c14 := validBuild()
	c14.GPU.PowerConnectors = []schemas.PowerConnector{
		schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe8Pin,
	}

	c15 := validBuild()
	c15.GPU = nil
	c15.CPU.HasIGPU = bp(false)

	c16 := validBuild()
	c16.Cooler.CoolingCapacityW = ip(64)

	c17 := validBuild()
	c17.CPU.Socket = nil
	c17.CPU.SupportedChipsets = nil
	c17.Memory.Generation = nil
	c17.Memory.SpeedMTS = nil

	c18 := validBuild()
	c18.GPU.LengthMM = nil
	c18.Cooler.Type = nil
	c18.Case.SupportedFormFactors = nil
	c18.Motherboard.M2Slots = nil
	c18.GPU.PowerConnectors = nil

	c19 := validBuild()
	c19.CPU.TDPW = nil
	c19.GPU = nil
	c19.CPU.HasIGPU = nil

	c20 := validBuild()
	c20.GPU = nil // 基线 has_igpu = true

	c21 := validBuild()
	c21.CPU.Socket = nil

	c22 := validBuild()
	c22.Motherboard.Socket = nil

	c23 := validBuild()
	c23.CPU.SupportedChipsets = []string{}

	c24 := validBuild()
	c24.Motherboard.Chipset = nil

	c25 := validBuild()
	c25.Memory.Generation = nil

	c26 := validBuild()
	c26.Motherboard.MemoryGeneration = nil

	c27 := validBuild()
	c27.Memory.SpeedMTS = nil

	c28 := validBuild()
	c28.Motherboard.MemorySpeedMaxMTS = nil

	c29 := validBuild()
	c29.GPU.LengthMM = nil

	c30 := validBuild()
	c30.Case.GPULengthMaxMM = nil

	c31 := validBuild()
	c31.GPU.LengthMM = ip(299)

	c32 := validBuild()
	c32.Cooler.HeightMM = nil

	c33 := validBuild()
	c33.Case.CoolerHeightMaxMM = nil

	c34 := aioBuild(240)
	c34.Cooler.RadiatorSizeMM = nil

	c35 := aioBuild(240)
	c35.Case.RadiatorSizesMM = nil

	c36 := aioBuild(240)

	c37 := validBuild()
	c37.CPU.TDPW = nil

	c38 := validBuild()
	c38.GPU.TDPW = nil

	c39 := validBuild()
	c39.PSU.WattageW = nil

	c40 := headroomBuild(519) // 519*100 < 400*130,但仍在 warning 区间

	c41 := validBuild()
	c41.GPU.PowerConnectors = nil

	c42 := validBuild()
	c42.PSU.PowerConnectors = nil

	c43 := validBuild()
	c43.GPU.PowerConnectors = []schemas.PowerConnector{schemas.ConnectorPCIe16Pin}
	c43.PSU.PowerConnectors = []schemas.PowerConnector{
		schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe8Pin,
	}

	c44 := validBuild()
	c44.Motherboard.FormFactor = nil

	c45 := validBuild()
	c45.Case.SupportedFormFactors = nil

	c46 := validBuild()
	c46.SSDs[0].Spec.FormFactor = nil

	c47 := validBuild()
	c47.Motherboard.M2Slots = nil

	c48 := validBuild()
	c48.SSDs[0].Spec.FormFactor = sfp(schemas.SSDFormFactorSATA25)

	c49 := validBuild()
	c49.Motherboard.Socket = sp("LGA1700")
	c49.Memory.Generation = sp("ddr4")
	c49.GPU.LengthMM = ip(330)
	c49.Cooler.HeightMM = ip(180)
	c49.PSU.WattageW = ip(400)
	c49.Case.SupportedFormFactors = []schemas.FormFactor{schemas.FormFactorITX}
	c49.SSDs[0].Quantity = 3
	c49.PSU.PowerConnectors = []schemas.PowerConnector{}

	c50 := validBuild()
	c50.CPU.Socket = nil
	c50.CPU.SupportedChipsets = nil
	c50.CPU.HasIGPU = nil
	c50.CPU.TDPW = nil
	c50.GPU.LengthMM = nil
	c50.GPU.TDPW = nil
	c50.GPU.PowerConnectors = nil
	c50.Motherboard.Socket = nil
	c50.Motherboard.Chipset = nil
	c50.Motherboard.MemoryGeneration = nil
	c50.Motherboard.MemorySpeedMaxMTS = nil
	c50.Motherboard.FormFactor = nil
	c50.Motherboard.M2Slots = nil
	c50.Memory.Generation = nil
	c50.Memory.SpeedMTS = nil
	c50.PSU.WattageW = nil
	c50.PSU.PowerConnectors = nil
	c50.Case.GPULengthMaxMM = nil
	c50.Case.CoolerHeightMaxMM = nil
	c50.Case.SupportedFormFactors = nil
	c50.Cooler.Type = nil
	c50.Cooler.CoolingCapacityW = nil
	c50.SSDs[0].Spec.FormFactor = nil

	return []struct {
		Name  string
		Build schemas.ResolvedBuild
	}{
		{"01_air_all_pass_boundary", c01},
		{"02_aio_all_pass", c02},
		{"03_socket_chipset_mismatch", c03},
		{"04_memory_generation_mismatch", c04},
		{"05_memory_speed_warning", c05},
		{"06_gpu_too_long", c06},
		{"07_air_cooler_too_tall", c07},
		{"08_aio_radiator_unsupported", c08},
		{"09_psu_ratio_exactly_115", c09},
		{"10_psu_ratio_below_115", c10},
		{"11_psu_ratio_exactly_130", c11},
		{"12_form_factor_unsupported", c12},
		{"13_m2_over_capacity", c13},
		{"14_gpu_connectors_insufficient", c14},
		{"15_no_igpu_no_gpu", c15},
		{"16_cooler_capacity_insufficient", c16},
		{"17_platform_memory_fields_missing", c17},
		{"18_dimension_slot_connector_missing", c18},
		{"19_power_igpu_thermal_missing", c19},
		{"20_igpu_with_gpu_null", c20},
		{"21_cpu_socket_missing", c21},
		{"22_motherboard_socket_missing", c22},
		{"23_chipset_support_empty", c23},
		{"24_motherboard_chipset_missing", c24},
		{"25_memory_generation_missing", c25},
		{"26_motherboard_memory_generation_missing", c26},
		{"27_memory_speed_missing", c27},
		{"28_motherboard_memory_speed_missing", c28},
		{"29_gpu_length_missing", c29},
		{"30_case_gpu_limit_missing", c30},
		{"31_gpu_below_limit", c31},
		{"32_air_cooler_height_missing", c32},
		{"33_case_cooler_limit_missing", c33},
		{"34_aio_radiator_size_missing", c34},
		{"35_case_radiator_sizes_missing", c35},
		{"36_aio_240_all_pass", c36},
		{"37_cpu_tdp_missing", c37},
		{"38_gpu_tdp_missing", c38},
		{"39_psu_wattage_missing", c39},
		{"40_psu_ratio_below_130", c40},
		{"41_gpu_connectors_missing", c41},
		{"42_psu_connectors_missing", c42},
		{"43_connector_type_mismatch", c43},
		{"44_motherboard_form_factor_missing", c44},
		{"45_case_form_factors_missing", c45},
		{"46_ssd_form_factor_missing", c46},
		{"47_m2_slots_missing", c47},
		{"48_sata_ssd_all_pass", c48},
		{"49_multiple_rule_failures", c49},
		{"50_wide_unknown_fields", c50},
	}
}

func TestGolden(t *testing.T) {
	engine := NewDefaultEngine()
	cases := goldenCases()
	if len(cases) != 50 {
		t.Fatalf("golden set 必须固定 50 组,得到 %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			rep, err := engine.Validate(tc.Build)
			if err != nil {
				t.Fatalf("Validate 报错: %v", err)
			}
			got := projectReport(t, rep)
			gotJSON, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			gotJSON = append(gotJSON, '\n')

			path := filepath.Join("testdata", "golden", tc.Name+".json")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, gotJSON, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("读取 golden 基线失败(可用 -update 生成): %v", err)
			}
			// 容忍 git autocrlf 引入的 \r,按 LF 归一后比对。
			want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(gotJSON, want) {
				t.Errorf("golden 不匹配 %s\n--- got ---\n%s\n--- want ---\n%s", path, gotJSON, want)
			}
		})
	}
}

// TestGoldenCoverageMatrix 保证扩容的用例不只是数量达标:
// 12 条规则均要经过 pass 和非 pass 分支,且错误/警告级语义不得漂移。
func TestGoldenCoverageMatrix(t *testing.T) {
	type coverage struct {
		pass, nonPass, errorFail, warningFail bool
	}
	got := make(map[schemas.RuleID]coverage, len(schemas.AllRuleIDs))
	engine := NewDefaultEngine()
	for _, tc := range goldenCases() {
		rep, err := engine.Validate(tc.Build)
		if err != nil {
			t.Fatalf("%s Validate 报错: %v", tc.Name, err)
		}
		for _, check := range rep.Checks {
			c := got[check.RuleID]
			if check.Outcome == schemas.OutcomePass {
				c.pass = true
			} else {
				c.nonPass = true
			}
			if check.Outcome == schemas.OutcomeFail && check.Severity == schemas.SeverityError {
				c.errorFail = true
			}
			if check.Outcome == schemas.OutcomeFail && check.Severity == schemas.SeverityWarning {
				c.warningFail = true
			}
			got[check.RuleID] = c
		}
	}

	warningOnly := map[schemas.RuleID]bool{
		schemas.RuleMemorySpeed:           true,
		schemas.RuleCoolerThermalCapacity: true,
	}
	for _, id := range schemas.AllRuleIDs {
		c := got[id]
		if !c.pass || !c.nonPass {
			t.Errorf("规则 %s 覆盖不完整: pass=%v non_pass=%v", id, c.pass, c.nonPass)
		}
		if warningOnly[id] {
			if !c.warningFail || c.errorFail {
				t.Errorf("警告级规则 %s 严重度漂移: warning=%v error=%v", id, c.warningFail, c.errorFail)
			}
		} else if !c.errorFail {
			t.Errorf("错误级规则 %s 没有 error fail 覆盖", id)
		}
	}
}
