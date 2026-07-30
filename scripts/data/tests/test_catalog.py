"""D4 测试:selection 文件校验、目录构建器,以及已提交 selection 的静态门禁。

构建测试只用冻结 fixture 上游(不下载);已提交的 catalog/*.json 做结构与
override 字段合法性静态检查(真实构建需完整快照,发生在本地 D4 任务中)。
"""

import json
from pathlib import Path

import pytest

from pcdata.canonical import SPEC_FIELDS, SpecError
from pcdata.catalog import (
    DEFAULT_CATALOG_DIR,
    build_category,
    load_selection,
    write_parts_jsonl,
)
from pcdata.coverage import EXPECTED_PER_CATEGORY, load_parts_jsonl

UPSTREAM = Path(__file__).parent / "fixtures" / "upstream"


def _write_selection(tmp_path: Path, doc: dict) -> Path:
    path = tmp_path / f"{doc.get('category', 'bad')}.json"
    path.write_text(json.dumps(doc, ensure_ascii=False), encoding="utf-8")
    return path


CPU_SELECTION = {
    "schema_version": 1,
    "category": "cpu",
    "entries": [
        {
            "sku": "cpu-r7-9800x3d",
            "brand": "AMD",
            "model": "Ryzen 7 9800X3D",
            "upstream_name": "AMD Ryzen 7 9800X3D",
            "override": {"socket": "AM5", "supported_chipsets": ["B650", "X670"]},
        },
        {
            "sku": "cpu-i5-12400f",
            "brand": "Intel",
            "model": "Core i5-12400F",
            "upstream_name": "Intel Core i5-12400F",
            "override": {"socket": "LGA1700", "supported_chipsets": ["B760", "Z790"]},
        },
    ],
}


class TestLoadSelection:
    def test_合法文件(self, tmp_path):
        doc = load_selection(_write_selection(tmp_path, CPU_SELECTION))
        assert doc["category"] == "cpu"
        assert len(doc["entries"]) == 2

    def test_schema_version非法(self, tmp_path):
        bad = {**CPU_SELECTION, "schema_version": 2}
        with pytest.raises(SpecError, match="schema_version 必须为 1"):
            load_selection(_write_selection(tmp_path, bad))

    def test_entry未知键失败(self, tmp_path):
        entry = {**CPU_SELECTION["entries"][0], "price": 100}
        bad = {**CPU_SELECTION, "entries": [entry]}
        with pytest.raises(SpecError, match="含未知键.*price"):
            load_selection(_write_selection(tmp_path, bad))

    def test_entry缺键失败(self, tmp_path):
        entry = {k: v for k, v in CPU_SELECTION["entries"][0].items() if k != "override"}
        bad = {**CPU_SELECTION, "entries": [entry]}
        with pytest.raises(SpecError, match="缺少键.*override"):
            load_selection(_write_selection(tmp_path, bad))

    def test_重复sku失败(self, tmp_path):
        bad = {**CPU_SELECTION, "entries": [CPU_SELECTION["entries"][0]] * 2}
        with pytest.raises(SpecError, match="重复 SKU"):
            load_selection(_write_selection(tmp_path, bad))


class TestBuildCategory:
    def test_cpu构建与来源标注(self):
        parts = build_category(CPU_SELECTION, UPSTREAM)
        # 输出按 sku 稳定排序。
        assert [p["sku"] for p in parts] == ["cpu-i5-12400f", "cpu-r7-9800x3d"]
        amd = parts[1]
        assert amd["specs"] == {
            "socket": "AM5",
            "supported_chipsets": ["B650", "X670"],
            "has_igpu": True,
            "tdp_w": 120,
        }
        assert amd["source_meta"] == {
            "socket": "override",
            "supported_chipsets": "override",
            "has_igpu": "pc-part-dataset",
            "tdp_w": "pc-part-dataset",
        }

    def test_motherboard构建(self):
        selection = {
            "schema_version": 1,
            "category": "motherboard",
            "entries": [
                {
                    "sku": "mb-b650m-mortar",
                    "brand": "MSI",
                    "model": "B650M MORTAR WIFI",
                    "upstream_name": "MSI B650M MORTAR WIFI",
                    "override": {
                        "chipset": "B650",
                        "memory_generation": "ddr5",
                        "memory_speed_max_mts": 7600,
                        "m2_slots": 2,
                    },
                }
            ],
        }
        (part,) = build_category(selection, UPSTREAM)
        assert part["specs"]["socket"] == "AM5"  # 上游产品行
        assert part["specs"]["form_factor"] == "matx"
        assert part["specs"]["m2_slots"] == 2  # 人工 override
        assert part["source_meta"]["socket"] == "pc-part-dataset"
        assert part["source_meta"]["m2_slots"] == "override"

    def test_上游查无记录失败(self):
        bad = {
            **CPU_SELECTION,
            "entries": [
                {**CPU_SELECTION["entries"][0], "upstream_name": "No Such CPU"}
            ],
        }
        with pytest.raises(SpecError, match="查无记录"):
            build_category(bad, UPSTREAM)

    def test_override非法字段带sku上下文(self):
        bad = {
            **CPU_SELECTION,
            "entries": [
                {
                    **CPU_SELECTION["entries"][0],
                    "override": {"socket": "AM5", "supported_chipsets": None, "wattage_w": 1},
                }
            ],
        }
        with pytest.raises(SpecError, match="cpu-r7-9800x3d.*未知字段.*wattage_w"):
            build_category(bad, UPSTREAM)

    def test_chip_provider仅限gpu(self):
        with pytest.raises(SpecError, match="仅限 gpu"):
            build_category(CPU_SELECTION, UPSTREAM, chip_provider=lambda c, skip: {})


class TestWriteParts:
    def test_写出并可被coverage读回(self, tmp_path):
        parts = build_category(CPU_SELECTION, UPSTREAM)
        out = tmp_path / "cpu.jsonl"
        write_parts_jsonl(parts, out)
        data = out.read_bytes()
        assert data.endswith(b"\n") and b"\r" not in data  # LF、末行换行
        loaded = load_parts_jsonl(out)
        assert [p["sku"] for p in loaded] == ["cpu-i5-12400f", "cpu-r7-9800x3d"]
        # 重复写出字节一致(稳定排序 + 固定键序)。
        write_parts_jsonl(parts, out)
        assert out.read_bytes() == data


class TestCommittedSelections:
    """已提交 selection 文件的静态门禁:结构合法、每类恰好 20 条、override 字段合法。"""

    @pytest.mark.parametrize(
        "path", sorted(DEFAULT_CATALOG_DIR.glob("*.json")), ids=lambda p: p.stem
    )
    def test_selection文件(self, path):
        doc = load_selection(path)
        assert doc["category"] == path.stem
        assert len(doc["entries"]) == EXPECTED_PER_CATEGORY
        allowed = set(SPEC_FIELDS[doc["category"]])
        for entry in doc["entries"]:
            unknown = sorted(set(entry["override"]) - allowed)
            assert not unknown, f"{entry['sku']} override 含未知字段: {unknown}"
