package rules

import (
	"reflect"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// headroomBuild 构造负载恰为 400W 的配置(80 + 220 + 100),便于取整数边界。
func headroomBuild(psuW int) schemas.ResolvedBuild {
	b := validBuild()
	b.CPU.TDPW = ip(80)
	b.GPU.TDPW = ip(220)
	b.PSU.WattageW = ip(psuW)
	return b
}

func TestPSUHeadroom(t *testing.T) {
	t.Run("pass 基线余量充裕", func(t *testing.T) {
		got := psuHeadroomRule{}.Check(validBuild()) // 750/385 ≈ 1.95
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
		if got.Observed["load_w"] != 385 {
			t.Errorf("observed.load_w = %v, want 385", got.Observed["load_w"])
		}
	})

	t.Run("pass 恰好 1.30(边界)", func(t *testing.T) {
		got := psuHeadroomRule{}.Check(headroomBuild(520)) // 520*100 == 400*130
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("恰好 1.30 应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("warning 略低于 1.30", func(t *testing.T) {
		got := psuHeadroomRule{}.Check(headroomBuild(519)) // 51900 < 52000
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityWarning {
			t.Errorf("应 fail/warning,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("warning 恰好 1.15(边界)", func(t *testing.T) {
		got := psuHeadroomRule{}.Check(headroomBuild(460)) // 460*100 == 400*115
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityWarning {
			t.Errorf("恰好 1.15 应 fail/warning,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("error 低于 1.15", func(t *testing.T) {
		got := psuHeadroomRule{}.Check(headroomBuild(459)) // 45900 < 46000
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("pass gpu 显式 null 计 0W", func(t *testing.T) {
		b := validBuild()
		b.GPU = nil // 负载 = 65 + 0 + 100 = 165
		got := psuHeadroomRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
		if got.Observed["load_w"] != 165 {
			t.Errorf("observed.load_w = %v, want 165", got.Observed["load_w"])
		}
		if got.Observed["gpu_tdp_w"] != 0 {
			t.Errorf("observed.gpu_tdp_w = %v, want 0", got.Observed["gpu_tdp_w"])
		}
	})

	t.Run("unknown CPU TDP 缺失", func(t *testing.T) {
		b := validBuild()
		b.CPU.TDPW = nil
		got := psuHeadroomRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"cpu.tdp_w"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown 有独显但 GPU TDP 缺失", func(t *testing.T) {
		b := validBuild()
		b.GPU.TDPW = nil
		got := psuHeadroomRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"gpu.tdp_w"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown 电源额定功率缺失且排序", func(t *testing.T) {
		b := validBuild()
		b.CPU.TDPW = nil
		b.PSU.WattageW = nil
		got := psuHeadroomRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"cpu.tdp_w", "psu.wattage_w"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})
}

func TestGPUPowerConnectors(t *testing.T) {
	t.Run("pass 基线单 16pin", func(t *testing.T) {
		got := gpuPowerConnectorsRule{}.Check(validBuild())
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("pass multiset 数量恰好相等(边界)", func(t *testing.T) {
		b := validBuild()
		b.GPU.PowerConnectors = []schemas.PowerConnector{
			schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe16Pin,
		}
		// PSU 恰好 [8pin,8pin,16pin]
		got := gpuPowerConnectorsRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("恰好相等应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("pass gpu 显式 null 无物可校", func(t *testing.T) {
		b := validBuild()
		b.GPU = nil
		got := gpuPowerConnectorsRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("无独显应 pass,得到 %s", got.Outcome)
		}
	})

	t.Run("pass 显卡已知无需外接供电(空集合)", func(t *testing.T) {
		b := validBuild()
		b.GPU.PowerConnectors = []schemas.PowerConnector{}
		got := gpuPowerConnectorsRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("空需求应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("fail 同类型数量不足不推断一分二", func(t *testing.T) {
		b := validBuild()
		b.GPU.PowerConnectors = []schemas.PowerConnector{
			schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe8Pin,
		}
		// PSU 只有 2 个 8pin,16pin 不可折算
		got := gpuPowerConnectorsRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("fail 类型不匹配不推断转接", func(t *testing.T) {
		b := validBuild()
		b.PSU.PowerConnectors = []schemas.PowerConnector{
			schemas.ConnectorPCIe8Pin, schemas.ConnectorPCIe8Pin,
		}
		// GPU 需要 16pin,PSU 只有 8pin
		got := gpuPowerConnectorsRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("fail 电源已知无 PCIe 供电(空集合)", func(t *testing.T) {
		b := validBuild()
		b.PSU.PowerConnectors = []schemas.PowerConnector{}
		got := gpuPowerConnectorsRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("空供给应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("unknown 双侧接口集合缺失且排序", func(t *testing.T) {
		b := validBuild()
		b.GPU.PowerConnectors = nil
		b.PSU.PowerConnectors = nil
		got := gpuPowerConnectorsRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"gpu.power_connectors", "psu.power_connectors"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})
}
