import { expect, test } from "@playwright/test";

const base = {
  schema_version: 1,
  id: "session-resilience",
  title: "恢复测试会话",
  created_at: "2026-08-09T10:00:00Z",
  updated_at: "2026-08-09T10:00:02Z",
  version_count: 0,
  pending_requirement: null,
  active_run: null,
  last_error: null,
  recovery_phase: null,
  degraded: false,
};

test.beforeEach(async ({ page }) => {
  await page.route("**/readyz", (route) => route.fulfill({ json: { schema_version: 1, status: "ready", dependencies: { postgres: "ok", redis: "ok", buildsvc: "ok" } } }));
  await page.route("**/api/v1/sessions/session-resilience/builds", (route) => route.fulfill({ json: { schema_version: 1, builds: [] } }));
});

test("collecting conversation survives a page refresh", async ({ page }, testInfo) => {
  const session = {
    ...base,
    phase: "collecting",
    messages: [
      { schema_version: 1, id: "00000000-0000-4000-8000-000000000001", role: "user", content: "我想配台电脑", created_at: "2026-08-09T10:00:00Z" },
      { schema_version: 1, id: "00000000-0000-4000-8000-000000000002", role: "assistant", content: "请先告诉我预算和主要用途。", created_at: "2026-08-09T10:00:01Z" },
    ],
  };
  await page.route("**/api/v1/sessions/session-resilience", (route) => route.fulfill({ json: session }));
  await page.goto("/s/session-resilience");
  await expect(page.getByText("请先告诉我预算和主要用途。")).toBeVisible();
  await page.reload();
  await expect(page.getByText("请先告诉我预算和主要用途。")).toBeVisible();
  if (testInfo.project.name !== "desktop") await page.getByRole("button", { name: "配置" }).first().click();
  await expect(page.getByText("配置尚未生成")).toBeVisible();
});

test("degraded interrupted session explains recovery instead of losing data", async ({ page }) => {
  await page.route("**/api/v1/sessions/session-resilience", (route) => route.fulfill({ json: {
    ...base,
    phase: "error",
    degraded: true,
    recovery_phase: "ready",
    messages: [{ schema_version: 1, id: "00000000-0000-4000-8000-000000000003", role: "user", content: "降 500", created_at: "2026-08-09T10:00:00Z" }],
    last_error: { type: "/problems/run_interrupted", title: "运行已中断", status: 409, code: "run_interrupted", detail: "API 重启中断", request_id: "request-1" },
  } }));
  await page.goto("/s/session-resilience");
  await expect(page.getByText(/Redis 当前不可用/)).toBeVisible();
  await expect(page.getByText("服务重启中断了本次运行，可以显式重试。")).toBeVisible();
  await expect(page.getByRole("button", { name: "重试上一步" })).toBeVisible();
});

test("ownership failure uses the same generic not-found surface", async ({ page }) => {
  await page.route("**/api/v1/sessions/session-resilience", (route) => route.fulfill({ status: 404, contentType: "application/problem+json", json: {
    type: "/problems/not_found", title: "资源不存在", status: 404, code: "not_found", detail: "资源不存在", request_id: "request-404",
  } }));
  await page.goto("/s/session-resilience");
  await expect(page.getByRole("heading", { name: "无法打开会话" })).toBeVisible();
  await expect(page.getByText("会话不存在或已失效。")).toBeVisible();
});
