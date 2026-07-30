"""目录构建器(D4):人工 selection/override 文件 → 上游查找 → 合并 → parts 分类目录。

selection 文件(scripts/data/catalog/<category>.json)是唯一入库的人工事实:
- upstream_name(+upstream_match)定位 pc-part-dataset 锁定快照里的产品行;
- override 携带上游缺失/需人工裁定的 canonical 字段(显式 null = 人工裁定未知);
- 合并优先级固定为 override > pc-part 产品字段 > dbgpu 芯片字段 > null(D3)。

产出 scripts/data/parts/<category>.jsonl:按 sku 稳定排序,逐条过 validate_part。
上游快照不入库,构建需先运行 python -m pcdata.pcpart fetch(CI 只校验已提交产物)。
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Callable

from .canonical import CATEGORIES, SpecError, validate_part
from .merge import merge_specs
from .pcpart import find, load_raw, to_candidate

__all__ = ["load_selection", "build_category", "write_parts_jsonl"]

_DATA_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_CATALOG_DIR = _DATA_ROOT / "catalog"
DEFAULT_UPSTREAM_DIR = _DATA_ROOT / "upstream"
DEFAULT_PARTS_DIR = _DATA_ROOT / "parts"

# entry 允许键:ref/note 仅作人工溯源注记,不参与构建。
_ENTRY_KEYS = {"sku", "brand", "model", "upstream_name", "upstream_match", "override", "ref", "note"}
_REQUIRED_ENTRY_KEYS = ("sku", "brand", "model", "upstream_name", "override")

# GPU 的芯片层由 D4c 注入((chipset, override 已裁定字段集) -> chip 候选 dict);
# override 已裁定的字段跳过芯片层解析,其余字段解析失败仍立即报错。
ChipProvider = Callable[[str, frozenset[str]], dict[str, Any]]


def load_selection(path: Path) -> dict[str, Any]:
    """读取并校验 selection 文件;结构非法立即失败。"""
    with path.open(encoding="utf-8") as f:
        doc = json.load(f)
    if not isinstance(doc, dict):
        raise SpecError(f"{path.name} 顶层必须为对象")
    if doc.get("schema_version") != 1:
        raise SpecError(f"{path.name} schema_version 必须为 1,得到 {doc.get('schema_version')!r}")
    category = doc.get("category")
    if category not in CATEGORIES:
        raise SpecError(f"{path.name} 非法类目 {category!r}(仅 {'|'.join(CATEGORIES)})")
    entries = doc.get("entries")
    if not isinstance(entries, list) or not entries:
        raise SpecError(f"{path.name} entries 必须为非空数组")
    seen: set[str] = set()
    for i, entry in enumerate(entries):
        where = f"{path.name} entries[{i}]"
        if not isinstance(entry, dict):
            raise SpecError(f"{where} 必须为对象")
        unknown = sorted(set(entry) - _ENTRY_KEYS)
        if unknown:
            raise SpecError(f"{where} 含未知键: {', '.join(unknown)}")
        missing = sorted(set(_REQUIRED_ENTRY_KEYS) - set(entry))
        if missing:
            raise SpecError(f"{where} 缺少键: {', '.join(missing)}")
        for key in ("sku", "brand", "model", "upstream_name"):
            if not isinstance(entry[key], str) or not entry[key]:
                raise SpecError(f"{where} 字段 {key} 必须为非空字符串")
        if not isinstance(entry["override"], dict):
            raise SpecError(f"{where} override 必须为对象")
        if "upstream_match" in entry and not isinstance(entry["upstream_match"], dict):
            raise SpecError(f"{where} upstream_match 必须为对象")
        if entry["sku"] in seen:
            raise SpecError(f"{where} 重复 SKU {entry['sku']!r}")
        seen.add(entry["sku"])
    return doc


def build_category(
    selection: dict[str, Any],
    upstream_dir: Path,
    chip_provider: ChipProvider | None = None,
) -> list[dict[str, Any]]:
    """按 selection 构建一类 parts 记录,输出按 sku 稳定排序。

    chip_provider 仅 GPU 类目使用(D4c 注入 dbgpu 查询);其余类目传入即错。
    """
    category = selection["category"]
    if chip_provider is not None and category != "gpu":
        raise SpecError(f"chip_provider 仅限 gpu 类目,得到 {category!r}")
    records = load_raw(upstream_dir, category)
    parts: list[dict[str, Any]] = []
    for entry in selection["entries"]:
        raw = find(records, entry["upstream_name"], **entry.get("upstream_match", {}))
        product = to_candidate(category, raw)
        chip: dict[str, Any] | None = None
        if category == "gpu":
            # chipset 是 D3 映射键,不属于 canonical specs,必须在合并前取出。
            chipset = product.pop("chipset", None)
            if chip_provider is not None:
                if chipset is None:
                    raise SpecError(f"{entry['sku']} 上游缺少 chipset,无法查询芯片层")
                chip = chip_provider(chipset, frozenset(entry["override"]))
        try:
            specs, source_meta = merge_specs(category, entry["override"], product, chip)
        except SpecError as e:
            raise SpecError(f"{entry['sku']}: {e}") from e
        record = {
            "sku": entry["sku"],
            "category": category,
            "brand": entry["brand"],
            "model": entry["model"],
            "schema_version": 1,
            "specs": specs,
            "source_meta": source_meta,
        }
        validate_part(record)
        parts.append(record)
    return sorted(parts, key=lambda r: r["sku"])


def write_parts_jsonl(parts: list[dict[str, Any]], out_path: Path) -> None:
    """写出一类 parts JSONL(UTF-8、无 BOM、LF、键序固定)。"""
    out_path.parent.mkdir(parents=True, exist_ok=True)
    lines = [json.dumps(p, ensure_ascii=False, separators=(", ", ": ")) for p in parts]
    out_path.write_bytes(("\n".join(lines) + "\n").encode("utf-8"))


def _main(argv: list[str]) -> int:
    import sys

    if len(argv) != 2 or argv[1] not in CATEGORIES:
        print(f"用法: python -m pcdata.catalog <{'|'.join(CATEGORIES)}>", file=sys.stderr)
        return 2
    category = argv[1]
    try:
        selection = load_selection(DEFAULT_CATALOG_DIR / f"{category}.json")
        if selection["category"] != category:
            raise SpecError(f"selection 类目 {selection['category']!r} 与文件名不符")
        chip_provider: ChipProvider | None = None
        if category == "gpu":
            from .gpudb import chip_candidate, load_database

            chipset_map = selection.get("chipset_map")
            if not isinstance(chipset_map, dict) or not chipset_map:
                raise SpecError("gpu selection 必须提供非空 chipset_map(D3 显式映射)")
            db = load_database()
            chip_provider = lambda chipset, skip: chip_candidate(db, chipset, chipset_map, skip)  # noqa: E731
        parts = build_category(selection, DEFAULT_UPSTREAM_DIR, chip_provider)
        out = DEFAULT_PARTS_DIR / f"{category}.jsonl"
        write_parts_jsonl(parts, out)
    except (SpecError, OSError, json.JSONDecodeError) as e:
        print(f"错误: {e}", file=sys.stderr)
        return 2
    print(f"已生成 {out}({len(parts)} 条)")
    return 0


if __name__ == "__main__":
    import sys

    sys.exit(_main(sys.argv))
