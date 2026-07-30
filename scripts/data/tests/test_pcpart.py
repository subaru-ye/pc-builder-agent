"""pc-part-dataset 适配器测试:只使用 tests/fixtures/upstream 冻结数据,不联网。"""

from pathlib import Path

import pytest

from pcdata.canonical import SpecError, validate_specs
from pcdata.pcpart import CATEGORY_FILES, find, load_raw, to_candidate

UPSTREAM = Path(__file__).parent / "fixtures" / "upstream"


class TestLoadRaw:
    def test_八类文件都能加载(self):
        for category in CATEGORY_FILES:
            records = load_raw(UPSTREAM, category)
            assert records, f"{category} fixture 为空"
            assert all("name" in r for r in records)

    def test_非法类目(self):
        with pytest.raises(SpecError, match="非法类目"):
            load_raw(UPSTREAM, "keyboard")

    def test_快照缺失给出指引(self, tmp_path):
        with pytest.raises(SpecError, match="上游快照缺失"):
            load_raw(tmp_path, "cpu")

    def test_缺name行失败(self, tmp_path):
        (tmp_path / "cpu.jsonl").write_text('{"price": 1}\n', encoding="utf-8")
        with pytest.raises(SpecError, match="缺少非空 name"):
            load_raw(tmp_path, "cpu")


class TestFind:
    def test_唯一命中(self):
        records = load_raw(UPSTREAM, "cpu")
        assert find(records, "AMD Ryzen 5 7600X")["tdp"] == 105

    def test_查无记录失败(self):
        with pytest.raises(SpecError, match="查无记录"):
            find(load_raw(UPSTREAM, "cpu"), "Imaginary CPU 9999")

    def test_同名多条立即失败(self):
        records = load_raw(UPSTREAM, "ssd")
        with pytest.raises(SpecError, match="重复.*2 条"):
            find(records, "Crucial P3 Plus")

    def test_同名同规格仅价格不同视为一条(self):
        # 上游存在同名同规格的重复行(仅美元价格不同);价格本就丢弃,不构成歧义。
        records = load_raw(UPSTREAM, "cpu")
        assert find(records, "AMD Ryzen 5 5500")["tdp"] == 65

    def test_判别字段消歧(self):
        records = load_raw(UPSTREAM, "ssd")
        assert find(records, "Crucial P3 Plus", capacity=2000)["price"] == 113.95
        gpus = load_raw(UPSTREAM, "gpu")
        hit = find(gpus, "Sapphire PULSE", chipset="Radeon RX 7600")
        assert hit["length"] == 240


class TestToCandidate:
    def test_cpu核显三态与tdp(self):
        records = load_raw(UPSTREAM, "cpu")
        with_igpu = to_candidate("cpu", find(records, "AMD Ryzen 7 9800X3D"))
        assert with_igpu == {"has_igpu": True, "tdp_w": 120}
        no_igpu = to_candidate("cpu", find(records, "Intel Core i5-12400F"))
        assert no_igpu == {"has_igpu": False, "tdp_w": 65}

    def test_gpu长度与chipset透传(self):
        records = load_raw(UPSTREAM, "gpu")
        cand = to_candidate("gpu", find(records, "MSI SHADOW 3X OC"))
        assert cand == {"chipset": "GeForce RTX 5070 Ti", "length_mm": 303}

    def test_主板板型映射(self):
        records = load_raw(UPSTREAM, "motherboard")
        assert to_candidate("motherboard", find(records, "MSI B650M MORTAR WIFI")) == {
            "socket": "AM5",
            "form_factor": "matx",
        }
        assert (
            to_candidate("motherboard", find(records, "Gigabyte B650I AORUS ULTRA"))[
                "form_factor"
            ]
            == "itx"
        )

    def test_主板未知板型立即失败(self):
        records = load_raw(UPSTREAM, "motherboard")
        with pytest.raises(SpecError, match="EATX.*无法映射"):
            to_candidate("motherboard", find(records, "Fake EATX Board"))

    def test_内存代际与速度(self):
        records = load_raw(UPSTREAM, "memory")
        assert to_candidate("memory", find(records, "Corsair Vengeance RGB 32 GB")) == {
            "generation": "ddr5",
            "speed_mts": 6000,
        }
        assert to_candidate("memory", find(records, "Corsair Vengeance LPX 16 GB")) == {
            "generation": "ddr4",
            "speed_mts": 3200,
        }

    def test_内存代际歧义失败(self):
        records = load_raw(UPSTREAM, "memory")
        with pytest.raises(SpecError, match="DDR 代际无法识别"):
            to_candidate("memory", find(records, "Broken Speed Kit"))

    def test_ssd形态映射(self):
        records = load_raw(UPSTREAM, "ssd")
        m2 = to_candidate("ssd", find(records, "Samsung 990 Pro"))
        assert m2 == {"form_factor": "m2"}
        sata = to_candidate("ssd", find(records, "Samsung 870 Evo"))
        assert sata == {"form_factor": "sata_2_5"}

    def test_机械盘拒收(self):
        records = load_raw(UPSTREAM, "ssd")
        with pytest.raises(SpecError, match="仅收录 SSD"):
            to_candidate("ssd", find(records, "Seagate BarraCuda"))

    def test_psu瓦数(self):
        records = load_raw(UPSTREAM, "psu")
        assert to_candidate("psu", find(records, "MSI MAG A650BN")) == {"wattage_w": 650}

    def test_机箱不产生候选字段(self):
        records = load_raw(UPSTREAM, "case")
        assert to_candidate("case", find(records, "Montech XR")) == {}

    def test_散热器仅冷排尺寸候选(self):
        records = load_raw(UPSTREAM, "cooler")
        aio = to_candidate("cooler", find(records, "ARCTIC Liquid Freezer III Pro 360"))
        assert aio == {"radiator_size_mm": 360}
        air = to_candidate("cooler", find(records, "Thermalright Peerless Assassin 120 SE"))
        assert air == {}

    def test_美元价格被丢弃(self):
        for category in CATEGORY_FILES:
            for raw in load_raw(UPSTREAM, category):
                try:
                    cand = to_candidate(category, raw)
                except SpecError:
                    continue  # 故意放入的歧义行
                assert "price" not in cand
                assert "price_per_gb" not in cand

    def test_单位歧义的小数立即失败(self):
        with pytest.raises(SpecError, match="单位歧义"):
            to_candidate("gpu", {"name": "x", "chipset": "RTX", "length": 240.5})

    def test_候选字段可直接进canonical校验(self):
        """适配器产出的字段名/值域必须与 canonical 字段字典兼容(补 null 后)。"""
        records = load_raw(UPSTREAM, "memory")
        cand = to_candidate("memory", find(records, "Corsair Vengeance RGB 32 GB"))
        validate_specs("memory", {"generation": None, "speed_mts": None} | cand)
