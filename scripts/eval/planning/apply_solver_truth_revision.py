#!/usr/bin/env python3
"""按压价求解器真值核验结论修订两套件的 authored 期望（零模型调用）。

- C123-003 step2：求解器证明档位不降约束下不存在 sub-6000 组合（各未锁定品类
  均已目录最低价），期望 ready + purchase_budget 改为最小牺牲标注 proposal。
- B2-002 step1：求解器证明加目录最低独显后新增采购 4667≤6000，预算不是必须
  用户取舍；DISPLAY_OUTPUT 硬规则要求显卡联动，期望 clarify 定版为标注 proposal。

mechanisms 套件修订后同步重算 provenance.json 的 suite_sha256（VerifyProvenance
校验），并更新 source_suite_sha256 与 expectations_changed 标记。
"""
import hashlib
import json

LIVE = "internal/planningeval/testdata/current-178-live-20260918/suite.json"
MECH = "internal/planningeval/testdata/current-178-20260917/mechanisms/suite.json"
PROV = "internal/planningeval/testdata/current-178-20260917/mechanisms/provenance.json"

C123_NOTE = "2026-09-19 修订：C123-003 step2 与 B2-002 step1 的 authored 期望按服务端压价求解器对冻结目录的真值核验结论定版（C123-003 step2 档位不降约束下无 sub-6000 组合，期望改为最小牺牲标注 proposal；B2-002 step1 预算内存在可行组合、显卡联动属必要偏差，期望由 clarify 定版为标注 proposal），理由与求解过程见 docs/eval/planning-v2/current-178-20260917.md。"


def revise_case(suite, case_id, step_index):
    expect = suite["cases"][[c["id"] for c in suite["cases"]].index(case_id)]["steps"][step_index]["expect"]
    if case_id == "C123-003":
        assert expect.get("outcome") == "ready" and expect.get("purchase_budget") is True, expect
        expect["outcome"] = "proposal"
        del expect["purchase_budget"]
        # issues_any 追加在 outcome 之后，保持字段可读顺序。
        rebuilt = {}
        for key, value in expect.items():
            rebuilt[key] = value
            if key == "outcome":
                rebuilt["issues_any"] = ["内存", "牺牲"]
        expect.clear()
        expect.update(rebuilt)
    else:
        assert expect.get("outcome") == "clarify", expect
        expect["outcome"] = "proposal"
        rebuilt = {}
        for key, value in expect.items():
            rebuilt[key] = value
            if key == "outcome":
                rebuilt["issues_any"] = ["显示输出", "独显"]
        expect.clear()
        expect.update(rebuilt)


def main():
    for path, revisions, note in (
        (LIVE, (("C123-003", 2), ("B2-002", 1)), True),
        (MECH, (("C123-003", 2), ("B2-002", 1)), False),
    ):
        with open(path, encoding="utf-8") as fh:
            raw = fh.read()
        suite = json.loads(raw)
        for case_id, step_index in revisions:
            revise_case(suite, case_id, step_index)
        if note and C123_NOTE not in suite["provenance"]:
            suite["provenance"] = suite["provenance"] + C123_NOTE
        out = (json.dumps(suite, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        with open(path, "wb") as fh:
            fh.write(out)
        print(path, "sha256", hashlib.sha256(out).hexdigest())

    with open(LIVE, "rb") as fh:
        live_sha = hashlib.sha256(fh.read()).hexdigest()
    with open(MECH, "rb") as fh:
        mech_sha = hashlib.sha256(fh.read()).hexdigest()
    with open(PROV, encoding="utf-8") as fh:
        prov = json.load(fh)
    prov["source_suite_sha256"] = live_sha
    prov["suite_sha256"] = mech_sha
    prov["expectations_changed"] = True
    with open(PROV, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(prov, fh, ensure_ascii=False, indent=2)
        fh.write("\n")
    print(PROV, "updated")


if __name__ == "__main__":
    main()
