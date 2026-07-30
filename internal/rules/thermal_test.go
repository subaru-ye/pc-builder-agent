package rules

import (
	"reflect"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestDisplayOutput(t *testing.T) {
	t.Run("pass 有独显", func(t *testing.T) {
		got := displayOutputRule{}.Check(validBuild())
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
		if got.Observed["gpu_present"] != true {
			t.Errorf("observed.gpu_present = %v, want true", got.Observed["gpu_present"])
		}
	})

	t.Run("pass 有独显时核显未知不影响", func(t *testing.T) {
		b := validBuild()
		b.CPU.HasIGPU = nil
		got := displayOutputRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("有独显应 pass,得到 %s", got.Outcome)
		}
	})

	t.Run("pass gpu 显式 null 但有核显", func(t *testing.T) {
		b := validBuild()
		b.GPU = nil // 基线 has_igpu = true
		got := displayOutputRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("核显兜底应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
		if got.Observed["cpu_has_igpu"] != true {
			t.Errorf("observed.cpu_has_igpu = %v, want true", got.Observed["cpu_has_igpu"])
		}
	})

	t.Run("fail 无独显且无核显", func(t *testing.T) {
		b := validBuild()
		b.GPU = nil
		b.CPU.HasIGPU = bp(false)
		got := displayOutputRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("unknown 无独显且核显未知", func(t *testing.T) {
		b := validBuild()
		b.GPU = nil
		b.CPU.HasIGPU = nil
		got := displayOutputRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"cpu.has_igpu"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})
}

func TestCoolerThermalCapacity(t *testing.T) {
	t.Run("pass 解热高于 TDP", func(t *testing.T) {
		got := coolerThermalCapacityRule{}.Check(validBuild()) // 220 > 65
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("pass 解热等于 TDP(边界)", func(t *testing.T) {
		b := validBuild()
		b.Cooler.CoolingCapacityW = ip(65)
		got := coolerThermalCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("等于边界应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("fail 散热不足只产生 warning", func(t *testing.T) {
		b := validBuild()
		b.Cooler.CoolingCapacityW = ip(64)
		got := coolerThermalCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityWarning {
			t.Errorf("应 fail/warning,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("散热不足整体只到 review", func(t *testing.T) {
		b := validBuild()
		b.Cooler.CoolingCapacityW = ip(64)
		rep, err := NewDefaultEngine().Validate(b)
		if err != nil {
			t.Fatal(err)
		}
		if rep.OverallStatus != schemas.OverallReview {
			t.Errorf("仅 warning 级 fail,整体应 review,得到 %s", rep.OverallStatus)
		}
	})

	t.Run("unknown 双字段缺失且排序", func(t *testing.T) {
		b := validBuild()
		b.Cooler.CoolingCapacityW = nil
		b.CPU.TDPW = nil
		got := coolerThermalCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"cooler.cooling_capacity_w", "cpu.tdp_w"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})
}

// TestFullRegistryAllPass B6 收口:12 条规则全部就位,基线配置全 pass。
func TestFullRegistryAllPass(t *testing.T) {
	rep, err := NewDefaultEngine().Validate(validBuild())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checks) != len(schemas.AllRuleIDs) {
		t.Fatalf("registry 应含全部 %d 条规则,得到 %d", len(schemas.AllRuleIDs), len(rep.Checks))
	}
	for i, c := range rep.Checks {
		if c.RuleID != schemas.AllRuleIDs[i] {
			t.Errorf("第 %d 条规则应为 %s,得到 %s", i, schemas.AllRuleIDs[i], c.RuleID)
		}
		if c.Outcome != schemas.OutcomePass {
			t.Errorf("基线下 %s 应 pass,得到 %s(%s)", c.RuleID, c.Outcome, c.Detail)
		}
	}
	if rep.OverallStatus != schemas.OverallPass {
		t.Errorf("整体应 pass,得到 %s", rep.OverallStatus)
	}
}

// TestGPUNullTriState gpu 显式 null 时三条相关规则的行为(#5/#10 vacuous pass,#11 看核显)。
func TestGPUNullTriState(t *testing.T) {
	b := validBuild()
	b.GPU = nil
	rep, err := NewDefaultEngine().Validate(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []schemas.RuleID{
		schemas.RuleGPUClearance, schemas.RuleGPUPowerConnectors, schemas.RuleDisplayOutput,
	} {
		c, ok := checkByID(rep, id)
		if !ok {
			t.Fatalf("报告中缺少 %s", id)
		}
		if c.Outcome != schemas.OutcomePass {
			t.Errorf("gpu null + 核显 true 时 %s 应 pass,得到 %s(%s)", id, c.Outcome, c.Detail)
		}
	}
	if rep.OverallStatus != schemas.OverallPass {
		t.Errorf("整体应 pass,得到 %s", rep.OverallStatus)
	}
}
