"""Freeze live smoke expectations for the snapshot-11 catalog before any provider calls."""
import hashlib
import json

from build_current_178 import OUT, ROOT, sha


def main():
    source = OUT / "mechanisms/suite.json"
    base = json.loads(source.read_bytes())
    assert base["version"] == "current-178-mechanisms-20260917"
    office_fields = {"budget_cny": {"value": 4000, "status": "active"}}
    itx_fields = {"budget_cny": {"value": 9000, "status": "active"},
                  "size_pref": {"value": "itx", "status": "active"}}
    cases = [
        {"id": "SM178-OFFICE-IGPU", "title": "4000元办公核显：无独显需求下交付完整方案",
         "source": "C123-006场景的新数据 live 变体；用户明确不要独立显卡，不锁定除预算外的模型决策", "steps": [
            {"kind": "message", "text": "预算4000元，全新办公主机，主要用于文档、网页浏览和视频会议，不需要独立显卡，其他不限。",
             "expect": {"versions": 0, "builder_calls": 0, "fields": office_fields}},
            {"kind": "confirm", "expect": {"versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                                           "budget_ceiling_cny": "4000", "require_tools": ["evaluate"],
                                           "forbid_tools": ["search_web", "read_page"], "fields": office_fields}},
            {"kind": "refresh", "expect": {"versions": 1, "builder_calls": 0}},
        ]},
        {"id": "SM178-ITX-SFX", "title": "ITX机箱指定SFX电源：形态规则下交付完整方案",
         "source": "C123-005场景的新数据 live 变体；用户点名电源型号，其余选型不锁定", "steps": [
            {"kind": "message", "text": "预算9000元，普通办公用，必须ITX小机箱，电源指定酷冷至尊V SFX Gold 850W 白色版，其他配件你来配。",
             "expect": {"versions": 0, "builder_calls": 0, "fields": itx_fields}},
            {"kind": "confirm", "expect": {"versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                                           "budget_ceiling_cny": "9000", "require_tools": ["evaluate"],
                                           "forbid_tools": ["search_web", "read_page"],
                                           "selected_parts": {"psu": "psu-cm-v850sfx-gold-white"}, "fields": itx_fields}},
            {"kind": "refresh", "expect": {"versions": 1, "builder_calls": 0}},
        ]},
    ]
    suite = {"live": True, "version": "live-smoke-178-20260917",
             "provenance": "两项真实模型冒烟（办公核显、ITX SFX 电源）：每场景只执行一次，无预置模型回答，网页与搜索仍离线；目录为 current-178-20260917 冻结快照11（126件active）。",
             "catalog": base["catalog"], "pages": {}, "cases": cases}
    raw = (json.dumps(suite, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    target = ROOT / "internal/planningeval/testdata/live-smoke-178-20260917"
    target.mkdir(exist_ok=False)
    (target / "suite.json").write_bytes(raw)
    (target / "provenance.json").write_bytes((json.dumps({
        "suite_sha256": hashlib.sha256(raw).hexdigest(),
        "source_suite_sha256": sha(source), "snapshot_id": 11,
        "parts_release": "4dc0da59-f63b-5adc-a3b0-cd30394b7bd9",
        "max_model_requests": 16, "external_search_requests": 0,
        "external_page_requests": 0, "embedding_requests": 0,
        "limitations": ["Single attempt per scenario, no representative success-rate estimate.",
                        "Smoke checks execution and delivery, not selection quality or speed.",
                        "Second scenario shares the remaining request allowance."]},
        ensure_ascii=False, indent=2) + "\n").encode("utf-8"))
    (target / "budget.json").write_text(json.dumps(dict(
        model_requests_limit=16, model_requests_used=0, model_requests_reserved=16,
        external_search_limit=3, external_search_used=0, page_read_limit=6, page_read_used=0,
        embedding_requests=0, buyer_requests=0,
        registered_cases=["SM178-OFFICE-IGPU", "SM178-ITX-SFX"],
        status="registered_not_executed", model_pin="docs/eval/planning-v2/baseline-20260915.json",
        policy="One batch; every provider attempt counts; no automatic retries or supplemental batch."),
        ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    print("Registered one batch: 2 scenarios, at most 16 provider requests total, zero external tools")


if __name__ == "__main__":
    main()
