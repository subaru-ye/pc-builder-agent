import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.route("**/readyz", (route) => route.fulfill({
    json: { schema_version: 1, status: "ready", dependencies: { postgres: "ok", redis: "ok", buildsvc: "ok", auth: "ok" } },
  }));
  await page.route("**/api/v1/sessions", (route) => route.fulfill({ json: {
    schema_version: 1,
    sessions: [
      { schema_version: 1, id: "confirm-session", title: "等待确认的配置", phase: "requirement_ready", created_at: "2026-08-31T00:00:00Z", updated_at: "2026-08-31T01:00:00Z", version_count: 0 },
      { schema_version: 1, id: "ready-session", title: "已经完成的配置", phase: "ready", created_at: "2026-08-30T00:00:00Z", updated_at: "2026-08-30T01:00:00Z", version_count: 1 },
    ],
  } }));
  await page.route("**/api/v1/auth/me", (route) => route.fulfill({
    json: { schema_version: 1, enabled: true, authenticated: false, account: null, claimed_session_count: 0 },
  }));
});

test("appearance menu persists an explicit theme and can return to system mode", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === "mobile", "移动端首页不显示左侧账号栏");
  await page.goto("/");

  await page.getByRole("button", { name: "打开本地访客菜单" }).click();
  const appearance = page.getByRole("menuitem", { name: /外观/ });
  await appearance.hover();
  await expect(appearance).toHaveAttribute("data-highlighted");
  await expect.poll(() => appearance.evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe("rgba(0, 0, 0, 0)");

  await appearance.click();
  await expect(page.getByRole("menuitemradio", { name: "系统" })).toHaveAttribute("data-state", "checked");
  await page.getByRole("menuitemradio", { name: "浅色" }).click();

  await expect(page.locator("html")).toHaveClass(/\blight\b/);
  await expect.poll(() => page.evaluate(() => localStorage.getItem("pcb-theme"))).toBe("light");

  await page.reload();
  await expect(page.locator("html")).toHaveClass(/\blight\b/);
  await expect(page.locator("html")).toHaveAttribute("data-theme-preference", "light");

  await page.getByRole("button", { name: "打开本地访客菜单" }).click();
  await page.getByRole("menuitem", { name: /外观/ }).click();
  await page.getByRole("menuitemradio", { name: "系统" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme-preference", "system");
});

test("workspace overview is useful only on wide desktop layouts", async ({ page }, testInfo) => {
  await page.goto("/");

  const sidebar = page.locator('aside[aria-label="工作台概览"]');
  if (testInfo.project.name === "desktop") {
    await expect(sidebar).toBeVisible();
    await expect(sidebar.getByRole("heading", { name: "待继续" })).toBeVisible();
    await expect(sidebar.getByText("等待确认的配置")).toBeVisible();
    await expect(sidebar.getByText("已经完成的配置")).toBeHidden();
    await expect(sidebar.getByRole("heading", { name: "运行状态" })).toBeVisible();
    await expect(sidebar.getByText("配置生成")).toBeVisible();
    await expect(sidebar.getByText("数据状态")).toBeVisible();
  } else {
    await expect(sidebar).toBeHidden();
  }
});
