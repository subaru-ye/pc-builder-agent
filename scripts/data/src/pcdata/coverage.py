"""完整率报告:每类 SKU 数、错误级规则字段覆盖、warning 专属字段 null 统计、价格覆盖。

门禁:每类至少 EXPECTED_PER_CATEGORY(20,历史种子下限,扩库后可超出且各类不等)、
错误级规则字段覆盖率 100%、价格覆盖率 100%;warning 专属字段允许 null,但必须统计。
重复 SKU、非法记录在加载时立即失败,数量开放不放宽身份与字段校验。

用法:uv run python -m pcdata.coverage parts.jsonl [prices.csv]
prices.csv 固定表头 sku,price_cny,source,captured_at(与 D6 导入器同格式)。
"""

from __future__ import annotations

import csv
import json
import sys
from pathlib import Path
from typing import Any

from .canonical import CATEGORIES, SpecError, validate_part

__all__ = [
    "EXPECTED_PER_CATEGORY",
    "ERROR_LEVEL_FIELDS",
    "WARNING_ONLY_FIELDS",
    "load_parts_jsonl",
    "load_priced_skus",
    "build_report",
]

# 历史种子下限:每类至少 20 条;扩库新增可超出且各类数量不等,不足即门禁失败。
EXPECTED_PER_CATEGORY = 20

# 错误级规则(#1/#2/#3/#5/#6/#7/#8/#9/#10/#11 及 #7 的 error 档)所需字段:
# 覆盖率必须 100%(null 即缺口)。cooler 的高度/冷排字段按类型条件计入。
ERROR_LEVEL_FIELDS: dict[str, tuple[str, ...]] = {
    "cpu": ("socket", "supported_chipsets", "has_igpu", "tdp_w"),
    "gpu": ("length_mm", "tdp_w", "power_connectors"),
    "motherboard": ("socket", "chipset", "memory_generation", "form_factor", "m2_slots"),
    "memory": ("generation",),
    "ssd": ("form_factor",),
    "psu": ("wattage_w", "power_connectors"),
    "case": (
        "gpu_length_max_mm",
        "cooler_height_max_mm",
        "supported_form_factors",
        "radiator_sizes_mm",
    ),
    "cooler": ("type",),  # height_mm / radiator_size_mm 按 type 条件追加
}

# 仅参与 warning 级判定(#4 内存频率、#12 解热能力)的字段:允许 null,只统计。
WARNING_ONLY_FIELDS: dict[str, tuple[str, ...]] = {
    "cpu": (),
    "gpu": (),
    "motherboard": ("memory_speed_max_mts",),
    "memory": ("speed_mts",),
    "ssd": (),
    "psu": ("form_factor", "length_mm"),
    "case": ("supported_psu_form_factors", "psu_length_max_mm"),
    "cooler": ("cooling_capacity_w",),
}


def load_parts_jsonl(path: Path) -> list[dict[str, Any]]:
    """读取并逐条校验 parts.jsonl;重复 SKU、非法记录立即失败。"""
    parts: list[dict[str, Any]] = []
    seen: set[str] = set()
    with path.open(encoding="utf-8") as f:
        for lineno, line in enumerate(f, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError as e:
                raise SpecError(f"{path.name}:{lineno} JSON 非法: {e}") from e
            try:
                validate_part(record)
            except SpecError as e:
                raise SpecError(f"{path.name}:{lineno} {e}") from e
            sku = record["sku"]
            if sku in seen:
                raise SpecError(f"{path.name}:{lineno} 重复 SKU {sku!r}")
            seen.add(sku)
            parts.append(record)
    return parts


def load_priced_skus(path: Path) -> set[str]:
    """读取价格 CSV(D6 固定表头),返回有价 SKU 集合;重复 SKU 立即失败。"""
    expected = ["sku", "price_cny", "source", "captured_at"]
    skus: set[str] = set()
    with path.open(encoding="utf-8", newline="") as f:
        reader = csv.DictReader(f)
        if reader.fieldnames != expected:
            raise SpecError(
                f"{path.name} 表头必须为 {','.join(expected)},得到 {reader.fieldnames}"
            )
        for lineno, row in enumerate(reader, start=2):
            sku = row["sku"]
            if not sku:
                raise SpecError(f"{path.name}:{lineno} sku 为空")
            if sku in skus:
                raise SpecError(f"{path.name}:{lineno} 重复 SKU {sku!r}")
            skus.add(sku)
    return skus


def _required_fields(record: dict[str, Any]) -> tuple[str, ...]:
    """单条记录的错误级必填字段(cooler 按类型追加条件字段)。"""
    fields = ERROR_LEVEL_FIELDS[record["category"]]
    if record["category"] == "cooler":
        cooler_type = record["specs"]["type"]
        if cooler_type == "air":
            fields = fields + ("height_mm",)
        elif cooler_type == "aio":
            fields = fields + ("radiator_size_mm",)
        # type 为 null 时本身已是缺口,条件字段无从谈起。
    return fields


def _warning_fields(record: dict[str, Any]) -> tuple[str, ...]:
    """返回当前规则实际会读取的 warning 字段。"""
    fields = WARNING_ONLY_FIELDS[record["category"]]
    if record["category"] == "case":
        # 当前电源安装检查仅对纯 ITX 机箱启用；混合尺寸机箱还缺少电源仓/转接架语义。
        return fields if record["specs"]["supported_form_factors"] == ["itx"] else ()
    return fields


def build_report(
    parts: list[dict[str, Any]], priced_skus: set[str] | None = None
) -> dict[str, Any]:
    """生成完整率报告(纯计算,不做 IO);parts 需已通过 load_parts_jsonl 校验。"""
    per_category: dict[str, Any] = {}
    all_ok = True
    for category in CATEGORIES:
        records = [p for p in parts if p["category"] == category]
        missing: dict[str, list[str]] = {}  # 字段 -> 缺口 SKU 列表
        warning_nulls: dict[str, list[str]] = {
            f: [] for f in WARNING_ONLY_FIELDS[category]
        }
        for r in records:
            for field in _required_fields(r):
                if r["specs"][field] is None:
                    missing.setdefault(field, []).append(r["sku"])
            for field in _warning_fields(r):
                if r["specs"].get(field) is None:
                    warning_nulls[field].append(r["sku"])
        count_ok = len(records) >= EXPECTED_PER_CATEGORY
        fields_ok = not missing
        all_ok = all_ok and count_ok and fields_ok
        per_category[category] = {
            "sku_count": len(records),
            "expected": EXPECTED_PER_CATEGORY,
            "count_ok": count_ok,
            "error_field_gaps": {f: sorted(v) for f, v in sorted(missing.items())},
            "error_fields_ok": fields_ok,
            "warning_field_null_skus": {
                f: sorted(v) for f, v in sorted(warning_nulls.items())
            },
        }

    report: dict[str, Any] = {
        "total_skus": len(parts),
        "per_category": per_category,
        "catalog_ok": all_ok,
    }
    if priced_skus is not None:
        all_skus = {p["sku"] for p in parts}
        missing_price = sorted(all_skus - priced_skus)
        extra_price = sorted(priced_skus - all_skus)
        price_ok = not missing_price and not extra_price
        report["price"] = {
            "covered": len(all_skus) - len(missing_price),
            "total": len(all_skus),
            "missing_skus": missing_price,
            "unknown_priced_skus": extra_price,  # 价格表里出现目录外 SKU 也是错误
            "price_ok": price_ok,
        }
        report["catalog_ok"] = all_ok and price_ok
    return report


def main(argv: list[str]) -> int:
    if len(argv) not in (2, 3):
        print("用法: python -m pcdata.coverage parts.jsonl [prices.csv]", file=sys.stderr)
        return 2
    try:
        parts = load_parts_jsonl(Path(argv[1]))
        priced = load_priced_skus(Path(argv[2])) if len(argv) == 3 else None
        report = build_report(parts, priced)
    except (SpecError, OSError) as e:
        print(f"错误: {e}", file=sys.stderr)
        return 2
    json.dump(report, sys.stdout, ensure_ascii=False, indent=2)
    print()
    return 0 if report["catalog_ok"] else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
