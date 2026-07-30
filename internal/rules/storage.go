package rules

import (
	"fmt"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// m2SlotCapacityRule 规则 #9 M2_SLOT_CAPACITY(错误级):
// 全部 M.2 SSD 的 quantity 累计 ≤ 主板 M.2 槽位数;等于槽位数即通过。
// sata_2_5 形态不占 M.2 槽位,不计入累计;任一 SSD 形态未知则无法准确计数,整条 unknown。
type m2SlotCapacityRule struct{}

func (m2SlotCapacityRule) ID() schemas.RuleID { return schemas.RuleM2SlotCapacity }

func (m2SlotCapacityRule) Check(b schemas.ResolvedBuild) schemas.CheckResult {
	observed := map[string]any{}
	var missing []string

	m2Count := 0
	formFactorKnown := true
	for _, s := range b.SSDs {
		if s.Spec.FormFactor == nil {
			formFactorKnown = false
			continue
		}
		if *s.Spec.FormFactor == schemas.SSDFormFactorM2 {
			m2Count += s.Quantity
		}
	}
	if formFactorKnown {
		observed["m2_ssd_count"] = m2Count
	} else {
		missing = append(missing, "ssd.form_factor")
	}

	if b.Motherboard.M2Slots == nil {
		missing = append(missing, "motherboard.m2_slots")
	} else {
		observed["motherboard_m2_slots"] = *b.Motherboard.M2Slots
	}

	if len(missing) > 0 {
		return unknown(schemas.RuleM2SlotCapacity, observed, missing, "M.2 槽位字段缺失,无法判定")
	}
	if m2Count <= *b.Motherboard.M2Slots {
		return pass(schemas.RuleM2SlotCapacity, observed, "M.2 SSD 数量不超过主板槽位数")
	}
	return fail(schemas.RuleM2SlotCapacity, schemas.SeverityError, observed,
		fmt.Sprintf("M.2 SSD 共 %d 块超过主板 M.2 槽位 %d 个", m2Count, *b.Motherboard.M2Slots))
}
