import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.route("**/readyz", (route) => route.fulfill({
    json: { schema_version: 1, status: "ready", dependencies: { postgres: "ok", redis: "ok", buildsvc: "ok", auth: "disabled" } },
  }));
  await page.route("**/api/v1/sessions", (route) => route.fulfill({ json: { schema_version: 1, sessions: [] } }));
  await page.route("**/api/v1/auth/me", (route) => route.fulfill({
    json: { schema_version: 1, enabled: false, authenticated: false, account: null, claimed_session_count: 0 },
  }));
});

test("appearance menu persists an explicit theme and can return to system mode", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === "mobile", "移动端首页不显示左侧账号栏");
  await page.goto("/");

  await page.getByRole("button", { name: "打开本地访客菜单" }).click();
  await page.getByRole("menuitem", { name: /外观/ }).click();
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

test("empty auxiliary sidebar is reserved only on wide desktop layouts", async ({ page }, testInfo) => {
  await page.goto("/");

  const sidebar = page.locator('aside[aria-label="辅助侧栏"]');
  if (testInfo.project.name === "desktop") {
    await expect(sidebar).toBeVisible();
  } else {
    await expect(sidebar).toBeHidden();
  }
});
