#!/usr/bin/env python3
"""Derive the Builder-v2 LIVE diagnostic suite from the frozen offline fixture.

The live suite keeps the scripted screening seeds (adapter inputs for the vetted
v2 requirement; zero provider calls) but strips every builder oracle so the real
Builder model executes each case once. Suite flag builder_v2=true makes
planningeval.Run keep screening offline and require a shared call budget.
"""
import hashlib
import json
import os
import sys

SOURCE = "internal/planningeval/testdata/builder-v2-20260925/suite.json"
SOURCE_PROV = "internal/planningeval/testdata/builder-v2-20260925/provenance.json"
OUT = "internal/planningeval/testdata/builder-v2-live-20260925"
VERSION = "builder-v2-live-diag-20260925-r1"


def sha(data):
    return hashlib.sha256(data if isinstance(data, bytes) else data.encode("utf-8")).hexdigest()


def main():
    with open(SOURCE, encoding="utf-8") as f:
        suite = json.load(f)
    for case in suite["cases"]:
        for step in case["steps"]:
            step.pop("builder_oracle", None)
    suite["live"] = True
    suite["builder_v2"] = True
    suite["version"] = VERSION
    suite["provenance"] = ("Builder v2 live 诊断套件：由冻结离线 fixture（r2）派生——剥离全部 builder oracle，"
                           "保留 scripted 种子轮（核定需求的适配器输入，零 provider 调用）。Builder 真实执行，"
                           "每 case 一次、不重跑追分；结果为诊断与用量观测，不是通过率宣称。逐题金标来源见离线套件 provenance。")
    os.makedirs(OUT, exist_ok=True)
    raw = json.dumps(suite, ensure_ascii=False, indent=1) + "\n"
    with open(os.path.join(OUT, "suite.json"), "w", encoding="utf-8", newline="\n") as f:
        f.write(raw)
    with open(SOURCE_PROV, encoding="utf-8") as f:
        prov = json.load(f)
    prov = {
        "suite_sha256": sha(raw),
        "derived_from": {"path": SOURCE, "sha256": prov["suite_sha256"], "revision": "r2"},
        "derivation": "剥离 builder_oracle；live=true；builder_v2=true；种子轮逐字保留（ scripted screening 适配器输入）",
        "grading_note": prov["grader_note"],
        "cases": prov["cases"],
        "not_migrated": prov["not_migrated"],
    }
    with open(os.path.join(OUT, "provenance.json"), "w", encoding="utf-8", newline="\n") as f:
        json.dump(prov, f, ensure_ascii=False, indent=1)
        f.write("\n")
    print(f"live suite: {OUT}/suite.json sha256={prov['suite_sha256'][:12]}… cases={len(suite['cases'])}")


if __name__ == "__main__":
    sys.exit(main())
