package rules

import (
	"reflect"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestGPUClearance(t *testing.T) {
	t.Run("pass 等于限长(边界)", func(t *testing.T) {
		got := gpuClearanceRule{}.Check(validBuild()) // 300 == 300
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("等于限长应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("pass 低于限长", func(t *testing.T) {
		b := validBuild()
		b.GPU.LengthMM = ip(250)
		got := gpuClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s", got.Outcome)
		}
	})

	t.Run("pass gpu 显式 null 无物可校", func(t *testing.T) {
		b := validBuild()
		b.GPU = nil
		got := gpuClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("无独显应 pass,得到 %s", got.Outcome)
		}
		if got.Observed["gpu_present"] != false {
			t.Errorf("observed.gpu_present 应为 false,得到 %v", got.Observed["gpu_present"])
		}
	})

	t.Run("fail 超长", func(t *testing.T) {
		b := validBuild()
		b.GPU.LengthMM = ip(301)
		got := gpuClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("unknown 双字段缺失且排序", func(t *testing.T) {
		b := validBuild()
		b.GPU.LengthMM = nil
		b.Case.GPULengthMaxMM = nil
		got := gpuClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"case.gpu_length_max_mm", "gpu.length_mm"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})
}

func TestCoolerClearance(t *testing.T) {
	t.Run("pass 风冷低于限高", func(t *testing.T) {
		got := coolerClearanceRule{}.Check(validBuild()) // 155 < 160
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("pass 风冷等于限高(边界)", func(t *testing.T) {
		b := validBuild()
		b.Cooler.HeightMM = ip(160)
		got := coolerClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("等于限高应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("fail 风冷超高", func(t *testing.T) {
		b := validBuild()
		b.Cooler.HeightMM = ip(161)
		got := coolerClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("pass AIO 冷排在支持位", func(t *testing.T) {
		b := aioBuild(360)
		got := coolerClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("fail AIO 冷排不在支持位", func(t *testing.T) {
		b := aioBuild(280) // 机箱只支持 240/360
		got := coolerClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("fail AIO 机箱冷排位已知为空", func(t *testing.T) {
		b := aioBuild(240)
		b.Case.RadiatorSizesMM = []int{} // 空集合 = 已知不支持任何冷排
		got := coolerClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("空集合应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("unknown 散热器类型缺失", func(t *testing.T) {
		b := validBuild()
		b.Cooler.Type = nil
		got := coolerClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"cooler.type"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown 风冷分支字段缺失", func(t *testing.T) {
		b := validBuild()
		b.Cooler.HeightMM = nil
		b.Case.CoolerHeightMaxMM = nil
		got := coolerClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"case.cooler_height_max_mm", "cooler.height_mm"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown AIO 分支字段缺失", func(t *testing.T) {
		b := aioBuild(240)
		b.Cooler.RadiatorSizeMM = nil
		b.Case.RadiatorSizesMM = nil // nil 集合 = 未知
		got := coolerClearanceRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"case.radiator_sizes_mm", "cooler.radiator_size_mm"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})
}

func TestFormFactorSupport(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		got := formFactorSupportRule{}.Check(validBuild())
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("fail 板型不支持", func(t *testing.T) {
		b := validBuild()
		b.Case.SupportedFormFactors = []schemas.FormFactor{schemas.FormFactorITX}
		got := formFactorSupportRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("fail 支持集合已知为空", func(t *testing.T) {
		b := validBuild()
		b.Case.SupportedFormFactors = []schemas.FormFactor{}
		got := formFactorSupportRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("空集合应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("unknown 主板板型缺失", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.FormFactor = nil
		got := formFactorSupportRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"motherboard.form_factor"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown 机箱支持集合缺失", func(t *testing.T) {
		b := validBuild()
		b.Case.SupportedFormFactors = nil
		got := formFactorSupportRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
	})
}

// aioBuild 在风冷基线上切换为 AIO 冷排配置(size 为冷排尺寸 mm)。
func aioBuild(size int) schemas.ResolvedBuild {
	b := validBuild()
	b.Cooler = schemas.CoolerSpec{
		Type:             ctp(schemas.CoolerTypeAIO),
		RadiatorSizeMM:   ip(size),
		CoolingCapacityW: ip(280),
	}
	return b
}
