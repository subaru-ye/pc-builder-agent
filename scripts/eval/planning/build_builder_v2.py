#!/usr/bin/env python3
"""Generate the Builder v2 mechanism fixture (builder-v2-20260925).

Catalog and the BV2-110 previous-build precondition are extracted verbatim from
the frozen current-178 mechanisms suite (snapshot 11, 126 active parts). Cases
are migrations of old Builder scenarios to vetted v2 requirement seeds; see
docs/changes/builder-v2-eval.md and the generated provenance.json.

Zero-model replay: python scripts/eval/planning/run.py --suite <out>/suite.json --out <artifacts dir>
"""
import copy
import hashlib
import json
import os
import sys

SOURCE = "internal/planningeval/testdata/current-178-20260917/mechanisms/suite.json"
OUT = "internal/planningeval/testdata/builder-v2-20260925"
VERSION = "builder-v2-mechanisms-20260925-r1"


def load_source():
    with open(SOURCE, encoding="utf-8") as f:
        return json.load(f)


def sha(data):
    return hashlib.sha256(data if isinstance(data, bytes) else data.encode("utf-8")).hexdigest()


def op(field, value, kind="constraint", strength="must", quote=""):
    return {"op": "set", "field": field, "value": value, "kind": kind,
            "strength": strength, "scope": "session", "evidence": "stated", "quote": quote}


def op_fact(field, value, quote=""):
    return op(field, value, kind="fact", quote=quote)


def turn_signals(requests_build=False):
    return {"asks_question": False, "requests_review": False,
            "requests_build": requests_build, "ambiguous": False}


def seed_message(text, ops, reply):
    # v2 一轮合同：operations/observations/turn_signals/proposals/answer。
    # op quote 必须逐字出现在 text 中；existing_parts=[] 需要“全新/新买”证据词。
    return {"kind": "message", "text": text,
            "screen_oracle": {"operations": ops, "observations": [],
                              "turn_signals": turn_signals(False), "proposals": [],
                              "answer": reply},
            "expect": {"versions": 0}}


def fc(action, payload, call_id):
    return {"role": "model", "parts": [{"functionCall": {
        "id": call_id, "name": "planning_action",
        "args": {"action": action, "payload": json.dumps(payload, ensure_ascii=False)}}}]}


def final_text(result):
    return {"role": "model", "parts": [{"text": json.dumps(result, ensure_ascii=False)}]}


def draft(selection, rationale):
    return {"schema_version": 1, "requirement_ref": "current", "build_ref": "proposal",
            "selection": selection, "rationale": rationale}


def assessment(field, status, explanation, evidence=None):
    return {"field": field, "status": status, "explanation": explanation,
            "evidence": evidence or ["local:fixture"]}


def selection_evidence(selection):
    ids = [v for v in selection.values() if isinstance(v, str)]
    for s in selection.get("ssd", []):
        ids.append(s["sku"])
    return ["local:" + i for i in ids if i]


def gold_spec(**kwargs):
    out = {}
    for key, value in kwargs.items():
        out[key.replace("__", ".")] = value
    return out


def frozen(spec=None, defaults=None, absent=None):
    out = {"spec": spec or {}}
    if defaults:
        out["defaults"] = defaults
    if absent:
        out["absent_defaults"] = absent
    return out


def default_gold(field, value, origin="system_default"):
    return {"field": field, "value": value, "origin": origin}


# ---------------------------------------------------------------- selections
OFFICE_16 = {
    "cpu": "cpu-r5-4600g", "gpu": None, "motherboard": "mb-msi-b550m-pro-vdh-wifi",
    "memory": "mem-crucial-ballistix-16-3200", "ssd": [{"sku": "ssd-crucial-bx500-1tb", "quantity": 1}],
    "psu": "psu-msi-mag-a650bn", "case": "case-asus-prime-ap201", "cooler": "cooler-deepcool-ag400"}
OFFICE_32 = {
    "cpu": "cpu-r5-4600g", "gpu": None, "motherboard": "mb-msi-b550m-pro-vdh-wifi",
    "memory": "mem-corsair-lpx-32-3600", "ssd": [{"sku": "ssd-zhitai-ti600-1tb", "quantity": 1}],
    "psu": "psu-msi-mag-a650bn", "case": "case-asus-prime-ap201", "cooler": "cooler-deepcool-ag400"}
ITX_SFX = {
    "cpu": "cpu-r5-7600", "gpu": None, "motherboard": "mb-msi-b650i-edge",
    "memory": "mem-kingston-beast-16-5200", "ssd": [{"sku": "ssd-zhitai-ti600-1tb", "quantity": 1}],
    "psu": "psu-cm-v850sfx-gold-white", "case": "case-coolermaster-nr200p", "cooler": "cooler-deepcool-ag400"}
CONFLICT = {
    "cpu": "cpu-r5-5600", "gpu": "gpu-msi-3060-ventus2x", "motherboard": "mb-msi-b650m-mortar-wifi",
    "memory": "mem-kingston-beast-16-5200", "ssd": [{"sku": "ssd-zhitai-ti600-1tb", "quantity": 1}],
    "psu": "psu-msi-mag-a650bn", "case": "case-asus-prime-ap201", "cooler": "cooler-deepcool-ag400"}
OVERSPEND = {
    "cpu": "cpu-r5-4500", "gpu": "gpu-msi-3060-ventus2x", "motherboard": "mb-msi-b550m-pro-vdh-wifi",
    "memory": "mem-crucial-ballistix-16-3200", "ssd": [{"sku": "ssd-crucial-bx500-1tb", "quantity": 1}],
    "psu": "psu-msi-mag-a650bn", "case": "case-asus-prime-ap201", "cooler": "cooler-coolermaster-hyper212-black"}
RETIRED_SWAP = {
    "cpu": "cpu-r5-5600", "gpu": "gpu-msi-3060-ventus2x", "motherboard": "mb-msi-b550m-pro-vdh-wifi",
    "memory": "mem-corsair-lpx-32-3600", "ssd": [{"sku": "ssd-crucial-bx500-1tb", "quantity": 1}],
    "psu": "psu-msi-mag-a650bn", "case": "case-asus-prime-ap201", "cooler": "cooler-deepcool-ag400"}


def rationale(text):
    return {k: text for k in ("cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler")}


def ready_result(selection, total, reply, extra_assessments=()):
    ev = selection_evidence(selection)
    out = {"outcome": "ready", "reply": reply, "draft": draft(selection, rationale("预算内且校验通过的确定性选型")),
           "assessments": [assessment("budget_cny", "met", f"总价{total}元在预算上限内", ev),
                           assessment("use_case.type", "met", "按核定用途选型", ev)],
           "issues": [], "assumptions": []}
    out["assessments"].extend(extra_assessments)
    return out


def proposal_result(selection, reply, issues):
    ev = selection_evidence(selection)
    return {"outcome": "proposal", "reply": reply, "draft": draft(selection, rationale("存在待解决问题，交付标注方案")),
            "assessments": [assessment("budget_cny", "met" if selection is not OVERSPEND else "unmet",
                                       "总价与预算上限的关系见问题标注", ev)],
            "issues": issues, "assumptions": []}


def clarify_result(reply, issues):
    return {"outcome": "clarify", "reply": reply, "draft": None,
            "assessments": [assessment("owned_parts", "unknown", "已有件目录无精确匹配，保留或改购需用户决定")],
            "issues": issues, "assumptions": []}


def batch(categories, call_id, order="price_asc", limit=8):
    return fc("search_local_batch", {"queries": [{"category": c, "order_by": order, "limit": limit} for c in categories]}, call_id)


def build_cases(source):
    cases = []
    cid = "bv2"

    # BV2-101 预算硬上限 + 默认弹性（C123-006 step1）
    cases.append({
        "id": "BV2-101",
        "title": "4000元办公核显：must 预算硬上限按冻结弹性默认0.1展开",
        "source": "C123-006 step1（current-178 mechanisms）；多轮收集折叠为一次核定 seed，预算上限按冻结合同口径 budget×(1+0.1)=4400 断言",
        "steps": [
            seed_message("预算4000，用于普通办公，配件全新买。",
                         [op("budget_cny", 4000, quote="预算4000"),
                          op_fact("use_case.type", "general", quote="用于普通办公"),
                          op_fact("existing_parts", [], quote="全新买")],
                         "已记录预算4000元和普通办公用途，请核对需求面板后开始选配。"),
            {"kind": "confirm", "expect": {
                "versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                "require_tools": ["search_local_batch", "evaluate"],
                "budget_ceiling_cny": "4400", "model_input_contains": ["effective_constraints"],
                "selected_specs": {"cpu": {"has_igpu": True}},
                "frozen_constraints": frozen(
                    spec=gold_spec(budget_cny=4000, budget_flex=0.1, use_case__type="general",
                                   configuration_scope=["tower"], constraint_strengths__budget_cny="must"),
                    defaults=[default_gold("budget_flex", 0.1)]),
            }, "builder_oracle": [
                batch(["cpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"], f"{cid}-101-1"),
                fc("evaluate", {"draft": draft(OFFICE_16, rationale("核显办公方案"))}, f"{cid}-101-2"),
                final_text(ready_result(OFFICE_16, "3134.80", "已在4000元预算内配好核显办公主机，总价3134.8元。")),
            ]},
        ]})

    # BV2-102 严格预算（弹性 0）
    cases.append({
        "id": "BV2-102",
        "title": "5000元严格预算（弹性显式0）：上限不加成",
        "source": "L1-013（legacy-builder v1.5，弹性0.15）+ 显式0口径（review_budget_consistency）；v2 下弹性为冻结字段",
        "steps": [
            seed_message("预算5000，普通办公，严格预算不要超，配件全新买。",
                         [op("budget_cny", 5000, quote="预算5000"),
                          op("budget_flex", 0, quote="严格预算不要超"),
                          op_fact("use_case.type", "general", quote="普通办公"),
                          op_fact("existing_parts", [], quote="全新买")],
                         "已记录5000元严格预算（不允超支）与办公用途。"),
            {"kind": "confirm", "expect": {
                "versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                "require_tools": ["evaluate"],
                "budget_ceiling_cny": "5000",
                "frozen_constraints": frozen(
                    spec=gold_spec(budget_cny=5000, budget_flex=0),
                    absent=["budget_flex"]),
            }, "builder_oracle": [
                fc("evaluate", {"draft": draft(OFFICE_16, rationale("严格预算内核显办公"))}, f"{cid}-102-1"),
                final_text(ready_result(OFFICE_16, "3134.80", "已在5000元严格预算内配好办公主机，总价3134.8元。")),
            ]},
        ]})

    # BV2-103 已有件精确匹配 + 新增采购口径
    cases.append({
        "id": "BV2-103",
        "title": "已有4600G核显CPU：new_purchase 口径只计新增采购",
        "source": "L4-206（legacy-builder v1.5）；已有CPU改为目录内可精确匹配的4600G（唯一确定性匹配）",
        "steps": [
            seed_message("办公用。CPU我已经有一块AMD Ryzen 5 4600G，新增采购预算4000，其余都买新的。",
                         [op_fact("use_case.type", "general", quote="办公用"),
                          op_fact("owned_parts", [{"category": "cpu", "model": "AMD Ryzen 5 4600G", "quantity": 1}],
                                  quote="CPU我已经有一块AMD Ryzen 5 4600G"),
                          op("budget_cny", 4000, quote="新增采购预算4000"),
                          op_fact("budget_basis", "new_purchase", quote="新增采购预算4000"),
                          op_fact("existing_parts", [], quote="其余都买新的")],
                         "已记录已有CPU与新增采购预算4000元。"),
            {"kind": "confirm", "expect": {
                "versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                "require_tools": ["evaluate"],
                "purchase_budget": True, "budget_ceiling_cny": "4400",
                "selected_options": {"cpu": ["cpu-r5-4600g"]},
                "frozen_constraints": frozen(spec=gold_spec(
                    budget_cny=4000, budget_basis="new_purchase",
                    owned_parts=[{"category": "cpu", "model": "AMD Ryzen 5 4600G", "quantity": 1}])),
            }, "builder_oracle": [
                fc("evaluate", {"draft": draft(OFFICE_16, rationale("已有CPU按品类核账不计采购价"))}, f"{cid}-103-1"),
                final_text(ready_result(OFFICE_16, "2477.00", "沿用你已有的4600G，新增采购总价2477元，在4000元预算内。")),
            ]},
        ]})

    # BV2-104 已有件无匹配 → clarify
    cases.append({
        "id": "BV2-104",
        "title": "已有32GB内存目录无精确匹配：保留或改购是用户取舍",
        "source": "C123-003 step1 + L4-204；多轮对话折叠为单轮 Builder 决策",
        "steps": [
            seed_message("做视频剪辑。已有AMD Ryzen 5 5600和G.Skill Ripjaws V 32GB (2x16GB) DDR4-3200 CL16各一件，新增采购预算6000。",
                         [op_fact("use_case.type", "productivity", quote="做视频剪辑"),
                          op_fact("use_case.titles", ["视频剪辑"], quote="做视频剪辑"),
                          op_fact("owned_parts", [
                              {"category": "cpu", "model": "AMD Ryzen 5 5600", "quantity": 1},
                              {"category": "memory", "model": "G.Skill Ripjaws V 32GB (2x16GB) DDR4-3200 CL16", "quantity": 1}],
                              quote="已有AMD Ryzen 5 5600和G.Skill Ripjaws V 32GB各一件"),
                          op("budget_cny", 6000, quote="新增采购预算6000"),
                          op_fact("budget_basis", "new_purchase", quote="新增采购预算6000"),
                          op_fact("existing_parts", [], quote="各一件")],
                         "已记录已有CPU与内存、新增采购预算6000元。"),
            {"kind": "confirm", "expect": {
                "versions": 0, "outcome_one_of": ["clarify"],
                "require_tools": ["search_local"],
                "issues_contain": ["内存"],
                "frozen_constraints": frozen(spec=gold_spec(
                    budget_cny=6000, budget_basis="new_purchase",
                    owned_parts=[
                        {"category": "cpu", "model": "AMD Ryzen 5 5600", "quantity": 1},
                        {"category": "memory", "model": "G.Skill Ripjaws V 32GB (2x16GB) DDR4-3200 CL16", "quantity": 1}])),
            }, "builder_oracle": [
                fc("search_local", {"query": "G.Skill Ripjaws V 32GB DDR4-3200", "category": "memory"}, f"{cid}-104-1"),
                final_text(clarify_result(
                    "你已有的G.Skill Ripjaws V 32GB内存在目录中没有精确匹配型号。可以选择保留这块内存（按品类核账、不计入采购合计），或改购一条新内存；确认后我再继续选配。",
                    ["已有内存G.Skill Ripjaws V 32GB在目录无精确匹配，需用户确认保留（按品类核账不计价）或改购新件"])),
            ]},
        ]})

    # BV2-105 ITX + SFX 电源规则
    cases.append({
        "id": "BV2-105",
        "title": "must ITX 机箱：ATX 电源被 FORM_FACTOR_SUPPORT 规则拦截后换 SFX",
        "source": "C123-005（原 mechanisms c5）；旧录制 oracle 超预算，v2 重写为预算内 oracle，断言改属性口径",
        "steps": [
            seed_message("预算9000，普通办公，必须ITX小机箱，配件全新买。",
                         [op("budget_cny", 9000, quote="预算9000"),
                          op_fact("use_case.type", "general", quote="普通办公"),
                          op("size_pref", "itx", quote="必须ITX小机箱"),
                          op_fact("existing_parts", [], quote="全新买")],
                         "已记录预算9000元、办公用途与必须ITX机箱。"),
            {"kind": "confirm", "expect": {
                "versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                "require_tools": ["search_local_batch", "evaluate"],
                "budget_ceiling_cny": "9900",
                "selected_specs": {"case": {"supported_psu_form_factors": ["sfx", "sfx_l"]},
                                   "psu": {"form_factor": "sfx"}},
                "frozen_constraints": frozen(spec=gold_spec(
                    budget_cny=9000, size_pref="itx", use_case__type="general")),
            }, "builder_oracle": [
                batch(["cpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"], f"{cid}-105-1"),
                fc("evaluate", {"draft": draft(
                    {**ITX_SFX, "psu": "psu-msi-mag-a650bn"},
                    rationale("先试ATX电源，待核验机箱兼容"))}, f"{cid}-105-2"),
                fc("evaluate", {"draft": draft(ITX_SFX, rationale("ITX机箱限SFX/SFX-L电源，换SFX电源后通过"))}, f"{cid}-105-3"),
                final_text(ready_result(ITX_SFX, "7977.70", "已配好ITX办公主机：B650I主板+核显CPU，机箱用SFX电源满足限长，总价7977.7元。",
                                        (assessment("size_pref", "met", "机箱与主板均为ITX板型，满足必须ITX",
                                                   ["local:case-coolermaster-nr200p", "local:mb-msi-b650i-edge"]),))),
            ]},
        ]})

    # BV2-106 真实兼容冲突不得伪装通过
    cases.append({
        "id": "BV2-106",
        "title": "AM4 CPU 配 AM5 主板：校验失败如实标注，不得 ready",
        "source": "P2-011（proposal-review-20260915）；SKU 换为快照11内的 AM4 CPU + AM5 板组合",
        "steps": [
            seed_message("预算8000，打1080p游戏，配件全新买。",
                         [op("budget_cny", 8000, quote="预算8000"),
                          op_fact("use_case.type", "gaming", quote="打1080p游戏"),
                          op_fact("use_case.resolution", "1080p", quote="1080p"),
                          op_fact("existing_parts", [], quote="全新买")],
                         "已记录预算8000元与1080p游戏用途。"),
            {"kind": "confirm", "expect": {
                "versions": 0, "outcome_one_of": ["proposal"], "validation": "fail",
                "require_tools": ["evaluate"], "issues_contain": ["插槽"],
                "frozen_constraints": frozen(spec=gold_spec(
                    budget_cny=8000, use_case__type="gaming", use_case__resolution="1080p")),
            }, "builder_oracle": [
                batch(["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"], f"{cid}-106-1"),
                fc("evaluate", {"draft": draft(CONFLICT, rationale("待核验插槽兼容"))}, f"{cid}-106-2"),
                final_text(proposal_result(CONFLICT, "当前方案的CPU与主板插槽不匹配（AM4 CPU 配 AM5 主板），见待解决问题。", ["CPU与主板插槽不匹配：AM4 处理器无法安装到 AM5 主板插槽"])),
                final_text(proposal_result(CONFLICT, "已复核：插槽冲突无法在当前选型内解决，保留标注方案交用户取舍。", ["CPU与主板插槽不匹配：AM4 处理器无法安装到 AM5 主板插槽"])),
                final_text(proposal_result(CONFLICT, "已复核：插槽冲突无法在当前选型内解决，保留标注方案交用户取舍。", ["CPU与主板插槽不匹配：AM4 处理器无法安装到 AM5 主板插槽"])),
            ]},
        ]})

    # BV2-107 缺规格 unknown → 换字段完整候选
    cases.append({
        "id": "BV2-107",
        "title": "散热器缺解热容量字段：unknown 不被默认值替代，换完整候选后通过",
        "source": "C123-007（原 mechanisms c7）；旧题以 base_draft 前提构造，v2 简化为新装单轮",
        "steps": [
            seed_message("预算6000，普通办公用，配件全新买。",
                         [op("budget_cny", 6000, quote="预算6000"),
                          op_fact("use_case.type", "general", quote="普通办公用"),
                          op_fact("existing_parts", [], quote="全新买")],
                         "已记录预算6000元与办公用途。"),
            {"kind": "confirm", "expect": {
                "versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                "require_tools": ["evaluate"],
                "budget_ceiling_cny": "6600",
                "selected_specs": {"cooler": {"cooling_capacity_w": 220}},
                "frozen_constraints": frozen(spec=gold_spec(budget_cny=6000, use_case__type="general")),
            }, "builder_oracle": [
                batch(["cpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"], f"{cid}-107-1"),
                fc("evaluate", {"draft": draft(
                    {**OFFICE_32, "cooler": "cooler-coolermaster-hyper212-black"},
                    rationale("先试Hyper 212，待核验散热能力字段"))}, f"{cid}-107-2"),
                fc("evaluate", {"draft": draft(OFFICE_32, rationale("Hyper 212缺解热容量字段，换字段完整的AG400"))}, f"{cid}-107-3"),
                final_text(ready_result(OFFICE_32, "4454.80", "已配好办公主机；散热器换为字段完整且220W解热能力的AG400，总价4454.8元。")),
            ]},
        ]})

    # BV2-108 无可行解 → 诚实标注 proposal
    cases.append({
        "id": "BV2-108",
        "title": "2500元独显整机不可行：如实标注超预算，不得伪造 ready",
        "source": "C123-006 step3（降预算超支）+ L4-215；v2 单轮 2500 元预算（目录下限独显整机约5677.5元）",
        "steps": [
            seed_message("预算2500，配一台带独显的办公机，配件全新买。",
                         [op("budget_cny", 2500, quote="预算2500"),
                          op_fact("use_case.type", "general", quote="办公机"),
                          op_fact("existing_parts", [], quote="全新买")],
                         "已记录预算2500元与办公用途。"),
            {"kind": "confirm", "expect": {
                "versions": 0, "outcome_one_of": ["proposal"],
                "issues_contain": ["预算"],
                "require_tools": ["search_local_batch", "evaluate"],
                "frozen_constraints": frozen(spec=gold_spec(budget_cny=2500, use_case__type="general")),
            }, "builder_oracle": [
                batch(["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"], f"{cid}-108-1"),
                fc("evaluate", {"draft": draft(OVERSPEND, rationale("目录最低价独显组合"))}, f"{cid}-108-2"),
                final_text(proposal_result(OVERSPEND, "按目录最低价的独显整机组合，总价5677.5元已超出2500元预算且各品类均为目录最低价，无法在预算内闭环；方案与待解决问题见标注。",
                                           ["总价超出2500元预算硬上限，各品类已为目录最低价，无法自行压回；需用户调整预算或取消独显要求"])),
                final_text(proposal_result(OVERSPEND, "已复核预算自纠反馈：各品类已为目录最低价，无更低有报价候选可换，保留标注方案。", ["总价超出2500元预算硬上限，各品类已为目录最低价，无法自行压回；需用户调整预算或取消独显要求"])),
                final_text(proposal_result(OVERSPEND, "已复核预算自纠反馈：各品类已为目录最低价，无更低有报价候选可换，保留标注方案。", ["总价超出2500元预算硬上限，各品类已为目录最低价，无法自行压回；需用户调整预算或取消独显要求"])),
                final_text(proposal_result(OVERSPEND, "已复核预算自纠反馈：各品类已为目录最低价，无更低有报价候选可换，保留标注方案。", ["总价超出2500元预算硬上限，各品类已为目录最低价，无法自行压回；需用户调整预算或取消独显要求"])),
                final_text(proposal_result(OVERSPEND, "已复核预算自纠反馈：各品类已为目录最低价，无更低有报价候选可换，保留标注方案。", ["总价超出2500元预算硬上限，各品类已为目录最低价，无法自行压回；需用户调整预算或取消独显要求"])),
            ]},
        ]})

    # BV2-109 正式版本持久化 + 幂等
    cases.append({
        "id": "BV2-109",
        "title": "ready 交付持久化为正式版本；重复确认幂等，刷新零执行",
        "source": "P2-005 + C123-001 step1；P2-005 的 proposal 自动交付路径改为显式 confirm",
        "steps": [
            seed_message("预算4000，普通办公，配件全新买。",
                         [op("budget_cny", 4000, quote="预算4000"),
                          op_fact("use_case.type", "general", quote="普通办公"),
                          op_fact("existing_parts", [], quote="全新买")],
                         "已记录预算4000元与办公用途。"),
            {"kind": "retry", "expect": {"versions": 0}},
            {"kind": "confirm", "expect": {
                "versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                "require_tools": ["evaluate"], "budget_ceiling_cny": "4400",
            }, "builder_oracle": [
                fc("evaluate", {"draft": draft(OFFICE_16, rationale("核显办公方案"))}, f"{cid}-109-1"),
                final_text(ready_result(OFFICE_16, "3134.80", "已在4000元预算内配好办公主机。")),
            ]},
            {"kind": "refresh", "expect": {"versions": 1, "builder_calls": 0}},
        ]})

    # BV2-110 改单继承 base_draft（C123-002 verbatim 前置版本）
    src_c2 = [c for c in source["cases"] if c["id"] == "C123-002"][0]
    previous = copy.deepcopy(src_c2["previous_build"])
    previous["requirement"] = {"schema_version": 1, "budget_cny": 7000,
                               "use_case": {"type": "productivity", "titles": ["视频剪辑"]},
                               "existing_parts": []}
    cases.append({
        "id": "BV2-110",
        "title": "改单继承 base_draft：退库配件换成当前有报价件并压回预算",
        "source": "C123-002（current-178 mechanisms）；previous_build 原样复用，requirement 补 use_case.titles/existing_parts（v2 readiness 必填，旧题编写于 readiness v2 之前）",
        "previous_build": previous,
        "steps": [
            {"kind": "refresh", "expect": {"versions": 1, "builder_calls": 0}},
            {"kind": "confirm", "expect": {
                "versions": 2, "outcome": "ready", "validation": "pass", "missing_prices": 0,
                "require_tools": ["search_local_batch", "evaluate"],
                "budget_ceiling_cny": "7700",
                "model_input_contains": ["base_draft", "unresolved_base_ids", "gpu-sapphire-6600-pulse", "effective_constraints"],
                "frozen_constraints": frozen(
                    spec=gold_spec(budget_cny=7000, use_case__type="productivity",
                                   use_case__titles=["视频剪辑"], existing_parts=[]),
                    defaults=[default_gold("budget_flex", 0.1)]),
            }, "builder_oracle": [
                batch(["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"], f"{cid}-110-1"),
                fc("evaluate", {"draft": draft(RETIRED_SWAP, rationale("退库件替换为当前有报价件，内存容量保持32GB"))}, f"{cid}-110-2"),
                final_text(ready_result(RETIRED_SWAP, "7304.00", "已把退库配件换成当前有报价的候选，保持32GB内存容量，总价7304元在预算内。")),
                final_text(ready_result(RETIRED_SWAP, "7304.00", "已把退库配件换成当前有报价的候选，保持32GB内存容量，总价7304元在预算内。")),
            ]},
            {"kind": "refresh", "expect": {"versions": 2, "builder_calls": 0}},
        ]})
    return cases


def main():
    source = load_source()
    os.makedirs(OUT, exist_ok=True)
    suite = {
        "version": VERSION,
        "live": False,
        "provenance": "Builder v2 专项评估：给 Builder 一份已核定的 v2 需求（确认事务冻结 EffectiveConstraints），考察其选件机制。"
                      "目录与 current-178 mechanisms（快照11，126 active）字节一致。旧用例只算回归来源，不冒充新盲测；"
                      "硬约束由确定性规则判，允许多个同样合格的 SKU。逐题来源与语义变化见 provenance.json 与 docs/changes/builder-v2-eval.md。",
        "catalog": source["catalog"],
        "pages": {},
        "cases": build_cases(source),
    }
    raw = json.dumps(suite, ensure_ascii=False, indent=1) + "\n"
    with open(os.path.join(OUT, "suite.json"), "w", encoding="utf-8", newline="\n") as f:
        f.write(raw)

    migration_notes = {
        "BV2-101": {"source_case_ids": ["C123-006"],
                    "semantic_changes": ["多轮 screening 收集折叠为一次核定 seed（适配器输入）",
                                         "预算上限断言改按冻结合同口径 budget×(1+默认弹性0.1)=4400；旧题断言 4000 属更严格的产品语义，在此登记差异",
                                         "核显路径用属性断言（cpu.has_igpu=true），不锁 SKU"],
                    "not_directly_migrated": None},
        "BV2-102": {"source_case_ids": ["L1-013"],
                    "semantic_changes": ["v1 spec 的 budget_flex 弹性语义升格为 v2 冻结字段（显式 0）",
                                         "15K 目录场景缩到 5K 以贴合 126 件隔离目录"],
                    "not_directly_migrated": "原 15K 预算与 0.15 弹性在快照11目录下无对应场景，价格结构不同"},
        "BV2-103": {"source_case_ids": ["L4-206"],
                    "semantic_changes": ["已有 CPU 改为目录内可精确匹配的 4600G（matchesOwnedPart 精确模型匹配，唯一确定性匹配，允许断言 ID）",
                                         "new_purchase 品类核账由服务端确定性执行，tight ceiling（4400）以算术证明排除"],
                    "not_directly_migrated": None},
        "BV2-104": {"source_case_ids": ["C123-003", "L4-204"],
                    "semantic_changes": ["旧题多轮对话（先检索方向再澄清）折叠为单轮 Builder 决策",
                                         "G.Skill 32GB 在快照11无精确匹配的语义不变；期望 draft=null 的 clarify（旧录制为带替身草稿的 clarify，服务端会强转，v2 直接断言干净 clarify）"],
                    "not_directly_migrated": None},
        "BV2-105": {"source_case_ids": ["C123-005"],
                    "semantic_changes": ["旧录制 oracle 总价超预算（r3 录制套件 replay 本就不过），v2 重写为预算内 oracle（核显 CPU + ITX 板）",
                                         "断言改属性口径：case 限 SFX/SFX-L、psu form_factor=sfx，不锁具体电源 SKU"],
                    "not_directly_migrated": "旧 oracle 的 3060 独显配置在快照11价格下必然超 9900 上限，无法原样迁移"},
        "BV2-106": {"source_case_ids": ["P2-011"],
                    "semantic_changes": ["插槽冲突 SKU 换为快照11内的 AM4 CPU（5600）+ AM5 板（B650M MORTAR）",
                                         "内存配 DDR5 以隔离单一冲突源"],
                    "not_directly_migrated": None},
        "BV2-107": {"source_case_ids": ["C123-007"],
                    "semantic_changes": ["旧题以 base_draft 前提构造（v1 已交付方案中散热器缺字段），v2 简化为新装单轮，保留『unknown 不被默认值替代、换字段完整候选』机制点"],
                    "not_directly_migrated": None},
        "BV2-108": {"source_case_ids": ["C123-006", "L4-215"],
                    "semantic_changes": ["旧题为续聊降预算步（4000→3000），v2 单轮 2500 元独显整机预算（目录下限约5677.5元，无条件可行解）",
                                         "期望 proposal+预算 issue；预算门不回环的前提（各品类已目录最低价）由 OVERSPEND 选型保证"],
                    "not_directly_migrated": None},
        "BV2-109": {"source_case_ids": ["P2-005", "C123-001"],
                    "semantic_changes": ["P2-005 的『proposal 自动交付』路径改为显式 confirm（v2 冻结必须经确认事务）",
                                         "新增 refresh/retry 幂等与 immutable_version 断言"],
                    "not_directly_migrated": None},
        "BV2-110": {"source_case_ids": ["C123-002"],
                    "semantic_changes": ["previous_build 原样复用；requirement 补 use_case.titles 与 existing_parts（v2 readiness 必填，旧题编写于 readiness v2 冻结之前）",
                                         "oracle 最终交付改 ready（退库件替换后 7304 ≤ 7700 冻结上限；旧录制 oracle 为超支 proposal）",
                                         "新增 frozen_constraints 金标断言与 effective_constraints 模型输入断言"],
                    "not_directly_migrated": None},
    }
    not_migrated = [
        {"source_case_ids": ["B2-001", "B2-002", "B2-003"],
         "reason": "需求收集节奏、free.* 提取与比较语境收口属 Screening 层语义，超出 Builder 专项评估范围"},
        {"source_case_ids": ["C123-001"],
         "reason": "step3 升级收益证据（changed_cpu/cpu_target）依赖多轮对话轨迹与上一版本上下文；step1/2 语义已由 BV2-109/BV2-110 覆盖"},
        {"source_case_ids": ["P2-006", "P2-007", "P2-010"],
         "reason": "外部候选注册、询价不联网、备选不误执行属会话/工具语义，非 v2 冻结需求下的选件机制"},
        {"source_case_ids": ["L1-006"],
         "reason": "thermal-recorded 已知失败录制，按『不重写历史录制』政策原样保留，不迁移"},
        {"source_case_ids": ["L2-101…L2-110"],
         "reason": "旧 spec 的锁定品类改单语义与 v2 冻结 EffectiveConstraints 差异较大（锁定语义未进 v2 合同），留待下阶段单列设计"},
    ]
    provenance = {
        "suite_sha256": sha(raw),
        "source_suite": {"path": SOURCE, "sha256": sha(open(SOURCE, encoding="utf-8").read())},
        "catalog_note": "catalog 与 current-178 mechanisms 逐字一致（快照11，126 active_core，全部有价）",
        "grader_note": "硬约束确定性判：预算硬上限=budget×(1+flex)（must，缺省0.1，显式0按陈述）；已有件品类核账；validation=pass 才可 ready；unknown/无解如实保留；正式版本持久化。旧用例只算回归来源，不冒充新盲测。",
        "cases": migration_notes,
        "not_migrated": not_migrated,
    }
    with open(os.path.join(OUT, "provenance.json"), "w", encoding="utf-8", newline="\n") as f:
        json.dump(provenance, f, ensure_ascii=False, indent=1)
        f.write("\n")
    print(f"suite: {OUT}/suite.json sha256={provenance['suite_sha256'][:12]}… cases={len(suite['cases'])}")


if __name__ == "__main__":
    sys.exit(main())
