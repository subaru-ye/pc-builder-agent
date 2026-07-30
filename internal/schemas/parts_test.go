package schemas

import (
	"testing"
)

func TestDecodeCPUSpecValid(t *testing.T) {
	got, err := DecodeCPUSpec([]byte(`{
		"socket": "AM5",
		"supported_chipsets": ["B650", "X670"],
		"has_igpu": false,
		"tdp_w": 65
	}`))
	if err != nil {
		t.Fatalf("合法 CPU specs 不应报错: %v", err)
	}
	if got.Socket == nil || *got.Socket != "AM5" {
		t.Errorf("socket 解析错误: %+v", got.Socket)
	}
	if got.TDPW == nil || *got.TDPW != 65 {
		t.Errorf("tdp_w 解析错误: %+v", got.TDPW)
	}
}

// null 语义:标量 null → nil 指针;集合 null → nil 切片;空集合 → 非 nil 空切片。
func TestNullSemantics(t *testing.T) {
	allNull, err := DecodeCPUSpec([]byte(`{"socket": null, "supported_chipsets": null, "has_igpu": null, "tdp_w": null}`))
	if err != nil {
		t.Fatalf("全 null 合法(未知): %v", err)
	}
	if allNull.Socket != nil || allNull.HasIGPU != nil || allNull.TDPW != nil {
		t.Errorf("标量 null 应为 nil 指针: %+v", allNull)
	}
	if allNull.SupportedChipsets != nil {
		t.Errorf("集合 null 应为 nil 切片(未知): %+v", allNull.SupportedChipsets)
	}

	empty, err := DecodeCPUSpec([]byte(`{"supported_chipsets": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if empty.SupportedChipsets == nil || len(empty.SupportedChipsets) != 0 {
		t.Errorf("空集合应为非 nil 空切片(已知为空): %#v", empty.SupportedChipsets)
	}

	// 字段整体缺失同样是未知(与显式 null 等价)。
	missing, err := DecodeCPUSpec([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Socket != nil || missing.SupportedChipsets != nil {
		t.Errorf("缺失字段应为 nil: %+v", missing)
	}
}

func TestDecodeSpecErrors(t *testing.T) {
	cases := []struct {
		name   string
		decode func() error
	}{
		{"cpu 未知字段", func() error { _, err := DecodeCPUSpec([]byte(`{"boost_clock": 5.0}`)); return err }},
		{"cpu tdp 非正", func() error { _, err := DecodeCPUSpec([]byte(`{"tdp_w": 0}`)); return err }},
		{"cpu socket 空串", func() error { _, err := DecodeCPUSpec([]byte(`{"socket": ""}`)); return err }},
		{"cpu 芯片组含空串", func() error { _, err := DecodeCPUSpec([]byte(`{"supported_chipsets": [""]}`)); return err }},
		{"gpu 长度为负", func() error { _, err := DecodeGPUSpec([]byte(`{"length_mm": -1}`)); return err }},
		{"gpu 非法接口枚举", func() error {
			_, err := DecodeGPUSpec([]byte(`{"power_connectors": ["12vhpwr"]}`))
			return err
		}},
		{"主板非法板型", func() error { _, err := DecodeMotherboardSpec([]byte(`{"form_factor": "eatx"}`)); return err }},
		{"主板 m2 槽位为负", func() error { _, err := DecodeMotherboardSpec([]byte(`{"m2_slots": -1}`)); return err }},
		{"内存频率非正", func() error { _, err := DecodeMemorySpec([]byte(`{"speed_mts": 0}`)); return err }},
		{"ssd 非法形态", func() error { _, err := DecodeSSDSpec([]byte(`{"form_factor": "u2"}`)); return err }},
		{"psu 功率非正", func() error { _, err := DecodePSUSpec([]byte(`{"wattage_w": -650}`)); return err }},
		{"机箱冷排尺寸非正", func() error { _, err := DecodeCaseSpec([]byte(`{"radiator_sizes_mm": [0]}`)); return err }},
		{"散热器非法类型", func() error { _, err := DecodeCoolerSpec([]byte(`{"type": "passive"}`)); return err }},
		{"散热器解热非正", func() error { _, err := DecodeCoolerSpec([]byte(`{"cooling_capacity_w": 0}`)); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.decode(); err == nil {
				t.Errorf("期望输入错误,却解析成功")
			}
		})
	}
}

// M.2 槽位数 0 是合法值(已知为 0),区别于 null(未知)。
func TestMotherboardZeroM2SlotsValid(t *testing.T) {
	got, err := DecodeMotherboardSpec([]byte(`{"m2_slots": 0}`))
	if err != nil {
		t.Fatalf("m2_slots=0 合法: %v", err)
	}
	if got.M2Slots == nil || *got.M2Slots != 0 {
		t.Errorf("m2_slots 解析错误: %+v", got.M2Slots)
	}
}

func TestConnectorEnumValid(t *testing.T) {
	got, err := DecodePSUSpec([]byte(`{"wattage_w": 750, "power_connectors": ["pcie_8pin", "pcie_8pin", "pcie_16pin"]}`))
	if err != nil {
		t.Fatalf("合法接口枚举不应报错: %v", err)
	}
	if len(got.PowerConnectors) != 3 {
		t.Errorf("multiset 应保留重复项: %v", got.PowerConnectors)
	}
}
