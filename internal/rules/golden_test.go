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

// B7 golden set:20 组固定 fixture,锁定规则顺序、outcome、severity、
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
	}
}

func TestGolden(t *testing.T) {
	engine := NewDefaultEngine()
	cases := goldenCases()
	if len(cases) != 20 {
		t.Fatalf("golden set 必须固定 20 组,得到 %d", len(cases))
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
