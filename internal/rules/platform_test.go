package rules

import (
	"reflect"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestSocketMatch(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		got := socketMatchRule{}.Check(validBuild())
		if got.Outcome != schemas.OutcomePass || got.Severity != schemas.SeverityNone {
			t.Errorf("应 pass/none,得到 %s/%s", got.Outcome, got.Severity)
		}
		want := map[string]any{"cpu_socket": "AM5", "motherboard_socket": "AM5"}
		if !reflect.DeepEqual(got.Observed, want) {
			t.Errorf("observed = %v, want %v", got.Observed, want)
		}
	})

	t.Run("fail", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.Socket = sp("LGA1851")
		got := socketMatchRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("unknown 双字段缺失且排序", func(t *testing.T) {
		b := validBuild()
		b.CPU.Socket = nil
		b.Motherboard.Socket = nil
		got := socketMatchRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown || got.Severity != schemas.SeverityNone {
			t.Errorf("应 unknown/none,得到 %s/%s", got.Outcome, got.Severity)
		}
		want := []string{"cpu.socket", "motherboard.socket"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})
}

func TestChipsetSupport(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		got := chipsetSupportRule{}.Check(validBuild())
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("fail 不在列表", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.Chipset = sp("Z890")
		got := chipsetSupportRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("fail 空集合已知为空", func(t *testing.T) {
		b := validBuild()
		b.CPU.SupportedChipsets = []string{}
		got := chipsetSupportRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail {
			t.Errorf("空集合 = 已知为空,应 fail 而非 unknown,得到 %s", got.Outcome)
		}
	})

	t.Run("unknown 集合为 nil", func(t *testing.T) {
		b := validBuild()
		b.CPU.SupportedChipsets = nil
		got := chipsetSupportRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("nil 集合 = 未知,应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"cpu.supported_chipsets"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown 主板芯片组缺失", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.Chipset = nil
		got := chipsetSupportRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
	})
}
