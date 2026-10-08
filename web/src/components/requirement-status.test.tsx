import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Session } from "@/lib/api/types";
import { makeRequirementState, makeSession } from "@/test/session-fixture";
import { RequirementStatusPane, RequirementSummary } from "./requirement-status";

const source = { kind: "chat" as const, message_id: "message-1", quote: "预算 8000，尽量安静，帮朋友装机" };
const state = makeRequirementState({
  budget_cny: { status: "active", value: 8000, strength: "must", scope: "session", source },
  noise_pref: { status: "active", value: "silent", strength: "prefer", scope: "session", source },
  recipient: { status: "active", value: "friend", strength: "must", scope: "session", source },
  "brand_pref.gpu": { status: "removed", source },
  "use_case.type": { status: "active", value: "gaming", strength: "must", scope: "session", source },
}, {
  revision: 2,
  alternatives: [{ field: "use_case.resolution", value: "4K", strength: "prefer", scope: "session", source: { ...source, quote: "如果上 4K 会怎样，先不改" } }],
  changes: [{ revision: 2, op: "remove", field: "brand_pref.gpu", source }],
  history: [{ revision: 2, op: "remove", field: "brand_pref.gpu", source }],
});

function paneSession(overrides: Partial<Session>) {
  return makeSession({ requirement_state: state, ...overrides });
}

const baseProps = { busy: false, onUpdate: vi.fn().mockResolvedValue(undefined), onSource: vi.fn(), onOpenReview: vi.fn(), onOpenEditor: vi.fn() };

function readySession(overrides: Partial<Session> = {}): Session {
  return paneSession({
    requirement_readiness: { status: "ready", missing_fields: [], blocking_conflicts: [], unsupported_capabilities: [], next_question: null, confirmation_eligible: true, effective_defaults: [{ field: "size_pref", value: "any", origin: "system_default" }] },
    review_hash: "hash-1", requirement_confirmation: { status: "unconfirmed", confirmed_revision: null, confirmed_at: null, confirmed_review_hash: null, review_diff: null },
    ...overrides,
  });
}

describe("RequirementStatusPane 字段状态", () => {
  it("preserves recipient background when explicitly editing it", async () => {
    const update = vi.fn().mockResolvedValue(true);
    render(<RequirementStatusPane {...baseProps} onUpdate={update} session={paneSession({ requirement_state: makeRequirementState({ recipient: { status: "active", value: "朋友", kind: "context", strength: "must", scope: "session", source } }) })} />);
    await userEvent.click(screen.getByRole("button", { name: "修改装机对象" }));
    await userEvent.click(screen.getByRole("button", { name: "保存需求" }));
    expect(update).toHaveBeenCalledWith([{ op: "set", field: "recipient", value: "朋友", kind: "context", strength: "must", scope: "session" }]);
  });

  it("explicitly corrects a legacy background value when editing the budget", async () => {
    const update = vi.fn().mockResolvedValue(true);
    const legacy = makeRequirementState({ budget_cny: { status: "active" as const, value: 3000, kind: "context" as const, strength: "must" as const, scope: "session" as const, source } });
    render(<RequirementStatusPane {...baseProps} onUpdate={update} session={paneSession({ requirement_state: legacy })} />);
    await userEvent.click(screen.getByRole("button", { name: "修改预算" }));
    // Even an unchanged number must replace the old, incompatible context kind.
    await userEvent.click(screen.getByRole("button", { name: "保存需求" }));
    expect(update).toHaveBeenCalledWith([{ op: "set", field: "budget_cny", value: 3000, kind: "constraint", strength: "must", scope: "session" }]);
    expect(legacy.fields.budget_cny.kind).toBe("context");
  });

  it("shows required unknown, optional unknown, removed, default and any states honestly", async () => {
    const withAny = makeRequirementState({ ...state.fields, size_pref: { status: "active", value: "any", strength: "prefer", scope: "session", source } }, { revision: 2 });
    const session = readySession({
      requirement_state: withAny,
      requirement_readiness: { status: "ready", missing_fields: ["use_case.resolution"], blocking_conflicts: [], unsupported_capabilities: ["monitor"], next_question: null, confirmation_eligible: false, effective_defaults: [{ field: "brand_pref.cpu", value: "any", origin: "system_default" }] },
    });
    render(<RequirementStatusPane {...baseProps} session={session} />);
    expect(screen.getByText("待填写").closest("[data-field-row]")).toHaveTextContent("分辨率");
    await userEvent.click(screen.getByRole("button", { name: "补充分辨率" }));
    expect(screen.getByLabelText("编辑分辨率")).toBeInTheDocument();
    expect(screen.getByText("已撤销").closest("[data-field-row]")).toHaveTextContent("显卡品牌");
    expect(screen.getAllByText("不限").some((node) => node.closest("[data-field-row]")?.textContent.includes("尺寸"))).toBe(true);
    expect(screen.getAllByText("系统默认").some((node) => node.closest("[data-field-row]")?.textContent.includes("CPU 品牌"))).toBe(true);
    expect(screen.getByText("显示器：当前版本不支持，未纳入配置")).toBeVisible();
  });

  it("renders the budget block from the backend effective projection without recomputing 10%", () => {
    render(<RequirementStatusPane {...baseProps} session={readySession({ effective_budget_ceiling_cny: 8800, review_spec: { schema_version: 2, budget_cny: 8000, budget_flex: 0.1, configuration_scope: ["tower"], use_case: { type: "gaming", titles: [], performance_goal: "balanced" }, size_pref: "any", noise_pref: "any", existing_parts: [], priority: [], notes: "" } })} />);
    const block = screen.getByTestId("budget-block");
    expect(within(block).getByText("¥8,000")).toBeVisible();
    expect(within(block).getByText("最多上浮 10%")).toBeVisible();
    expect(within(block).getByText("最高预算")).toBeVisible();
    expect(within(block).getByText("¥8,800")).toBeVisible();
  });

  it("labels a soft budget preference as an upper reference, not a hard ceiling", () => {
    const soft = makeRequirementState({ budget_cny: { status: "active", value: 8000, strength: "prefer", scope: "session", source } });
    render(<RequirementStatusPane {...baseProps} session={readySession({ requirement_state: soft, effective_budget_ceiling_cny: 8800 })} />);
    expect(screen.getByText("预算参考上沿")).toBeVisible();
    expect(screen.queryByText("最高预算")).not.toBeInTheDocument();
  });

  it("surfaces conflicts outside collapsible sections", () => {
    const conflicted = makeRequirementState({ size_pref: { status: "conflict", value: "itx", strength: "prefer", scope: "session", source } });
    render(<RequirementStatusPane {...baseProps} session={readySession({
      requirement_state: conflicted,
      requirement_readiness: { status: "ready", missing_fields: [], blocking_conflicts: ["size_pref"], unsupported_capabilities: [], next_question: null, confirmation_eligible: false, effective_defaults: [] },
    })} />);
    expect(screen.getByText("需确认")).toBeVisible();
    expect(screen.getByText("原话：预算 8000，尽量安静，帮朋友装机")).toBeVisible();
  });

  it("collects conflicts and missing fields into the top 必须澄清 section without duplicating groups", () => {
    const conflicted = makeRequirementState({
      budget_cny: { status: "active", value: 8000, strength: "must", scope: "session", source },
      "use_case.type": { status: "conflict", value: "gaming", strength: "must", scope: "session", source },
    });
    render(<RequirementStatusPane {...baseProps} session={paneSession({
      requirement_state: conflicted,
      requirement_readiness: { status: "incomplete", missing_fields: ["use_case.resolution"], blocking_conflicts: ["use_case.type"], unsupported_capabilities: [], next_question: null, confirmation_eligible: false, effective_defaults: [] },
    })} />);
    const section = screen.getByLabelText("必须澄清");
    const badges = within(section).getAllByText(/需确认|待填写/);
    // 冲突排在待填写之前;两行只在该分区渲染,分组里不再重复。
    expect(badges.map((node) => node.textContent)).toEqual(["需确认", "待填写"]);
    expect(badges[0].closest("[data-field-row]")).toHaveTextContent("主要用途");
    expect(badges[1].closest("[data-field-row]")).toHaveTextContent("分辨率");
    expect(document.querySelectorAll('[data-field-row="use_case.type"]')).toHaveLength(1);
    expect(document.querySelectorAll('[data-field-row="use_case.resolution"]')).toHaveLength(1);
  });

  it("hides the 必须澄清 section once nothing blocks readiness", () => {
    render(<RequirementStatusPane {...baseProps} session={readySession()} />);
    expect(screen.queryByLabelText("必须澄清")).not.toBeInTheDocument();
  });
});

describe("RequirementStatusPane 主要操作", () => {
  it("disables the primary CTA while incomplete and names the top missing items", () => {
    render(<RequirementStatusPane {...baseProps} session={paneSession({})} />);
    expect(screen.getByRole("button", { name: "核对当前需求" })).toBeDisabled();
    expect(screen.getByText(/还缺：预算、主要用途/)).toBeVisible();
  });

  it("opens the review panel once ready but never auto-confirms", async () => {
    const onOpenReview = vi.fn();
    render(<RequirementStatusPane {...baseProps} onOpenReview={onOpenReview} session={readySession()} />);
    const button = screen.getByRole("button", { name: "核对当前需求" });
    expect(button).toBeEnabled();
    await userEvent.click(button);
    expect(onOpenReview).toHaveBeenCalledOnce();
  });

  it("switches the CTA across confirmed, modified and failed relation states", () => {
    const confirmed = readySession({ requirement_confirmation: { status: "confirmed", confirmed_revision: 1, confirmed_at: "2026-09-09T02:00:00Z", confirmed_review_hash: "hash-1", review_diff: [] }, build_relation: { status: "current", version: 1, snapshot_id: "s1", review_hash: "hash-1", builder_input_hash: "b1", retry_run_id: null } });
    const view = render(<RequirementStatusPane {...baseProps} session={confirmed} />);
    expect(screen.getByText(/需求已确认，当前配置基于这份需求/)).toBeVisible();
    view.rerender(<RequirementStatusPane {...baseProps} session={{ ...confirmed, requirement_confirmation: { status: "modified", confirmed_revision: 1, confirmed_at: "2026-09-09T02:00:00Z", confirmed_review_hash: "hash-1", review_diff: [{ field: "budget_cny", before: 8000, after: 9000 }] } }} />);
    expect(screen.getByRole("button", { name: "重新核定并生成" })).toBeEnabled();
    view.rerender(<RequirementStatusPane {...baseProps} session={{ ...confirmed, build_relation: { status: "failed", version: 1, snapshot_id: "s1", review_hash: "hash-1", builder_input_hash: "b1", retry_run_id: "run-9" } }} />);
    expect(screen.getByRole("button", { name: "按相同需求重新生成" })).toBeEnabled();
  });

  it("allows editing while a build runs but keeps confirm/generate unavailable", async () => {
    const running = readySession({ requirement_confirmation: { status: "confirmed", confirmed_revision: 1, confirmed_at: "2026-09-09T02:00:00Z", confirmed_review_hash: "hash-1", review_diff: [] }, build_relation: { status: "running", version: null, snapshot_id: "s1", review_hash: "hash-1", builder_input_hash: "b1", retry_run_id: null } });
    const onOpenEditor = vi.fn();
    render(<RequirementStatusPane {...baseProps} onOpenEditor={onOpenEditor} session={running} />);
    expect(screen.getByText(/正在生成并校验配置，本次生成基于已确认的需求快照（修订 1）/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "核对当前需求" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "编辑全部需求" }));
    expect(onOpenEditor).toHaveBeenCalledOnce();
    // 行内编辑仍然可用。
    expect(screen.getByRole("button", { name: "修改预算" })).toBeEnabled();
  });
});

describe("RequirementStatusPane 编辑与来源", () => {
  it("sends only the edited field and renders the authoritative server response", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    const props = { ...baseProps, onUpdate: update };
    const session = paneSession({});
    const view = render(<RequirementStatusPane {...props} session={session} />);
    await userEvent.click(screen.getByRole("button", { name: "修改预算" }));
    const input = screen.getByLabelText("编辑预算");
    await userEvent.clear(input); await userEvent.type(input, "6000");
    await userEvent.click(screen.getByRole("button", { name: "保存需求" }));
    expect(update).toHaveBeenCalledWith([{ op: "set", field: "budget_cny", value: 6000, strength: "must", scope: "session" }]);
    // 未收到服务端响应前不猜测结果。
    expect(screen.getByText("¥8,000")).toBeVisible();
    expect(screen.queryByText("¥6,000")).not.toBeInTheDocument();
    view.rerender(<RequirementStatusPane {...props} session={paneSession({ requirement_state: makeRequirementState({ ...state.fields, budget_cny: { ...state.fields.budget_cny, value: 6000 } }, { revision: 3 }) })} />);
    expect(screen.getByText("¥6,000")).toBeVisible();
  });

  it("supports budget source navigation, explicit removal and restoring a temporary exception", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    const showSource = vi.fn();
    const previous = state.fields.noise_pref;
    render(<RequirementStatusPane {...baseProps} onUpdate={update} onSource={showSource} session={paneSession({ requirement_state: makeRequirementState({ budget_cny: state.fields.budget_cny, noise_pref: { ...previous, value: "normal", scope: "temporary", previous } }) })} />);
    expect(screen.getByText(/临时例外/)).toBeVisible();
    // 常规字段行不再提供来源浏览;预算块保留来源跳转。
    expect(screen.queryByRole("button", { name: "查看静音来源与操作" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "查看预算来源与操作" }));
    await userEvent.click(screen.getByRole("button", { name: "查看来源消息" }));
    expect(showSource).toHaveBeenCalledWith("message-1");
    // 撤销收进字段编辑器。
    await userEvent.click(screen.getByRole("button", { name: "修改静音" }));
    await userEvent.click(screen.getByRole("button", { name: "撤销这项要求" }));
    expect(update).toHaveBeenLastCalledWith([{ op: "remove", field: "noise_pref" }]);
    // 恢复由行内图标承担。
    await userEvent.click(screen.getByRole("button", { name: "恢复撤销前的静音" }));
    expect(update).toHaveBeenLastCalledWith([{ op: "restore", field: "noise_pref" }]);
  });

  it("preserves edits after a failed save and reports the conflict honestly", async () => {
    const update = vi.fn().mockRejectedValue(new Error("offline"));
    render(<RequirementStatusPane {...baseProps} onUpdate={update} session={paneSession({})} />);
    await userEvent.click(screen.getByRole("button", { name: "修改静音" }));
    await userEvent.selectOptions(screen.getByLabelText("静音要求强度"), "must");
    await userEvent.click(screen.getByRole("button", { name: "保存需求" }));
    expect(update).toHaveBeenCalledWith([{ op: "set", field: "noise_pref", value: "silent", strength: "must", scope: "session" }]);
    expect(screen.getByLabelText("静音要求强度")).toHaveValue("must");
    expect(screen.getByRole("alert")).toHaveTextContent("当前输入已保留");
  });

  it("keeps alternatives and unresolved quotes separate from effective requirements", async () => {
    render(<RequirementStatusPane {...baseProps} session={paneSession({})} />);
    expect(screen.getByText(/未解决原话/)).toBeVisible();
    expect(screen.getByText(/存在歧义，本次未采用：分辨率：4K/)).toBeVisible();
    expect(screen.getByText(/不会自动记为个人长期偏好/)).toBeVisible();
  });
});

describe("RequirementStatusPane 系统默认的诚实展示", () => {
  // 真实后端形状:所有已知字段都以 unknown 键存在,有效默认来自 readiness。
  const effectiveDefaults: Session["requirement_readiness"] extends null ? never : NonNullable<Session["requirement_readiness"]>["effective_defaults"] = [
    { field: "size_pref", value: "any", origin: "system_default" },
    { field: "noise_pref", value: "any", origin: "system_default" },
    { field: "brand_pref.cpu", value: "any", origin: "system_default" },
    { field: "configuration_scope", value: ["tower"], origin: "system_default" },
  ];

  it("unknown 字段存在有效系统默认时标为系统默认,不误写未指定", () => {
    const withDefaults = makeRequirementState({
      budget_cny: { status: "active", value: 8000, strength: "must", scope: "session", source },
      "use_case.type": { status: "active", value: "gaming", strength: "must", scope: "session", source },
      size_pref: { status: "unknown" },
      noise_pref: { status: "unknown" },
    });
    render(<RequirementStatusPane {...baseProps} session={paneSession({
      requirement_state: withDefaults,
      requirement_readiness: { status: "incomplete", missing_fields: ["existing_parts"], blocking_conflicts: [], unsupported_capabilities: [], next_question: null, confirmation_eligible: false, effective_defaults: effectiveDefaults },
    })} />);
    const rows = screen.getAllByText("系统默认")
      .map((node) => node.closest("[data-field-row]"))
      .filter((row): row is HTMLElement => row !== null);
    const texts = rows.map((row) => row.textContent ?? "");
    for (const label of ["尺寸", "静音", "CPU 品牌"]) {
      expect(texts.some((text) => text.includes(label))).toBe(true);
    }
    for (const text of texts) {
      expect(text).not.toContain("未指定");
      expect(text).toContain("不限");
    }
  });

  it("撤销墓碑不清除系统默认:显示当前按系统默认与有效值", () => {
    const removed = makeRequirementState({
      budget_cny: { status: "active", value: 8000, strength: "must", scope: "session", source },
      "use_case.type": { status: "active", value: "gaming", strength: "must", scope: "session", source },
      "brand_pref.cpu": { status: "removed", source },
    });
    render(<RequirementStatusPane {...baseProps} session={paneSession({
      requirement_state: removed,
      requirement_readiness: { status: "incomplete", missing_fields: ["existing_parts"], blocking_conflicts: [], unsupported_capabilities: [], next_question: null, confirmation_eligible: false, effective_defaults: effectiveDefaults },
    })} />);
    const row = screen.getAllByText("系统默认")
      .map((node) => node.closest("[data-field-row]"))
      .find((rowNode) => rowNode?.textContent.includes("CPU 品牌"));
    expect(row?.textContent).toContain("已撤销");
    expect(row?.textContent).toContain("不限");
  });

  it("configuration_scope 是范围声明,不作为需求字段行渲染", () => {
    render(<RequirementStatusPane {...baseProps} session={paneSession({
      requirement_readiness: { status: "incomplete", missing_fields: ["budget_cny", "use_case.type"], blocking_conflicts: [], unsupported_capabilities: [], next_question: null, confirmation_eligible: false, effective_defaults: effectiveDefaults },
    })} />);
    expect(screen.queryByText(/configuration_scope/)).not.toBeInTheDocument();
    expect(document.querySelector('[data-field-row="configuration_scope"]')).toBeNull();
  });
});

describe("聊天顶部摘要", () => {
  it("stays a summary and never duplicates the editable requirement table", () => {
    const open = vi.fn();
    render(<RequirementSummary session={paneSession({})} onOpen={open} />);
    expect(screen.getByText("收集需求")).toBeVisible();
    expect(screen.getByLabelText("需求进度")).toHaveTextContent("还缺 2 项");
    expect(screen.queryByText("¥8,000")).not.toBeInTheDocument();
    expect(screen.queryByText(/预算/)).not.toBeInTheDocument();
  });

  it("shows 可以核定 once readiness is ready", () => {
    render(<RequirementSummary session={readySession()} onOpen={vi.fn()} />);
    expect(screen.getByLabelText("需求进度")).toHaveTextContent("可以核定");
  });
});
