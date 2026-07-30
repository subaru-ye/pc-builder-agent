"""来源锁文件(sources.lock.json)读取与校验。

D2/D3 适配器只允许消费此处锁定的上游版本;锁文件缺项、结构非法立即失败,
杜绝"顺手升级上游"造成的数据漂移。
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from .canonical import SpecError

__all__ = ["DEFAULT_LOCK_PATH", "load_lock", "get_source"]

# 锁文件与本包同级(scripts/data/sources.lock.json)。
DEFAULT_LOCK_PATH = Path(__file__).resolve().parents[2] / "sources.lock.json"

_COMMON_KEYS = {"name", "kind", "usage"}
# kind -> 额外必填键
_KIND_KEYS = {
    "git": {"url", "commit"},
    "pypi": {"package", "version"},
}


def load_lock(path: Path = DEFAULT_LOCK_PATH) -> dict[str, dict[str, Any]]:
    """读取来源锁,返回 name -> source 映射;结构非法抛 SpecError。"""
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict) or data.get("schema_version") != 1:
        raise SpecError(f"{path.name} schema_version 必须为 1")
    sources = data.get("sources")
    if not isinstance(sources, list) or not sources:
        raise SpecError(f"{path.name} sources 必须为非空数组")
    out: dict[str, dict[str, Any]] = {}
    for i, src in enumerate(sources):
        if not isinstance(src, dict):
            raise SpecError(f"sources[{i}] 必须为对象")
        kind = src.get("kind")
        if kind not in _KIND_KEYS:
            raise SpecError(f"sources[{i}] 非法 kind {kind!r}(仅 {'|'.join(_KIND_KEYS)})")
        required = _COMMON_KEYS | _KIND_KEYS[kind]
        missing = sorted(required - set(src))
        if missing:
            raise SpecError(f"sources[{i}] 缺少键: {', '.join(missing)}")
        for key in required:
            v = src[key]
            if not isinstance(v, str) or v == "":
                raise SpecError(f"sources[{i}].{key} 必须为非空字符串,得到 {v!r}")
        name = src["name"]
        if name in out:
            raise SpecError(f"来源 {name!r} 重复")
        out[name] = src
    return out


def get_source(name: str, path: Path = DEFAULT_LOCK_PATH) -> dict[str, Any]:
    """按名称取单个锁定来源;不存在抛 SpecError。"""
    sources = load_lock(path)
    if name not in sources:
        raise SpecError(f"来源 {name!r} 未在 {path.name} 中锁定")
    return sources[name]
