"""P11 来源登记读取与静态安全校验。

registry 决定一个来源是否允许进入定时主链；版本锁仍由 sources.py 管理。
这里不做网络探测，避免 `source check` 默认产生费用或副作用。
"""

from __future__ import annotations

import json
import re
from datetime import date
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

from .canonical import SpecError

__all__ = ["DEFAULT_REGISTRY_PATH", "load_registry", "get_registered_source"]

DEFAULT_REGISTRY_PATH = Path(__file__).resolve().parents[2] / "sources.registry.json"

_ID_RE = re.compile(r"^[a-z][a-z0-9_]{1,63}$")
_KINDS = {
    "local_seed",
    "local_package",
    "open_dataset",
    "official_catalog",
    "official_product",
    "public_retail",
    "manual",
}
_ADAPTERS = {
    "local_parts",
    "locked_git_version",
    "locked_package",
    "http_snapshot",
    "amd_cpu_official",
    "manual",
}
_TRUST_TIERS = {"A", "B", "C", "D"}
_ACCESS = {
    "local_only",
    "version_check_only",
    "allowed",
    "allowed_after_review",
    "permission_required",
    "disabled",
}
_ROBOTS = {"obey", "not_applicable"}
_SCHEDULES = {"daily", "weekly", "monthly", "manual"}
_REQUIRED_KEYS = {
    "id",
    "kind",
    "adapter",
    "base_url",
    "purpose",
    "trust_tier",
    "license_or_terms_url",
    "attribution",
    "requires_credentials",
    "automated_access",
    "robots_policy",
    "rate_limit_policy",
    "schedule",
    "enabled",
    "terms_reviewed_at",
    "failure_mode",
}
_OPTIONAL_KEYS = {"resources_path", "policy_sha256"}
_KEYS = _REQUIRED_KEYS | _OPTIONAL_KEYS


def _nonempty_string(where: str, value: Any) -> str:
    if not isinstance(value, str) or not value:
        raise SpecError(f"{where} 必须为非空字符串")
    return value


def _https_url(where: str, value: Any, *, nullable: bool = False) -> str | None:
    if value is None and nullable:
        return None
    text = _nonempty_string(where, value)
    parsed = urlparse(text)
    if (
        parsed.scheme != "https"
        or not parsed.netloc
        or parsed.username
        or parsed.password
        or parsed.query
        or parsed.fragment
    ):
        raise SpecError(f"{where} 必须为不含凭据的 https URL")
    return text


def _validate_source(raw: Any, index: int) -> dict[str, Any]:
    where = f"sources[{index}]"
    if not isinstance(raw, dict):
        raise SpecError(f"{where} 必须为对象")
    unknown = sorted(set(raw) - _KEYS)
    missing = sorted(_REQUIRED_KEYS - set(raw))
    if unknown:
        raise SpecError(f"{where} 含未知键: {', '.join(unknown)}")
    if missing:
        raise SpecError(f"{where} 缺少键: {', '.join(missing)}")

    source_id = _nonempty_string(f"{where}.id", raw["id"])
    if not _ID_RE.fullmatch(source_id):
        raise SpecError(f"{where}.id 非法: {source_id!r}")
    if raw["kind"] not in _KINDS:
        raise SpecError(f"{where}.kind 非法: {raw['kind']!r}")
    if raw["adapter"] not in _ADAPTERS:
        raise SpecError(f"{where}.adapter 非法: {raw['adapter']!r}")
    if raw["trust_tier"] not in _TRUST_TIERS:
        raise SpecError(f"{where}.trust_tier 非法: {raw['trust_tier']!r}")
    if raw["automated_access"] not in _ACCESS:
        raise SpecError(f"{where}.automated_access 非法: {raw['automated_access']!r}")
    if raw["robots_policy"] not in _ROBOTS:
        raise SpecError(f"{where}.robots_policy 非法: {raw['robots_policy']!r}")
    if raw["schedule"] not in _SCHEDULES:
        raise SpecError(f"{where}.schedule 非法: {raw['schedule']!r}")
    if raw["failure_mode"] not in {"block_run", "isolate_source"}:
        raise SpecError(f"{where}.failure_mode 非法: {raw['failure_mode']!r}")
    if not isinstance(raw["enabled"], bool) or not isinstance(raw["requires_credentials"], bool):
        raise SpecError(f"{where}.enabled/requires_credentials 必须为布尔")
    purpose = raw["purpose"]
    if not isinstance(purpose, list) or not purpose or any(not isinstance(v, str) or not v for v in purpose):
        raise SpecError(f"{where}.purpose 必须为非空字符串数组")
    if len(set(purpose)) != len(purpose):
        raise SpecError(f"{where}.purpose 不得重复")
    _nonempty_string(f"{where}.rate_limit_policy", raw["rate_limit_policy"])

    local = raw["adapter"] in {"local_parts", "locked_package", "manual"}
    _https_url(f"{where}.base_url", raw["base_url"], nullable=local)
    _https_url(
        f"{where}.license_or_terms_url",
        raw["license_or_terms_url"],
        nullable=raw["kind"] in {"local_seed", "manual"} or not raw["enabled"],
    )
    if raw["attribution"] is not None:
        _nonempty_string(f"{where}.attribution", raw["attribution"])

    reviewed = raw["terms_reviewed_at"]
    if reviewed is not None:
        if not isinstance(reviewed, str):
            raise SpecError(f"{where}.terms_reviewed_at 必须为 YYYY-MM-DD 或 null")
        try:
            date.fromisoformat(reviewed)
        except ValueError as exc:
            raise SpecError(f"{where}.terms_reviewed_at 必须为 YYYY-MM-DD") from exc

    if raw["enabled"]:
        if raw["requires_credentials"]:
            raise SpecError(f"{where} 需要凭据时不得启用自动采集")
        if raw["automated_access"] in {"permission_required", "disabled"}:
            raise SpecError(f"{where} 未获自动访问权限却启用")
        if raw["automated_access"] == "allowed_after_review" and reviewed is None:
            raise SpecError(f"{where} 启用前必须完成条款复核")
        if raw["adapter"] in {"http_snapshot", "amd_cpu_official"} and raw["robots_policy"] != "obey":
            raise SpecError(f"{where} HTTP 自动来源必须遵守 robots")

    if raw["adapter"] == "amd_cpu_official":
        resources_path = _nonempty_string(f"{where}.resources_path", raw.get("resources_path"))
        resource = Path(resources_path)
        if resource.is_absolute() or ".." in resource.parts or resource.suffix != ".json":
            raise SpecError(f"{where}.resources_path 必须为数据目录内的 JSON 相对路径")
        policy = raw.get("policy_sha256")
        if not isinstance(policy, dict) or set(policy) != {"robots", "terms"}:
            raise SpecError(f"{where}.policy_sha256 必须只包含 robots/terms")
        for key, digest in policy.items():
            if not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest):
                raise SpecError(f"{where}.policy_sha256.{key} 必须为 SHA-256")
    elif any(key in raw for key in _OPTIONAL_KEYS):
        raise SpecError(f"{where} 仅 amd_cpu_official 可配置 resources_path/policy_sha256")

    return dict(raw)


def load_registry(path: Path = DEFAULT_REGISTRY_PATH) -> dict[str, dict[str, Any]]:
    """读取来源登记，返回 id -> source；任一结构或许可状态非法即失败。"""
    try:
        doc = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SpecError(f"读取 {path} 失败: {exc}") from exc
    if not isinstance(doc, dict) or set(doc) != {"schema_version", "sources"}:
        raise SpecError(f"{path.name} 顶层必须仅包含 schema_version/sources")
    if doc["schema_version"] != 1:
        raise SpecError(f"{path.name} schema_version 必须为 1")
    if not isinstance(doc["sources"], list) or not doc["sources"]:
        raise SpecError(f"{path.name} sources 必须为非空数组")

    sources: dict[str, dict[str, Any]] = {}
    for index, raw in enumerate(doc["sources"]):
        source = _validate_source(raw, index)
        if source["id"] in sources:
            raise SpecError(f"来源 {source['id']!r} 重复")
        sources[source["id"]] = source
    return sources


def get_registered_source(
    source_id: str, path: Path = DEFAULT_REGISTRY_PATH
) -> dict[str, Any]:
    sources = load_registry(path)
    if source_id not in sources:
        raise SpecError(f"来源 {source_id!r} 未登记")
    return sources[source_id]
