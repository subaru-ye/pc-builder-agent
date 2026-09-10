import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { ChatMessage } from "./chat-message";
import type { Session } from "@/lib/api/types";

const writeText = vi.fn();
beforeEach(() => {
  writeText.mockReset().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
});

function renderMessage(role: "user" | "assistant", content: string, activeRunID?: string, displayContent?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const message = { id: "message-1", role, content, display_content: displayContent, run_id: "run-1", created_at: "2026-09-10T00:00:00Z" } as Session["messages"][number];
  return render(<QueryClientProvider client={client}><ChatMessage message={message} activeRunID={activeRunID} /></QueryClientProvider>);
}

it.each(["user", "assistant"] as const)("copies the complete original %s message, preserving Markdown and newlines", async (role) => {
  const content = "预算改为 8000 元\n\n**必须安静**，保留其他偏好。\n- 不要 RGB";
  renderMessage(role, content);
  fireEvent.click(screen.getByRole("button", { name: "复制这条消息" }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(content));
  expect(await screen.findByText("已复制")).toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("renders and copies the server summary while keeping action buttons icon-only", async () => {
  renderMessage("assistant", "配置单(build_ref: internal_ref)", undefined, "核心搭配：**Ryzen 5 7600**。\n\n超出预算 ¥181.83。");
  expect(screen.queryByText(/internal_ref/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "不满意" }).textContent).toBe("");
  const copy = screen.getByRole("button", { name: "复制这条消息" });
  expect(copy.textContent).toBe("");
  fireEvent.click(copy);
  await waitFor(() => expect(writeText).toHaveBeenCalledWith("核心搭配：**Ryzen 5 7600**。\n\n超出预算 ¥181.83。"));
});

it("reports clipboard rejection and allows retry without reporting false success", async () => {
  writeText.mockRejectedValueOnce(new Error("permission denied"));
  renderMessage("assistant", "完整回复");
  fireEvent.click(screen.getByRole("button", { name: "复制这条消息" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("复制失败");
  expect(screen.queryByText("已复制")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "复制这条消息" }));
  expect(await screen.findByText("已复制")).toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("keeps source anchors and limits feedback to completed assistant replies", () => {
  const view = renderMessage("user", "用户要求");
  const article = screen.getByRole("article", { name: "你的消息" });
  expect(article).toHaveAttribute("id", "message-message-1");
  expect(article).toHaveAttribute("tabindex", "-1");
  expect(within(article).queryByRole("button", { name: "不满意" })).not.toBeInTheDocument();
  view.unmount();
  renderMessage("assistant", "运行中的回复", "run-1");
  expect(screen.queryByRole("button", { name: "不满意" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "复制这条消息" })).toBeInTheDocument();
});
