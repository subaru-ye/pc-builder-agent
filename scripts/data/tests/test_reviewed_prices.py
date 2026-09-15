"""Reviewed aggregate quotes remain dated reference prices with unknown stock."""
import json
from copy import deepcopy

import pytest

from pcdata.automation import PipelineError
from pcdata.prices import (
    _observation_identity, _select_candidate, create_price_review,
    import_price_observations, publish_price_review,
)
from test_prices import _paths, _row


def reviewed():
    row = _row("case-asus-prime-ap201", "500.00", "maishou88:jd", price_type="listing", stock="unknown")
    row.update(schema_version=1, collector_id="maishou_reviewed", availability_basis="unknown",
               decision_status="qualified", rejection_reasons=[])
    row["observation_id"] = _observation_identity(row)
    return row


def test_reviewed_quote_preserves_date_and_requires_manual_publication(tmp_path):
    paths = _paths(tmp_path)
    row = reviewed()
    imported = import_price_observations(paths, [row], source_file_sha256="a"*64)
    review = create_price_review(paths, imported["run_id"])
    assert review["model_used"] is True
    selection = paths.data_root / "work/price-runs" / imported["run_id"] / "selection.jsonl"
    chosen = next(json.loads(line) for line in selection.read_text().splitlines() if json.loads(line)["sku"] == row["sku"])
    assert chosen["observed_at"] == row["observed_at"]
    assert chosen["price_type"] == "listing" and chosen["availability_basis"] == "unknown"
    with pytest.raises(PipelineError) as error:
        publish_price_review(paths, imported["run_id"], policy="automatic")
    assert error.value.code == "manual_review_required"


@pytest.mark.parametrize("change", [
    {"stock_status":"in_stock"}, {"price_type":"regular"},
    {"variant_match":"unknown"}, {"raw_sha256":"b"*64},
    {"availability_basis":"confirmed_stock"},
])
def test_reviewed_quote_rejects_unverified_claims(tmp_path, change):
    row = reviewed()
    row.update(change)
    row["observation_id"] = _observation_identity(row)
    with pytest.raises(PipelineError):
        import_price_observations(_paths(tmp_path), [row], source_file_sha256="a"*64)


def test_reviewed_quote_requires_one_explicit_offer():
    row = reviewed()
    assert _select_candidate([row]) == row
    other = deepcopy(row)
    other["price_cny"] = "400.00"
    with pytest.raises(PipelineError) as error:
        _select_candidate([row, other])
    assert error.value.code == "ambiguous_reviewed_offer"
