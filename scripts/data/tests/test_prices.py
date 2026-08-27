from __future__ import annotations

import csv
import json
from pathlib import Path

import pytest

from pcdata.automation import DataPaths, PipelineError
from pcdata.prices import (
    PRICE_CSV_HEADER,
    _stage_price_release,
    create_price_review,
    import_price_csv,
    load_price_csv,
    price_health,
)


def _paths(tmp_path: Path) -> DataPaths:
    repo = Path(__file__).resolve().parents[3]
    return DataPaths(repo_root=repo, data_root=repo / "scripts" / "data", runtime_root=tmp_path / "runtime")


def _row(sku: str, price: str, source: str, *, price_type: str = "regular", stock: str = "in_stock", variant: str = "exact") -> dict[str, str]:
    return {
        "sku": sku,
        "price_cny": price,
        "currency": "CNY",
        "source_id": source,
        "product_id": f"product-{source}-{sku}",
        "source_url": f"https://example.com/{source}/{sku}",
        "seller": source,
        "price_type": price_type,
        "stock_status": stock,
        "variant_match": variant,
        "observed_at": "2026-08-20T08:00:00+08:00",
        "raw_sha256": "a" * 64,
    }


def _csv(tmp_path: Path, rows: list[dict[str, str]]) -> Path:
    path = tmp_path / "prices.csv"
    with path.open("w", encoding="utf-8", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=PRICE_CSV_HEADER, lineterminator="\n")
        writer.writeheader()
        writer.writerows(rows)
    return path


def test_price_csv_classifies_observed_and_rejected(tmp_path: Path) -> None:
    sku = "case-asus-prime-ap201"
    rows = [
        _row(sku, "499", "official_store"),
        _row(sku, "450.00", "member-source", price_type="member"),
        _row(sku, "399.00", "sold-out", stock="out_of_stock"),
    ]
    observations = load_price_csv(_csv(tmp_path, rows), {sku})
    assert [item["decision_status"] for item in observations] == ["qualified", "observed_only", "rejected"]
    assert observations[2]["rejection_reasons"] == ["not_in_stock"]
    assert all(len(item["observation_id"]) == 64 for item in observations)


def test_three_sources_choose_actual_quote_closest_to_median(tmp_path: Path) -> None:
    paths = _paths(tmp_path)
    sku = "case-asus-prime-ap201"
    imported = import_price_csv(paths, _csv(tmp_path, [
        _row(sku, "400.00", "source-a"),
        _row(sku, "500.00", "source-b"),
        _row(sku, "900.00", "source-c"),
    ]), run_id="11111111-1111-4111-8111-111111111111")
    review = create_price_review(paths, imported["run_id"])
    selection_path = paths.data_root / "work" / "price-runs" / imported["run_id"] / "selection.jsonl"
    selected = [json.loads(line) for line in selection_path.read_text(encoding="utf-8").splitlines()]
    chosen = next(item for item in selected if item["sku"] == sku)
    assert chosen["price_cny"] == "500.00"
    assert chosen["source_id"] == "source-b"
    assert review["decision"] == "publish"


def test_over_25_percent_is_quarantined_and_keeps_last_known_good(tmp_path: Path) -> None:
    paths = _paths(tmp_path)
    sku = "case-asus-prime-ap201"
    imported = import_price_csv(paths, _csv(tmp_path, [_row(sku, "999.00", "manual")]), run_id="22222222-2222-4222-8222-222222222222")
    review = create_price_review(paths, imported["run_id"])
    selection_path = paths.data_root / "work" / "price-runs" / imported["run_id"] / "selection.jsonl"
    selected = [json.loads(line) for line in selection_path.read_text(encoding="utf-8").splitlines()]
    chosen = next(item for item in selected if item["sku"] == sku)
    assert chosen["price_cny"] == "499.00"
    assert chosen["carried_forward"] is True
    assert review["quarantined"][0]["reason"] == "price_change_over_25_percent"
    assert review["systemic_quarantine"] is False


def test_three_anomalies_quarantine_whole_batch(tmp_path: Path) -> None:
    paths = _paths(tmp_path)
    rows = [
        _row("case-asus-prime-ap201", "999.00", "manual"),
        _row("case-bequiet-pure-base-500dx", "1499.00", "manual"),
        _row("case-bequiet-shadow-base-800-fx", "3999.00", "manual"),
    ]
    imported = import_price_csv(paths, _csv(tmp_path, rows), run_id="33333333-3333-4333-8333-333333333333")
    review = create_price_review(paths, imported["run_id"])
    assert review["decision"] == "quarantine"
    assert review["systemic_quarantine"] is True


def test_invalid_header_unknown_sku_and_missing_health(tmp_path: Path) -> None:
    paths = _paths(tmp_path)
    bad = tmp_path / "bad.csv"
    bad.write_text("sku,price\nunknown,1\n", encoding="utf-8")
    with pytest.raises(PipelineError, match="表头"):
        load_price_csv(bad, {"known"})
    with pytest.raises(PipelineError, match="active_core"):
        load_price_csv(_csv(tmp_path, [_row("unknown", "1.00", "manual")]), {"known"})
    health = price_health(paths)
    assert health["status"] == "stale"
    assert health["selected"] == 160


def test_price_release_is_immutable_and_hashes_all_inputs(tmp_path: Path) -> None:
    paths = _paths(tmp_path)
    run_id = "44444444-4444-4444-8444-444444444444"
    imported = import_price_csv(paths, _csv(tmp_path, [_row("case-asus-prime-ap201", "500.00", "manual")]), run_id=run_id)
    review = create_price_review(paths, imported["run_id"])
    release_dir, manifest = _stage_price_release(paths, run_id, review, "manual")
    assert manifest["schema_version"] == 1
    assert {item["path"] for item in manifest["files"]} == {"observations.jsonl", "selection.jsonl", "review.json"}
    assert len(manifest["manifest_sha256"]) == 64
    same_dir, same_manifest = _stage_price_release(paths, run_id, review, "manual")
    assert same_dir == release_dir
    assert same_manifest["manifest_sha256"] == manifest["manifest_sha256"]
