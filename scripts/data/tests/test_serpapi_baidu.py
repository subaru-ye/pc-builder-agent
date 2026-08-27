from __future__ import annotations

import json
import threading
from datetime import UTC, datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

import pytest

from pcdata.automation import DataPaths, PipelineError
from pcdata.serpapi_baidu import (
    SerpApiClient,
    SerpApiSettings,
    _derived_identity,
    _load_config,
    _listing_rows,
    _matches,
    _parse_price,
    collect_serpapi_baidu,
)


class FakeSerpApi:
    def __init__(self, *, status: int = 200, plan: str = "Free", usage: int = 10, remaining: int = 240) -> None:
        self.status = status
        self.plan = plan
        self.usage = usage
        self.remaining = remaining
        self.requests: list[tuple[str, dict[str, list[str]]]] = []
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802
                parsed = urlsplit(self.path)
                query = parse_qs(parsed.query)
                owner.requests.append((parsed.path, query))
                if owner.status != 200:
                    self.send_response(owner.status)
                    self.end_headers()
                    return
                if parsed.path == "/account.json":
                    payload = {"plan_name": owner.plan, "this_month_usage": owner.usage, "plan_searches_left": owner.remaining}
                else:
                    title = query.get("q", [""])[0]
                    payload = {"shopping_results": [
                        {"title": title, "price": "￥499.00", "source": "商家甲", "link": "https://shop-a.example/item?utm_source=baidu"},
                        {"title": title, "price": "509", "source": "商家乙", "link": "https://shop-b.example/item#offer"},
                    ]}
                body = json.dumps(payload, ensure_ascii=False).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, _format: str, *_args: object) -> None:
                return

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self) -> "FakeSerpApi":
        self.thread.start()
        return self

    def __exit__(self, *_args: object) -> None:
        self.server.shutdown()
        self.thread.join(timeout=2)
        self.server.server_close()

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.server.server_port}"


def _settings(server: FakeSerpApi, *, budget: int = 240, free: bool = True) -> SerpApiSettings:
    return SerpApiSettings("test-secret-key", server.base_url, budget, 7, free)


def _paths(tmp_path: Path) -> DataPaths:
    repo = Path(__file__).resolve().parents[3]
    return DataPaths(repo, repo / "scripts" / "data", tmp_path / "runtime")


def test_account_free_plan_budget_and_auth_errors() -> None:
    with FakeSerpApi() as server:
        account = SerpApiClient(_settings(server)).account(12)
        assert account == {"plan": "Free", "usage": 10, "remaining": 240}
        assert server.requests[0][1]["api_key"] == ["test-secret-key"]
    with FakeSerpApi(plan="Developer") as server:
        with pytest.raises(PipelineError) as error:
            SerpApiClient(_settings(server)).account(1)
        assert error.value.code == "serpapi_not_free_plan"
        assert "test-secret-key" not in str(error.value)
    with FakeSerpApi(usage=239) as server:
        with pytest.raises(PipelineError) as error:
            SerpApiClient(_settings(server)).account(2)
        assert error.value.code == "serpapi_budget_exhausted"
    with FakeSerpApi(status=429) as server:
        with pytest.raises(PipelineError) as error:
            SerpApiClient(_settings(server)).account(1)
        assert error.value.code == "serpapi_rate_limit"


def test_listing_matching_requires_exact_identity_and_two_merchants() -> None:
    identity = {"brand_aliases": ["西部数据", "wd"], "required_tokens": ["sn770", "1tb"], "forbidden_tokens": ["2tb"]}
    payload = {"shopping_results": [
        {"title": "西部数据 WD SN770 1TB SSD", "price": "￥499", "source": "甲", "link": "https://a.example/p"},
        {"title": "WD SN770 1TB 固态硬盘", "price": "509.00", "source": "乙", "link": "https://b.example/p"},
        {"title": "WD SN770 2TB 固态硬盘", "price": "899", "source": "丙", "link": "https://c.example/p"},
        {"title": "WD SN770 1TB", "price": "月供 49 元", "source": "丁", "link": "https://d.example/p"},
    ]}
    rows = _listing_rows("ssd-wd-sn770-1tb", payload, "a" * 64, identity, "2026-08-27T00:00:00Z")
    assert len(rows) == 2
    assert {row["decision_status"] for row in rows} == {"qualified"}
    assert {row["availability_basis"] for row in rows} == {"search_listing"}
    assert all(row["stock_status"] == "unknown" for row in rows)
    assert _parse_price("399-499元") is None
    assert _parse_price("券后￥399") is None
    cpu_identity = {"brand_aliases": ["intel"], "required_tokens": ["i5", "12400f"], "forbidden_tokens": []}
    assert _matches("Intel Core i5-12400F 盒装", cpu_identity) is True
    assert _matches("Intel Core i5-13400F 盒装", cpu_identity) is False
    gpu_identity = {"brand_aliases": ["gigabyte"], "required_tokens": ["5060"], "forbidden_tokens": ["ti", "super"]}
    assert _matches("Gigabyte GeForce RTX 5060 Windforce", gpu_identity) is True
    assert _matches("Gigabyte GeForce RTX 5060 Ti Windforce", gpu_identity) is False
    ryzen_identity = {"brand_aliases": ["amd"], "required_tokens": ["7600"], "forbidden_tokens": ["x", "g"]}
    assert _matches("AMD Ryzen 5 7600", ryzen_identity) is True
    assert _matches("AMD Ryzen 5 7600X", ryzen_identity) is False


def test_canary_uses_exactly_twelve_calls_and_never_publishes(tmp_path: Path) -> None:
    with FakeSerpApi() as server:
        report = collect_serpapi_baidu(
            _paths(tmp_path), mode="canary",
            scheduled_for=datetime(2026, 8, 27, 3, 45, tzinfo=UTC),
            client=SerpApiClient(_settings(server)),
        )
    assert report["calls"] == 12
    assert report["shopping_result_skus"] == 12
    assert report["dual_match_skus"] == 12
    assert report["automatic_thresholds_met"] is True
    assert report["status"] == "awaiting_manual_review"
    assert report["manual_zero_mismatch_review_required"] is True
    assert len([path for path, _ in server.requests if path == "/search.json"]) == 12
    serialized = json.dumps(report, ensure_ascii=False)
    assert "test-secret-key" not in serialized


def test_daily_refuses_before_canary_activation(tmp_path: Path) -> None:
    with FakeSerpApi() as server:
        with pytest.raises(PipelineError) as error:
            collect_serpapi_baidu(
                _paths(tmp_path), mode="daily",
                scheduled_for=datetime(2026, 8, 27, 3, 45, tzinfo=UTC),
                client=SerpApiClient(_settings(server)),
            )
    assert error.value.code == "serpapi_not_activated"
    assert server.requests == []


def test_all_96_query_mappings_are_frozen_and_match_catalog() -> None:
    config, parts = _load_config(DataPaths.defaults())
    flattened = [sku for values in config["core_skus"].values() for sku in values]
    assert len(flattened) == len(set(flattened)) == 96
    assert all(config["products"][sku] == _derived_identity(config, parts[sku]) for sku in flattened)
