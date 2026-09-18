"""Register the full snapshot-11 mechanisms batch as an oracle-free live suite; no provider calls."""
import copy
import hashlib
import json

from build_current_178 import OUT, ROOT, sha, write

BUDGET = 200


def strip(step):
    step = copy.deepcopy(step)
    step.pop("screen_oracle", None)
    step.pop("builder_oracle", None)
    expect = step.setdefault("expect", {})
    # Authored tool-query shape, exact alternative SKU and turn counts are
    # model choices; delivery assertions and prompt-plumbing checks stay.
    expect.pop("search_candidates", None)
    expect.pop("candidate_specs", None)
    expect.pop("builder_calls", None)
    # Live mode keeps web and semantic tools offline; requiring them can
    # never pass. Authored local-tool requirements stay and stay binding.
    offline = {"search_semantic", "read_page"}
    if expect.get("require_tools"):
        kept = [a for a in expect["require_tools"] if a not in offline]
        if kept:
            expect["require_tools"] = kept
        else:
            expect.pop("require_tools")
    if step["kind"] == "refresh":
        # Refresh must never execute; keep the zero-builder-call contract.
        expect["builder_calls"] = 0
    expect.pop("selected_parts", None)
    return step


def main():
    source = OUT / "mechanisms/suite.json"
    report_path = ROOT / "artifacts/planning-replay-178-mech-r2/report.json"
    suite = json.loads(source.read_bytes())
    report = json.loads(report_path.read_bytes())
    assert report["suite_sha256"] == sha(source) and report["passed"] == 10

    cases = []
    for original in suite["cases"]:
        case = dict(original)
        case["source"] = original["source"] + " Live full-batch variant; oracles stripped, business assertions kept, model choices free."
        steps = [strip(s) for s in original["steps"]]
        case["steps"] = steps
        if case["id"] == "C123-005":
            # Authored flow forces an ATX PSU first; a live model may pick an
            # SFX PSU directly, so keep one ITX build observed end to end.
            steps = [steps[0], steps[1], steps[-1]]
            steps[1]["expect"].pop("issues_contain", None)
        if case["id"] == "C123-007":
            # Whether the live model picks a cooler with unknown capacity is a
            # model decision; record whatever delivery results without pinning.
            steps = [steps[0], steps[1]]
            steps[1]["expect"] = {"missing_prices": 0}
        if case["id"] == "C123-001":
            # r2 evidence: v1 spent 7381/7500 and the 5700X upgrade adds 510,
            # so step-3 cannot both preserve every part and stay under the
            # must budget; the catalog has no cheaper 32GB DDR4 kit. Grade
            # "尽量不动" by its substance: GPU tier kept, memory capacity not
            # silently reduced; cheaper same-category swaps remain allowed.
            for s in steps:
                if s.get("expect", {}).pop("preserve_other_parts", None):
                    s["expect"]["preserve_essential_parts"] = True
        if case["id"] in ("B2-002", "B2-003"):
            # r1/r2 evidence: the authored step-2 confirm cannot run because the
            # user explicitly deferred configuration ("先看看升级方向" /
            # "先比较处理器"), so screening correctly stays collecting and the
            # session never reaches requirement_ready. Drop that confirm; the
            # later message/refresh steps still exercise plan and continuation.
            steps = [s for i, s in enumerate(steps) if not (i == 1 and s.get("kind") == "confirm")]
        # The per-id blocks rebind `steps` (slice or filter); write the result
        # back or only in-place dict edits survive into the suite.
        case["steps"] = steps
        cases.append(case)
    # r1/r2 evidence: C123-001 alone consumed ~35 calls and the tail cases
    # (C123-007, B2-*) starved at 0 calls, so the B2 fixes were never live
    # validated. Run the cheap B2 scenarios first; their screening+confirm
    # flow needs only a handful of calls.
    order = ["B2-001", "B2-002", "B2-003", "C123-007", "C123-001",
             "C123-002", "C123-003", "C123-004", "C123-005", "C123-006"]
    cases.sort(key=lambda c: order.index(c["id"]))
    suite = dict(version="current-178-live-mechanisms-20260918-r3", live=True,
                 provenance="全量10场景真实模型：oracle剥离，保留authored业务交付断言与本地工具要求（离线工具要求剔除），不额外强加工具断言；模型选型自由，外部工具离线。目录为 current-178-20260917 冻结快照11（126件active）。相对r2套件：B2-002/003剔除被用户明确推迟配置所阻挡的step2 confirm，C123-001 step3的preserve_other_parts改为preserve_essential_parts（r2证据：逐件相等与预算硬上限在v1余量119元+CPU差价510元下不可同时满足，见 docs/eval/planning-v2/current-178-20260917.md）；执行顺序B2与C123-007在前（r1/r2证据：C123-001单例消耗~35次调用导致队尾case饿死0调用，B2修复从未被现场验证）；待验证修复含keyword_hits检索、budget_alternatives、evaluate时机、noise_pref与confirm时机提示词、超预算最小替换策略、已有件缺失检索取证与clarify、缺字段候选换选重试。",
                 catalog=suite["catalog"], pages={}, cases=cases)
    target = ROOT / "internal/planningeval/testdata/current-178-live-20260918"
    assert not target.exists()
    write(target / "suite.json", suite)
    write(target / "provenance.json", dict(suite_sha256=sha(target / "suite.json"), source_suite_sha256=sha(source),
          source_report_sha256=sha(report_path), generator_sha256=sha(ROOT / "scripts/eval/planning/prepare_current_live_178.py"),
          snapshot_id=11, parts_release="4dc0da59-f63b-5adc-a3b0-cd30394b7bd9",
          max_model_requests=BUDGET, external_search_requests=0, external_page_requests=0, embedding_requests=0,
          limitations=["Single attempt per scenario, no representative success-rate estimate.",
                       "Correct delivery does not independently prove workload speedup or noise.",
                       "Scenarios share one request allowance; later cases may stop early when it runs out."]))
    write(target / "budget.json", dict(model_requests_limit=BUDGET, model_requests_used=0, model_requests_reserved=BUDGET,
          external_search_limit=3, external_search_used=0, page_read_limit=6, page_read_used=0,
          embedding_requests=0, buyer_requests=0, registered_cases=[c["id"] for c in cases],
          status="registered_not_executed", model_pin="docs/eval/planning-v2/baseline-20260915.json",
          policy="One batch; every provider attempt counts; no automatic retries or supplemental batch."))
    print(f"Registered one batch: {len(cases)} scenarios, at most {BUDGET} provider requests total, zero external tools")


if __name__ == "__main__":
    main()
