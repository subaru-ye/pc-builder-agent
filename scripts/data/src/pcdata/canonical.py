"""canonical 校验器:与 internal/schemas 逐字段同口径。

null 语义:标量 None = 未知;集合 None = 未知,空列表 = 已知为空。
单位统一为整数 mm / W / MT/s;未知字段、非法枚举、非正数量均为输入错误。

比 Go 读取端更严的一点(有意为之):specs 必须显式写出全部字段,
未知用显式 null,不允许省略键——目录数据必须显式表态,完整率报告才可统计。
"""

from __future__ import annotations

from typing import Any, Callable

__all__ = [
    "SpecError",
    "CATEGORIES",
    "FORM_FACTORS",
    "SSD_FORM_FACTORS",
    "COOLER_TYPES",
    "POWER_CONNECTORS",
    "SPEC_FIELDS",
    "validate_specs",
    "validate_part",
]


class SpecError(ValueError):
    """canonical 数据非法(字段、枚举、类型、数值任一不符)。"""


# 八大类固定枚举,与 parts.category CHECK 约束一致。
CATEGORIES = ("cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler")

FORM_FACTORS = ("atx", "matx", "itx")
SSD_FORM_FACTORS = ("m2", "sata_2_5")
COOLER_TYPES = ("air", "aio")
# 12VHPWR / 12V-2×6 由适配器归一为 pcie_16pin,本层不做别名兼容。
POWER_CONNECTORS = ("pcie_8pin", "pcie_16pin")

Validator = Callable[[str, Any], None]


def _fail(field: str, msg: str) -> None:
    raise SpecError(f"字段 {field} {msg}")


def _is_int(v: Any) -> bool:
    # bool 是 int 的子类,必须显式排除。
    return isinstance(v, int) and not isinstance(v, bool)


def _scalar_str(field: str, v: Any) -> None:
    if v is None:
        return
    if not isinstance(v, str):
        _fail(field, f"必须为字符串或 null,得到 {type(v).__name__}")
    if v == "":
        _fail(field, "不得为空字符串(未知请用 null)")


def _scalar_bool(field: str, v: Any) -> None:
    if v is None:
        return
    if not isinstance(v, bool):
        _fail(field, f"必须为布尔或 null,得到 {type(v).__name__}")


def _scalar_pos_int(field: str, v: Any) -> None:
    if v is None:
        return
    if not _is_int(v):
        _fail(field, f"必须为整数或 null,得到 {type(v).__name__}")
    if v <= 0:
        _fail(field, f"必须为正整数,得到 {v}")


def _scalar_nonneg_int(field: str, v: Any) -> None:
    if v is None:
        return
    if not _is_int(v):
        _fail(field, f"必须为整数或 null,得到 {type(v).__name__}")
    if v < 0:
        _fail(field, f"不得为负数,得到 {v}")


def _list_of(elem: Validator) -> Validator:
    def check(field: str, v: Any) -> None:
        if v is None:
            return  # 集合 null = 未知
        if not isinstance(v, list):
            _fail(field, f"必须为数组或 null,得到 {type(v).__name__}")
        for i, item in enumerate(v):
            if item is None:
                _fail(f"{field}[{i}]", "集合元素不得为 null")
            elem(f"{field}[{i}]", item)

    return check


def _enum(values: tuple[str, ...]) -> Validator:
    def check(field: str, v: Any) -> None:
        if v is None:
            return
        if not isinstance(v, str) or v not in values:
            _fail(field, f"非法枚举 {v!r}(仅 {'|'.join(values)})")

    return check


# 字段字典(设计方案 §四.1;顺序与 Go 结构体一致)。
SPEC_FIELDS: dict[str, dict[str, Validator]] = {
    "cpu": {
        "socket": _scalar_str,
        "supported_chipsets": _list_of(_scalar_str),
        "has_igpu": _scalar_bool,
        "tdp_w": _scalar_pos_int,
    },
    "gpu": {
        "length_mm": _scalar_pos_int,
        "tdp_w": _scalar_pos_int,
        "power_connectors": _list_of(_enum(POWER_CONNECTORS)),
    },
    "motherboard": {
        "socket": _scalar_str,
        "chipset": _scalar_str,
        "memory_generation": _scalar_str,
        "memory_speed_max_mts": _scalar_pos_int,
        "form_factor": _enum(FORM_FACTORS),
        "m2_slots": _scalar_nonneg_int,
    },
    "memory": {
        "generation": _scalar_str,
        "speed_mts": _scalar_pos_int,
    },
    "ssd": {
        "form_factor": _enum(SSD_FORM_FACTORS),
    },
    "psu": {
        "wattage_w": _scalar_pos_int,
        "power_connectors": _list_of(_enum(POWER_CONNECTORS)),
    },
    "case": {
        "gpu_length_max_mm": _scalar_pos_int,
        "cooler_height_max_mm": _scalar_pos_int,
        "supported_form_factors": _list_of(_enum(FORM_FACTORS)),
        "radiator_sizes_mm": _list_of(_scalar_pos_int),
    },
    "cooler": {
        "type": _enum(COOLER_TYPES),
        "height_mm": _scalar_pos_int,
        "radiator_size_mm": _scalar_pos_int,
        "cooling_capacity_w": _scalar_pos_int,
    },
}


def validate_specs(category: str, specs: Any) -> None:
    """校验单个零件的 canonical specs;非法时抛 SpecError。"""
    if category not in SPEC_FIELDS:
        raise SpecError(f"非法类目 {category!r}(仅 {'|'.join(CATEGORIES)})")
    if not isinstance(specs, dict):
        raise SpecError(f"specs 必须为对象,得到 {type(specs).__name__}")
    fields = SPEC_FIELDS[category]
    unknown = sorted(set(specs) - set(fields))
    if unknown:
        raise SpecError(f"{category} specs 含未知字段: {', '.join(unknown)}")
    missing = sorted(set(fields) - set(specs))
    if missing:
        raise SpecError(
            f"{category} specs 缺少字段: {', '.join(missing)}(未知请显式写 null)"
        )
    for name, check in fields.items():
        check(name, specs[name])


# parts.jsonl 单条记录的固定键集(与 parts 表列一致,时间戳由 DB 生成)。
_PART_REQUIRED_KEYS = ("sku", "category", "brand", "model", "schema_version", "specs", "source_meta")
_PART_KEYS = (*_PART_REQUIRED_KEYS, "catalog_state")


def validate_part(record: Any) -> None:
    """校验 parts.jsonl 单条记录;非法时抛 SpecError。"""
    if not isinstance(record, dict):
        raise SpecError(f"记录必须为对象,得到 {type(record).__name__}")
    unknown = sorted(set(record) - set(_PART_KEYS))
    if unknown:
        raise SpecError(f"记录含未知键: {', '.join(unknown)}")
    missing = sorted(set(_PART_REQUIRED_KEYS) - set(record))
    if missing:
        raise SpecError(f"记录缺少键: {', '.join(missing)}")
    for key in ("sku", "brand", "model"):
        v = record[key]
        if not isinstance(v, str) or v == "":
            raise SpecError(f"字段 {key} 必须为非空字符串,得到 {v!r}")
    if record["category"] not in CATEGORIES:
        raise SpecError(f"非法类目 {record['category']!r}(仅 {'|'.join(CATEGORIES)})")
    if record["schema_version"] != 1:
        raise SpecError(f"schema_version 必须为 1,得到 {record['schema_version']!r}")
    if record.get("catalog_state", "active_core") not in {"active_core", "catalog_only", "retired"}:
        raise SpecError(f"catalog_state 非法: {record.get('catalog_state')!r}")
    if not isinstance(record["source_meta"], dict) or not record["source_meta"]:
        raise SpecError("source_meta 必须为非空对象(每个字段值都要可追溯来源)")
    validate_specs(record["category"], record["specs"])
