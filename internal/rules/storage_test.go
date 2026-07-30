package rules

import (
	"reflect"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func TestM2SlotCapacity(t *testing.T) {
	t.Run("pass 单盘低于槽位数", func(t *testing.T) {
		got := m2SlotCapacityRule{}.Check(validBuild()) // 1 M.2 < 2 槽
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("pass quantity 累计等于槽位数(边界)", func(t *testing.T) {
		b := validBuild()
		b.SSDs = []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 2},
		}
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("等于槽位数应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
		if got.Observed["m2_ssd_count"] != 2 {
			t.Errorf("observed.m2_ssd_count = %v, want 2", got.Observed["m2_ssd_count"])
		}
	})

	t.Run("pass 多条目累计等于槽位数(边界)", func(t *testing.T) {
		b := validBuild()
		b.SSDs = []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 1},
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 1},
		}
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("累计等于槽位数应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})

	t.Run("pass SATA 盘不占 M.2 槽位", func(t *testing.T) {
		b := validBuild()
		b.SSDs = []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 2},
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorSATA25)}, Quantity: 3},
		}
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("SATA 不计入,应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
		if got.Observed["m2_ssd_count"] != 2 {
			t.Errorf("observed.m2_ssd_count = %v, want 2", got.Observed["m2_ssd_count"])
		}
	})

	t.Run("fail 单条目 quantity 超槽位", func(t *testing.T) {
		b := validBuild()
		b.SSDs = []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 3},
		}
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("fail 多条目累计超槽位", func(t *testing.T) {
		b := validBuild()
		b.SSDs = []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 2},
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 1},
		}
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomeFail || got.Severity != schemas.SeverityError {
			t.Errorf("累计 3 > 2 应 fail/error,得到 %s/%s", got.Outcome, got.Severity)
		}
	})

	t.Run("unknown SSD 形态缺失", func(t *testing.T) {
		b := validBuild()
		b.SSDs = []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorM2)}, Quantity: 1},
			{Spec: schemas.SSDSpec{FormFactor: nil}, Quantity: 1},
		}
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"ssd.form_factor"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown 主板槽位数缺失", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.M2Slots = nil
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"motherboard.m2_slots"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("unknown 双缺失且排序", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.M2Slots = nil
		b.SSDs = []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: nil}, Quantity: 1},
		}
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomeUnknown {
			t.Errorf("应 unknown,得到 %s", got.Outcome)
		}
		want := []string{"motherboard.m2_slots", "ssd.form_factor"}
		if !reflect.DeepEqual(got.MissingFields, want) {
			t.Errorf("missing_fields = %v, want %v", got.MissingFields, want)
		}
	})

	t.Run("pass 槽位数为 0 且无 M.2 盘", func(t *testing.T) {
		b := validBuild()
		b.Motherboard.M2Slots = ip(0)
		b.SSDs = []schemas.ResolvedSSD{
			{Spec: schemas.SSDSpec{FormFactor: sfp(schemas.SSDFormFactorSATA25)}, Quantity: 1},
		}
		got := m2SlotCapacityRule{}.Check(b)
		if got.Outcome != schemas.OutcomePass {
			t.Errorf("0 槽位 + 全 SATA 应 pass,得到 %s(%s)", got.Outcome, got.Detail)
		}
	})
}
