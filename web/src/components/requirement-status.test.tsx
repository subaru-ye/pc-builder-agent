import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { RequirementState, Session } from "@/lib/api/types";
import { RequirementStatus, RequirementSummary } from "./requirement-status";

const source = { kind: "chat" as const, message_id: "message-1", quote: "预算 8000，尽量安静，帮朋友装机" };
const state: RequirementState = {
  schema_version: 1, revision: 2,
  fields: {
    budget_cny: { status: "active", value: 8000, strength: "must", scope: "session", source },
    noise_pref: { status: "active", value: "silent", strength: "prefer", scope: "session", source },
    recipient: { status: "active", value: "friend", strength: "must", scope: "session", source },
    "brand_pref.gpu": { status: "removed", source },
    appearance: { status: "unknown" },
    "use_case.resolution": { status: "unknown" },
  },
  alternatives: [{ field: "use_case.resolution", value: "4K", strength: "prefer", scope: "session", source: { ...source, quote: "如果上 4K 会怎样，先不改" } }],
  changes: [{ revision: 2, op: "remove", field: "brand_pref.gpu", source }],
  history: [{ revision: 2, op: "remove", field: "brand_pref.gpu", source }],
};
const session = {
  schema_version: 1, id: "session-1", title: "帮朋友装机", phase: "collecting", created_at: "2026-09-09T01:00:00Z", updated_at: "2026-09-09T01:00:00Z", version_count: 0,
  messages: [], pending_requirement: null, active_run: null, last_error: null, recovery_phase: null, degraded: false,
  requirement_state: state, requirement_status: "collecting", confirmed_requirement_state: null, confirmed_requirement: null, confirmed_at: null, missing_fields: ["use_case.resolution"],
} satisfies Session;

describe("RequirementStatus", () => {
  it("does not classify legacy facts or repeat the context field label", () => {
    render(<RequirementStatus session={{ ...session, requirement_state: { ...state, fields: { "use_case.type": { status: "active", value: "productivity", strength: "must", source }, notes: { status: "active", value: "剪4K视频", kind: "context", strength: "must", source } } } }} busy={false} onUpdate={vi.fn()} onConfirm={vi.fn()} onSource={vi.fn()} />);
    expect(screen.getByText("生产力")).toBeVisible();
    expect(screen.getAllByText("补充说明", { exact: true })).toHaveLength(1);
    expect(screen.queryByText("必须满足")).not.toBeInTheDocument();
    expect(screen.queryByText("用途事实")).not.toBeInTheDocument();
  });

  it("preserves context strength without presenting it as a mandatory configuration condition", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    render(<RequirementStatus session={{ ...session, requirement_state: { ...state, fields: { notes: { status: "active", value: "剪4K视频", kind: "context", strength: "must", source } } } }} busy={false} onUpdate={update} onConfirm={vi.fn()} onSource={vi.fn()} />);
    expect(screen.getByText("剪4K视频")).toBeVisible();
    expect(screen.queryByText("必须满足")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "修改补充说明" }));
    expect(screen.getByLabelText("补充说明信息用途")).toHaveValue("context");
    expect(screen.queryByLabelText("补充说明要求强度")).not.toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText("补充说明信息用途"), "constraint");
    expect(screen.getByLabelText("补充说明要求强度")).toHaveValue("must");
    await userEvent.click(screen.getByRole("button", { name: "保存需求" }));
    expect(update).toHaveBeenCalledWith([{ op: "set", field: "notes", value: "剪4K视频", kind: "constraint", strength: "must", scope: "session" }]);
  });

  it("requires an explicit meaning for legacy free text and retains unresolved source text", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    const onSource = vi.fn();
    render(<RequirementStatus session={{ ...session, requirement_state: { ...state, fields: { notes: { status: "active", value: "剪4K视频", strength: "must", source } }, observations: [{ field: "notes", text: "还想偶尔折腾点别的", reason: "用途待补充", source }] } }} busy={false} onUpdate={update} onConfirm={vi.fn()} onSource={onSource} />);
    await userEvent.click(screen.getByRole("button", { name: "修改补充说明" }));
    await userEvent.click(screen.getByRole("button", { name: "保存需求" }));
    expect(update).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("请选择这段说明的信息用途");
    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    await userEvent.click(screen.getByText(/保留的原话/));
    expect(screen.getByText("还想偶尔折腾点别的")).toBeVisible();
    await userEvent.click(within(screen.getByText("还想偶尔折腾点别的").closest("li")!).getByRole("button", { name: "查看来源消息" }));
    expect(onSource).toHaveBeenCalledWith("message-1");
    await userEvent.click(screen.getByRole("button", { name: "撤销原话及补充说明记录" }));
    expect(update).toHaveBeenCalledWith([{ op: "remove", field: "notes" }]);
  });

  it("shows unknown and revoked facts honestly while keeping alternatives separate", async () => {
    render(<RequirementStatus session={session} busy={false} onUpdate={vi.fn()} onConfirm={vi.fn()} onSource={vi.fn()} />);
    expect(screen.getByText("¥8,000")).toBeVisible();
    expect(screen.getByText("安静")).toBeVisible();
    expect(screen.getByText("朋友")).toBeVisible();
    expect(screen.getByText(/还需要补充：分辨率/)).toBeVisible();
    expect(screen.getByRole("button", { name: "补充分辨率" })).toBeVisible();
    expect(screen.getByText("未知 · 需要补充")).toBeVisible();
    expect(screen.getByRole("button", { name: "补充外观" })).not.toBeVisible();
    await userEvent.click(screen.getByText(/尚未说明 ·/));
    expect(screen.getByText(/显卡品牌：未知（已撤销）/)).toBeVisible();
    expect(screen.getByText(/外观：未知/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "修改分辨率" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByText(/讨论中的备选/));
    expect(screen.getByText("分辨率：4K")).toBeVisible();
    expect(screen.getByText("尚未作为当前要求生效。")).toBeVisible();
    expect(screen.getByText(/不会自动记为个人长期偏好/)).toBeVisible();
  });

  it("sends only the edited field and renders the authoritative server response", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    const props = { session, busy: false, onUpdate: update, onConfirm: vi.fn(), onSource: vi.fn() };
    const view = render(<RequirementStatus {...props} />);
    await userEvent.click(screen.getByRole("button", { name: "修改预算" }));
    const input = screen.getByLabelText("编辑预算");
    await userEvent.clear(input); await userEvent.type(input, "6000");
    await userEvent.click(screen.getByRole("button", { name: "保存需求" }));
    expect(update).toHaveBeenCalledWith([{ op: "set", field: "budget_cny", value: 6000, strength: "must", scope: "session" }]);
    expect(screen.getByText("¥8,000")).toBeVisible();
    expect(screen.queryByText("¥6,000")).not.toBeInTheDocument();
    view.rerender(<RequirementStatus {...props} session={{ ...session, requirement_state: { ...state, revision: 3, fields: { ...state.fields, budget_cny: { ...state.fields.budget_cny, value: 6000 } } } }} />);
    expect(screen.getByText("¥6,000")).toBeVisible();
    expect(screen.getByText("安静")).toBeVisible();
    expect(screen.getByText("尽量满足")).toBeVisible();
  });

  it("supports source navigation, explicit removal and restoring a temporary exception", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    const showSource = vi.fn();
    const previous = state.fields.noise_pref;
    render(<RequirementStatus session={{ ...session, requirement_state: { ...state, fields: { noise_pref: { ...previous, value: "normal", scope: "temporary", previous } } } }} busy={false} onUpdate={update} onConfirm={vi.fn()} onSource={showSource} />);
    expect(screen.getByText("临时例外")).toBeVisible();
    const sourceButton = screen.getByRole("button", { name: "查看静音来源与操作" });
    await userEvent.click(sourceButton);
    await userEvent.click(within(sourceButton.parentElement!.parentElement!.parentElement!).getByRole("button", { name: "查看来源消息" }));
    expect(showSource).toHaveBeenCalledWith("message-1");
    await userEvent.click(screen.getByRole("button", { name: "恢复例外前要求" }));
    expect(update).toHaveBeenLastCalledWith([{ op: "restore", field: "noise_pref" }]);
    await userEvent.click(screen.getByRole("button", { name: "撤销这项要求" }));
    expect(update).toHaveBeenLastCalledWith([{ op: "remove", field: "noise_pref" }]);
  });

  it("preserves edits after a failed save and lets the user explicitly choose strength", async () => {
    const update = vi.fn().mockRejectedValue(new Error("offline"));
    render(<RequirementStatus session={session} busy={false} onUpdate={update} onConfirm={vi.fn()} onSource={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: "修改静音" }));
    await userEvent.selectOptions(screen.getByLabelText("静音要求强度"), "must");
    await userEvent.click(screen.getByRole("button", { name: "保存需求" }));
    expect(update).toHaveBeenCalledWith([{ op: "set", field: "noise_pref", value: "silent", strength: "must", scope: "session" }]);
    expect(screen.getByLabelText("静音要求强度")).toHaveValue("must");
    expect(screen.getByRole("alert")).toHaveTextContent("当前输入已保留");
  });

  it("distinguishes confirmed versions from a modified draft using server status", () => {
    const view = render(<RequirementStatus session={{ ...session, requirement_status: "confirmed", confirmed_requirement_state: { ...state, revision: 1 } }} busy={false} onUpdate={vi.fn()} onConfirm={vi.fn()} onSource={vi.fn()} />);
    expect(screen.getByText("已确认")).toBeVisible();
    expect(screen.queryByText(/以下为修改中的草稿/)).not.toBeInTheDocument();
    view.rerender(<RequirementStatus session={{ ...session, requirement_status: "modified", confirmed_requirement_state: state }} busy={false} onUpdate={vi.fn()} onConfirm={vi.fn()} onSource={vi.fn()} />);
    expect(screen.getByText(/已确认需求和已有配置保持原样/)).toBeVisible();
  });

  it("keeps a compact requirement entry available in the conversation", async () => {
    const open = vi.fn();
    render(<RequirementSummary session={session} onOpen={open} />);
    expect(screen.getByText(/¥8,000 · 主要用途未知 · 分辨率未知/)).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: /查看 \/ 修改/ }));
    expect(open).toHaveBeenCalledOnce();
    expect(within(screen.getByText(/本轮更新/)).getByText(/显卡品牌/)).toBeVisible();
  });
});
