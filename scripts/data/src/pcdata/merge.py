"""字段合并器(D3):人工 override > pc-part 产品字段 > dbgpu 芯片字段 > null。

- 每层只允许出现 canonical 字段字典里的键,未知键立即失败;
- product/chip 层不得携带 null(适配器对未知字段直接省略);
- override 层允许显式 null:表示人工裁定"未知",可压制下层的可疑值;
- 合并结果必须通过 canonical 校验,并逐字段生成来源标注(source_meta)。
"""

from __future__ import annotations

from typing import Any

from .canonical import SPEC_FIELDS, SpecError, validate_specs

__all__ = ["merge_specs"]

# 来源标注值(写入 parts.source_meta,逐字段可追溯)。
_SRC_OVERRIDE = "override"
_SRC_PCPART = "pc-part-dataset"
_SRC_DBGPU = "dbgpu"
_SRC_NULL = "null"


def _check_layer(category: str, layer_name: str, layer: dict[str, Any], allow_null: bool) -> None:
    unknown = sorted(set(layer) - set(SPEC_FIELDS[category]))
    if unknown:
        raise SpecError(f"{layer_name} 层含未知字段: {', '.join(unknown)}")
    if not allow_null:
        nulls = sorted(k for k, v in layer.items() if v is None)
        if nulls:
            raise SpecError(
                f"{layer_name} 层不得携带 null 字段: {', '.join(nulls)}(未知字段应省略)"
            )


def merge_specs(
    category: str,
    override: dict[str, Any],
    product: dict[str, Any],
    chip: dict[str, Any] | None = None,
) -> tuple[dict[str, Any], dict[str, str]]:
    """按固定优先级合并三层候选,返回 (specs, source_meta)。

    specs 含该类目全部字段(未覆盖的为 null);source_meta 逐字段标注
    override / pc-part-dataset / dbgpu / null 四种来源之一。
    """
    if category not in SPEC_FIELDS:
        raise SpecError(f"非法类目 {category!r}")
    chip = chip or {}
    _check_layer(category, "override", override, allow_null=True)
    _check_layer(category, "product(pc-part)", product, allow_null=False)
    _check_layer(category, "chip(dbgpu)", chip, allow_null=False)

    specs: dict[str, Any] = {}
    source_meta: dict[str, str] = {}
    for field in SPEC_FIELDS[category]:
        if field in override:
            specs[field] = override[field]
            source_meta[field] = _SRC_OVERRIDE
        elif field in product:
            specs[field] = product[field]
            source_meta[field] = _SRC_PCPART
        elif field in chip:
            specs[field] = chip[field]
            source_meta[field] = _SRC_DBGPU
        else:
            specs[field] = None
            source_meta[field] = _SRC_NULL

    validate_specs(category, specs)
    return specs, source_meta
