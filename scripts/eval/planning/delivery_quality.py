#!/usr/bin/env python3
"""交付质量评分口径：从既有 report.json 重算，零模型调用。

可用标准（对话中与用户对齐）：10 case × ≥3 次重复（≥30 次交付）中，
硬性全过（事实正确 + 预算内 + 兼容错误 0 + 无缺价）≥ 27/30（90%）。

硬性门由失败 check 名分类：
  预算内   -> budget_ceiling
  兼容错误 -> validation
  无缺价   -> missing_prices
  事实正确 -> final_outcome / allowed_outcome / server_delivery /
              meaningful_planning_progress / selected_* / candidate_fact:* /
              preserved_other_parts / changed_cpu / cpu_target
其余 check（require_tools、state:*、tool_*、version_count 等）是机制/评估侧
口径，不计入交付质量门。

用法：python delivery_quality.py report1.json [report2.json ...]
"""
import json
import sys
from collections import Counter

FACT_EXACT = {"final_outcome", "allowed_outcome", "server_delivery",
              "meaningful_planning_progress", "preserved_other_parts",
              "preserved_essential_parts", "changed_cpu", "cpu_target"}
FACT_PREFIX = ("selected_", "candidate_fact:")


def hard_failures(checks):
    failures = []
    for c in checks or []:
        if c.get("pass", False):
            continue
        name = c.get("name", "")
        if name == "budget_ceiling":
            failures.append(("预算内", name))
        elif name == "missing_prices":
            failures.append(("无缺价", name))
        elif name == "validation":
            failures.append(("兼容错误", name))
        elif name in FACT_EXACT or name.startswith(FACT_PREFIX):
            failures.append(("事实正确", name))
    return failures


def main(paths):
    total_deliveries = 0
    total_hard_pass = 0
    per_case = Counter()
    for path in paths:
        with open(path, encoding="utf-8") as fh:
            report = json.load(fh)
        print(f"== {path} (suite {report.get('suite_version', '?')}) ==")
        for case in report.get("cases", []):
            fails = []
            for step in case.get("steps") or []:
                fails += hard_failures(step.get("checks"))
            hard_pass = not fails
            total_deliveries += 1
            total_hard_pass += hard_pass
            per_case[case["id"]] += hard_pass
            mark = "PASS" if hard_pass else "FAIL"
            detail = "" if hard_pass else " | " + ", ".join(
                f"{gate}:{name}" for gate, name in fails)
            print(f"  {case['id']} {mark}{detail}")
    print(f"\n硬性全过: {total_hard_pass}/{total_deliveries}"
          f" ({100 * total_hard_pass / max(total_deliveries, 1):.0f}%)，可用线 27/30 (90%)")
    if per_case:
        print("各 case 硬过分布:", dict(sorted(per_case.items())))


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    main(sys.argv[1:])
