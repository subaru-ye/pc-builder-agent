import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api/client";
import type { SessionSummary } from "@/lib/api/types";
import { WorkspaceOverview } from "./workspace-overview";

const sessions: SessionSummary[] = [
  { schema_version: 1, id: "ready", title: "已经完成", phase: "ready", created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-31T01:00:00Z", version_count: 2 },
  { schema_version: 1, id: "collecting", title: "继续补充需求", phase: "collecting", created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-31T02:00:00Z", version_count: 0 },
  { schema_version: 1, id: "confirm", title: "确认需求卡", phase: "requirement_ready", created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-31T03:00:00Z", version_count: 0 },
  { schema_version: 1, id: "error", title: "处理生成错误", phase: "error", created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-31T04:00:00Z", version_count: 1 },
];

function renderOverview(items = sessions) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>
    <WorkspaceOverview sessions={items} sessionsPending={false} sessionsError={false} />
  </QueryClientProvider>);
}

describe("WorkspaceOverview", () => {
  afterEach(() => vi.restoreAllMocks());

  it("只按行动优先级展示未完成会话", async () => {
    vi.spyOn(api, "readiness").mockResolvedValue({
      schema_version: 1,
      status: "ready",
      dependencies: { postgres: "ok", redis: "ok", buildsvc: "ok" },
    });
    renderOverview();

    const links = screen.getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual([
      "处理生成错误需要处理1 版",
      "确认需求卡等待确认0 版",
      "继续补充需求收集需求0 版",
    ]);
    expect(screen.queryByText("已经完成")).not.toBeInTheDocument();
    expect(screen.getByText("3")).toBeVisible();
    await waitFor(() => expect(screen.getByText("未启用")).toBeVisible());
  });

  it("逐项表达依赖降级且保留数据状态占位", async () => {
    vi.spyOn(api, "readiness").mockResolvedValue({
      schema_version: 1,
      status: "degraded",
      dependencies: { postgres: "ok", redis: "degraded", buildsvc: "unavailable", auth: "unavailable" },
    });
    renderOverview([]);

    expect(screen.getByText("暂无待处理会话。可以从中央输入框开始新需求。")).toBeVisible();
    await waitFor(() => expect(screen.getByText("恢复受限")).toBeVisible());
    expect(screen.getAllByText("不可用")).toHaveLength(2);
    expect(screen.getByText("规格与价格更新时间将在数据健康接口接入后显示。")).toBeVisible();
  });
});
