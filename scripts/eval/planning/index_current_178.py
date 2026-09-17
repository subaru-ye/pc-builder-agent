"""Index existing evaluation assets and summarize the frozen snapshot-11 catalog, offline."""
import json
import sys
from collections import Counter
from decimal import Decimal

from build_current_178 import ROOT, OUT, FREEZE, SNAPSHOT_SHA, sha, write
from build_snapshot_pair import load_pinned

sys.path.insert(0, str(ROOT / "scripts/data/src"))
from pcdata.coverage import WARNING_ONLY_FIELDS, _required_fields


def main():
    data = load_pinned(FREEZE / "database-snapshot.json", SNAPSHOT_SHA)
    active = [p for p in data["parts"] if p["active"] and p["catalog_state"] == "active_core"]
    retired = sum(1 for p in data["parts"] if p["catalog_state"] == "retired")
    prices = {p["sku"]: Decimal(str(p["price_cny"])) for p in data["prices"] if p["snapshot_id"] == 11}
    gaps = [{"sku": p["sku"], "category": p["category"], "missing": [f for f in _required_fields(p) + WARNING_ONLY_FIELDS[p["category"]] if p["specs"].get(f) is None]} for p in active]
    gaps = [p for p in gaps if p["missing"]]
    cheapest = {}
    for category in sorted({p["category"] for p in active}):
        cheapest[category] = min(prices[p["sku"]] for p in active if p["category"] == category)
    # A relaxed price lower bound: ignore compatibility, but an absent GPU
    # requires a CPU with a known iGPU. A valid build cannot be cheaper.
    igpu = min(prices[p["sku"]] for p in active if p["category"] == "cpu" and p["specs"].get("has_igpu") is True)
    common = sum(v for k, v in cheapest.items() if k not in {"cpu", "gpu"})
    summary = dict(active_parts=126, retired_parts=retired, snapshot_id=11, snapshot_date="2026-09-17",
                   parts_release="4dc0da59-f63b-5adc-a3b0-cd30394b7bd9", categories=dict(Counter(p["category"] for p in active)),
                   compatibility_spec_gaps=gaps, unpriced_active_parts=0,
                   price_lower_bound=dict(with_gpu=str(common+cheapest["cpu"]+cheapest["gpu"]), without_gpu=str(common+igpu),
                                          meaning="Necessary lower bound only; ignores compatibility and unknown specs, not a selectable build."),
                   limits=["No noise or FPS measurement dataset.", "Current PSU schema has no dimensions/form factor, so ITX electrical-rule pass does not establish enclosure fit.",
                           "Buyer observation dates remain unchanged; prices do not establish current stock.",
                           "RX 6600/7600 and 5600G/5700G intake is deferred (docs/data/数据获取与发布规则.md §5.4).",
                           "10 coolers keep cooling_capacity_w unknown after recheck; unknowns stay honest."])
    write(OUT / "coverage.json", summary)
    entries = []
    for name in ["mechanisms", "screening-recorded", "thermal-recorded"]:
        path = OUT / name / "suite.json"
        suite = json.loads(path.read_bytes())
        entries.append(dict(id=name, path=path.relative_to(ROOT).as_posix(), sha256=sha(path),
                            cases=[dict(id=c["id"], title=c["title"], source=c["source"]) for c in suite["cases"]],
                            steps=sum(len(c["steps"]) for c in suite["cases"]), active_parts=126,
                            mode="authored_oracle" if name == "mechanisms" else "recorded_responses",
                            is_live_score=False))
    cpu = OUT / "cpu-recorded/suite.json"
    if cpu.exists():
        entries.append(dict(id="cpu-recorded", path=cpu.relative_to(ROOT).as_posix(), sha256=sha(cpu),
                            cases=["LIVE123-CPU"], active_parts=126, mode="recorded_responses", is_live_score=False,
                            source="current-123-20260915-r2/live-recording; unchanged first-case answers and expectations"))
    historical = ROOT / "internal/evalsuite/testdata/suites/v1.5.json"
    manifest = json.loads(historical.read_bytes())
    mapped = []
    for c in manifest["cases"]:
        original = json.loads((historical.parent.parent / "cases" / c["file"]).read_bytes())
        stage = "screening" if original.get("stage") == "screening" else "builder"
        mapped.append(dict(id=c["id"], original_file=c["file"], original_sha256=c["sha256"],
                           migration=f"internal/planningeval/testdata/legacy-{stage}-v1.5-20260915/suite.json",
                           status="Historical input migrated; not a full live re-evaluation on the current 126-part catalog."))
    write(OUT / "index.json", dict(version="current-178-20260917", default_suite=entries[0]["path"], suites=entries,
          historical_v15=dict(cases=50, manifest_sha256=sha(historical), mappings=mapped),
          fixed_catalog_regressions=["internal/planningeval/testdata/proposal-review-20260915/suite.json"],
          supplement_regressions=["internal/planning/supplement_test.go", "internal/producthttp/planning_supplement_test.go", "web/e2e-requirements/supplement.spec.ts"],
          no_combined_score="Overlapping historical, authored and recorded scenarios retain their own denominators."))
    print(json.dumps(dict(categories=summary["categories"], spec_gap_parts=len(gaps), price_lower_bound=summary["price_lower_bound"], indexed_suites=len(entries), historical_mappings=len(mapped))))


if __name__ == "__main__":
    main()
