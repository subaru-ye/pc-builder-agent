import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api/client";
import type { Session } from "@/lib/api/types";
import { PreferencesPanel } from "./preferences-panel";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const session = {
  id: "s1",
  requirement_state: {
    schema_version: 2,
    revision: 0,
    fields: {
      noise_pref: {
        status: "active", value: "silent", strength: "prefer", evidence: "stated", scope: "session",
        source: { kind: "chat", message_id: "m1", quote: "尽量安静" },
      },
    },
    alternatives: [], changes: [], history: [],
  },
} as unknown as Session;

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><PreferencesPanel session={session} /></QueryClientProvider>);
}

describe("PreferencesPanel 召回确认", () => {
  afterEach(() => vi.restoreAllMocks());

  it("自定义归属把输入的名称作为真实 subject 传递,而不是占位值", async () => {
    const suggestions = vi.spyOn(api, "preferenceSuggestions").mockResolvedValue([]);
    vi.spyOn(api, "listPreferences").mockResolvedValue([]);
    renderPanel();

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "选择要召回的归属" }), "custom");
    await userEvent.type(screen.getByRole("textbox", { name: "召回的代配对象名称" }), "朋友小王");
    await userEvent.click(screen.getByRole("button", { name: "载入建议" }));

    await waitFor(() => expect(suggestions).toHaveBeenCalledWith("s1", "朋友小王"));
    expect(suggestions).not.toHaveBeenCalledWith("s1", "custom");
  });

  it("建议逐项展示 must/prefer 强度与原话日期", async () => {
    vi.spyOn(api, "listPreferences").mockResolvedValue([]);
    vi.spyOn(api, "preferenceSuggestions").mockResolvedValue([
      {
        field: "noise_pref", status: "suggest",
        choices: [{
          id: "p1", subject: "self", field: "noise_pref", value: "silent", strength: "must",
          evidence: "stated", volatile: false,
          source: { kind: "chat", session_id: "s0", message_id: "m0", quote: "声音必须小" },
          created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
        }],
      },
    ]);
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "载入建议" }));
    await waitFor(() => expect(screen.getByTitle("声音必须小")).toBeInTheDocument());
    expect(screen.getByText("必须满足")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /静音：安静/ })).toBeDisabled();
  });

  it("多身份冲突时每个候选值各自展示来源原话,不能只显示首选", async () => {
    vi.spyOn(api, "listPreferences").mockResolvedValue([]);
    vi.spyOn(api, "preferenceSuggestions").mockResolvedValue([
      {
        field: "noise_pref", status: "conflict",
        choices: [
          {
            id: "p1", subject: "self", field: "noise_pref", value: "silent", strength: "prefer",
            evidence: "stated", volatile: false,
            source: { kind: "chat", session_id: "s0", message_id: "m0", quote: "我想要安静一点的机子" },
            created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
          },
          {
            id: "p2", subject: "self", field: "noise_pref", value: "normal", strength: "prefer",
            evidence: "stated", volatile: false,
            source: { kind: "chat", session_id: "s0", message_id: "m9", quote: "先看普通噪音的方案就行" },
            created_at: "2026-09-28T00:00:00Z", updated_at: "2026-09-28T00:00:00Z",
          },
        ],
      },
    ]);
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "载入建议" }));
    await waitFor(() => expect(screen.getByText("不同身份的记录有冲突，请选一项或忽略")).toBeInTheDocument());
    // 两个候选值的来源原话都必须可见,否则用户无法辨认选项出自哪个身份。
    expect(screen.getByTitle("我想要安静一点的机子")).toBeInTheDocument();
    expect(screen.getByTitle("先看普通噪音的方案就行")).toBeInTheDocument();
  });
});
