"""P11 来源登记：许可状态和自动访问边界。"""

import json
from pathlib import Path

import pytest

from pcdata.canonical import SpecError
from pcdata.registry import DEFAULT_REGISTRY_PATH, get_registered_source, load_registry


def _write(tmp_path: Path, sources: list[dict]) -> Path:
    path = tmp_path / "registry.json"
    path.write_text(
        json.dumps({"schema_version": 1, "sources": sources}, ensure_ascii=False),
        encoding="utf-8",
    )
    return path


def _source(**overrides):
    value = {
        "id": "vendor_specs",
        "kind": "official_catalog",
        "adapter": "http_snapshot",
        "base_url": "https://vendor.example/products",
        "purpose": ["spec"],
        "trust_tier": "A",
        "license_or_terms_url": "https://vendor.example/terms",
        "attribution": "Vendor",
        "requires_credentials": False,
        "automated_access": "allowed_after_review",
        "robots_policy": "obey",
        "rate_limit_policy": "weekly_single_request",
        "failure_mode": "block_run",
        "schedule": "weekly",
        "enabled": True,
        "terms_reviewed_at": "2026-08-24",
    }
    value.update(overrides)
    return value


def test_默认registry合法且京东不在实施来源():
    sources = load_registry()
    assert {
        "seed_catalog", "pc_part_dataset", "dbgpu", "amd_products",
        "manual_price_csv", "zol_price_candidate",
    } == set(sources)
    assert sources["manual_price_csv"]["enabled"] is True
    assert sources["zol_price_candidate"]["enabled"] is False
    assert sources["zol_price_candidate"]["automated_access"] == "permission_required"
    assert "jd" not in " ".join(sources).lower()
    assert sources["amd_products"]["enabled"]
    assert sources["amd_products"]["adapter"] == "amd_cpu_official"
    assert get_registered_source("seed_catalog")["adapter"] == "local_parts"


@pytest.mark.parametrize(
    ("change", "message"),
    [
        ({"id": "../escape"}, "id 非法"),
        ({"base_url": "http://vendor.example"}, "https URL"),
        ({"base_url": "https://user:pass@vendor.example"}, "不含凭据"),
        ({"base_url": "https://vendor.example/products?token=x"}, "不含凭据"),
        ({"enabled": True, "terms_reviewed_at": None}, "条款复核"),
        ({"enabled": True, "automated_access": "permission_required"}, "未获自动访问权限"),
        ({"enabled": True, "requires_credentials": True}, "需要凭据时不得启用"),
        ({"robots_policy": "not_applicable"}, "必须遵守 robots"),
    ],
)
def test_非法自动来源失败(tmp_path, change, message):
    with pytest.raises(SpecError, match=message):
        load_registry(_write(tmp_path, [_source(**change)]))


def test_未知键和重复来源失败(tmp_path):
    source = _source()
    source["unexpected"] = True
    with pytest.raises(SpecError, match="未知键"):
        load_registry(_write(tmp_path, [source]))
    source = _source()
    with pytest.raises(SpecError, match="重复"):
        load_registry(_write(tmp_path, [source, source]))


def test_五份schema均为合法json():
    schema_dir = DEFAULT_REGISTRY_PATH.parent / "schemas"
    names = {
        "source-registry.schema.json",
        "run-manifest.schema.json",
        "evidence.schema.json",
        "review.schema.json",
        "release-manifest.schema.json",
    }
    assert names <= {path.name for path in schema_dir.glob("*.json")}
    for name in names:
        value = json.loads((schema_dir / name).read_text(encoding="utf-8"))
        assert value["$schema"].endswith("2020-12/schema")
