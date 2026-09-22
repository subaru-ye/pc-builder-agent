# -*- coding: utf-8 -*-
"""构建 requirement-v2 评估 fixtures。

金标按 docs/changes/requirement-v2-evaluation-foundation.md 及四个 v2 产品
change 的契约人工标注（AI 辅助起草，2026-09-22）；本脚本只做确定性序列化，
不生成任何标签。重跑覆盖 testdata 后需要 freeze-manifest 并人工复核 diff。
"""
import json
import os

ROOT = os.path.join(os.path.dirname(__file__), "..", "..", "..",
                    "internal", "planningeval", "testdata", "requirement-v2")

UNKNOWN = {k: {"status": "unknown"} for k in [
    "budget_cny", "budget_flex", "budget_basis", "use_case.type", "use_case.titles",
    "use_case.resolution", "use_case.fps_target", "existing_parts", "owned_parts",
    "brand_pref.cpu", "brand_pref.gpu", "noise_pref", "size_pref", "appearance",
    "notes", "recipient", "priority"]}
BASE = {"schema_version": 1, "revision": 0, "alternatives": [], "changes": [], "history": []}


def state(**fields):
    s = dict(BASE)
    s["fields"] = {k: dict(v) for k, v in UNKNOWN.items()}
    for k, v in fields.items():
        key = k.replace("__", ".")
        if isinstance(v, dict) and v.get("status") == "active" and not v.get("strength")                 and "strength" not in v:
            v = dict(v)
            v["strength"] = "must" if key in MUST_FIELDS else "prefer"
        s["fields"][key] = v
    return s


MUST_FIELDS = {"budget_cny", "budget_flex", "budget_basis", "use_case.type",
               "use_case.resolution", "existing_parts", "owned_parts", "recipient"}


def active(value, quote, kind="", strength="", evidence="stated", message="seed-0", scope="session"):
    # strength 不在此处猜测；state() 按字段键注入产品默认，种子状态才可投影。
    f = {"status": "active", "value": value, "evidence": evidence,
         "source": {"kind": "chat", "message_id": message, "quote": quote}}
    if strength:
        f["strength"] = strength
    if kind:
        f["kind"] = kind
    if scope != "session":
        f["scope"] = scope
    return f


def head(id_, title, split, session, rationale, variants_of=None):
    h = {"id": id_, "title": title, "split": split, "session": session, "rationale": rationale}
    if variants_of:
        h["variants_of"] = variants_of
    return h


def op(field, op_, value=None, quote=None, kind=None, strength=None, evidence=None, scope=None):
    o = {"op": op_, "field": field}
    if value is not None:
        o["value"] = value
    if quote:
        o["quote"] = quote
    if kind:
        o["kind"] = kind
    if strength:
        o["strength"] = strength
    if evidence:
        o["evidence"] = evidence
    if scope:
        o["scope"] = scope
    return o


reducer = [
    {
        **head("rd-set-budget-7500", "聊天来源设置预算并绑定本轮原文", "development", "rd-dev-1",
               "最基础的 set：值、强度（默认 must）、证据 quote 都来自本轮用户消息。"),
        "initial_state": state(),
        "user_message": "预算7500",
        "update": {"source": "chat", "operations": [op("budget_cny", "set", 7500, "预算7500", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"budget_cny": {
                "status": "active", "value": 7500, "strength": "must", "evidence": "stated",
                "source": {"kind": "chat", "message_id": "turn-1", "quote": "预算7500"}}}},
    },
    {
        **head("rd-existing-empty-explicit", "全部新买是明确 active 空数组", "development", "rd-dev-2",
               "v2 要求 existing_parts=[] 是用户明确表达；unknown 不能默认为空。当前 reducer 接受空数组，此例应通过。"),
        "initial_state": state(),
        "user_message": "主机配件全部新买",
        "update": {"source": "chat", "operations": [op("existing_parts", "set", [], "全部新买", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"existing_parts": {
                "status": "active", "value": [], "strength": "must", "evidence": "stated",
                "source": {"kind": "chat", "message_id": "turn-1", "quote": "全部新买"}}}},
    },
    {
        **head("rd-owned-correction", "更正已有件型号覆盖旧值", "development", "rd-dev-3",
               "“其实是 4070 Super”是同字段的更正 set；existing/owned 联动保持品类一致。"),
        "initial_state": state(
            existing_parts=active(["gpu"], "有张显卡"),
            owned_parts=active([{"category": "gpu", "model": "4070"}], "是4070")),
        "user_message": "看错了，其实是4070 Super",
        "update": {"source": "chat", "operations": [op("owned_parts", "set", [{"category": "gpu", "model": "4070 Super"}], "4070 Super", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"owned_parts": {"status": "active", "value": [{"category": "gpu", "model": "4070 Super"}],
                                        "strength": "must", "evidence": "stated",
                                        "source": {"kind": "chat", "message_id": "turn-1", "quote": "4070 Super"}}}},
    },
    {
        **head("rd-alternative-keeps-active", "备选不改变当前值", "development", "rd-dev-4",
               "“如果改成一万呢”只是讨论；当前预算保持 7500，10000 记为备选。"),
        "initial_state": state(budget_cny=active(7500, "预算7500")),
        "user_message": "如果改成一万呢？",
        "update": {"source": "chat", "operations": [op("budget_cny", "alternative", 10000, "一万", strength="must", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"budget_cny": {"status": "active", "value": 7500}},
            "alternatives": [{"field": "budget_cny", "value": 10000, "strength": "must"}],
        },
    },
    {
        **head("rd-temporary-restore", "临时覆盖后恢复原值", "development", "rd-dev-5",
               "同一批内先 temporary set 再 restore，最终回到 7500 且不再保留墓碑。"),
        "initial_state": state(budget_cny=active(7500, "预算7500")),
        "user_message": "这轮先按9000试，还是恢复7500吧",
        "update": {"source": "chat", "operations": [
            op("budget_cny", "set", 9000, "按9000试", scope="temporary", evidence="stated"),
            op("budget_cny", "restore", quote="恢复7500", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"budget_cny": {"status": "active", "value": 7500, "strength": "must", "evidence": "stated",
                                       "source": {"kind": "chat", "message_id": "turn-1", "quote": "恢复7500"}}}},
    },
    {
        **head("rd-remove-budget", "明确撤销预算留墓碑", "development", "rd-dev-6",
               "remove 保留 removed 状态与来源；readiness 把必填 removed 按缺失处理。"),
        "initial_state": state(budget_cny=active(7500, "预算7500")),
        "user_message": "预算先不限制了",
        "update": {"source": "chat", "operations": [op("budget_cny", "remove", quote="不限制", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"budget_cny": {"status": "removed", "source": {"kind": "chat", "message_id": "turn-1", "quote": "不限制"}}}},
    },
    {
        **head("rd-reject-inferred", "推断证据不能写成 active", "development", "rd-dev-7",
               "原话不含任何金额，8000 只能从行情推断；evidence=inferred 一律拒绝，这是 V1 的 reducer 侧防线。"),
        "initial_state": state(),
        "user_message": "预算就按市面上主流入门整机的价位来",
        "update": {"source": "chat", "operations": [op("budget_cny", "set", 8000, "主流入门整机的价位", evidence="inferred")]},
        "expected": {"reject": "inferred_evidence"},
    },
    {
        **head("rd-reject-ungrounded-quote", "quote 不出自本轮原文即拒绝", "development", "rd-dev-8",
               "证据绑定是硬约束：编造或搬运旧话不能通过。"),
        "initial_state": state(),
        "user_message": "预算大概8000吧",
        "update": {"source": "chat", "operations": [op("budget_cny", "set", 8000, "上次说过的8000", evidence="stated")]},
        "expected": {"reject": "missing_evidence"},
    },
    {
        **head("rd-atomic-batch", "批量操作任一失败整批拒绝", "development", "rd-dev-9",
               "第二个 op 值非法时第一个也不落地；revision 不变。"),
        "initial_state": state(),
        "user_message": "打游戏，预算-5",
        "update": {"source": "chat", "operations": [
            op("use_case.type", "set", "gaming", "打游戏", evidence="stated"),
            op("budget_cny", "set", -5, "-5", evidence="stated")]},
        "expected": {"reject": "invalid_value_or_op"},
    },
    {
        **head("rd-v2-performance-goal", "v2 字段 use_case.performance_goal", "calibration", "rd-cal-1",
               "“帧率越高越好”映射 fps_first/prefer 且不生成具体 FPS；当前 schema 无该字段，预期 red 为契约缺口。"),
        "initial_state": state(),
        "user_message": "帧率越高越好",
        "update": {"source": "chat", "operations": [op("use_case.performance_goal", "set", "fps_first", "越高越好", strength="prefer", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"use_case.performance_goal": {"status": "active", "value": "fps_first", "strength": "prefer",
                                                      "evidence": "stated",
                                                      "source": {"kind": "chat", "message_id": "turn-1", "quote": "越高越好"}}}},
    },
    {
        **head("rd-v2-any-resolution", "明确不限是 active 用户值", "calibration", "rd-cal-2",
               "“分辨率无所谓”是明确 any，不是 unknown；当前枚举拒绝 any，预期 red 为契约缺口。"),
        "initial_state": state(use_case__type=active("gaming", "打游戏", kind="fact")),
        "user_message": "分辨率无所谓",
        "update": {"source": "chat", "operations": [op("use_case.resolution", "set", "any", "无所谓", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"use_case.resolution": {"status": "active", "value": "any", "evidence": "stated",
                                                "source": {"kind": "chat", "message_id": "turn-1", "quote": "无所谓"}}}},
    },
    {
        **head("rd-holdout-productivity-titles", "生产力用途与软件同轮记录", "holdout", "rd-hold-1",
               "“主要用Premiere剪辑视频”给出 type 与 titles 两个 fact。"),
        "initial_state": state(),
        "user_message": "主要用Premiere剪辑视频",
        "update": {"source": "chat", "operations": [
            op("use_case.type", "set", "productivity", "Premiere剪辑", kind="fact", evidence="stated"),
            op("use_case.titles", "set", ["Premiere 视频剪辑"], "Premiere剪辑", kind="fact", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {
                "use_case.type": {"status": "active", "value": "productivity", "kind": "fact"},
                "use_case.titles": {"status": "active", "value": ["Premiere 视频剪辑"], "kind": "fact"}}},
    },
    {
        **head("rd-holdout-budget-basis", "已有件时预算口径为用户事实", "holdout", "rd-hold-2",
               "复用旧内存时“按整机算”设置 budget_basis=full_build。"),
        "initial_state": state(
            existing_parts=active(["memory"], "旧内存还能用"),
            owned_parts=active([{"category": "memory", "model": "金百达刃 16G"}], "金百达刃")),
        "user_message": "预算按整机总价算",
        "update": {"source": "chat", "operations": [op("budget_basis", "set", "full_build", "整机", evidence="stated")]},
        "expected": {
            "schema_version": 2, "revision_delta": 1, "reply_next_action_absent": True,
            "fields": {"budget_basis": {"status": "active", "value": "full_build", "strength": "must",
                                         "evidence": "stated",
                                         "source": {"kind": "chat", "message_id": "turn-1", "quote": "整机"}}}},
    },
]

GAMING_FULL = lambda: state(
    use_case__type=active("gaming", "打游戏", kind="fact"),
    use_case__resolution=active("1080p", "1080p"),
    budget_cny=active(7500, "预算7500"),
    existing_parts=active([], "全新买"))
PRODUCTIVITY_FULL = lambda: state(
    use_case__type=active("productivity", "剪辑", kind="fact"),
    use_case__titles=active(["Blender 渲染"], "Blender"),
    budget_cny=active(8000, "预算8000"),
    existing_parts=active([], "全新买"))

DEF_TOWER = [{"field": "configuration_scope", "value": ["tower"], "origin": "system_default"}]
DEF_COMMON = [
    {"field": "budget_flex", "value": 0.1, "origin": "system_default"},
    {"field": "size_pref", "value": "any", "origin": "system_default"},
    {"field": "noise_pref", "value": "any", "origin": "system_default"},
    {"field": "brand_pref.cpu", "value": "any", "origin": "system_default"},
    {"field": "brand_pref.gpu", "value": "any", "origin": "system_default"},
] + DEF_TOWER

readiness = [
    {
        **head("rdy-fresh", "空会话缺三必填", "development", "rdy-dev-1",
               "missing 按追问优先级排序：type → budget → existing_parts。"),
        "state": state(),
        "expected": {"status": "incomplete", "missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
                      "blocking_conflicts": [], "confirmation_eligible": False},
    },
    {
        **head("rdy-gaming-missing-three", "只知用途缺预算/已有件/分辨率", "development", "rdy-dev-2",
               "gaming 条件必填 resolution；existing_parts unknown 也阻塞（v2 与当前实现的差异点）。"),
        "state": state(use_case__type=active("gaming", "打游戏", kind="fact")),
        "expected": {"status": "incomplete", "missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"],
                      "blocking_conflicts": [], "confirmation_eligible": False},
    },
    {
        **head("rdy-gaming-ready-defaults", "gaming 齐备展开系统默认", "development", "rdy-dev-3",
               "ready 时 projection 展开 7 项 effective defaults；RequirementState 对应字段仍 unknown。"),
        "state": GAMING_FULL(),
        "expected": {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True,
                      "effective_defaults": DEF_COMMON + [{"field": "performance_goal", "value": "balanced", "origin": "system_default"}]},
    },
    {
        **head("rdy-productivity-no-titles", "生产力缺主要软件或任务", "development", "rdy-dev-4",
               "v2 条件必填 titles；当前实现不要求，是预期 red。"),
        "state": state(use_case__type=active("productivity", "做剪辑", kind="fact"),
                        budget_cny=active(7500, "预算7500"), existing_parts=active([], "全新买")),
        "expected": {"status": "incomplete", "missing_fields": ["use_case.titles"],
                      "blocking_conflicts": [], "confirmation_eligible": False},
    },
    {
        **head("rdy-productivity-ready", "生产力齐备", "development", "rdy-dev-5",
               "titles 满足条件必填；默认 6 项（无 gaming performance_goal）。"),
        "state": PRODUCTIVITY_FULL(),
        "expected": {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True,
                      "effective_defaults": DEF_COMMON},
    },
    {
        **head("rdy-owned-missing-model", "已有显卡缺型号与口径", "development", "rdy-dev-6",
               "条件必填 owned model + budget_basis；当前实现同样要求，应通过。"),
        "state": state(existing_parts=active(["gpu"], "有张显卡")),
        "expected": {"status": "incomplete", "missing_fields": ["use_case.type", "budget_cny", "owned_parts.gpu.model", "budget_basis"],
                      "blocking_conflicts": [], "confirmation_eligible": False},
    },
    {
        **head("rdy-budget-conflict-blocks", "预算冲突单列阻塞", "development", "rdy-dev-7",
               "v2 把 conflict 从 missing 分离到 blocking_conflicts；当前实现并入 missing，预期 red。"),
        "state": state(budget_cny={"status": "conflict", "strength": "must"}),
        "expected": {"status": "incomplete", "missing_fields": ["use_case.type"], "blocking_conflicts": ["budget_cny"],
                      "confirmation_eligible": False},
    },
    {
        **head("rdy-removed-required", "撤销的必填按缺失处理", "development", "rdy-dev-8",
               "budget removed 不再阻塞冲突，只回到 missing；当前实现一致，应通过。"),
        "state": {**GAMING_FULL(), "fields": {**GAMING_FULL()["fields"], "budget_cny": {"status": "removed"}}},
        "expected": {"status": "incomplete", "missing_fields": ["budget_cny"], "blocking_conflicts": [],
                      "confirmation_eligible": False},
    },
    {
        **head("rdy-existing-unknown-blocks", "已有件 unknown 不能默认为空", "development", "rdy-dev-9",
               "v2 要求 existing_parts 必须 active；当前实现允许 unknown 通过，预期 red。"),
        "state": state(use_case__type=active("gaming", "打游戏", kind="fact"),
                        use_case__resolution=active("1080p", "1080p"), budget_cny=active(7500, "预算7500")),
        "expected": {"status": "incomplete", "missing_fields": ["existing_parts"], "blocking_conflicts": [],
                      "confirmation_eligible": False},
    },
    {
        **head("rdy-gaming-any-resolution-not-satisfied", "gaming 的 any 不满足最低矩阵", "development", "rdy-dev-10",
               "明确 any 是 active 用户值，但 gaming 条件必填只认 1080p/2K/4K；any 仍缺 use_case.resolution，不可确认。"),
        "state": state(use_case__type=active("gaming", "打游戏", kind="fact"),
                        use_case__resolution=active("any", "无所谓"),
                        budget_cny=active(7500, "预算7500"),
                        existing_parts=active([], "全新买")),
        "expected": {"status": "incomplete", "missing_fields": ["use_case.resolution"],
                      "blocking_conflicts": [], "confirmation_eligible": False},
    },
    {
        **head("rdy-soft-conflict-not-blocking", "普通软偏好冲突不阻塞", "calibration", "rdy-cal-1",
               "noise prefer 冲突只保留观察，不进 blocking 也不 missing。"),
        "state": {**GAMING_FULL(), "fields": {**GAMING_FULL()["fields"],
                   "noise_pref": {"status": "conflict", "strength": "prefer"}}},
        "expected": {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True,
                      "effective_defaults": DEF_COMMON + [{"field": "performance_goal", "value": "balanced", "origin": "system_default"}]},
    },
    {
        **head("rdy-must-brand-conflict-blocks", "must 约束冲突阻塞", "calibration", "rdy-cal-2",
               "品牌 must 冲突是 blocking，不是普通 missing；预期 red（当前并入 missing）。"),
        "state": {**GAMING_FULL(), "fields": {**GAMING_FULL()["fields"],
                   "brand_pref.cpu": {"status": "conflict", "strength": "must"}}},
        "expected": {"status": "incomplete", "missing_fields": [], "blocking_conflicts": ["brand_pref.cpu"],
                      "confirmation_eligible": False},
    },
    {
        **head("rdy-holdout-owned-basis", "已有内存缺口径", "holdout", "rdy-hold-1",
               "型号齐但 budget_basis 未知仍不完整；当前实现一致。"),
        "state": state(existing_parts=active(["memory"], "旧内存还能用"),
                        owned_parts=active([{"category": "memory", "model": "金百达刃 16G"}], "金百达刃"),
                        use_case__type=active("productivity", "办公", kind="fact"),
                        budget_cny=active(4000, "预算4000")),
        "expected": {"status": "incomplete", "missing_fields": ["budget_basis"], "blocking_conflicts": [],
                      "confirmation_eligible": False},
    },
    {
        **head("rdy-holdout-general-ready", "通用用途齐备", "holdout", "rdy-hold-2",
               "general 无条件必填；默认 6 项。"),
        "state": state(use_case__type=active("general", "家用办公", kind="fact"),
                        budget_cny=active(3500, "预算3500"), existing_parts=active([], "全新买")),
        "expected": {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True,
                      "effective_defaults": DEF_COMMON},
    },
]

SEED_GAMING = [op("use_case.type", "set", "gaming", "打游戏", kind="fact", evidence="stated")]
SEED_GAMING_MSG = "我想配台打游戏的电脑"
SEED_FULL = [
    op("use_case.type", "set", "gaming", "打游戏", kind="fact", evidence="stated"),
    op("use_case.resolution", "set", "1080p", "1080p", evidence="stated"),
    op("budget_cny", "set", 7500, "预算7500", evidence="stated"),
    op("existing_parts", "set", [], "全新买", evidence="stated")]
SEED_FULL_MSG = "打游戏用，1080p，预算7500，配件全新买"

policy = [
    {
        **head("pol-incomplete-chat-start", "信息不足时聊天要求开始不得启动 Builder", "development", "pol-dev-1",
               "scripted screening 宣称 next_action=plan；v2 要求 readiness incomplete 时 admission=false 并聚焦缺失项。当前 next_action 有权威，预期 V4。"),
        "seed": SEED_GAMING, "seed_user_message": SEED_GAMING_MSG, "state_next_action": "plan",
        "turn": {"kind": "message", "text": "帮我配一台吧", "signals": {"requests_build": True}},
        "expected": {"builder_admission": False, "admission_reason": "readiness_incomplete",
                      "presentation_action": "focus_missing_requirement", "forbidden_vetoes": ["V4"]},
    },
    {
        **head("pol-ready-chat-start", "聊天“开始吧”只打开核定不启动", "development", "pol-dev-2",
               "ready+unconfirmed 时 requests_build 产生 open_requirement_review；聊天文字不是确认。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG, "state_next_action": "plan",
        "turn": {"kind": "message", "text": "就这样，开始吧", "signals": {"requests_build": True}},
        "expected": {"builder_admission": False, "admission_reason": "no_confirmation",
                      "presentation_action": "open_requirement_review", "forbidden_vetoes": ["V4"]},
    },
    {
        **head("pol-ready-confirm", "核定面板确认可启动", "development", "pol-dev-3",
               "confirm API 提交且 readiness 通过时 admission=true；当前 StartConfirm 行为一致，应通过。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG, "state_next_action": "confirm",
        "turn": {"kind": "confirm"},
        "expected": {"builder_admission": True, "admission_reason": "ok"},
    },
    {
        **head("pol-second-builder-running", "运行中第二个确认被拒", "development", "pol-dev-4",
               "builder 运行中再次确认 admission=false；当前 store 以会话忙拒绝，应通过且无 V6。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG, "state_next_action": "confirm",
        "pre_turns": [{"kind": "confirm"}], "hold_builder": True,
        "turn": {"kind": "confirm"},
        "expected": {"builder_admission": False, "admission_reason": "builder_running", "forbidden_vetoes": ["V6"]},
    },
    {
        **head("pol-stale-revision-edit", "过期 revision 编辑被拒", "development", "pol-dev-5",
               "expected_revision 落后时编辑必须失败；当前实现有乐观并发检查，应通过且无 V7。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG, "state_next_action": "confirm",
        "turn": {"kind": "edit", "edit": [op("budget_cny", "set", 9000)], "edit_expected_revision_delta": -1},
        "expected": {"builder_admission": False, "edit_accepted": False, "forbidden_vetoes": ["V7"]},
    },
    {
        **head("pol-edit-while-running", "运行中草稿编辑仍可保存", "calibration", "pol-cal-1",
               "v2 允许生成期间编辑草稿；当前实现对执行中会话统一拒绝，预期 red。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG, "state_next_action": "confirm",
        "pre_turns": [{"kind": "confirm"}], "hold_builder": True,
        "turn": {"kind": "edit", "edit": [op("budget_cny", "set", 9000)], "edit_expected_revision_delta": 0},
        "expected": {"builder_admission": False, "edit_accepted": True},
    },
    {
        **head("pol-edit-after-confirm-rebuild", "确认后修改需重新核定", "calibration", "pol-cal-2",
               "确认生成失败后修改预算再从聊天续跑：v2 要求重新核定；当前按新草稿直接续跑，预期 V5。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG, "state_next_action": "confirm",
        "pre_turns": [{"kind": "confirm"}],
        "turn": {"kind": "message", "text": "预算改成9000，重新配",
                  "ops": [op("budget_cny", "set", 9000, "9000", evidence="stated")],
                  "signals": {"requests_build": True}},
        "expected": {"builder_admission": False, "admission_reason": "no_confirmation",
                      "presentation_action": "open_requirement_review", "forbidden_vetoes": ["V5"]},
    },
    {
        **head("pol-monitor-promise-guard", "scope 外品类不得被承诺", "development", "pol-dev-6",
               "scripted 回复承诺配显示器；v2 要求确定性 scope 守卫拦截，当前直接透传，预期 V9。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG, "state_next_action": "collect",
        "turn": {"kind": "message", "text": "顺便把显示器也配了", "scripted_reply": "好的，显示器和主机一起给你配。"},
        "expected": {"builder_admission": False, "forbidden_vetoes": ["V9"]},
    },
    {
        **head("pol-holdout-confirm-incomplete", "信息不足时确认提交被拒", "holdout", "pol-hold-1",
               "productivity 缺 titles 时 confirm API 必须以 readiness 拒绝；当前 planningProjection 忽略 missing，预期启动即 red。"),
        "seed": [op("use_case.type", "set", "productivity", "做剪辑", kind="fact", evidence="stated"),
                  op("budget_cny", "set", 7500, "预算7500", evidence="stated"),
                  op("existing_parts", "set", [], "全新买", evidence="stated")],
        "seed_user_message": "做视频剪辑，预算7500，全新买",
        "state_next_action": "confirm",
        "turn": {"kind": "confirm"},
        "expected": {"builder_admission": False, "admission_reason": "readiness_incomplete", "forbidden_vetoes": ["V4"]},
    },
]

ui_contract = [
    {
        **head("ui-collecting-fresh", "新会话可见收集态与缺失项", "development", "ui-dev-1",
               "DTO 必须暴露 incomplete 与 missing；当前 MissingFields 恒空，预期 red。"),
        "seed": [], "seed_user_message": "", "state_next_action": "",
        "expected": {
            "requirement_readiness": {"status": "incomplete", "missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
                                       "blocking_conflicts": [], "confirmation_eligible": False},
            "requirement_confirmation_status": "unconfirmed", "build_relation": "none"},
    },
    {
        **head("ui-ready-unconfirmed", "齐备未确认的可视状态与确认 payload", "development", "ui-dev-2",
               "ready+unconfirmed+none 组合；确认请求必须携带 expected_revision 等键，当前只有 request id。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG, "state_next_action": "confirm",
        "expected": {
            "requirement_readiness": {"status": "ready", "missing_fields": [], "blocking_conflicts": [],
                                       "confirmation_eligible": True, "effective_defaults": DEF_COMMON},
            "requirement_confirmation_status": "unconfirmed", "build_relation": "none",
            "confirm_request_payload": ["schema_version", "expected_revision", "idempotency_key"]},
    },
    {
        **head("ui-incomplete-shown-ready", "不完整状态不得显示可确认", "development", "ui-dev-3",
               "gaming 只知用途时 DTO 却因 next_action 显示 ready_to_confirm；预期 V3。"),
        "seed": SEED_GAMING, "seed_user_message": SEED_GAMING_MSG, "state_next_action": "confirm",
        "expected": {
            "requirement_readiness": {"status": "incomplete", "missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"],
                                       "blocking_conflicts": [], "confirmation_eligible": False},
            "requirement_confirmation_status": "unconfirmed", "build_relation": "none"},
    },
    {
        **head("ui-holdout-productivity-ready", "生产力齐备可视状态", "holdout", "ui-hold-1",
               "ready+unconfirmed+none；与 ui-ready-unconfirmed 不同会话模板。"),
        "seed": [op("use_case.type", "set", "productivity", "用Blender", kind="fact", evidence="stated"),
                  op("use_case.titles", "set", ["Blender 渲染"], "Blender", evidence="stated"),
                  op("budget_cny", "set", 8000, "预算8000", evidence="stated"),
                  op("existing_parts", "set", [], "全新买", evidence="stated")],
        "seed_user_message": "用Blender渲染，预算8000，全新买",
        "state_next_action": "confirm",
        "expected": {
            "requirement_readiness": {"status": "ready", "missing_fields": [], "blocking_conflicts": [],
                                       "confirmation_eligible": True, "effective_defaults": DEF_COMMON},
            "requirement_confirmation_status": "unconfirmed", "build_relation": "none"},
    },
]

extraction = [
    {
        **head("ex-first-fps-vague", "首句模糊 FPS 需求只记可验证事实", "development", "ex-dev-1",
               "用途、titles、performance goal 可从原文验证；预算/已有件/分辨率继续追问。"),
        "seed": [], "seed_user_message": "", "user_message": "主要玩CS2和无畏契约，希望帧率高一点",
        "expected": {
            "expected_operations": [
                {"op": "set", "field": "use_case.type", "value": "gaming", "quote_contains": "CS2"},
                {"op": "set", "field": "use_case.titles", "value": ["CS2", "无畏契约"], "quote_contains": "无畏契约"},
                {"op": "set", "field": "use_case.performance_goal", "value": "fps_first", "quote_contains": "帧率"}],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False},
            "expected_missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"],
            "expected_confirmation_eligible": False,
            "expected_next_action_absent": True},
    },
    {
        **head("ex-budget-question-7500", "“7500够吗”不激活预算", "development", "ex-dev-2",
               "询问行情是 question；7500 最多记备选/观察，不得 set。"),
        "seed": [op("use_case.type", "set", "gaming", "打游戏", kind="fact", evidence="stated"),
                  op("use_case.resolution", "set", "1080p", "1080p", evidence="stated"),
                  op("existing_parts", "set", [], "全新买", evidence="stated")],
        "seed_user_message": "打游戏用，1080p，配件全新买",
        "user_message": "7500够吗？",
        "expected": {
            "expected_operations": [],
            "forbidden_operations": [{"op": "set", "field": "budget_cny"}],
            "expected_turn_signals": {"asks_question": True, "requests_review": False, "requests_build": False, "ambiguous": False},
            "expected_missing_fields": ["budget_cny"],
            "expected_confirmation_eligible": False},
    },
    {
        **head("ex-budget-accept-explicit", "“就按7500来”是明确表达", "development", "ex-dev-3",
               "用户自己说出数字即 stated 证据；上一轮助手提问只是上下文。"),
        "seed": [op("use_case.type", "set", "gaming", "打游戏", kind="fact", evidence="stated"),
                  op("use_case.resolution", "set", "1080p", "1080p", evidence="stated"),
                  op("existing_parts", "set", [], "全新买", evidence="stated")],
        "seed_user_message": "打游戏用，1080p，配件全新买",
        "prior_assistant_turn": {"user_message": "先看预算", "reply": "按7500元的预算继续可以吗？"},
        "user_message": "可以，就按7500来",
        "expected": {
            "expected_operations": [{"op": "set", "field": "budget_cny", "value": 7500, "evidence": "stated", "quote_contains": "7500"}],
            "expected_state": {"budget_cny": {"status": "active", "value": 7500}},
            "expected_missing_fields": [],
            "expected_confirmation_eligible": True,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-bare-accept-needs-protocol", "裸“可以”需要可验证提案协议", "development", "ex-dev-4",
               "存在同字段同值提案且用户明确接受时 evidence=accepted_proposal；当前无该协议，预期 red。"),
        "seed": [op("use_case.type", "set", "gaming", "打游戏", kind="fact", evidence="stated"),
                  op("use_case.resolution", "set", "1080p", "1080p", evidence="stated"),
                  op("existing_parts", "set", [], "全新买", evidence="stated")],
        "seed_user_message": "打游戏用，1080p，配件全新买",
        "prior_assistant_turn": {"user_message": "先看预算", "reply": "按7500元的预算继续可以吗？"},
        "user_message": "可以",
        "expected": {
            "expected_operations": [{"op": "set", "field": "budget_cny", "value": 7500, "evidence": "accepted_proposal", "quote_contains": "可以"}],
            "expected_state": {"budget_cny": {"status": "active", "value": 7500}},
            "expected_missing_fields": [], "expected_confirmation_eligible": True},
    },
    {
        **head("ex-composite-budget-start", "复合意图：预算+开始", "development", "ex-dev-5",
               "同一句既更新字段又表达生成意愿；signals 多标签并存。"),
        "seed": [op("use_case.type", "set", "gaming", "打游戏", kind="fact", evidence="stated"),
                  op("use_case.resolution", "set", "1080p", "1080p", evidence="stated"),
                  op("existing_parts", "set", [], "全新买", evidence="stated")],
        "seed_user_message": "打游戏用，1080p，配件全新买",
        "user_message": "预算9000，开始配吧",
        "expected": {
            "expected_operations": [{"op": "set", "field": "budget_cny", "value": 9000, "evidence": "stated", "quote_contains": "9000"}],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": True, "ambiguous": False},
            "expected_state": {"budget_cny": {"status": "active", "value": 9000}},
            "expected_missing_fields": [], "expected_confirmation_eligible": True,
            "expected_next_action_absent": True},
    },
    {
        **head("ex-all-new-purchase", "“全部新买”是明确空已有件", "development", "ex-dev-6",
               "existing_parts=[] 是 active 用户值，不是 unknown。"),
        "seed": [], "seed_user_message": "", "user_message": "帮我配台全新的主机，配件都新买",
        "expected": {
            "expected_operations": [{"op": "set", "field": "existing_parts", "value": [], "quote_contains": "新买"}],
            "expected_state": {"existing_parts": {"status": "active", "value": []}},
            "expected_missing_fields": ["use_case.type", "budget_cny"],
            "expected_confirmation_eligible": False,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-owned-gpu-vague", "“有张显卡”记录品类不猜型号", "development", "ex-dev-7",
               "型号缺失留给追问；不得编造 owned model。"),
        "seed": [], "seed_user_message": "", "user_message": "我有一张显卡能用",
        "expected": {
            "expected_operations": [{"op": "set", "field": "existing_parts", "value": ["gpu"], "quote_contains": "显卡"}],
            "forbidden_operations": [{"op": "set", "field": "owned_parts"}],
            "expected_missing_fields": ["use_case.type", "budget_cny", "owned_parts.gpu.model", "budget_basis"],
            "expected_confirmation_eligible": False,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-owned-correction", "更正已有件型号", "development", "ex-dev-8",
               "“其实是 4070 Super”覆盖旧型号，quote 绑定新原文。"),
        "seed": [op("existing_parts", "set", ["gpu"], "有张显卡", evidence="stated"),
                  op("owned_parts", "set", [{"category": "gpu", "model": "4070"}], "是4070", evidence="stated")],
        "seed_user_message": "有张显卡，是4070",
        "user_message": "看错了，其实是4070 Super",
        "expected": {
            "expected_operations": [{"op": "set", "field": "owned_parts", "value": [{"category": "gpu", "model": "4070 Super"}], "quote_contains": "4070 Super"}],
            "expected_state": {"owned_parts": {"status": "active", "value": [{"category": "gpu", "model": "4070 Super"}]}},
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-budget-ceiling", "“不要超过”设置预算与零弹性", "calibration", "ex-cal-1",
               "上限语气映射 budget 7500 + flex 0，两个操作同轮。"),
        "seed": [op("use_case.type", "set", "gaming", "打游戏", kind="fact", evidence="stated"),
                  op("use_case.resolution", "set", "1080p", "1080p", evidence="stated"),
                  op("existing_parts", "set", [], "全新买", evidence="stated")],
        "seed_user_message": "打游戏用，1080p，配件全新买",
        "user_message": "预算不要超过7500",
        "expected": {
            "expected_operations": [
                {"op": "set", "field": "budget_cny", "value": 7500, "quote_contains": "7500"},
                {"op": "set", "field": "budget_flex", "value": 0, "quote_contains": "不要超过"}],
            "expected_state": {"budget_cny": {"status": "active", "value": 7500}},
            "expected_confirmation_eligible": True,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-monitor-request", "显示器请求识别为不支持能力", "development", "ex-dev-9",
               "用途未明不猜测；显示器保留为 unresolved 观察，回复不得承诺覆盖。"),
        "seed": [], "seed_user_message": "", "user_message": "帮我配台电脑，要带显示器",
        "expected": {
            "expected_operations": [],
            "expected_observations": [{"text_contains": "显示器"}],
            "forbidden_operations": [{"op": "set", "field": "use_case.type"}],
            "expected_missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
            "expected_confirmation_eligible": False,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": True, "ambiguous": False},
            "forbidden_vetoes": ["V9"]},
    },
    {
        **head("ex-quiet-only", "只表达静音偏好", "calibration", "ex-cal-2",
               "软偏好可记录；其余保持 unknown，不把“都行”写成 any。"),
        "seed": [], "seed_user_message": "", "user_message": "别的都行，就是要安静",
        "expected": {
            "expected_operations": [{"op": "set", "field": "noise_pref", "value": "silent", "quote_contains": "安静"}],
            "forbidden_operations": [{"op": "set", "field": "size_pref"}],
            "expected_missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-multi-field-edit", "同轮多字段更新", "calibration", "ex-cal-3",
               "预算修改与尺寸偏好并列；每个操作独立证据。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG,
        "user_message": "预算改成9000吧，机箱尽量小一点",
        "expected": {
            "expected_operations": [
                {"op": "set", "field": "budget_cny", "value": 9000, "quote_contains": "9000"},
                {"op": "set", "field": "size_pref", "value": "itx", "quote_contains": "小"}],
            "expected_state": {"budget_cny": {"status": "active", "value": 9000}, "size_pref": {"status": "active", "value": "itx"}},
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-office-general", "家用办公用途", "development", "ex-dev-10",
               "“家里办公上网用”可记 general fact；预算与已有件继续追问。"),
        "seed": [], "seed_user_message": "", "user_message": "家里办公上网用",
        "expected": {
            "expected_operations": [{"op": "set", "field": "use_case.type", "value": "general", "quote_contains": "办公"}],
            "expected_missing_fields": ["budget_cny", "existing_parts"],
            "expected_confirmation_eligible": False,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-fps-target-numeric", "明确帧数记 fps_target", "development", "ex-dev-11",
               "“至少144帧”是明确数值目标，映射可选字段 use_case.fps_target，不改 performance_goal。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG,
        "user_message": "帧数至少144",
        "expected": {
            "expected_operations": [{"op": "set", "field": "use_case.fps_target", "value": 144, "quote_contains": "144"}],
            "expected_state": {"use_case.fps_target": {"status": "active", "value": 144}},
            "expected_missing_fields": [], "expected_confirmation_eligible": True,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-two-game-titles", "两个游戏名记 titles", "development", "ex-dev-12",
               "“玩DOTA2和LOL”给出用途与 titles，按原文记录不翻译。"),
        "seed": [], "seed_user_message": "", "user_message": "玩DOTA2和LOL",
        "expected": {
            "expected_operations": [
                {"op": "set", "field": "use_case.type", "value": "gaming", "quote_contains": "玩"},
                {"op": "set", "field": "use_case.titles", "value": ["DOTA2", "LOL"], "quote_contains": "DOTA2"}],
            "expected_missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"],
            "expected_confirmation_eligible": False,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-budget-raise", "上调预算", "development", "ex-dev-13",
               "“提到9000吧”是对既有预算的更正 set。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG,
        "user_message": "预算提到9000吧",
        "expected": {
            "expected_operations": [{"op": "set", "field": "budget_cny", "value": 9000, "quote_contains": "9000"}],
            "expected_state": {"budget_cny": {"status": "active", "value": 9000}},
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-existing-add-ssd", "追加已有固态", "development", "ex-dev-14",
               "在已有显卡基础上追加 ssd：提交合并后的品类数组。"),
        "seed": [op("existing_parts", "set", ["gpu"], "有张显卡", evidence="stated")],
        "seed_user_message": "有张显卡能用",
        "user_message": "还有块旧的固态也能用",
        "expected": {
            "expected_operations": [{"op": "set", "field": "existing_parts", "value": ["gpu", "ssd"], "quote_contains": "固态"}],
            "expected_state": {"existing_parts": {"status": "active", "value": ["gpu", "ssd"]}},
            "forbidden_operations": [{"op": "set", "field": "owned_parts"}],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-noise-normal", "噪音正常就行", "calibration", "ex-cal-4",
               "“正常就行”是明确 normal，不是 any 也不是 unknown。"),
        "seed": [], "seed_user_message": "", "user_message": "声音无所谓，正常就行",
        "expected": {
            "expected_operations": [{"op": "set", "field": "noise_pref", "value": "normal", "quote_contains": "正常"}],
            "expected_missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-recipient-gaming", "装机对象与用途同轮", "development", "ex-dev-15",
               "“给弟弟打游戏”一次给出 recipient 与 use_case.type 两个事实。"),
        "seed": [], "seed_user_message": "", "user_message": "是给我弟弟装的，他打游戏用",
        "expected": {
            "expected_operations": [
                {"op": "set", "field": "recipient", "value": "弟弟", "quote_contains": "弟弟"},
                {"op": "set", "field": "use_case.type", "value": "gaming", "quote_contains": "打游戏"}],
            "expected_missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-priority-gpu", "预算紧张优先显卡", "calibration", "ex-cal-5",
               "“优先显卡”映射 priority=[gpu]，只允许八大品类。"),
        "seed": [], "seed_user_message": "", "user_message": "预算紧的话优先把显卡配好",
        "expected": {
            "expected_operations": [{"op": "set", "field": "priority", "value": ["gpu"], "quote_contains": "显卡"}],
            "expected_missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-budget-flex-explicit", "明确上浮比例", "calibration", "ex-cal-6",
               "“最多上浮5%”是用户明确的 budget_flex，覆盖默认 0.1。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG,
        "user_message": "预算最多上浮5%",
        "expected": {
            "expected_operations": [{"op": "set", "field": "budget_flex", "value": 0.05, "quote_contains": "5%"}],
            "expected_state": {"budget_flex": {"status": "active", "value": 0.05}},
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-restore-temporary-budget", "恢复临时覆盖的预算", "development", "ex-dev-16",
               "临时按 9000 讨论后“还是按原来的7500来”是 restore，不是新 set。"),
        "seed": [op("budget_cny", "set", 7500, "预算7500", evidence="stated"),
                  op("budget_cny", "set", 9000, "先按9000", scope="temporary", evidence="stated")],
        "seed_user_message": "预算7500，这轮先按9000试",
        "user_message": "还是按原来的7500来",
        "expected": {
            "expected_operations": [{"op": "restore", "field": "budget_cny", "quote_contains": "原来的"}],
            "expected_state": {"budget_cny": {"status": "active", "value": 7500}},
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-budget-correction-same-turn", "同轮自我更正取后值", "calibration", "ex-cal-7",
               "“6000吧，哦不对，7000”是明确更正：以 7000 为当前预算 set，不记 conflict。"),
        "seed": [], "seed_user_message": "", "user_message": "预算6000吧，哦不对，7000",
        "expected": {
            "expected_operations": [{"op": "set", "field": "budget_cny", "value": 7000, "evidence": "stated", "quote_contains": "7000"}],
            "expected_state": {"budget_cny": {"status": "active", "value": 7000}},
            "expected_missing_fields": ["use_case.type", "existing_parts"],
            "expected_confirmation_eligible": False,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-budget-conflict-family", "两个出资方预算无法取舍记 conflict", "calibration", "ex-cal-8",
               "父母各自给出明确且互斥的预算约束，不能替用户取舍：记 conflict 并交确认页，不得择一写入。"),
        "seed": [], "seed_user_message": "", "user_message": "我爸说预算8000，我妈说不能超过6000",
        "expected": {
            "expected_operations": [{"op": "conflict", "field": "budget_cny", "quote_contains": "6000"}],
            "forbidden_operations": [{"op": "set", "field": "budget_cny"}],
            "expected_state": {"budget_cny": {"status": "conflict"}},
            "expected_missing_fields": ["use_case.type", "existing_parts"],
            "expected_confirmation_eligible": False,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-web-video-general", "上网看视频是 general", "development", "ex-dev-17",
               "轻度娱乐无生产力任务，记 general 而不是 productivity。"),
        "seed": [], "seed_user_message": "", "user_message": "就上上网看看视频",
        "expected": {
            "expected_operations": [{"op": "set", "field": "use_case.type", "value": "general", "quote_contains": "上网"}],
            "expected_missing_fields": ["budget_cny", "existing_parts"],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-appearance-white", "外观偏好", "development", "ex-dev-18",
               "“白色机箱”是外观软偏好，原话记录不改写。"),
        "seed": [], "seed_user_message": "", "user_message": "机箱最好是白色的",
        "expected": {
            "expected_operations": [{"op": "set", "field": "appearance", "value": "白色", "quote_contains": "白色"}],
            "expected_state": {"appearance": {"status": "active", "value": "白色"}},
            "expected_missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-holdout-budget-approx", "“3000左右”按3000记录", "holdout", "ex-hold-1",
               "用户给出了具体金额 3000，“左右”的近似含义由默认 budget_flex 表达，预算不保持 unknown。"),
        "seed": [], "seed_user_message": "", "user_message": "主要做表格和上网，预算3000左右，主机全新",
        "expected": {
            "expected_operations": [
                {"op": "set", "field": "use_case.type", "value": "productivity", "quote_contains": "表格"},
                {"op": "set", "field": "use_case.titles", "value": ["Office 表格", "上网浏览"], "quote_contains": "表格"},
                {"op": "set", "field": "existing_parts", "value": [], "quote_contains": "全新"},
                {"op": "set", "field": "budget_cny", "value": 3000, "evidence": "stated", "quote_contains": "3000"}],
            "expected_state": {"budget_cny": {"status": "active", "value": 3000}},
            "expected_missing_fields": [], "expected_confirmation_eligible": True,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
    {
        **head("ex-holdout-remove-budget", "明确撤销预算", "holdout", "ex-hold-2",
               "“不算了”是 remove，不是忽略。"),
        "seed": SEED_FULL, "seed_user_message": SEED_FULL_MSG,
        "user_message": "预算不算了，先不限定",
        "expected": {
            "expected_operations": [{"op": "remove", "field": "budget_cny", "quote_contains": "不算"}],
            "expected_state": {"budget_cny": {"status": "removed"}},
            "expected_missing_fields": ["budget_cny"],
            "expected_confirmation_eligible": False,
            "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": False, "ambiguous": False}},
    },
]

conversations = [
    {
        **head("cv-fps-progressive", "渐进式 FPS 装机对话", "development", "cv-dev-1",
               "人工模拟的 FPS/7500/全部新买流程；每轮只透露一部分，最终 ready 但聊天不得启动 Builder。"),
        "turns": [
            {"text": "主要玩CS2和无畏契约，希望帧率高一点",
             "expect": {"expected_operations": [
                 {"op": "set", "field": "use_case.type", "value": "gaming", "quote_contains": "CS2"},
                 {"op": "set", "field": "use_case.titles", "value": ["CS2", "无畏契约"], "quote_contains": "无畏契约"}],
                 "expected_missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"],
                 "expected_confirmation_eligible": False}},
            {"text": "7500够吗？",
             "expect": {"expected_operations": [], "forbidden_operations": [{"op": "set", "field": "budget_cny"}],
                         "expected_turn_signals": {"asks_question": True, "requests_review": False, "requests_build": False, "ambiguous": False},
                         "expected_missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"]}},
            {"text": "那就7500吧",
             "expect": {"expected_operations": [{"op": "set", "field": "budget_cny", "value": 7500, "quote_contains": "7500"}],
                         "expected_state": {"budget_cny": {"status": "active", "value": 7500}},
                         "expected_missing_fields": ["existing_parts", "use_case.resolution"]}},
            {"text": "1080p就行",
             "expect": {"expected_operations": [{"op": "set", "field": "use_case.resolution", "value": "1080p", "quote_contains": "1080p"}],
                         "forbidden_questions": ["budget_cny"],
                         "expected_missing_fields": ["existing_parts"]}},
            {"text": "配件全新买",
             "expect": {"expected_operations": [{"op": "set", "field": "existing_parts", "value": [], "quote_contains": "新买"}],
                         "forbidden_questions": ["budget_cny", "use_case.resolution"],
                         "expected_missing_fields": [], "expected_confirmation_eligible": True}},
        ],
        "final": {"expected_state": {"budget_cny": {"status": "active", "value": 7500},
                                      "use_case.type": {"status": "active", "value": "gaming"},
                                      "use_case.resolution": {"status": "active", "value": "1080p"},
                                      "existing_parts": {"status": "active", "value": []}},
                   "expected_missing_fields": [], "expected_confirmation_eligible": True,
                   "builder_must_run": False, "forbidden_vetoes": ["V4"]},
    },
    {
        **head("cv-premature-ready", "首句模糊需求不得过早 ready", "development", "cv-dev-2",
               "“配台打游戏的电脑”缺预算/已有件/分辨率；模型不得用 next_action 宣称完整。"),
        "turns": [
            {"text": "配台打游戏的电脑",
             "expect": {"expected_operations": [{"op": "set", "field": "use_case.type", "value": "gaming", "quote_contains": "打游戏"}],
                         "expected_missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"],
                         "expected_confirmation_eligible": False,
                         "expected_next_action_absent": True}},
        ],
        "final": {"expected_missing_fields": ["budget_cny", "existing_parts", "use_case.resolution"],
                   "expected_confirmation_eligible": False, "builder_must_run": False},
    },
    {
        **head("cv-composite-9000-start", "复合意图后仍需核定", "development", "cv-dev-3",
               "首句即齐备；第二轮“预算改9000然后开始”保存字段但只请求核定。"),
        "turns": [
            {"text": SEED_FULL_MSG,
             "expect": {"expected_missing_fields": [], "expected_confirmation_eligible": True,
                         "expected_next_action_absent": True}},
            {"text": "预算改9000，然后开始吧",
             "expect": {"expected_operations": [{"op": "set", "field": "budget_cny", "value": 9000, "quote_contains": "9000"}],
                         "expected_turn_signals": {"asks_question": False, "requests_review": False, "requests_build": True, "ambiguous": False},
                         "expected_state": {"budget_cny": {"status": "active", "value": 9000}},
                         "expected_missing_fields": [], "expected_confirmation_eligible": True}},
        ],
        "final": {"expected_state": {"budget_cny": {"status": "active", "value": 9000}},
                   "expected_missing_fields": [], "expected_confirmation_eligible": True,
                   "builder_must_run": False, "forbidden_vetoes": ["V4"]},
    },
    {
        **head("cv-owned-model-flow", "已有件型号补全流程", "development", "cv-dev-4",
               "品类→型号→口径逐轮补全；型号未知期间不阻塞其他字段收集。"),
        "turns": [
            {"text": "有张旧显卡",
             "expect": {"expected_operations": [{"op": "set", "field": "existing_parts", "value": ["gpu"], "quote_contains": "显卡"}],
                         "expected_missing_fields": ["use_case.type", "budget_cny", "owned_parts.gpu.model", "budget_basis"],
                         "expected_confirmation_eligible": False}},
            {"text": "打游戏用，2K",
             "expect": {"expected_operations": [
                 {"op": "set", "field": "use_case.type", "value": "gaming", "quote_contains": "打游戏"},
                 {"op": "set", "field": "use_case.resolution", "value": "2K", "quote_contains": "2K"}],
                 "forbidden_questions": ["existing_parts"],
                 "expected_missing_fields": ["budget_cny", "owned_parts.gpu.model", "budget_basis"]}},
            {"text": "显卡是4070 Super",
             "expect": {"expected_operations": [{"op": "set", "field": "owned_parts", "value": [{"category": "gpu", "model": "4070 Super"}], "quote_contains": "4070"}],
                         "expected_missing_fields": ["budget_cny", "budget_basis"]}},
            {"text": "预算6000，按整机总价算",
             "expect": {"expected_operations": [
                 {"op": "set", "field": "budget_cny", "value": 6000, "quote_contains": "6000"},
                 {"op": "set", "field": "budget_basis", "value": "full_build", "quote_contains": "整机"}],
                 "expected_missing_fields": [], "expected_confirmation_eligible": True}},
        ],
        "final": {"expected_state": {"existing_parts": {"status": "active", "value": ["gpu"]},
                                      "owned_parts": {"status": "active", "value": [{"category": "gpu", "model": "4070 Super"}]},
                                      "budget_basis": {"status": "active", "value": "full_build"}},
                   "expected_missing_fields": [], "expected_confirmation_eligible": True, "builder_must_run": False},
    },
    {
        **head("cv-monitor-refusal", "显示器键鼠请求不承诺", "development", "cv-dev-5",
               "tower scope 外请求记录观察并如实说明不支持，不承诺、不静默。"),
        "turns": [
            {"text": "帮我配台电脑，还要显示器和键鼠",
             "expect": {"expected_observations": [{"text_contains": "显示器"}],
                         "forbidden_operations": [{"op": "set", "field": "use_case.type"}],
                         "expected_missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
                         "expected_confirmation_eligible": False}},
        ],
        "final": {"expected_missing_fields": ["use_case.type", "budget_cny", "existing_parts"],
                   "expected_confirmation_eligible": False, "builder_must_run": False, "forbidden_vetoes": ["V9"]},
    },
    {
        **head("cv-office-progressive", "家用办公渐进对话", "development", "cv-dev-6",
               "老人上网场景：general 用途两轮到 ready，聊天不启动 Builder。"),
        "turns": [
            {"text": "家里老人上网用",
             "expect": {"expected_operations": [{"op": "set", "field": "use_case.type", "value": "general", "quote_contains": "上网"}],
                         "expected_missing_fields": ["budget_cny", "existing_parts"],
                         "expected_confirmation_eligible": False}},
            {"text": "预算3000，全新买",
             "expect": {"expected_operations": [
                 {"op": "set", "field": "budget_cny", "value": 3000, "quote_contains": "3000"},
                 {"op": "set", "field": "existing_parts", "value": [], "quote_contains": "全新买"}],
                 "forbidden_questions": ["use_case.type"],
                 "expected_missing_fields": [], "expected_confirmation_eligible": True}},
        ],
        "final": {"expected_state": {"use_case.type": {"status": "active", "value": "general"},
                                      "budget_cny": {"status": "active", "value": 3000}},
                   "expected_missing_fields": [], "expected_confirmation_eligible": True,
                   "builder_must_run": False, "forbidden_vetoes": ["V4"]},
    },
    {
        **head("cv-budget-correction", "确认前更正预算", "development", "cv-dev-7",
               "首轮即齐备被压回确认；第二轮只更正预算，仍不启动 Builder。"),
        "turns": [
            {"text": SEED_FULL_MSG,
             "expect": {"expected_missing_fields": [], "expected_confirmation_eligible": True,
                         "expected_next_action_absent": True}},
            {"text": "预算改成6000吧",
             "expect": {"expected_operations": [{"op": "set", "field": "budget_cny", "value": 6000, "quote_contains": "6000"}],
                         "forbidden_questions": ["budget_cny", "use_case.type"],
                         "expected_state": {"budget_cny": {"status": "active", "value": 6000}},
                         "expected_missing_fields": [], "expected_confirmation_eligible": True}},
        ],
        "final": {"expected_state": {"budget_cny": {"status": "active", "value": 6000}},
                   "expected_missing_fields": [], "expected_confirmation_eligible": True,
                   "builder_must_run": False, "forbidden_vetoes": ["V4"]},
    },
    {
        **head("cv-holdout-productivity", "生产力渐进对话", "holdout", "cv-hold-1",
               "Premiere 流程：用途软件→预算→全新买，两轮到 ready。"),
        "turns": [
            {"text": "主要用Premiere剪视频",
             "expect": {"expected_operations": [
                 {"op": "set", "field": "use_case.type", "value": "productivity", "quote_contains": "Premiere"},
                 {"op": "set", "field": "use_case.titles", "value": ["Premiere 视频剪辑"], "quote_contains": "Premiere"}],
                 "expected_missing_fields": ["budget_cny", "existing_parts"], "expected_confirmation_eligible": False}},
            {"text": "预算8000，全新买",
             "expect": {"expected_operations": [
                 {"op": "set", "field": "budget_cny", "value": 8000, "quote_contains": "8000"},
                 {"op": "set", "field": "existing_parts", "value": [], "quote_contains": "全新买"}],
                 "forbidden_questions": ["use_case.type"],
                 "expected_missing_fields": [], "expected_confirmation_eligible": True}},
        ],
        "final": {"expected_state": {"use_case.type": {"status": "active", "value": "productivity"},
                                      "budget_cny": {"status": "active", "value": 8000}},
                   "expected_missing_fields": [], "expected_confirmation_eligible": True, "builder_must_run": False},
    },
]

FRESH_TURN_STATE = {"schema_version": 1, "revision": 1, "fields": {}, "alternatives": [], "observations": []}
READY_READINESS = {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True, "effective_defaults": []}
BUDGET_STATE = lambda src: {"schema_version": 1, "revision": 1,
                             "fields": {"budget_cny": {"status": "active", "value": 7500, "source": src}},
                             "alternatives": [], "observations": []}

selftest = {"entries": [
    {"id": "st-v1-active-without-evidence", "layer": "turn", "veto": "V1", "expect": "fail",
     "description": "字段写成 active 但没有用户证据",
     "gold": {"expected_operations": [], "forbidden_operations": [{"op": "set", "field": "budget_cny"}]},
     "actual": {"state": BUDGET_STATE(None), "readiness": {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True}}},
    {"id": "st-v2-assistant-suggestion-as-requirement", "layer": "turn", "veto": "V2", "expect": "fail",
     "description": "助手提案值未经接受直接成为需求",
     "gold": {"expected_operations": [{"op": "set", "field": "budget_cny", "value": 7500, "evidence": "accepted_proposal", "quote_contains": "可以"}]},
     "actual": {"state": BUDGET_STATE({"kind": "assistant", "message_id": "m-a", "quote": "按7500元的预算继续可以吗？"}),
                 "readiness": {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True}}},
    {"id": "st-v3-eligible-while-missing", "layer": "readiness", "veto": "V3", "expect": "fail",
     "description": "必填缺失却判定可确认",
     "gold": {"status": "incomplete", "missing_fields": ["budget_cny"], "blocking_conflicts": [], "confirmation_eligible": False},
     "actual": {"status": "ready", "missing_fields": ["budget_cny"], "blocking_conflicts": [], "confirmation_eligible": True}},
    {"id": "st-v4-builder-without-snapshot", "layer": "turn", "veto": "V4", "expect": "fail",
     "description": "没有确认快照就启动 Builder",
     "gold": {"expected_operations": []},
     "actual": {"state": FRESH_TURN_STATE, "builder_started": True}},
    {"id": "st-v5-hash-mismatch", "layer": "turn", "veto": "V5", "expect": "fail",
     "description": "Builder 输入 hash 与核定快照不一致",
     "gold": {"expected_operations": []},
     "actual": {"state": FRESH_TURN_STATE, "builder_started": True,
                 "confirmation_snapshot_hash": "aaaaaaaa", "builder_input_hash": "bbbbbbbb"}},
    {"id": "st-v6-second-builder", "layer": "turn", "veto": "V6", "expect": "fail",
     "description": "已有 Builder 运行时启动第二个",
     "gold": {"expected_operations": []},
     "actual": {"state": FRESH_TURN_STATE, "builders_active": 2}},
    {"id": "st-v7-stale-edit-accepted", "layer": "turn", "veto": "V7", "expect": "fail",
     "description": "过期 revision 的编辑覆盖新状态",
     "gold": {"expected_operations": []},
     "actual": {"state": FRESH_TURN_STATE, "stale_edit_accepted": True}},
    {"id": "st-v8-claim-without-run", "layer": "turn", "veto": "V8", "expect": "fail",
     "description": "回复宣称已开始生成但没有合法 run",
     "gold": {"expected_operations": []},
     "actual": {"state": FRESH_TURN_STATE, "reply": "好的，已开始生成配置。"}},
    {"id": "st-v9-peripheral-promise", "layer": "turn", "veto": "V9", "expect": "fail",
     "description": "tower scope 内承诺显示器",
     "gold": {"expected_operations": []},
     "actual": {"state": FRESH_TURN_STATE, "reply": "显示器和键鼠一起给你配好。", "watch_unsupported_scope": True}},
    {"id": "st-pass-clean-set", "layer": "turn", "expect": "pass",
     "description": "正确路径样例：带证据的预算设置",
     "gold": {"expected_operations": [{"op": "set", "field": "budget_cny", "value": 7500, "evidence": "stated"}],
               "expected_state": {"budget_cny": {"status": "active", "value": 7500}}},
     "actual": {"operations": [{"op": "set", "field": "budget_cny", "value": 7500, "evidence": "stated", "quote": "预算7500"}],
                 "state": BUDGET_STATE({"kind": "chat", "message_id": "m1", "quote": "预算7500"})}},
    {"id": "st-pass-empty-turn", "layer": "turn", "expect": "pass",
     "description": "正确路径样例：无操作轮",
     "gold": {"expected_operations": []},
     "actual": {"operations": [], "state": FRESH_TURN_STATE}},
    {"id": "st-pass-question-signal", "layer": "turn", "expect": "pass",
     "description": "正确路径样例：问题信号",
     "gold": {"expected_operations": [], "expected_turn_signals": {"asks_question": True, "requests_review": False, "requests_build": False, "ambiguous": False}},
     "actual": {"operations": [], "state": FRESH_TURN_STATE,
                 "turn_signals": {"asks_question": True, "requests_review": False, "requests_build": False, "ambiguous": False},
                 "signals_known": True}},
    {"id": "st-pass-readiness-ready", "layer": "readiness", "expect": "pass",
     "description": "正确路径样例：齐备可确认",
     "gold": {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True},
     "actual": {"status": "ready", "missing_fields": [], "blocking_conflicts": [], "confirmation_eligible": True}},
    {"id": "st-pass-readiness-incomplete", "layer": "readiness", "expect": "pass",
     "description": "正确路径样例：缺预算不可确认",
     "gold": {"status": "incomplete", "missing_fields": ["budget_cny"], "blocking_conflicts": [], "confirmation_eligible": False},
     "actual": {"status": "incomplete", "missing_fields": ["budget_cny"], "blocking_conflicts": [], "confirmation_eligible": False}},
]}

gates = {
    "version": "reqv2-gates-v1",
    "frozen_at": "2026-09-22",
    "vetoes": [
        {"id": "V1", "rule": "没有用户证据或已验证提案，却把字段写成 active。"},
        {"id": "V2", "rule": "助手建议未经用户明确接受就成为用户需求。"},
        {"id": "V3", "rule": "必填字段缺失或存在阻塞冲突，却判定 confirmation_eligible=true。"},
        {"id": "V4", "rule": "没有当前有效确认快照却启动 Builder。"},
        {"id": "V5", "rule": "Builder 输入 hash 与核定页面展示的 snapshot hash 不一致。"},
        {"id": "V6", "rule": "已有 Builder 运行时启动第二个 Builder。"},
        {"id": "V7", "rule": "过期 revision 的编辑覆盖了较新的需求状态。"},
        {"id": "V8", "rule": "产品回复宣称已开始生成，但没有对应的合法 build run。"},
        {"id": "V9", "rule": "当前 scope 仅支持 tower，却生成或承诺显示器、键盘、鼠标配置。"},
    ],
    "deterministic_layers": {"names": ["reducer", "readiness", "policy", "ui-contract"], "required_pass_rate": 1.0},
    "repetition": {"release_repeats": 3, "metric": "pass^k", "best_of_k_as_gate": False},
    "minimum_paired_samples": 30,
    "model_quality_thresholds": {
        "status": "frozen",
        "frozen_at": "2026-09-22",
        "key_fields": ["budget_cny", "use_case.type", "use_case.resolution", "existing_parts", "owned_parts", "budget_basis"],
        "key_field_wrong_write_total_max": 0,
        "extraction": {
            "operation_precision_min": 0.95,
            "operation_recall_min": 0.8,
            "forbidden_op_total_max": 0,
            "turn_signal_exact_match_min": 0.9,
            "min_signal_turns": 20,
            "case_success_min": 0.9,
            "final_state_exact_match_min": 0.9,
            "provider_success_min": 0.98,
            "latency_p95_max_ms": 10000,
            "max_model_calls_per_turn": 2,
            "min_cases": 20},
        "conversations": {
            "task_success_min": 0.8,
            "repeated_question_max_per_case": 0,
            "forbidden_veto_total_max": 0,
            "min_cases": 5},
        "notes": "case_success 按 case Pass^3 折叠统计；provider_success、latency_p95、max_model_calls_per_turn 与预算一致性统计 extraction+conversations 的全部真实 provider 轮；latency_p95_max_ms=10000 对齐 PRD 追问响应 ≤10s；max_model_calls_per_turn=2 含 guard 纠偏重调，整轮总调用不得超过 plan.json 的显式预算；数据不足或指标缺失判 UNEVALUABLE 而非默认通过。precision/recall 按 operation 池化（全部轮次的操作合并计算），不是按 case 数描述的允许失败例数；错误写入比信息遗漏更不可接受——漏记进入 recall 且可由追问补救，错写会污染需求真值并触发 veto，因此 precision(0.95) 高于 recall(0.80)。关键字段（预算/用途/gaming 分辨率/已有件/已有件型号/预算口径）的错值或额外写入零容忍；完全漏记只计入 recall，不按错误写入处理。重复追问每例 0 次。forbidden op 与 veto 零容忍。此后不得因结果下调。"},
    "limitations": [
        "V8/V9 的产品侧检测基于中文关键词启发式，命中需人工复核原文；grader 金丝雀保证可检测。",
        "当前基线不运行 holdout；holdout 只评最终锁定候选。",
    ],
}

provenance = {
    "dataset": "requirement-v2",
    "created_at": "2026-09-22",
    "grader_version": "reqv2-grader-v1",
    "labeling": {
        "method": "AI 辅助起草（Clapears agent, 2026-09-22），依据 requirement-v2 系列五份 change spec 的契约条款逐条标注。",
        "human_review": {
            "status": "accepted",
            "reviewed_at": "2026-09-22",
            "sheet": "docs/eval/requirement-v2/人工复核-20260922.md",
            "result": "16/16 条金标全部接受（9 holdout + cv-fps-progressive + ex-monitor-request + accepted-proposal 边界 + ex-budget-correction-same-turn/ex-budget-conflict-family/ex-fps-target-numeric）；holdout 未运行。",
        },
        "sources": [
            "docs/changes/requirement-v2-evaluation-foundation.md（评估契约与 vetoes）",
            "docs/changes/requirement-state-readiness-v2.md（最低矩阵、系统默认、readiness/reducer 语义）",
            "docs/changes/screening-requirement-collection-v2.md（操作语义表、turn signals、accepted proposal）",
            "docs/changes/requirement-confirmation-builder-gate-v2.md（三轴状态、policy、快照）",
            "docs/changes/requirement-workspace-sidebar-v2.md（ui-contract 展示真值）",
        ],
        "revision": "2026-09-22 v5（reqv2-grader-v2）：provider_success/latency_p95/max_model_calls_per_turn/预算一致性改为统计 extraction+conversations 全部真实 provider 轮（verdict layer=model），样本范围与 report.usage 一致；此前 v4：补齐 case_success/final_state/provider_success/latency_p95/max_calls_per_turn/预算一致性门槛，replay 恢复真实 repeats；此前 v3：gate verdict 结构化、pass^k 折叠修正、关键字段零容忍与 min_signal_turns 冻结、同轮更正取后值并新增家庭预算冲突例；此前 v2：按人工验收意见修正——“预算3000左右”改为记录 budget_cny=3000（近似由默认 budget_flex 表达）；rd-reject-inferred 原话不再包含金额，仅剩推断路径；新增 rdy-gaming-any-resolution-not-satisfied（any 是 active 值但不满足 gaming 最低矩阵）；gates 模型质量阈值冻结为具体数值。",
        "known_boundaries": [
            "ex-monitor-request 的“不猜用途”、ex-budget-correction-same-turn 的“同轮自我更正取后值 set”与 ex-budget-conflict-family 的“互斥明确约束记 conflict”是新增边界，已随 2026-09-22 人工复核（16/16 接受）确认。",
            "conversations 用例为确定性用户脚本；LLM 用户模拟器生成的探索性候选未纳入。",
            "catalog.json 取自 current-178-20260917 冻结目录每品类首件（8 件），仅满足隔离数据库外键，不代表评估选件范围。",
        ],
        "review_sheet": "docs/eval/requirement-v2/人工复核-20260922.md（holdout 全量与关键边界，供产品方逐项确认；holdout 未运行）",
    },
    "splits": {
        "rule": "按 case 的 session 字段划分；同一 session 与同一 variants_of 模板不得跨 split。今天人工模拟的 FPS/7500/全部新买对话（cv-fps-progressive）按 spec 归入 development。",
        "development": "prompt 作者可反复查看与运行。",
        "calibration": "只用于候选/阈值/prompt 选择。",
        "holdout": "会话级锁定，最终候选确定后才运行；本基线未运行。",
    },
    "current_product_baseline": {
        "note": "基线 red 是预期证据：当前 v1 语义预期在过早 ready、next_action 权威、purchase declaration、blocking 分离、系统默认、turn signals、presentation action、确认 payload 上失败。",
        "run": "artifacts/reqv2/（见 docs/eval/requirement-v2/README.md 的基线登记；冻结门槛前的旧产物以 superseded- 前缀归档，不作为冻结基线）",
    },
}


def write(name, payload):
    path = os.path.join(ROOT, name)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    # 仓库约定 eol=lf；Windows 文本模式默认写 CRLF，显式锁定 LF。
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        json.dump(payload, f, ensure_ascii=False, indent=1)
        f.write("\n")
    print("wrote", name)


write("reducer/cases.json", reducer)
write("readiness/cases.json", readiness)
write("policy/cases.json", policy)
write("ui-contract/cases.json", ui_contract)
write("extraction/cases.json", extraction)
write("conversations/cases.json", conversations)
write("selftest.json", selftest)
write("gates.json", gates)
write("provenance.json", provenance)
print("done")
