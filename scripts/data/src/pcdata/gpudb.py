"""dbgpu 适配器(D3):按显式 chipset 映射查芯片级兜底字段。

- 只允许显式映射:pc-part 的 chipset 字符串必须出现在人工维护的
  chipset_map 里,否则立即失败;绝不做 fuzzy/模糊匹配。
- 供电接口:仅识别 "Nx 8-pin" 与 "Nx 16-pin"(TechPowerUp 把 12VHPWR/12V-2×6
  记作 16-pin);6-pin、12-pin(Ampere FE 专有)等一律视为歧义,交人工 override。
- 版本锁定 dbgpu==2025.12(sources.lock.json 与 uv.lock 双重固定),
  默认数据库随包分发,离线可用。
"""

from __future__ import annotations

import re
from typing import Any

from dbgpu import GPUDatabase

from .canonical import SpecError

__all__ = ["load_database", "chip_candidate", "parse_power_connectors"]

_CONNECTOR_RE = re.compile(r"^(\d+)x (\d+)-pin$")
_PIN_MAP = {8: "pcie_8pin", 16: "pcie_16pin"}


def load_database() -> GPUDatabase:
    """加载 dbgpu 自带默认数据库(离线,不访问网络)。"""
    return GPUDatabase.default()


def parse_power_connectors(raw: Any) -> list[str]:
    """把 dbgpu 的 power_connectors 文本解析为 canonical 枚举 multiset。

    仅接受 "1x 8-pin"、"2x 8-pin + 1x 16-pin" 这类形态;其他一律 SpecError。
    """
    if not isinstance(raw, str) or not raw.strip():
        raise SpecError(f"power_connectors 文本歧义: {raw!r}")
    out: list[str] = []
    for part in (p.strip() for p in raw.split("+")):
        m = _CONNECTOR_RE.match(part)
        if not m:
            raise SpecError(f"power_connectors 片段无法解析: {part!r}(仅 Nx 8-pin / Nx 16-pin)")
        count, pins = int(m.group(1)), int(m.group(2))
        if pins not in _PIN_MAP:
            raise SpecError(f"power_connectors {part!r} 非 8/16-pin,须人工 override 裁定")
        if count <= 0:
            raise SpecError(f"power_connectors 数量非法: {part!r}")
        out.extend([_PIN_MAP[pins]] * count)
    return out


def chip_candidate(
    db: GPUDatabase,
    chipset: str,
    chipset_map: dict[str, str],
    skip_fields: frozenset[str] = frozenset(),
) -> dict[str, Any]:
    """按显式映射取芯片级候选字段(tdp_w、power_connectors、length_mm)。

    chipset 不在映射表 → SpecError(在 D4c 的 selection 里补映射,不做猜测);
    dbgpu 查无此名同样失败(映射表写错必须暴露)。
    可解析失败的接口文本不静默丢弃,直接失败,由人工 override 压制:
    skip_fields 传入 override 已裁定的字段,这些字段跳过芯片层解析
    (如 RTX 3060 12GB 的 "1x 12-pin" 只在接口未被 override 时才报错)。
    """
    if chipset not in chipset_map:
        raise SpecError(f"chipset {chipset!r} 未在显式映射表中(不做 fuzzy 匹配)")
    dbgpu_name = chipset_map[chipset]
    try:
        spec = db[dbgpu_name]
    except KeyError as e:
        raise SpecError(f"dbgpu 查无芯片 {dbgpu_name!r}(映射自 {chipset!r})") from e

    out: dict[str, Any] = {}
    tdp = spec.thermal_design_power_w
    if "tdp_w" not in skip_fields and tdp is not None:
        if not isinstance(tdp, int) or isinstance(tdp, bool) or tdp <= 0:
            raise SpecError(f"dbgpu {dbgpu_name!r} TDP 歧义: {tdp!r}")
        out["tdp_w"] = tdp
    if "power_connectors" not in skip_fields and spec.power_connectors is not None:
        out["power_connectors"] = parse_power_connectors(spec.power_connectors)
    length = spec.board_length_mm
    if "length_mm" not in skip_fields and length is not None:
        if not float(length).is_integer() or float(length) <= 0:
            raise SpecError(f"dbgpu {dbgpu_name!r} 板长歧义: {length!r}")
        out["length_mm"] = int(length)  # 公版参考长度,优先级低于 pc-part 产品行
    return out
