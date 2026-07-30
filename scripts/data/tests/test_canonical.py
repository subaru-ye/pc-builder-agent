"""canonical 校验器单测:只使用内联数据与冻结 fixture,不依赖网络。"""

import pytest

from pcdata.canonical import SpecError, validate_part, validate_specs


def cpu_specs(**overrides):
    base = {
        "socket": "AM5",
        "supported_chipsets": ["B650", "X670"],
        "has_igpu": True,
        "tdp_w": 65,
    }
    base.update(overrides)
    return base


class TestValidateSpecs:
    def test_八类合法样例全通过(self):
        samples = {
            "cpu": cpu_specs(),
            "gpu": {"length_mm": 300, "tdp_w": 220, "power_connectors": ["pcie_16pin"]},
            "motherboard": {
                "socket": "AM5",
                "chipset": "B650",
                "memory_generation": "ddr5",
                "memory_speed_max_mts": 6000,
                "form_factor": "matx",
                "m2_slots": 2,
            },
            "memory": {"generation": "ddr5", "speed_mts": 6000},
            "ssd": {"form_factor": "m2"},
            "psu": {"wattage_w": 750, "power_connectors": ["pcie_8pin", "pcie_8pin"]},
            "case": {
                "gpu_length_max_mm": 300,
                "cooler_height_max_mm": 160,
                "supported_form_factors": ["atx", "matx", "itx"],
                "radiator_sizes_mm": [240, 360],
            },
            "cooler": {
                "type": "air",
                "height_mm": 155,
                "radiator_size_mm": None,
                "cooling_capacity_w": 220,
            },
        }
        for category, specs in samples.items():
            validate_specs(category, specs)  # 不抛即通过

    def test_标量null为未知(self):
        validate_specs("cpu", cpu_specs(socket=None, has_igpu=None, tdp_w=None))

    def test_集合null未知_空集合已知为空(self):
        validate_specs("cpu", cpu_specs(supported_chipsets=None))
        validate_specs("gpu", {"length_mm": 300, "tdp_w": 220, "power_connectors": []})

    def test_m2_slots允许为零(self):
        validate_specs(
            "motherboard",
            {
                "socket": "AM5",
                "chipset": "B650",
                "memory_generation": "ddr5",
                "memory_speed_max_mts": None,
                "form_factor": "itx",
                "m2_slots": 0,
            },
        )

    def test_非法类目(self):
        with pytest.raises(SpecError, match="非法类目"):
            validate_specs("keyboard", {})

    def test_未知字段报错(self):
        with pytest.raises(SpecError, match="未知字段.*price_usd"):
            validate_specs("cpu", cpu_specs(price_usd=299))

    def test_缺少字段报错(self):
        specs = cpu_specs()
        del specs["tdp_w"]
        with pytest.raises(SpecError, match="缺少字段.*tdp_w"):
            validate_specs("cpu", specs)

    def test_空字符串报错(self):
        with pytest.raises(SpecError, match="不得为空字符串"):
            validate_specs("cpu", cpu_specs(socket=""))

    def test_非正整数报错(self):
        with pytest.raises(SpecError, match="必须为正整数"):
            validate_specs("cpu", cpu_specs(tdp_w=0))

    def test_布尔字段拒绝整数(self):
        with pytest.raises(SpecError, match="必须为布尔"):
            validate_specs("cpu", cpu_specs(has_igpu=1))

    def test_整数字段拒绝布尔(self):
        with pytest.raises(SpecError, match="必须为整数"):
            validate_specs("cpu", cpu_specs(tdp_w=True))

    def test_整数字段拒绝浮点(self):
        with pytest.raises(SpecError, match="必须为整数"):
            validate_specs("cpu", cpu_specs(tdp_w=65.0))

    def test_非法供电接口枚举(self):
        with pytest.raises(SpecError, match="非法枚举.*12vhpwr"):
            validate_specs(
                "gpu",
                {"length_mm": 300, "tdp_w": 450, "power_connectors": ["12vhpwr"]},
            )

    def test_非法板型枚举(self):
        with pytest.raises(SpecError, match="非法枚举.*eatx"):
            validate_specs(
                "case",
                {
                    "gpu_length_max_mm": 300,
                    "cooler_height_max_mm": 160,
                    "supported_form_factors": ["eatx"],
                    "radiator_sizes_mm": None,
                },
            )

    def test_集合元素不得为null(self):
        with pytest.raises(SpecError, match=r"supported_chipsets\[1\].*不得为 null"):
            validate_specs("cpu", cpu_specs(supported_chipsets=["B650", None]))


def valid_part(**overrides):
    base = {
        "sku": "cpu-r5-7600",
        "category": "cpu",
        "brand": "AMD",
        "model": "Ryzen 5 7600",
        "schema_version": 1,
        "specs": cpu_specs(),
        "source_meta": {"all": "fixture"},
    }
    base.update(overrides)
    return base


class TestValidatePart:
    def test_合法记录通过(self):
        validate_part(valid_part())

    def test_未知键报错(self):
        with pytest.raises(SpecError, match="未知键.*price"):
            validate_part(valid_part(price=1299))

    def test_缺少键报错(self):
        record = valid_part()
        del record["source_meta"]
        with pytest.raises(SpecError, match="缺少键.*source_meta"):
            validate_part(record)

    def test_sku为空报错(self):
        with pytest.raises(SpecError, match="sku 必须为非空字符串"):
            validate_part(valid_part(sku=""))

    def test_schema_version必须为1(self):
        with pytest.raises(SpecError, match="schema_version 必须为 1"):
            validate_part(valid_part(schema_version=2))

    def test_source_meta不得为空对象(self):
        with pytest.raises(SpecError, match="source_meta"):
            validate_part(valid_part(source_meta={}))

    def test_specs非法向上抛(self):
        with pytest.raises(SpecError, match="必须为正整数"):
            validate_part(valid_part(specs=cpu_specs(tdp_w=-1)))
