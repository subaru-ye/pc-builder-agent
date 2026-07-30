package rules

import (
	"slices"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// 引擎执行顺序必须是 schemas.AllRuleIDs 的子序列且无重复;
// 12 条全部就位后(B6)另有全量断言。
func TestEngineRuleOrder(t *testing.T) {
	rep, err := NewDefaultEngine().Validate(validBuild())
	if err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	if len(rep.Checks) == 0 {
		t.Fatal("报告不应为空")
	}
	pos := -1
	seen := map[schemas.RuleID]bool{}
	for _, c := range rep.Checks {
		if seen[c.RuleID] {
			t.Errorf("规则 %s 重复执行", c.RuleID)
		}
		seen[c.RuleID] = true
		idx := slices.Index(schemas.AllRuleIDs, c.RuleID)
		if idx < 0 {
			t.Errorf("未知规则 ID %s", c.RuleID)
			continue
		}
		if idx <= pos {
			t.Errorf("规则 %s 执行顺序违反固定顺序", c.RuleID)
		}
		pos = idx
	}
}

func TestEngineAllPassBaseline(t *testing.T) {
	rep, err := NewDefaultEngine().Validate(validBuild())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Checks {
		if c.Outcome != schemas.OutcomePass {
			t.Errorf("基线配置规则 %s 应 pass,得到 %s(%s)", c.RuleID, c.Outcome, c.Detail)
		}
	}
	if rep.OverallStatus != schemas.OverallPass {
		t.Errorf("基线整体应 pass,得到 %s", rep.OverallStatus)
	}
	if rep.BuildRef != "build_test" {
		t.Errorf("build_ref 透传错误: %s", rep.BuildRef)
	}
}

// 不短路:即使首条规则 error 级 fail,后续规则仍然全部执行。
func TestEngineNoShortCircuit(t *testing.T) {
	b := validBuild()
	b.Motherboard.Socket = sp("LGA1851") // 触发 #1 fail
	rep, err := NewDefaultEngine().Validate(b)
	if err != nil {
		t.Fatal(err)
	}
	full, err := NewDefaultEngine().Validate(validBuild())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checks) != len(full.Checks) {
		t.Errorf("fail 后不得短路:期望 %d 条检查,得到 %d", len(full.Checks), len(rep.Checks))
	}
	if c, ok := checkByID(rep, schemas.RuleSocketMatch); !ok || c.Outcome != schemas.OutcomeFail {
		t.Errorf("SOCKET_MATCH 应 fail,得到 %+v", c)
	}
	if rep.OverallStatus != schemas.OverallFail {
		t.Errorf("存在 error 级 fail,整体应为 fail,得到 %s", rep.OverallStatus)
	}
}

// Go error 仅表示输入结构非法。
func TestEngineStructureErrors(t *testing.T) {
	noRef := validBuild()
	noRef.BuildRef = ""

	noSSD := validBuild()
	noSSD.SSDs = nil

	badQty := validBuild()
	badQty.SSDs = []schemas.ResolvedSSD{{Spec: schemas.SSDSpec{}, Quantity: 0}}

	for name, b := range map[string]schemas.ResolvedBuild{
		"build_ref 为空": noRef, "ssd 为空": noSSD, "ssd 数量非正": badQty,
	} {
		if _, err := NewDefaultEngine().Validate(b); err == nil {
			t.Errorf("%s: 期望结构性 error,却校验成功", name)
		}
	}
}
