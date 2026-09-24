import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Session } from "@/lib/api/types";
import { ApiError } from "@/lib/api/client";
import { makeRequirementState, makeSession } from "@/test/session-fixture";
import { RequirementFullEditor } from "./requirement-full-editor";

const source = { kind: "chat" as const, message_id: "message-1", quote: "预算 8000，2K 游戏" };

// 服务端 effective_defaults 的典型形状:偏好类默认 + 预算弹性 + 范围声明。
function defaultMap() {
  return new Map<string, unknown>([
    ["budget_flex", 0.1],
    ["size_pref", "any"],
    ["noise_pref", "any"],
    ["brand_pref.cpu", "any"],
    ["brand_pref.gpu", "any"],
    ["configuration_scope", ["tower"]],
  ]);
}

function editorSession(fields: NonNullable<Session["requirement_state"]>["fields"]) {
  return makeSession({ requirement_state: makeRequirementState(fields) });
}

function renderEditor(session: Session, onSave = vi.fn().mockResolvedValue(undefined)) {
  const onClose = vi.fn();
  const view = render(<RequirementFullEditor session={session} defaults={defaultMap()} busy={false} onSave={onSave} onClose={onClose} />);
  return { onSave, onClose, unmount: view.unmount };
}

describe("RequirementFullEditor 批量编辑只提交实际改动", () => {
  it("未做任何改动时保存不产生操作,直接关闭", async () => {
    const { onSave, onClose } = renderEditor(editorSession({}));
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(onSave).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("系统默认预填不会被写成 active:只提交用户实际改动的字段", async () => {
    const { onSave, onClose } = renderEditor(editorSession({}));
    // 尺寸预填系统默认 any;用户改为 ATX,其余默认保持不动。
    await userEvent.selectOptions(screen.getByLabelText("尺寸"), "atx");
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith([{ op: "set", field: "size_pref", value: "atx", strength: "prefer", kind: "constraint" }], 1);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("把预填的系统默认改回未指定不产生操作,默认继续生效", async () => {
    const { onSave, onClose } = renderEditor(editorSession({}));
    await userEvent.selectOptions(screen.getByLabelText("尺寸"), "");
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(onSave).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("预算未知时可以单独保存其他字段", async () => {
    const { onSave } = renderEditor(editorSession({
      "use_case.type": { status: "active", value: "gaming", strength: "must", scope: "session", source },
    }));
    await userEvent.selectOptions(screen.getByLabelText("静音"), "silent");
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith([{ op: "set", field: "noise_pref", value: "silent", strength: "prefer", kind: "constraint" }], 1);
  });

  it("数字输入沿用后端口径校验:预算须为正整数", async () => {
    const { onSave } = renderEditor(editorSession({}));
    const budget = screen.getByLabelText("预算（元）");
    for (const invalid of ["0", "-5", "12.5"]) {
      await userEvent.clear(budget);
      await userEvent.type(budget, invalid);
      await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
      expect(await screen.findByText("请填写正整数")).toBeVisible();
    }
    expect(onSave).not.toHaveBeenCalled();
  });

  it("预算弹性超出 0–0.3 被拒绝", async () => {
    const { onSave } = renderEditor(editorSession({}));
    const flex = screen.getByLabelText(/预算弹性/);
    await userEvent.clear(flex);
    await userEvent.type(flex, "0.5");
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(await screen.findByText(/0–0\.3/)).toBeVisible();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("修改 active 预算只提交该字段;清空 active 预算产生撤销", async () => {
    const budget = { status: "active" as const, value: 8000, strength: "must" as const, scope: "session" as const, source };
    const change = renderEditor(editorSession({ budget_cny: budget }));
    await userEvent.clear(screen.getByLabelText("预算（元）"));
    await userEvent.type(screen.getByLabelText("预算（元）"), "6000");
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(change.onSave).toHaveBeenCalledTimes(1);
    expect(change.onSave).toHaveBeenCalledWith([{ op: "set", field: "budget_cny", value: 6000, strength: "must" }], 1);

    change.unmount();
    const clear = renderEditor(editorSession({ budget_cny: budget }));
    await userEvent.clear(screen.getByLabelText("预算（元）"));
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(clear.onSave).toHaveBeenCalledTimes(1);
    expect(clear.onSave).toHaveBeenCalledWith([{ op: "remove", field: "budget_cny" }], 1);
  });

  it("保存失败保留输入并展示错误", async () => {
    const onSave = vi.fn().mockRejectedValue(new Error("409 修订冲突"));
    const { onClose } = renderEditor(editorSession({}), onSave);
    await userEvent.selectOptions(screen.getByLabelText("尺寸"), "itx");
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("409 修订冲突");
    expect(screen.getByLabelText("尺寸")).toHaveValue("itx");
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe("RequirementFullEditor 打开时固定编辑基线", () => {
  it("父组件刷新(Session 新对象、revision 变化、新 defaults Map)不丢失未保存输入", async () => {
    const budget = { status: "active" as const, value: 8000, strength: "must" as const, scope: "session" as const, source };
    const initial = editorSession({ budget_cny: budget });
    const onSave = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    const view = render(<RequirementFullEditor session={initial} defaults={defaultMap()} busy={false} onSave={onSave} onClose={onClose} />);
    await userEvent.clear(screen.getByLabelText("预算（元）"));
    await userEvent.type(screen.getByLabelText("预算（元）"), "6000");
    // 模拟父组件刷新:全新 Session 对象(revision +1,服务端并发把静音写成 active)与全新 defaults Map。
    const refreshed = makeSession({
      requirement_state: makeRequirementState({
        budget_cny: budget,
        noise_pref: { status: "active", value: "silent", strength: "prefer", scope: "session", source },
      }, { revision: 5 }),
    });
    view.rerender(<RequirementFullEditor session={refreshed} defaults={defaultMap()} busy={false} onSave={onSave} onClose={onClose} />);
    expect(screen.getByLabelText("预算（元）")).toHaveValue(6000);
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    // diff 仍按打开时的基线:只提交用户的预算改动,服务端后来的静音变化不属于本表单。
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith([{ op: "set", field: "budget_cny", value: 6000, strength: "must" }], 1);
    // 请求级:expected_revision 必须是打开时的 1,而不是刷新后的 5,
    // 否则并发修改会被新 revision 静默覆盖而不是 409。
    expect(onSave.mock.calls[0][1]).toBe(1);
  });

  it("保存遇到 409 修订冲突时保留输入并明示冲突,不静默按新 revision 覆盖", async () => {
    const onSave = vi.fn().mockRejectedValue(new ApiError({
      schema_version: 1, type: "/problems/requirement_revision_conflict", title: "需求已发生变化", status: 409,
      code: "requirement_revision_conflict", detail: "此次修改未保存。请刷新当前需求后重试。", request_id: "req-1",
    }));
    const onClose = vi.fn();
    render(<RequirementFullEditor session={editorSession({})} defaults={defaultMap()} busy={false} onSave={onSave} onClose={onClose} />);
    await userEvent.selectOptions(screen.getByLabelText("尺寸"), "itx");
    await userEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("冲突");
    expect(screen.getByRole("alert")).toHaveTextContent("当前输入已保留");
    expect(screen.getByLabelText("尺寸")).toHaveValue("itx");
    expect(onClose).not.toHaveBeenCalled();
    expect(onSave).toHaveBeenCalledTimes(1);
  });
});
