"""Register two current-catalog live scenarios without making provider calls."""
import copy
import json

from build_current_123 import ROOT, OUT, sha, write


def main():
    source = OUT / "mechanisms/suite.json"
    report_path = ROOT / "artifacts/current-123-mechanisms-20260915-r3/report.json"
    suite = json.loads(source.read_bytes())
    report = json.loads(report_path.read_bytes())
    assert report["suite_sha256"] == sha(source) and report["passed"] == 10
    upgrade = suite["cases"][0]
    base = report["cases"][0]["steps"][1]["result"]
    d = base["draft"]
    upgrade = dict(id="LIVE123-CPU", title="已有有效配置直接升级CPU，保留预算和软偏好",
        source="C123-001原文；原配置为当前目录离线可行见证，不是历史真实模型成功。",
        previous_build=dict(selection=dict(schema_version=1, build_ref=d["build_ref"], parts=d["selection"]),
                            requirement=dict(schema_version=1, budget_cny=7500, use_case=dict(type="productivity"), noise_pref="silent"),
                            source="current-123-mechanisms-20260915-r3/C123-001/confirm", snapshot=base),
        steps=[copy.deepcopy(upgrade["steps"][2]), dict(kind="refresh", expect=dict(versions=2, builder_calls=0))])
    retired = copy.deepcopy(suite["cases"][1])
    retired["id"] = "LIVE123-RETIRED"
    retired["source"] = "C123-002原文及原历史快照；检验模型是否自行替换退库配件。"
    for c in [upgrade, retired]:
        for step in c["steps"]:
            step.pop("screen_oracle", None)
            step.pop("builder_oracle", None)
            # Do not require authored query shape, exact alternative SKU or
            # number of model turns. Keep original business delivery assertions.
            step["expect"].pop("search_candidates", None)
            step["expect"].pop("require_tools", None)
            if step["kind"] == "message":
                step["expect"]["require_tools"] = ["evaluate"]
    suite.update(version="current-123-live-20260915", live=True, cases=[upgrade, retired],
                 provenance="两场景一次真实验证；固定当前模型，共享12次请求硬上限，不重跑追分，外部工具离线。")
    target = OUT / "live"
    assert not target.exists()
    write(target / "suite.json", suite)
    write(target / "provenance.json", dict(suite_sha256=sha(target / "suite.json"), source_suite_sha256=sha(source),
          source_report_sha256=sha(report_path), generator_sha256=sha(ROOT / "scripts/eval/planning/prepare_current_live.py"),
          max_model_requests=12, external_search_requests=0, external_page_requests=0, embedding_requests=0,
          limitations=["Single attempt per scenario, no representative success-rate estimate.",
                       "Correct CPU change does not independently prove workload speedup.",
                       "Second scenario may have only the remaining shared request allowance."]))
    write(OUT / "budget.json", dict(model_requests_limit=12, model_requests_used=0, model_requests_reserved=12,
          external_search_limit=3, external_search_used=0, page_read_limit=6, page_read_used=0,
          embedding_requests=0, buyer_requests=0, registered_cases=["LIVE123-CPU", "LIVE123-RETIRED"],
          status="registered_not_executed", model_pin="docs/eval/planning-v2/baseline-20260915.json",
          policy="One batch; every provider attempt counts; no automatic retries or supplemental batch."))
    print("Registered one batch: 2 scenarios, at most 12 provider requests total, zero external tools")


if __name__ == "__main__":
    main()
