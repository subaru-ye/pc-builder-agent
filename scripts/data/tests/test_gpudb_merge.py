"""D3 测试:dbgpu 显式映射适配器 + 三层合并器。

dbgpu 默认数据库随包分发、离线可用,不访问网络(符合 CI 门禁);
chipset 映射表在测试内显式给出,不做任何 fuzzy。
"""

import pytest

from pcdata.canonical import SpecError
from pcdata.gpudb import chip_candidate, load_database, parse_power_connectors
from pcdata.merge import merge_specs


@pytest.fixture(scope="module")
def db():
    return load_database()


CHIPSET_MAP = {
    "GeForce RTX 4070": "GeForce RTX 4070",
    "GeForce RTX 5070 Ti": "GeForce RTX 5070 Ti",
    "Radeon RX 7600": "Radeon RX 7600",
    "GeForce RTX 3060 12GB": "GeForce RTX 3060 12 GB",  # pc-part 与 TPU 写法差异,显式映射
    "Ghost Chip": "No Such GPU",
}


class TestParsePowerConnectors:
    def test_单16pin(self):
        assert parse_power_connectors("1x 16-pin") == ["pcie_16pin"]

    def test_多8pin组合(self):
        assert parse_power_connectors("2x 8-pin + 1x 16-pin") == [
            "pcie_8pin",
            "pcie_8pin",
            "pcie_16pin",
        ]

    def test_12pin歧义失败(self):
        with pytest.raises(SpecError, match="非 8/16-pin"):
            parse_power_connectors("1x 12-pin")

    def test_6pin歧义失败(self):
        with pytest.raises(SpecError, match="非 8/16-pin"):
            parse_power_connectors("1x 6-pin")

    def test_无法解析文本失败(self):
        with pytest.raises(SpecError, match="无法解析"):
            parse_power_connectors("300 W")


class TestChipCandidate:
    def test_rtx4070芯片候选(self, db):
        cand = chip_candidate(db, "GeForce RTX 4070", CHIPSET_MAP)
        assert cand["tdp_w"] == 200
        assert cand["power_connectors"] == ["pcie_16pin"]
        assert cand["length_mm"] == 240

    def test_radeon8pin(self, db):
        cand = chip_candidate(db, "Radeon RX 7600", CHIPSET_MAP)
        assert cand["tdp_w"] == 165
        assert cand["power_connectors"] == ["pcie_8pin"]

    def test_写法差异靠显式映射(self, db):
        # RTX 3060 12GB 是 12-pin(Ampere FE):TDP 可用,接口必须显式失败。
        with pytest.raises(SpecError, match="非 8/16-pin"):
            chip_candidate(db, "GeForce RTX 3060 12GB", CHIPSET_MAP)

    def test_override已裁定字段跳过芯片层解析(self, db):
        # 接口已被人工 override 时,12-pin 文本不再报错,其余字段照常产出。
        cand = chip_candidate(
            db, "GeForce RTX 3060 12GB", CHIPSET_MAP,
            skip_fields=frozenset({"power_connectors"}),
        )
        assert cand["tdp_w"] == 170
        assert "power_connectors" not in cand

    def test_未映射chipset立即失败(self, db):
        with pytest.raises(SpecError, match="未在显式映射表"):
            chip_candidate(db, "GeForce RTX 9999", CHIPSET_MAP)

    def test_映射到不存在芯片失败(self, db):
        with pytest.raises(SpecError, match="dbgpu 查无芯片"):
            chip_candidate(db, "Ghost Chip", CHIPSET_MAP)


class TestMergeSpecs:
    def test_gpu三层优先级(self):
        specs, meta = merge_specs(
            "gpu",
            override={"power_connectors": ["pcie_16pin"]},
            product={"length_mm": 303},
            chip={"tdp_w": 300, "power_connectors": ["pcie_8pin"], "length_mm": 304},
        )
        assert specs == {
            "length_mm": 303,  # 产品行 > 芯片公版
            "tdp_w": 300,  # 芯片兜底
            "power_connectors": ["pcie_16pin"],  # override 压制 dbgpu
        }
        assert meta == {
            "length_mm": "pc-part-dataset",
            "tdp_w": "dbgpu",
            "power_connectors": "override",
        }

    def test_无覆盖字段落null(self):
        specs, meta = merge_specs("gpu", override={}, product={"length_mm": 240})
        assert specs["tdp_w"] is None
        assert meta["tdp_w"] == "null"
        assert meta["power_connectors"] == "null"

    def test_override显式null压制下层(self):
        specs, meta = merge_specs(
            "gpu",
            override={"length_mm": None},  # 人工裁定:上游长度不可信,记未知
            product={"length_mm": 240},
        )
        assert specs["length_mm"] is None
        assert meta["length_mm"] == "override"

    def test_product层不得携带null(self):
        with pytest.raises(SpecError, match=r"product\(pc-part\) 层不得携带 null"):
            merge_specs("gpu", override={}, product={"length_mm": None})

    def test_未知字段立即失败(self):
        with pytest.raises(SpecError, match="override 层含未知字段.*chipset"):
            merge_specs("gpu", override={"chipset": "RTX"}, product={})

    def test_合并结果过canonical校验(self):
        with pytest.raises(SpecError, match="必须为正整数"):
            merge_specs("gpu", override={"tdp_w": -5}, product={})

    def test_非gpu类目同样可用(self):
        specs, meta = merge_specs(
            "motherboard",
            override={"chipset": "B650", "memory_generation": "ddr5", "m2_slots": 2,
                      "memory_speed_max_mts": 6000},
            product={"socket": "AM5", "form_factor": "matx"},
        )
        assert specs["socket"] == "AM5"
        assert specs["chipset"] == "B650"
        assert meta["socket"] == "pc-part-dataset"
        assert meta["chipset"] == "override"
