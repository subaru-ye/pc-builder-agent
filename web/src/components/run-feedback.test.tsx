import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { RunFeedback } from "./run-feedback";

const apiMock = vi.hoisted(() => ({ getFeedback: vi.fn(), submitFeedback: vi.fn() }));
vi.mock("@/lib/api/client", async (importOriginal) => ({ ...await importOriginal<typeof import("@/lib/api/client")>(), api: apiMock }));

function renderFeedback() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><RunFeedback runID="run-1" /></QueryClientProvider>);
}
beforeEach(() => {
  vi.clearAllMocks();
  apiMock.getFeedback.mockResolvedValue(null);
  apiMock.submitFeedback.mockImplementation(async (runID, reason, comment) => ({ run_id: runID, reason, comment }));
});

it("saves feedback and lets the user edit the saved content", async () => {
  renderFeedback();
  expect(apiMock.getFeedback).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "不满意" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "提交反馈" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("哪里需要改进？"), { target: { value: "price_issue" } });
  fireEvent.change(screen.getByLabelText(/补充说明/), { target: { value: " 价格不准确 " } });
  fireEvent.click(screen.getByRole("button", { name: "提交反馈" }));
  expect(await screen.findByRole("status")).toHaveTextContent("反馈已保存");
  expect(apiMock.submitFeedback).toHaveBeenCalledWith("run-1", "price_issue", "价格不准确");
  fireEvent.click(screen.getByRole("button", { name: "已反馈 · 修改" }));
  expect(screen.getByLabelText(/补充说明/)).toHaveValue("价格不准确");
});

it("keeps the comment on failure and retries; other requires a comment", async () => {
  apiMock.submitFeedback.mockRejectedValueOnce(new Error("temporary"));
  renderFeedback();
  fireEvent.click(screen.getByRole("button", { name: "不满意" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "提交反馈" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("哪里需要改进？"), { target: { value: "other" } });
  expect(screen.getByRole("button", { name: "提交反馈" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText(/补充说明/), { target: { value: "请再检查" } });
  fireEvent.click(screen.getByRole("button", { name: "提交反馈" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("说明仍保留");
  expect(screen.getByLabelText(/补充说明/)).toHaveValue("请再检查");
  fireEvent.click(screen.getByRole("button", { name: "提交反馈" }));
  expect(await screen.findByRole("status")).toHaveTextContent("反馈已保存");
});

it("loads historical feedback after opening", async () => {
  apiMock.getFeedback.mockResolvedValue({ reason: "unclear_explanation", comment: "历史说明" });
  renderFeedback();
  fireEvent.click(screen.getByRole("button", { name: "不满意" }));
  fireEvent.click(await screen.findByRole("button", { name: "载入已保存内容" }));
  expect(screen.getByLabelText("哪里需要改进？")).toHaveValue("unclear_explanation");
  expect(screen.getByLabelText(/补充说明/)).toHaveValue("历史说明");
});
