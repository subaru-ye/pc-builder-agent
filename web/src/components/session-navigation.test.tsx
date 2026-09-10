import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import type { Session } from "@/lib/api/types";
import { useUIStore } from "@/stores/ui";
import { NewSessionButton, SessionNavigation } from "./session-navigation";

const push = vi.hoisted(() => vi.fn());
const replace = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({ useRouter: () => ({ push, replace }) }));
vi.mock("./local-profile-menu", () => ({ LocalProfileMenu: () => <div>本地访客</div> }));

const session = {
  schema_version: 1, id: "new-session", title: "新对话", phase: "collecting", created_at: "2026-09-10T00:00:00Z", updated_at: "2026-09-10T00:00:00Z", version_count: 0,
  messages: [], pending_requirement: null, active_run: null, last_error: null, recovery_phase: null, degraded: false,
  requirement_state: { schema_version: 1, revision: 0, fields: {}, alternatives: [], changes: [], history: [] }, requirement_status: "collecting", confirmed_requirement_state: null, confirmed_requirement: null, confirmed_at: null, missing_fields: ["budget_cny", "use_case.type"],
} satisfies Session;

function renderNavigation(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
  return client;
}

describe("SessionNavigation", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    push.mockReset();
    replace.mockReset();
    useUIStore.setState({ mobilePane: "chat", inspectorTab: "build", diffFrom: null, diffTo: null });
  });

  it("renames through the server and updates the active session cache", async () => {
    vi.spyOn(api, "listSessions").mockResolvedValue([session]);
    const update = vi.spyOn(api, "updateSession").mockResolvedValue({ ...session, title: "朋友的电脑" });
    const client = renderNavigation(<SessionNavigation currentSessionID={session.id} />);
    await userEvent.click(await screen.findByRole("button", { name: "新对话的对话菜单" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "重命名" }));
    const input = screen.getByLabelText("对话标题");
    await userEvent.clear(input);
    expect(screen.getByRole("button", { name: "保存标题" })).toBeDisabled();
    await userEvent.type(input, "朋友的电脑");
    await userEvent.click(screen.getByRole("button", { name: "保存标题" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith(session.id, { title: "朋友的电脑" }));
    await waitFor(() => expect(client.getQueryData(queryKeys.session(session.id))).toMatchObject({ title: "朋友的电脑" }));
    expect(replace).not.toHaveBeenCalled();
  });

  it("requires confirmation before deletion, retaining the dialog on server rejection", async () => {
    vi.spyOn(api, "listSessions").mockResolvedValue([session]);
    const remove = vi.spyOn(api, "deleteSession").mockRejectedValueOnce(new Error("运行中")).mockResolvedValueOnce(undefined);
    renderNavigation(<SessionNavigation currentSessionID={session.id} />);
    await userEvent.click(await screen.findByRole("button", { name: "新对话的对话菜单" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "删除对话" }));
    expect(remove).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toHaveTextContent("无法撤销");
    await userEvent.click(screen.getByRole("button", { name: "确认删除" }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(replace).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "确认删除" }));
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/"));
  });

  it("loads archived sessions and restores them through the same server update", async () => {
    vi.spyOn(api, "listSessions").mockResolvedValue([]);
    vi.spyOn(api, "listArchivedSessions").mockResolvedValue([{ ...session, archived: true }]);
    const update = vi.spyOn(api, "updateSession").mockResolvedValue({ ...session, archived: false });
    renderNavigation(<SessionNavigation />);
    await userEvent.click(screen.getByRole("button", { name: "已归档" }));
    await userEvent.click(await screen.findByRole("button", { name: "新对话的对话菜单" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "取消归档" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith(session.id, { archived: false }));
  });

  it("creates an empty server session without sending a model message and clears previous view state", async () => {
    const create = vi.spyOn(api, "createSession").mockResolvedValue(session);
    const send = vi.spyOn(api, "sendMessage");
    const onCreated = vi.fn();
    const client = renderNavigation(<NewSessionButton onCreated={onCreated} />);
    const invalidate = vi.spyOn(client, "invalidateQueries");
    useUIStore.setState({ mobilePane: "build", inspectorTab: "versions", diffFrom: 1, diffTo: 2 });

    await userEvent.click(screen.getByRole("button", { name: "新建对话" }));

    await waitFor(() => expect(push).toHaveBeenCalledWith("/s/new-session"));
    expect(create).toHaveBeenCalledOnce();
    expect(send).not.toHaveBeenCalled();
    expect(onCreated).toHaveBeenCalledOnce();
    expect(client.getQueryData(queryKeys.session(session.id))).toEqual(session);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.sessions });
    expect(useUIStore.getState()).toMatchObject({ mobilePane: "chat", inspectorTab: "build", diffFrom: null, diffTo: null });
  });

  it("reuses the creation key after an uncertain response instead of creating duplicate sessions", async () => {
    const create = vi.spyOn(api, "createSession").mockRejectedValueOnce(new Error("网络连接中断")).mockResolvedValueOnce(session);
    renderNavigation(<NewSessionButton />);
    await userEvent.click(screen.getByRole("button", { name: "新建对话" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("可点击新建对话重试");
    expect(push).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "新建对话" }));
    await waitFor(() => expect(push).toHaveBeenCalledWith("/s/new-session"));
    expect(create.mock.calls[1][0]).toBe(create.mock.calls[0][0]);
  });

  it("keeps list errors actionable and marks the selected server session", async () => {
    const list = vi.spyOn(api, "listSessions").mockRejectedValueOnce(new Error("离线")).mockResolvedValueOnce([session]);
    renderNavigation(<SessionNavigation currentSessionID={session.id} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("已有对话仍保留");
    await userEvent.click(screen.getByRole("button", { name: "重试读取" }));
    const link = await screen.findByRole("link", { name: /新对话/ });
    expect(link).toHaveAttribute("href", "/s/new-session");
    expect(link).toHaveAttribute("aria-current", "page");
    expect(list).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
