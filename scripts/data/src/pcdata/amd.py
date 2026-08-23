"""AMD 官方 CPU 页面确定性解析与字段证据生成。

本模块不发起网络请求。调用方负责 robots、条款、限流、条件请求和原始
快照；这里仅把已保存的 HTML 转为可核验字段，任何不确定性都失败关闭。
"""

from __future__ import annotations

import hashlib
import html
import json
import re
from html.parser import HTMLParser
from pathlib import Path
from typing import Any

from .canonical import SpecError

__all__ = ["load_amd_products", "parse_amd_cpu_html", "build_amd_evidence"]

_REQUIRED_LABELS = ("Name", "Default TDP", "CPU Socket", "Supporting Chipsets", "Graphics Model")
_TRADEMARKS = str.maketrans({"™": "", "®": "", "©": ""})


class _DefinitionListParser(HTMLParser):
    """只读取可见的 dt/dd 文本，忽略脚本和样式。"""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self._ignored = 0
        self._capture: str | None = None
        self._buffer: list[str] = []
        self.pairs: list[tuple[str, str]] = []
        self._last_label: str | None = None

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        del attrs
        if tag in {"script", "style", "noscript", "svg"}:
            self._ignored += 1
            return
        if self._ignored == 0 and tag in {"dt", "dd"}:
            self._capture = tag
            self._buffer = []

    def handle_endtag(self, tag: str) -> None:
        if tag in {"script", "style", "noscript", "svg"}:
            self._ignored = max(0, self._ignored - 1)
            return
        if self._ignored or tag != self._capture:
            return
        value = _collapse(" ".join(self._buffer))
        if tag == "dt":
            self._last_label = value
        elif self._last_label is not None:
            self.pairs.append((self._last_label, value))
            self._last_label = None
        self._capture = None
        self._buffer = []

    def handle_data(self, data: str) -> None:
        if self._ignored == 0 and self._capture is not None:
            self._buffer.append(data)


def _collapse(value: str) -> str:
    return re.sub(r"\s+", " ", html.unescape(value)).strip()


def _identity(value: str) -> str:
    value = _collapse(value).translate(_TRADEMARKS).casefold()
    value = re.sub(r"\bdesktop processor\b", "", value)
    return re.sub(r"[^a-z0-9]+", "", value)


def load_amd_products(path: Path) -> list[dict[str, str]]:
    """读取固定 SKU/URL 映射；不允许查询参数、重复项或非 AMD 主机。"""
    try:
        doc = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SpecError(f"读取 AMD 产品映射失败: {exc}") from exc
    if not isinstance(doc, dict) or set(doc) != {"schema_version", "products"} or doc.get("schema_version") != 1:
        raise SpecError("AMD 产品映射顶层结构非法")
    products = doc.get("products")
    if not isinstance(products, list) or not products:
        raise SpecError("AMD 产品映射 products 必须为非空数组")
    out: list[dict[str, str]] = []
    seen_skus: set[str] = set()
    seen_urls: set[str] = set()
    for index, item in enumerate(products):
        if not isinstance(item, dict) or set(item) != {"sku", "expected_name", "url"}:
            raise SpecError(f"AMD products[{index}] 结构非法")
        if any(not isinstance(item[key], str) or not item[key] for key in item):
            raise SpecError(f"AMD products[{index}] 字段必须为非空字符串")
        if not item["sku"].startswith("cpu-") or item["sku"] in seen_skus or item["url"] in seen_urls:
            raise SpecError(f"AMD products[{index}] SKU/URL 非法或重复")
        if not item["url"].startswith("https://www.amd.com/") or "?" in item["url"] or "#" in item["url"]:
            raise SpecError(f"AMD products[{index}].url 必须为无查询参数的 amd.com HTTPS URL")
        seen_skus.add(item["sku"])
        seen_urls.add(item["url"])
        out.append(dict(item))
    return out


def parse_amd_cpu_html(data: bytes, expected_name: str) -> tuple[dict[str, Any], dict[str, str]]:
    """解析 AMD dt/dd 规格；标签缺失、重复冲突或单位不明均拒绝。"""
    try:
        text = data.decode("utf-8", errors="strict")
    except UnicodeDecodeError as exc:
        raise SpecError("AMD 页面不是合法 UTF-8") from exc
    parser = _DefinitionListParser()
    parser.feed(text)
    values: dict[str, list[str]] = {label: [] for label in _REQUIRED_LABELS}
    for label, value in parser.pairs:
        if label in values and value and value not in values[label]:
            values[label].append(value)
    missing = [label for label, found in values.items() if not found]
    ambiguous = [label for label, found in values.items() if len(found) != 1]
    if missing or ambiguous:
        raise SpecError(f"AMD 页面规格标签不完整或冲突: missing={missing}, ambiguous={ambiguous}")
    raw = {label: found[0] for label, found in values.items()}
    if _identity(raw["Name"]) != _identity(expected_name):
        raise SpecError(f"AMD 页面型号不匹配: 期望 {expected_name!r}，实际 {raw['Name']!r}")
    tdp = re.fullmatch(r"([1-9][0-9]{0,3})\s*W", raw["Default TDP"], re.IGNORECASE)
    if tdp is None:
        raise SpecError(f"AMD Default TDP 单位非法: {raw['Default TDP']!r}")
    socket = raw["CPU Socket"].upper()
    if not re.fullmatch(r"AM[0-9]+", socket):
        raise SpecError(f"AMD CPU Socket 非法: {raw['CPU Socket']!r}")
    chipsets = [_collapse(item).upper() for item in raw["Supporting Chipsets"].split(",")]
    if not chipsets or any(not re.fullmatch(r"[A-Z][A-Z0-9-]{1,15}", item) for item in chipsets):
        raise SpecError(f"AMD Supporting Chipsets 非法: {raw['Supporting Chipsets']!r}")
    graphics = raw["Graphics Model"].casefold()
    if "discrete graphics card required" in graphics:
        has_igpu = False
    elif "radeon" in graphics:
        has_igpu = True
    else:
        raise SpecError(f"AMD Graphics Model 无法确定核显语义: {raw['Graphics Model']!r}")
    parsed = {
        "model": raw["Name"],
        "specs.socket": socket,
        "specs.supported_chipsets": chipsets,
        "specs.tdp_w": int(tdp.group(1)),
        "specs.has_igpu": has_igpu,
    }
    excerpts = {
        "model": f"Name: {raw['Name']}",
        "specs.socket": f"CPU Socket: {raw['CPU Socket']}",
        "specs.supported_chipsets": f"Supporting Chipsets: {raw['Supporting Chipsets']}",
        "specs.tdp_w": f"Default TDP: {raw['Default TDP']}",
        "specs.has_igpu": f"Graphics Model: {raw['Graphics Model']}",
    }
    return parsed, excerpts


def _matches(field: str, observed: Any, current: Any, expected_name: str) -> bool:
    if field == "model":
        return _identity(str(observed)) == _identity("AMD " + str(current)) == _identity(expected_name)
    if field == "specs.supported_chipsets":
        # 目录只保留本项目支持的芯片组子集；官方集合可以更大，但不能缺少目录值。
        return set(current).issubset(set(observed))
    return observed == current


def build_amd_evidence(
    *,
    source_id: str,
    source_url: str,
    sku: str,
    expected_name: str,
    current: dict[str, Any],
    parsed: dict[str, Any],
    excerpts: dict[str, str],
    raw_sha256: str,
    captured_at: str,
) -> list[dict[str, Any]]:
    """生成稳定字段 evidence；状态只由确定性当前值比较得出。"""
    evidence: list[dict[str, Any]] = []
    for field in ("model", "specs.socket", "specs.supported_chipsets", "specs.tdp_w", "specs.has_igpu"):
        current_value = current["model"] if field == "model" else current["specs"][field.removeprefix("specs.")]
        status = "verified" if _matches(field, parsed[field], current_value, expected_name) else "conflict"
        identity = {
            "source_id": source_id,
            "sku": sku,
            "field": field,
            "value": parsed[field],
            "raw_sha256": raw_sha256,
        }
        evidence_id = hashlib.sha256(
            json.dumps(identity, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
        ).hexdigest()
        evidence.append(
            {
                "schema_version": 1,
                "id": evidence_id,
                "sku": sku,
                "field": field,
                "value": parsed[field],
                "source_id": source_id,
                "source_url": source_url,
                "captured_at": captured_at,
                "raw_sha256": raw_sha256,
                "method": "deterministic",
                "evidence_excerpt": excerpts[field][:500],
                "evidence_status": status,
            }
        )
    return evidence
