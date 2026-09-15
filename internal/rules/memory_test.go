package rules

import (
	"reflect"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestMemoryGeneration(t *testing.T) {
	t.Run("发布规格大小写不影响代际但保留原观察值", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.MemoryGeneration = sp("DDR5")
		b.Memory.Generation = sp("ddr5")
		got := memoryGenerationRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass || got.Observed["motherboard_memory_generation"] != "DDR5" {
			t.Fatalf("等价代际未通过或来源被改写: %+v", got)
		}
		b.Memory.Generation = sp("ddr4")
		if got = (memoryGenerationRule{}).Check(b); got.Outcome != schemas.OutcomeFail {
			t.Fatal("真实代际冲突不得通过")
		}
	})
	t.Run("pass", func(t *testing.T) {
		got := memoryGenerationRule{}.Check(validBuild())
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("fail 代际错配", func(t *testing.T) {
		b := validBuild()
		b.Memory.Generation = sp("ddr4")
		got := memoryGenerationRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("unknown 内存代际缺失", func(t *testing.T) {
		b := validBuild()
		b.Memory.Generation = nil
		got := memoryGenerationRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"memory.generation"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown 主板代际缺失", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.MemoryGeneration = nil
		got := memoryGenerationRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
	})
}

func TestMemorySpeed(t *testing.T) {
	t.Run("pass 低于上限", func(t *testing.T) {
		b := validBuild()
		b.Memory.SpeedMTS = ip(5600)
		got := memorySpeedRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s", got.Outcome)
		}
	})

	t.Run("pass 等于上限(边界)", func(t *testing.T) {
		b := validBuild() // 6000 == 6000
		got := memorySpeedRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("等于上限应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("fail 超上限只产生 warning", func(t *testing.T) {
		b := validBuild()
		b.Memory.SpeedMTS = ip(6400)
		got := memorySpeedRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityWarning {
			t.Errorf("应 fail/warning,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("超频率整体只到 review", func(t *testing.T) {
		b := validBuild()
		b.Memory.SpeedMTS = ip(6400)
		rep, err := NewDefaultEngine().Validate(b)
		if err != nil {
			t.Fatal(err)
		}
		if rep.OverallStatus != schemas.OverallReview {
			t.Errorf("仅 warning 级 fail,整体应 review,得到 %s", rep.OverallStatus)
		}
	})

	t.Run("unknown 上限缺失", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.MemorySpeedMaxMTS = nil
		got := memorySpeedRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"motherboard.memory_speed_max_mts"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})
}
