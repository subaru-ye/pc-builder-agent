import { expect, type Page } from "@playwright/test";
import type { Session } from "../src/lib/api/types";

// 桌面 ≥1024:右栏常驻双 Tab;窄屏:共享 inspector 以详情抽屉打开。
export function isDesktop(page: Page) {
  return (page.viewportSize()?.width ?? 1440) >= 1024;
}

export async function snapshot(page: Page, sessionID: string): Promise<Session> {
  const response = await page.request.get(`/api/v1/sessions/${sessionID}`);
  expect(response.ok()).toBeTruthy();
  return response.json();
}

export async function showRequirements(page: Page) {
  const region = page.getByRole("region", { name: "需求状态", exact: true });
  if (await region.isVisible()) return region;
  if (isDesktop(page)) {
    await page.getByRole("tab", { name: "需求状态" }).click();
  } else {
    await page.getByRole("button", { name: "打开需求状态" }).click();
    await expect(page.getByRole("dialog", { name: "需求状态", exact: true })).toBeVisible();
  }
  await expect(region).toBeVisible();
  return region;
}

export async function showBuild(page: Page) {
  if (isDesktop(page)) {
    const tab = page.getByRole("tab", { name: "配置详情" });
    if ((await tab.getAttribute("aria-selected")) !== "true") {
      await expect(tab).toBeEnabled();
      await tab.click();
    }
    await expect(page.getByRole("region", { name: "配置检查器", exact: true })).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  } else {
    await page.getByRole("button", { name: "查看配置详情", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "配置详情", exact: true })).toBeVisible();
  }
  return page.getByRole("region", { name: "配置检查器", exact: true });
}

export async function closeDetails(page: Page) {
  // 逐层关闭(详情抽屉/核定面板可能同时存在),Escape 兜底 Radix 焦点弹层。
  for (let attempt = 0; attempt < 4 && (await page.getByRole("dialog").count()) > 0; attempt++) {
    const close = page.getByRole("button", { name: "关闭详情", exact: true });
    if (await close.isVisible().catch(() => false)) await close.click();
    else await page.keyboard.press("Escape");
    await page.waitForTimeout(250);
  }
  await expect(page.getByRole("dialog")).toHaveCount(0);
}

export async function showNavigation(page: Page) {
  if (!isDesktop(page)) {
    await page.getByRole("button", { name: "打开会话列表", exact: true }).click();
    return page.getByRole("dialog", { name: "会话", exact: true });
  }
  return page.getByRole("complementary", { name: "会话导航", exact: true });
}

export async function closeNavigation(page: Page) {
  const close = page.getByRole("button", { name: "关闭会话列表", exact: true });
  if (await close.isVisible().catch(() => false)) await close.click();
}

// 面板关闭后焦点回到右栏需求状态 Tab(桌面)或需求视图内部(窄屏)。
export async function expectReviewClosedWithFocus(page: Page) {
  await expect(page.getByRole("dialog", { name: "核定当前需求" })).toHaveCount(0);
  if (isDesktop(page)) {
    await expect(page.getByRole("tab", { name: "需求状态" })).toBeFocused();
    return;
  }
  // 嵌套抽屉的 FocusScope 由 Radix 恢复;焦点必须回到承载需求视图的详情抽屉内。
  await expect.poll(async () => page.evaluate(() => {
    const pane = document.querySelector('[aria-label="需求状态"]');
    const dialog = pane?.closest('[role="dialog"]');
    return !!(dialog && dialog.contains(document.activeElement));
  })).toBe(true);
}

export async function send(page: Page, sessionID: string, text: string) {
  const before = await snapshot(page, sessionID);
  const detailsOpenBefore = await page.getByRole("dialog", { name: /需求状态|配置详情/ }).isVisible().catch(() => false);
  await closeDetails(page);
  await page.getByLabel("输入需求或改单内容").fill(text);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect.poll(async () => {
    const current = await snapshot(page, sessionID);
    return !current.active_run && (current.requirement_state?.revision ?? 0) > (before.requirement_state?.revision ?? 0);
  }).toBeTruthy();
  // 窄屏详情抽屉发送前要先让位给 composer;发送完成后恢复需求视图。
  if (detailsOpenBefore && !isDesktop(page)) await showRequirements(page);
}
