#!/usr/bin/env python3
"""Compose the frozen jev-intent-v1 suite from reviewed current-178 fixtures.

Sources are three frozen suites sharing one identical catalog (current-178,
2026-09-17): mechanisms, screening-recorded, cpu-recorded. Every message step
receives a reviewer-owned expect.next_action label. Six synthetic cases add the
ambiguity/conflict, first-turn-execution-override, compound-confirm,
discussion-negation, vague-open and record-reference slices without any
Builder execution (no builder oracles required).

Labels were reviewed per step against the current action contract in
internal/agents/pipeline/screening_requirement_state.go; the 15 existing frozen
labels all matched the independent review. See the labeling section written
into provenance.json.

Usage: python scripts/eval/planning/build_jev_intent.py
"""
import hashlib
import json
import pathlib

ROOT = pathlib.Path(__file__).resolve().parents[3]
BASE = ROOT / "internal/planningeval/testdata"
SOURCES = [
    "current-178-20260917/mechanisms",
    "current-178-20260917/screening-recorded",
    "current-178-20260917/cpu-recorded",
]
OUT = BASE / "jev-intent-v1"

# Reviewed ground truth per (case id, message step index, step text prefix).
# The text prefix guards against silent fixture drift between review and build.
LABELS = {
    ("B2-001", 0): ("confirm", "做本地Whisper转写"),
    ("B2-001", 2): ("plan", "取消显卡品牌限制"),
    ("B2-002", 0): ("confirm", "手里有个5600和B550M"),
    ("B2-002", 1): ("plan", "预算6000，只先换CPU"),
    ("B2-003", 0): ("confirm", "预算7000，先比较视频转写处理器"),
    ("B2-003", 1): ("plan", "那先用已有资料继续比较"),
    ("C123-007", 0): ("confirm", "预算7500，用于视频剪辑。"),
    ("C123-001", 0): ("confirm", "预算7500，用于视频剪辑，尽量安静。"),
    ("C123-001", 2): ("plan", "直接把CPU升级为Ryzen 7 5700X"),
    ("C123-001", 3): ("plan", "预算降到6500"),
    ("C123-002", 1): ("plan", "预算7000，沿用原需求，把退库配件换成当前有报价的配件"),
    ("C123-003", 0): ("confirm", "用于视频剪辑，已有AMD Ryzen 5 5600"),
    ("C123-003", 2): ("plan", "那内存也买新的"),
    ("C123-004", 0): ("confirm", "预算7500，用于视频剪辑，必须静音。"),
    ("C123-004", 2): ("plan", "静音改成尽量即可"),
    ("C123-005", 0): ("confirm", "预算9000，普通办公，必须ITX小机箱。"),
    ("C123-006", 0): ("confirm", "预算4000，用于普通办公。"),
    ("C123-006", 2): ("plan", "预算只有3000了"),
    ("L3-101", 0): ("confirm", "我想要一台安静的电脑打游戏"),
    ("L3-102", 0): ("collect", "我想装台电脑"),
    ("L3-103", 0): ("confirm", "8000 块组装一台电脑"),
    ("L3-104", 0): ("confirm", "8000 元预算,2K 分辨率玩黑神话"),
    ("L3-105", 0): ("confirm", "8000 块,2K 玩游戏"),
    ("L4-211", 0): ("confirm", "CPU和内存我已经有了"),
    ("L4-212", 0): ("confirm", "我已有一颗AMD Ryzen 5 7600"),
    ("L4-213", 0): ("confirm", "我已有一张显卡"),
    ("L4-214", 0): ("collect", "金额还没告诉你"),
    ("L5-301", 0): ("confirm", "预算8000元的新主机"),
    ("L5-301", 1): ("confirm", "预算改为6000元"),
    ("L5-301", 2): ("confirm", "用2K分辨率。"),
    ("L5-302", 0): ("confirm", "想配一台安静的游戏主机"),
    ("L5-302", 1): ("confirm", "显示器是2K，游戏名称暂时不确定。"),
    ("L5-303", 0): ("collect", "看中一张售价3000元的显卡"),
    ("L5-303", 1): ("confirm", "整机预算确定为8000元"),
    ("L5-304", 0): ("confirm", "办公电脑预算6000元"),
    ("L5-304", 1): ("confirm", "已有CPU是 AMD Ryzen 5 7600，一颗。"),
    ("L5-304", 2): ("confirm", "这6000元只算新增购买费用。"),
    ("L5-305", 0): ("confirm", "办公主机，已有一颗 AMD Ryzen 5 7600"),
    ("L5-305", 1): ("confirm", "更正刚才的口径"),
    ("L5-306", 0): ("collect", "日常办公的新电脑，预算还没确定。"),
    ("L5-306", 1): ("confirm", "预算八千五百元。"),
    ("L5-307", 0): ("confirm", "新主机预算10000元"),
    ("L5-307", 1): ("confirm", "CPU改成Intel"),
    ("L5-308", 0): ("confirm", "办公电脑，新增购买预算6000元"),
    ("L5-308", 1): ("confirm", "我看错了，实际已有CPU是一颗 Intel Core i5-12400F"),
    ("L5-309", 0): ("confirm", "新主机预算8000元，用来日常办公。"),
    ("L5-309", 1): ("confirm", "用途改成主要玩游戏"),
    ("L5-309", 2): ("confirm", "分辨率2K，预算仍是8000元。"),
    ("L5-310", 0): ("confirm", "配新电脑，预算8000元"),
    ("L5-310", 1): ("collect", "先取消刚才8000元的预算"),
    ("R-REF-01", 0): ("collect", "帮同事记一下"),
    ("R-REF-01", 1): ("collect", "整机预算改为9000元，那条内存报价留着"),
    ("LIVE123-CPU", 0): ("plan", "直接把CPU升级为Ryzen 7 5700X"),
}


def set_op(field, value, kind, strength, quote):
    return {"op": "set", "field": field, "value": value, "kind": kind,
            "strength": strength, "scope": "session", "evidence": "stated", "quote": quote}


def msg(text, oracle, expect, next_action):
    expect = dict(expect)
    expect["next_action"] = next_action
    expect.setdefault("versions", 0)
    expect.setdefault("builder_calls", 0)
    return {"kind": "message", "text": text, "expect": expect, "screen_oracle": oracle}


def refresh(expect):
    return {"kind": "refresh", "expect": expect}


SYNTHETIC = [
    {
        "id": "JEV-A",
        "title": "冲突轮:口头执行与明确暂缓同时出现",
        "source": "Synthetic intent fixture (2026-09-22); hand-written oracle reviewed against requirementStateInstruction.",
        "steps": [
            msg("预算8000直接开始配吧，不过先别执行，我再想想。",
                {"operations": [set_op("budget_cny", 8000, "constraint", "must", "预算8000")],
                 "observations": [{"field": "use_case.type", "quote": "先别执行，我再想想", "reason": "用户暂缓执行，用途未明确，保持未知"}],
                 "next_action": "collect",
                 "reply": "已记录预算8000元。本轮先不执行——你打算主要用来做什么？想清楚用途和具体需求后我们再继续。"},
                {"fields": {"budget_cny": {"status": "active", "value": 8000},
                            "use_case.type": {"status": "unknown"}}},
                "collect"),
            refresh({"fields": {"budget_cny": {"status": "active", "value": 8000},
                                "use_case.type": {"status": "unknown"}}}),
        ],
    },
    {
        "id": "JEV-B",
        "title": "首轮点名执行:产品协议仍要求首次面板确认",
        "source": "Synthetic intent fixture (2026-09-22); label is the final state action after the first-message confirmation override, not the oracle wording.",
        "steps": [
            msg("预算8000，2K玩3A大作，直接开始配吧。",
                {"operations": [set_op("budget_cny", 8000, "constraint", "must", "预算8000"),
                                set_op("use_case.type", "gaming", "fact", "must", "2K玩3A大作"),
                                set_op("use_case.resolution", "2K", "fact", "must", "2K玩3A大作")],
                 "next_action": "plan",
                 "reply": "已记录预算与2K游戏需求。首次配置需要在面板上确认后再开始选配。"},
                {"fields": {"budget_cny": {"status": "active", "value": 8000},
                            "use_case.type": {"status": "active", "value": "gaming"},
                            "use_case.resolution": {"status": "active", "value": "2K"}}},
                "confirm"),
            refresh({"fields": {"budget_cny": {"status": "active", "value": 8000},
                                "use_case.type": {"status": "active", "value": "gaming"},
                                "use_case.resolution": {"status": "active", "value": "2K"}}}),
        ],
    },
    {
        "id": "JEV-C",
        "title": "复合更新:预算与噪声偏好同轮修改,明确暂缓执行",
        "source": "Synthetic intent fixture (2026-09-22); hand-written oracle reviewed against requirementStateInstruction.",
        "steps": [
            msg("预算7000，主要2K视频剪辑。",
                {"operations": [set_op("budget_cny", 7000, "constraint", "must", "预算7000"),
                                set_op("use_case.type", "productivity", "fact", "must", "主要2K视频剪辑"),
                                set_op("use_case.resolution", "2K", "fact", "must", "主要2K视频剪辑")],
                 "next_action": "confirm",
                 "reply": "已记录预算7000元和2K视频剪辑用途，请核对需求面板后开始选配。"},
                {"fields": {"budget_cny": {"status": "active", "value": 7000},
                            "use_case.type": {"status": "active", "value": "productivity"},
                            "noise_pref": {"status": "unknown"}}},
                "confirm"),
            msg("预算改到6500，尽量安静，先这样，回头一起配。",
                {"operations": [set_op("budget_cny", 6500, "constraint", "must", "预算改到6500"),
                                set_op("noise_pref", "silent", "constraint", "prefer", "尽量安静")],
                 "next_action": "confirm",
                 "reply": "已更新预算6500元并记录尽量安静的偏好。本轮不开始选配，等你在面板确认后再生成。"},
                {"fields": {"budget_cny": {"status": "active", "value": 6500},
                            "use_case.type": {"status": "active", "value": "productivity"},
                            "noise_pref": {"status": "active", "value": "silent"}}},
                "confirm"),
            refresh({"fields": {"budget_cny": {"status": "active", "value": 6500},
                                "noise_pref": {"status": "active", "value": "silent"}}}),
        ],
    },
    {
        "id": "JEV-D",
        "title": "答疑轮:技术讨论并明确不改配置",
        "source": "Synthetic intent fixture (2026-09-22); hand-written oracle reviewed against requirementStateInstruction.",
        "steps": [
            msg("预算6000，普通办公用。",
                {"operations": [set_op("budget_cny", 6000, "constraint", "must", "预算6000"),
                                set_op("use_case.type", "general", "fact", "must", "普通办公用")],
                 "next_action": "confirm",
                 "reply": "已记录预算6000元和办公用途，请核对需求面板后开始选配。"},
                {"fields": {"budget_cny": {"status": "active", "value": 6000},
                            "use_case.type": {"status": "active", "value": "general"}}},
                "confirm"),
            msg("DDR4和DDR5差多少？先别改配置。",
                {"operations": [],
                 "next_action": "collect",
                 "reply": "简单说：DDR5 带宽更高、起步容量更大，但价格更贵；办公场景两者体验差距很小。你的配置本轮不做任何修改，需要我顺带确认某款主板支持哪种内存吗？"},
                {"fields": {"budget_cny": {"status": "active", "value": 6000},
                            "use_case.type": {"status": "active", "value": "general"}}},
                "collect"),
            refresh({"fields": {"budget_cny": {"status": "active", "value": 6000}}}),
        ],
    },
    {
        "id": "JEV-E",
        "title": "模糊授权:开放委托但无明确执行点名",
        "source": "Synthetic intent fixture (2026-09-22); boundary label documented in provenance.",
        "steps": [
            msg("帮我看看呗。",
                {"operations": [],
                 "next_action": "collect",
                 "reply": "好的，请告诉我用途和预算，比如办公、游戏或视频剪辑，我好开始帮你整理。"},
                {"fields": {"budget_cny": {"status": "unknown"},
                            "use_case.type": {"status": "unknown"}}},
                "collect"),
            msg("预算9000，要求不高，你看着办。",
                {"operations": [set_op("budget_cny", 9000, "constraint", "must", "预算9000")],
                 "next_action": "confirm",
                 "reply": "已记录预算9000元。请在需求面板核对后确认，我再开始选配。"},
                {"fields": {"budget_cny": {"status": "active", "value": 9000},
                            "use_case.type": {"status": "unknown"}}},
                "confirm"),
            refresh({"fields": {"budget_cny": {"status": "active", "value": 9000}}}),
        ],
    },
    {
        "id": "JEV-F",
        "title": "报价参考记录:同轮参考与后续完整意图",
        "source": "Synthetic intent fixture (2026-09-22); hand-written oracle reviewed against requirementStateInstruction.",
        "steps": [
            msg("同事说京东一张6750GRE卖2450，帮我记着，先不买。",
                {"operations": [set_op("free.gpu_price_ref", "京东一张6750GRE卖2450，还没买，只是参考",
                                       "context", "prefer", "京东一张6750GRE卖2450")],
                 "next_action": "collect",
                 "reply": "已记录6750GRE 2450元的报价参考（未购买）。暂不开始选配，等你补充整机需求。"},
                {"fields": {"free.gpu_price_ref": {"status": "active", "contains": ["6750GRE", "2450"]},
                            "budget_cny": {"status": "unknown"}}},
                "collect"),
            msg("整台预算定为8500，主要打游戏，刚才那张卡当参考。",
                {"operations": [set_op("budget_cny", 8500, "constraint", "must", "整台预算定为8500"),
                                set_op("use_case.type", "gaming", "fact", "must", "主要打游戏")],
                 "next_action": "confirm",
                 "reply": "已记录预算8500元和游戏用途，6750GRE报价继续作为参考保留。请核对需求面板后开始选配。"},
                {"fields": {"budget_cny": {"status": "active", "value": 8500},
                            "use_case.type": {"status": "active", "value": "gaming"},
                            "free.gpu_price_ref": {"status": "active"}}},
                "confirm"),
            refresh({"fields": {"budget_cny": {"status": "active", "value": 8500},
                                "free.gpu_price_ref": {"status": "active"}}}),
        ],
    },
]


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    suites = {}
    catalogs = []
    for name in SOURCES:
        raw = json.loads((BASE / name / "suite.json").read_text(encoding="utf-8"))
        suites[name] = raw
        catalogs.append(json.dumps(raw["catalog"], sort_keys=True, ensure_ascii=False))
    if len(set(catalogs)) != 1:
        raise SystemExit("source catalogs drifted; refusing to compose a mixed fixture")
    pages = {}
    for name, suite in suites.items():
        for url, body in (suite.get("pages") or {}).items():
            if url in pages and pages[url] != body:
                raise SystemExit(f"page collision for {url}")
            pages[url] = body

    cases, seen = [], set()
    for name in SOURCES:
        for case in suites[name]["cases"]:
            if case["id"] in seen:
                raise SystemExit(f"duplicate case id {case['id']}")
            seen.add(case["id"])
            for index, step in enumerate(case["steps"]):
                if step["kind"] != "message":
                    continue
                key = (case["id"], index)
                if key not in LABELS:
                    raise SystemExit(f"missing reviewed label for {key}")
                label, fragment = LABELS[key]
                if fragment not in step["text"]:
                    raise SystemExit(f"text drift for {key}: {step['text']!r} lacks {fragment!r}")
                existing = (step.get("expect") or {}).get("next_action")
                if existing and existing != label:
                    raise SystemExit(f"frozen label {existing} disagrees with review {label} for {key}")
                step.setdefault("expect", {})["next_action"] = label
            cases.append(case)

    synthetic_ids = {c["id"] for c in SYNTHETIC}
    if synthetic_ids & seen:
        raise SystemExit("synthetic case id collision")
    synthetic_labels = 0
    for case in SYNTHETIC:
        for step in case["steps"]:
            if step["kind"] == "message":
                synthetic_labels += 1
        cases.append(case)

    ordered_ids = sorted(c["id"] for c in cases)
    calibration = ordered_ids[0::2]
    holdout = ordered_ids[1::2]
    suite = {
        "version": "jev-intent-v1-20260922",
        "provenance": "Composed for Jev shadow intent evaluation: reviewed current-178 fixtures plus six synthetic intent slices; labels per-step reviewed against the current screening action contract.",
        "catalog": suites[SOURCES[0]]["catalog"],
        "pages": pages,
        "cases": cases,
        "intent_split": {"calibration": calibration, "holdout": holdout},
    }
    OUT.mkdir(parents=True, exist_ok=True)
    raw = (json.dumps(suite, ensure_ascii=False, indent=1) + "\n").encode("utf-8")
    (OUT / "suite.json").write_bytes(raw)

    labelled = sum(1 for c in cases for s in c["steps"] if s["kind"] == "message")
    provenance = {
        "sources": {f"{name}/suite.json": sha256(BASE / name / "suite.json") for name in SOURCES},
        "suite_sha256": hashlib.sha256(raw).hexdigest(),
        "synthetic_changes": [
            "JEV-A..JEV-F synthetic intent cases with hand-written screening oracles",
            "expect.next_action labels added to every reused message step",
            "intent_split calibration/holdout partition added (round-robin over sorted case ids; session-disjoint)",
        ],
        "labeling": {
            "method": "每条 message 步骤的 expect.next_action 由 AI 编程助手按 internal/agents/pipeline/screening_requirement_state.go 的现行动作决策契约逐条复核；15 条已有冻结标注全部与独立复核一致；其余复用步骤采纳其冻结 screen_oracle 的 next_action（复核确认与现行契约一致，Screening 结果仅作证据、不作为标注来源）；6 个合成用例为手写 oracle。",
            "review_status": "AI-assisted review 2026-09-22; user spot-check of LABELS table recommended before treating directional results as decision-grade.",
            "boundary_notes": [
                "B2-002#1 confirm：先看看升级方向按冻结评审采纳 confirm（边界）。",
                "C123-006#3 plan：按现有要求继续看看按冻结评审采纳 plan（继续即执行）。",
                "L4-214/L5-306#1 collect：最小首轮缺金额按冻结评审采纳 collect。",
                "L5-310#2 collect：撤销预算后回退，按冻结评审采纳 collect。",
                "JEV-B/B2-003#1 confirm：首轮点名执行/继续比较被产品的首次确认门覆盖，标注为最终状态动作 confirm（oracle 原文为 plan）。",
                "JEV-E#2 confirm：开放委托无点名执行，按现行契约标注 confirm（边界）。",
            ],
        },
        "distribution_note": f"{labelled} labelled message steps (incl. {synthetic_labels} synthetic); class counts are recorded in the suite itself.",
    }
    (OUT / "provenance.json").write_text(
        json.dumps(provenance, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"jev-intent-v1: {len(cases)} cases, {labelled} labelled steps, "
          f"calibration={len(calibration)} holdout={len(holdout)}, "
          f"sha256={hashlib.sha256(raw).hexdigest()}")


if __name__ == "__main__":
    main()
