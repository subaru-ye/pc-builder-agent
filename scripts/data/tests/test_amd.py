"""AMD 官方 CPU 固定映射、确定性解析、政策门禁和字段 evidence。"""

import hashlib
import json
from email.message import Message
from pathlib import Path

import pytest

from pcdata.amd import build_amd_evidence, load_amd_products, parse_amd_cpu_html
from pcdata.automation import HTTPCollector, PipelineError, _terms_policy_digest
from pcdata.canonical import SpecError


DATA_ROOT = Path(__file__).resolve().parents[1]


def _html(**overrides: str) -> bytes:
    values = {
        "Name": "AMD Ryzen™ 5 7600",
        "Default TDP": "65 W",
        "CPU Socket": "AM5",
        "Supporting Chipsets": "A620, B650, X670",
        "Graphics Model": "AMD Radeon™ Graphics",
    }
    values.update(overrides)
    pairs = "".join(f"<dt>{key}</dt><dd>{value}</dd>" for key, value in values.items())
    return f"<!doctype html><html><body><dl>{pairs}</dl></body></html>".encode()


class _Response:
    def __init__(self, body: bytes):
        self.body = body
        self.status = 200
        self.headers = Message()

    def getcode(self):
        return self.status

    def read(self, amount):
        return self.body[:amount]

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None


def test_固定映射精确覆盖当前amd_cpu():
    products = load_amd_products(DATA_ROOT / "amd-cpu-products.json")
    cpus = [json.loads(line) for line in (DATA_ROOT / "parts/cpu.jsonl").read_text(encoding="utf-8").splitlines()]
    expected = {row["sku"] for row in cpus if row["brand"] == "AMD"}
    assert {item["sku"] for item in products} == expected
    assert len(products) == len(expected)
    assert all(item["url"].startswith("https://www.amd.com/") for item in products)


def test_确定性解析五个字段():
    parsed, excerpts = parse_amd_cpu_html(_html(), "AMD Ryzen 5 7600")
    assert parsed == {
        "model": "AMD Ryzen™ 5 7600",
        "specs.socket": "AM5",
        "specs.supported_chipsets": ["A620", "B650", "X670"],
        "specs.tdp_w": 65,
        "specs.has_igpu": True,
    }
    assert excerpts["specs.tdp_w"] == "Default TDP: 65 W"


@pytest.mark.parametrize(
    ("overrides", "message"),
    [
        ({"Name": "AMD Ryzen 7 7700"}, "型号不匹配"),
        ({"Default TDP": "65"}, "单位非法"),
        ({"CPU Socket": "Socket AM5"}, "Socket 非法"),
        ({"Graphics Model": "unknown"}, "无法确定核显"),
    ],
)
def test_身份或字段异常失败关闭(overrides, message):
    with pytest.raises(SpecError, match=message):
        parse_amd_cpu_html(_html(**overrides), "AMD Ryzen 5 7600")


def test_evidence_id稳定并区分一致与冲突():
    parsed, excerpts = parse_amd_cpu_html(_html(), "AMD Ryzen 5 7600")
    current = {
        "model": "Ryzen 5 7600",
        "specs": {
            "socket": "AM5",
            "supported_chipsets": ["B650"],
            "tdp_w": 65,
            "has_igpu": True,
        },
    }
    arguments = {
        "source_id": "amd_products",
        "source_url": "https://www.amd.com/en/products/test.html",
        "sku": "cpu-r5-7600",
        "expected_name": "AMD Ryzen 5 7600",
        "current": current,
        "parsed": parsed,
        "excerpts": excerpts,
        "raw_sha256": "a" * 64,
        "captured_at": "2026-08-24T00:00:00Z",
    }
    first = build_amd_evidence(**arguments)
    second = build_amd_evidence(**arguments)
    assert [item["id"] for item in first] == [item["id"] for item in second]
    assert {item["evidence_status"] for item in first} == {"verified"}
    current["specs"]["tdp_w"] = 105
    conflict = build_amd_evidence(**arguments)
    assert next(item for item in conflict if item["field"] == "specs.tdp_w")["evidence_status"] == "conflict"


def test_政策哈希变化和robots禁止均隔离():
    robots = b"User-agent: *\nAllow: /products/\n"
    terms = ("<html><body><h1>Terms and Conditions</h1><p>" + "legal text " * 120 + "</p></body></html>").encode()

    def opener(request, timeout):
        del timeout
        return _Response(robots if request.full_url.endswith("/robots.txt") else terms)

    source = {
        "id": "amd_products",
        "base_url": "https://www.amd.com",
        "license_or_terms_url": "https://www.amd.com/en/legal/terms-and-conditions.html",
        "policy_sha256": {
            "robots": hashlib.sha256(robots).hexdigest(),
            "terms": _terms_policy_digest(terms),
        },
    }
    collector = HTTPCollector(opener=opener, allow_http_for_test=True)
    collector.check_policy(source, ["https://www.amd.com/products/cpu.html"])
    source["policy_sha256"]["terms"] = "0" * 64
    with pytest.raises(PipelineError) as changed:
        collector.check_policy(source, ["https://www.amd.com/products/cpu.html"])
    assert changed.value.code == "source_policy_changed"

    denied = b"User-agent: *\nDisallow: /products/\n"
    source["policy_sha256"] = {
        "robots": hashlib.sha256(denied).hexdigest(),
        "terms": _terms_policy_digest(terms),
    }

    def denied_opener(request, timeout):
        del timeout
        return _Response(denied if request.full_url.endswith("/robots.txt") else terms)

    with pytest.raises(PipelineError) as blocked:
        HTTPCollector(opener=denied_opener, allow_http_for_test=True).check_policy(
            source, ["https://www.amd.com/products/cpu.html"]
        )
    assert blocked.value.code == "robots_disallowed"
