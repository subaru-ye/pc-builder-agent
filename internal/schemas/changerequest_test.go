package schemas

import (
	"strings"
	"testing"
)

func TestDecodeChangeRequest_SwapPart(t *testing.T) {
	cr, err := DecodeChangeRequest([]byte(`{
		"schema_version": 1,
		"base_build_ref": "v1",
		"intent": "swap_part",
		"swap": {"category": "gpu", "target_hint": "AMD 显卡"},
		"notes": "换成 A 卡"
	}`))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if cr.Intent != IntentSwapPart || cr.Swap == nil {
		t.Fatalf("intent/swap 解码不符: %+v", cr)
	}
	if cr.Swap.Category != CategoryGPU || cr.Swap.TargetHint != "AMD 显卡" {
		t.Fatalf("swap 内容不符: %+v", cr.Swap)
	}
	// swap_part 锁定全集 = 除 gpu 外的七类。
	locked := cr.HardLocked()
	if len(locked) != len(AllCategories)-1 {
		t.Fatalf("swap_part 硬锁定应为七类,得到 %v", locked)
	}
	for _, c := range locked {
		if c == CategoryGPU {
			t.Fatalf("解锁品类 gpu 不应出现在硬锁定集: %v", locked)
		}
	}
}

func TestDecodeChangeRequest_AdjustBudget(t *testing.T) {
	cr, err := DecodeChangeRequest([]byte(`{
		"schema_version": 1,
		"base_build_ref": "v1",
		"intent": "adjust_budget",
		"budget_delta_cny": -500,
		"locked_categories": ["cpu", "motherboard"]
	}`))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if cr.BudgetDeltaCNY == nil || *cr.BudgetDeltaCNY != -500 {
		t.Fatalf("budget_delta_cny 不符: %+v", cr.BudgetDeltaCNY)
	}
	// 非 swap 意图:硬锁定 = 显式 locked_categories。
	locked := cr.HardLocked()
	if len(locked) != 2 || locked[0] != CategoryCPU || locked[1] != CategoryMotherboard {
		t.Fatalf("硬锁定应为显式两类,得到 %v", locked)
	}
}

func TestDecodeChangeRequest_ChangeConstraint(t *testing.T) {
	cr, err := DecodeChangeRequest([]byte(`{
		"schema_version": 1,
		"base_build_ref": "v2",
		"intent": "change_constraint",
		"constraint_patch": {"noise_pref": "silent"}
	}`))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if len(cr.ConstraintPatch) == 0 {
		t.Fatalf("constraint_patch 应保留原文: %+v", cr)
	}
}

func TestDecodeChangeRequest_Errors(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantMsg string
	}{
		{"未知字段拒绝", `{"schema_version":1,"base_build_ref":"v1","intent":"swap_part","swap":{"category":"gpu"},"extra":1}`, "extra"},
		{"缺 base_build_ref", `{"schema_version":1,"intent":"adjust_budget","budget_delta_cny":-500}`, "base_build_ref"},
		{"非法意图枚举", `{"schema_version":1,"base_build_ref":"v1","intent":"repaint"}`, "非法改单意图"},
		{"swap_part 缺 swap", `{"schema_version":1,"base_build_ref":"v1","intent":"swap_part"}`, "必须提供 swap"},
		{"swap_part 夹带预算", `{"schema_version":1,"base_build_ref":"v1","intent":"swap_part","swap":{"category":"gpu"},"budget_delta_cny":-500}`, "不得携带"},
		{"swap 品类非法", `{"schema_version":1,"base_build_ref":"v1","intent":"swap_part","swap":{"category":"mobo"}}`, "swap.category"},
		{"adjust_budget 缺 delta", `{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget"}`, "budget_delta_cny"},
		{"delta 为 0", `{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget","budget_delta_cny":0}`, "不得为 0"},
		{"change_constraint 缺 patch", `{"schema_version":1,"base_build_ref":"v1","intent":"change_constraint"}`, "constraint_patch"},
		{"patch 空对象", `{"schema_version":1,"base_build_ref":"v1","intent":"change_constraint","constraint_patch":{}}`, "不得为空对象"},
		{"patch 越界改预算", `{"schema_version":1,"base_build_ref":"v1","intent":"change_constraint","constraint_patch":{"budget_cny":7500}}`, "adjust_budget"},
		{"locked 品类非法", `{"schema_version":1,"base_build_ref":"v1","intent":"adjust_budget","budget_delta_cny":-500,"locked_categories":["mobo"]}`, "locked_categories"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeChangeRequest([]byte(tc.json))
			if err == nil {
				t.Fatalf("应报错,却解码成功")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("错误信息应含 %q,得到: %v", tc.wantMsg, err)
			}
		})
	}
}
