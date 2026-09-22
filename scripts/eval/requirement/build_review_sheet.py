# -*- coding: utf-8 -*-
"""生成 requirement-v2 人工复核摘要。

只读取冻结 fixtures 渲染 Markdown，不运行任何 holdout 或模型调用。
覆盖：全部 holdout case + cv-fps-progressive + ex-monitor-request +
accepted-proposal 边界（ex-budget-accept-explicit / ex-bare-accept-needs-protocol）。
"""
import json
import os

ROOT = os.path.join(os.path.dirname(__file__), "..", "..", "..",
                    "internal", "planningeval", "testdata", "requirement-v2")
OUT = os.path.join(os.path.dirname(__file__), "..", "..", "..",
                   "docs", "eval", "requirement-v2", "人工复核-20260922.md")


def load(name):
    with open(os.path.join(ROOT, name), encoding="utf-8") as f:
        return json.load(f)


def fmt_value(v):
    if isinstance(v, str):
        return f'"{v}"'
    return json.dumps(v, ensure_ascii=False)


def fmt_op(o):
    parts = [o.get("op", "?"), o.get("field", "?")]
    if "value" in o:
        parts.append(fmt_value(o["value"]))
    bits = ["=".join(parts[:2])]
    if "value" in o:
        bits.append(f'值 {fmt_value(o["value"])}')
    for k, label in (("strength", "强度"), ("evidence", "证据"), ("kind", "类别"), ("scope", "作用域")):
        if o.get(k):
            bits.append(f'{label} {o[k]}')
    if o.get("quote"):
        bits.append(f'quote「{o["quote"]}」')
    return "；".join(bits)


def fmt_expect(e):
    lines = []
    if e.get("reject"):
        lines.append(f"- 预期拒绝：`{e['reject']}`")
    if "expected_operations" in e and e["expected_operations"] is not None:
        if e["expected_operations"]:
            lines.append("- 预期操作：" + "；".join(fmt_op(o) for o in e["expected_operations"]))
        else:
            lines.append("- 预期操作：无（不得产生任何 operation）")
    if e.get("forbidden_operations"):
        lines.append("- 禁止操作：" + "；".join(f'{o.get("op", "set")} {o.get("field", "?")}' for o in e["forbidden_operations"]))
    if e.get("expected_observations"):
        lines.append("- 预期观察：" + "；".join(f'原文含「{o["text_contains"]}」' for o in e["expected_observations"]))
    if e.get("expected_turn_signals") is not None:
        sig = e["expected_turn_signals"]
        lines.append(f"- 预期 turn signals：asks_question={sig.get('asks_question')} requests_review={sig.get('requests_review')} requests_build={sig.get('requests_build')} ambiguous={sig.get('ambiguous')}")
    if e.get("expected_state"):
        fields = e["expected_state"]
        lines.append("- 预期状态：" + "；".join(f'{k}={fmt_value(v.get("value")) if isinstance(v, dict) and "value" in v else json.dumps(v, ensure_ascii=False)}' for k, v in fields.items()))
    if "missing_fields" in e and e.get("missing_fields") is not None:
        lines.append(f'- 预期缺失：`{e["missing_fields"]}`')
    if "expected_missing_fields" in e:
        lines.append(f'- 预期缺失：`{e["expected_missing_fields"]}`')
    if e.get("expected_confirmation_eligible") is not None:
        lines.append(f'- 确认资格：{e["expected_confirmation_eligible"]}')
    if e.get("expected_next_action_absent"):
        lines.append("- next_action 不得进入状态真值")
    if "expected" in e:  # readiness / reducer 顶层
        pass
    return lines


def fmt_readiness(exp):
    lines = [f'- 状态：`{exp["status"]}`']
    lines.append(f'- 缺失字段：`{exp["missing_fields"]}`')
    lines.append(f'- 阻塞冲突：`{exp["blocking_conflicts"]}`')
    lines.append(f'- 确认资格：{exp["confirmation_eligible"]}')
    if exp.get("effective_defaults"):
        lines.append("- 有效默认：" + "；".join(f'{d["field"]}={fmt_value(d["value"])}（{d["origin"]}）' for d in exp["effective_defaults"]))
    return lines


def case_block(title, meta, user_inputs, expect_lines):
    out = [f"### {title}", ""]
    out.append(f"- split：`{meta.get('split')}`（session `{meta.get('session')}`）")
    for u in user_inputs:
        out.append(f"- 用户输入：「{u}」")
    out.extend(expect_lines)
    if meta.get("rationale"):
        out.append(f"- 理由：{meta['rationale']}")
    out.append("")
    return out


doc = ["# Requirement v2 人工复核摘要（2026-09-22）", "",
       "> 由 `scripts/eval/requirement/build_review_sheet.py` 从冻结 fixtures 生成；holdout 未运行，本表仅供产品方逐项确认金标。确认后如需修改，走 fixture 修订 + freeze-manallet 流程，不得为通过运行改标。".replace("freeze-manallet", "freeze-manifest"),
       ""]

# 1) holdout 全量
doc.append("## 一、holdout 全量（9 例，锁定未运行）")
doc.append("")
for name, layer in (("reducer/cases.json", "reducer"), ("readiness/cases.json", "readiness"),
                    ("policy/cases.json", "policy"), ("ui-contract/cases.json", "ui-contract"),
                    ("extraction/cases.json", "extraction"), ("conversations/cases.json", "conversations")):
    for c in load(name):
        if c.get("split") != "holdout":
            continue
        if layer == "reducer":
            users = [f'{c["user_message"]}（操作：{ "；".join(fmt_op(o) for o in c["update"]["operations"]) }）']
            exp = fmt_expect(c["expected"])
        elif layer == "readiness":
            users = ["（给定状态，见理由）"]
            exp = fmt_readiness(c["expected"])
        elif layer == "policy":
            users = [c.get("seed_user_message") or "（无种子轮）", c["turn"].get("text") or f'（{c["turn"]["kind"]} 请求）']
            exp = [f'- Builder 准入：{c["expected"]["builder_admission"]}（原因 `{c["expected"].get("admission_reason", "-")}`）']
            if c["expected"].get("presentation_action"):
                exp.append(f'- presentation action：`{c["expected"]["presentation_action"]}`')
            if c["expected"].get("forbidden_vetoes"):
                exp.append(f'- 禁止 veto：`{c["expected"]["forbidden_vetoes"]}`')
        elif layer == "ui-contract":
            users = [c.get("seed_user_message") or "（无种子轮）"]
            e = c["expected"]
            exp = [f'- DTO 就绪块：{fmt_readiness(e["requirement_readiness"])[0]}']
            exp.append(f'- 确认状态：`{e["requirement_confirmation_status"]}`；build relation：`{e["build_relation"]}`')
            if e.get("confirm_request_payload"):
                exp.append(f'- 确认请求必须携带：`{e["confirm_request_payload"]}`')
        elif layer == "extraction":
            users = [c.get("seed_user_message") or "（无种子轮）", c["user_message"]]
            exp = fmt_expect(c["expected"])
        else:
            users = [t["text"] for t in c["turns"]]
            exp = []
            for i, t in enumerate(c["turns"], 1):
                inner = fmt_expect(t["expect"])
                exp.append(f"- 第 {i} 轮期望：" + ("；".join(x.lstrip("- ") for x in inner) if inner else "无特别断言"))
            f = c["final"]
            exp.append(f'- 最终：缺失 `{f.get("expected_missing_fields", [])}`，确认资格 {f["expected_confirmation_eligible"]}，builder_must_run={f["builder_must_run"]}')
            if f.get("forbidden_vetoes"):
                exp.append(f'- 禁止 veto：`{f["forbidden_vetoes"]}`')
        doc.extend(case_block(f'{layer} / {c["id"]}', c, users, exp))

# 2) 关键边界
doc.append("## 二、关键 development 边界（供确认）")
doc.append("")
targets = {"conversations": {"cv-fps-progressive"},
           "extraction": {"ex-monitor-request", "ex-budget-accept-explicit", "ex-bare-accept-needs-protocol",
                          "ex-budget-correction-same-turn", "ex-budget-conflict-family", "ex-fps-target-numeric"}}
for name, layer in (("conversations/cases.json", "conversations"), ("extraction/cases.json", "extraction")):
    for c in load(name):
        if c["id"] not in targets[layer]:
            continue
        if layer == "conversations":
            users = [t["text"] for t in c["turns"]]
            exp = []
            for i, t in enumerate(c["turns"], 1):
                inner = fmt_expect(t["expect"])
                exp.append(f"- 第 {i} 轮期望：" + ("；".join(x.lstrip("- ") for x in inner) if inner else "无特别断言"))
            f = c["final"]
            exp.append(f'- 最终：缺失 `{f.get("expected_missing_fields", [])}`，确认资格 {f["expected_confirmation_eligible"]}，builder_must_run={f["builder_must_run"]}')
            if f.get("forbidden_vetoes"):
                exp.append(f'- 禁止 veto：`{f["forbidden_vetoes"]}`')
        else:
            users = []
            if c.get("prior_assistant_turn"):
                users.append(f'（上下文）助手上一轮回复：「{c["prior_assistant_turn"]["reply"]}」')
            users.append(c["user_message"])
            exp = fmt_expect(c["expected"])
        doc.extend(case_block(f'{layer} / {c["id"]}', c, users, exp))

os.makedirs(os.path.dirname(OUT), exist_ok=True)
with open(OUT, "w", encoding="utf-8", newline="\n") as f:
    f.write("\n".join(doc))
print("wrote", os.path.relpath(OUT))
