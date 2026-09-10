import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import type { Session } from "../src/lib/api/types";

async function showRequirements(page: Page) {
  if (await page.getByRole("region", { name: "当前需求", exact: true }).isVisible()) return;
  await page.getByRole("button", { name: "查看 / 修改", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "当前需求", exact: true })).toBeVisible();
}

async function showBuild(page: Page) {
  if ((page.viewportSize()?.width ?? 1440) < 1024) {
    await page.getByRole("button", { name: "查看配置详情", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "配置详情", exact: true })).toBeVisible();
  } else {
    await expect(page.getByRole("complementary", { name: "配置详情", exact: true })).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  }
  return page.getByRole("region", { name: "配置检查器", exact: true });
}

async function closeDetails(page: Page) {
  const close = page.getByRole("button", { name: "关闭详情", exact: true });
  if (await close.isVisible()) await close.click();
}

async function showNavigation(page: Page) {
  if ((page.viewportSize()?.width ?? 1440) < 1024) {
    await page.getByRole("button", { name: "打开会话列表", exact: true }).click();
    return page.getByRole("dialog", { name: "会话", exact: true });
  }
  return page.getByRole("complementary", { name: "会话导航", exact: true });
}

async function closeNavigation(page: Page) {
  const close = page.getByRole("button", { name: "关闭会话列表", exact: true });
  if (await close.isVisible()) await close.click();
}

async function checkAccountEntry(page: Page) {
  await expect(page.getByRole("banner").getByRole("button", { name: /打开.*菜单/ })).toHaveCount(0);
  const navigation = await showNavigation(page);
  const account = navigation.getByRole("button", { name: "打开本地访客菜单", exact: true });
  await expect(account).toBeVisible();
  await account.click();
  await expect(page.getByRole("menuitem", { name: /外观/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(account).toBeFocused();
  await closeNavigation(page);
}

async function snapshot(page: Page, sessionID: string): Promise<Session> {
  const response = await page.request.get(`/api/v1/sessions/${sessionID}`);
  expect(response.ok()).toBeTruthy();
  return response.json();
}

async function send(page: Page, sessionID: string, text: string) {
  const before = await snapshot(page, sessionID);
  await closeDetails(page);
  await page.getByLabel("输入需求或改单内容").fill(text);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect.poll(async () => {
    const current = await snapshot(page, sessionID);
    return !current.active_run && current.requirement_state!.revision > before.requirement_state!.revision;
  }).toBeTruthy();
  await showRequirements(page);
}

test("real session state survives chat, edits, confirmation and refresh", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByLabel("输入需求或改单内容").fill("预算8000，玩游戏，要安静一点，尽量用N卡，帮朋友装机");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.waitForURL(/\/s\/[^/]+$/);
  const sessionID = new URL(page.url()).pathname.split("/").at(-1)!;
  await expect.poll(async () => (await snapshot(page, sessionID)).requirement_state?.fields.budget_cny?.value).toBe(8000);
  // Collecting requirements must leave the conversation in view until the user opens details.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await showRequirements(page);
  const panel = page.getByRole("region", { name: "当前需求", exact: true });
  await expect(panel.getByText("¥8,000", { exact: true })).toBeVisible();
  await expect(panel.getByText("安静", { exact: true })).toBeVisible();
  await expect(panel.getByText("NVIDIA", { exact: true })).toBeVisible();
  await expect(panel.getByText(/还需要补充/)).toContainText("分辨率");
  await expect(panel.getByRole("button", { name: "补充分辨率", exact: true })).toBeVisible();
  await expect(panel.getByRole("button", { name: "补充外观", exact: true })).toHaveCount(0);
  const initialBudgetRow = panel.getByRole("button", { name: "修改预算", exact: true }).locator("xpath=../../..");
  const initialBudgetBounds = await initialBudgetRow.boundingBox();
  expect(initialBudgetBounds).not.toBeNull();
  expect(initialBudgetBounds!.height).toBeLessThanOrEqual(72);

  await send(page, sessionID, "预算改成6000");
  await expect(panel.getByText("¥6,000", { exact: true })).toBeVisible();
  let current = await snapshot(page, sessionID);
  expect(current.requirement_state!.fields.noise_pref).toMatchObject({ status: "active", value: "silent", strength: "prefer" });
  expect(current.requirement_state!.fields["brand_pref.gpu"]).toMatchObject({ status: "active", value: "nvidia", strength: "prefer" });
  expect(current.missing_fields).not.toContain("budget_cny");
  expect(current.missing_fields).not.toContain("noise_pref");

  await send(page, sessionID, "用2K");
  await panel.getByRole("button", { name: "修改预算", exact: true }).click();
  await panel.getByLabel("编辑预算", { exact: true }).fill("8600");
  await panel.getByRole("button", { name: "保存需求", exact: true }).click();
  await expect(panel.getByText("¥8,600", { exact: true })).toBeVisible();
  await closeDetails(page);
  // This entry disappears after confirmation; focus must return to the stable summary entry.
  await page.getByRole("button", { name: "核对当前需求", exact: true }).click();
  await expect(panel.getByRole("button", { name: "确认并生成配置", exact: true })).toBeEnabled();
  await panel.getByRole("button", { name: "确认并生成配置", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "查看 / 修改", exact: true })).toBeFocused();
  await expect.poll(async () => (await snapshot(page, sessionID)).version_count).toBe(1);
  await expect.poll(async () => (await snapshot(page, sessionID)).messages.some((message) => !!message.display_content)).toBeTruthy();
  current = await snapshot(page, sessionID);
  const summaryMessage = current.messages.find((message) => !!message.display_content)!;
  const summaryArticle = page.locator(`[id="message-${summaryMessage.id}"]`);
  await expect(summaryArticle).toContainText("核心搭配");
  await expect(summaryArticle).not.toContainText("build_ref");
  await expect(summaryArticle.getByRole("button", { name: "不满意", exact: true })).toHaveText("");
  await expect(summaryArticle.getByRole("button", { name: "复制这条消息", exact: true })).toHaveText("");
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await summaryArticle.getByRole("button", { name: "复制这条消息", exact: true }).click();
  // Windows 剪贴板将 LF 规范化为 CRLF，文本内容与分段应保持一致。
  await expect.poll(() => page.evaluate(async () => (await navigator.clipboard.readText()).replace(/\r\n/g, "\n"))).toBe(summaryMessage.display_content);
  const firstBuildResponse = await page.request.get(`/api/v1/sessions/${sessionID}/builds/1`);
  const firstBuild = await firstBuildResponse.json();
  const frozenState = current.confirmed_requirement_state;
  const frozenRequirement = current.confirmed_requirement;
  expect(frozenRequirement!.budget_cny).toBe(8600);

  await expect(page.getByRole("dialog")).toHaveCount(0);
  const inspector = await showBuild(page);
  await expect(inspector.getByRole("tab", { name: "当前需求", exact: true })).toHaveCount(0);
  await expect(inspector.getByRole("tab", { name: "配置", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(inspector.getByText("价格快照", { exact: true })).toBeVisible();
  if ((page.viewportSize()?.width ?? 1440) >= 1024) {
    await expect(page.getByRole("region", { name: "会话", exact: true })).toBeVisible();
    await expect(page.getByLabel("输入需求或改单内容")).toBeVisible();
    await expect(page.getByRole("complementary", { name: "会话导航", exact: true })).toBeVisible();
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath("build-details.png"), fullPage: true });
  await inspector.getByRole("button", { name: "更换此件", exact: true }).first().click();
  const composer = page.getByLabel("输入需求或改单内容");
  await expect(composer).toHaveValue(/^把.+换成……，其他配件尽量不动$/);
  await expect(composer).toBeFocused();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await composer.fill("");
  await showBuild(page);
  await inspector.getByRole("tab", { name: "校验", exact: true }).click();
  await expect(inspector.getByRole("tab", { name: "校验", exact: true })).toHaveAttribute("aria-selected", "true");
  await inspector.getByRole("tab", { name: "版本", exact: true }).click();
  await expect(inspector.getByText("查看 v1 的已确认需求（只读）", { exact: true })).toBeVisible();
  await closeDetails(page);

  await showRequirements(page);
  await panel.getByRole("button", { name: "修改预算", exact: true }).click();
  await panel.getByLabel("编辑预算", { exact: true }).fill("6500");
  await panel.getByRole("button", { name: "保存需求", exact: true }).click();
  await expect(panel.getByText("¥6,500", { exact: true })).toBeVisible();
  await expect(panel.getByText(/已确认需求和已有配置保持原样/)).toBeVisible();
  current = await snapshot(page, sessionID);
  expect(current.requirement_state!.fields.budget_cny).toMatchObject({ value: 6500, source: { kind: "edit" } });
  expect(current.requirement_state!.fields.noise_pref).toMatchObject({ value: "silent", strength: "prefer" });
  expect(current.confirmed_requirement_state).toEqual(frozenState);
  expect(current.confirmed_requirement).toEqual(frozenRequirement);
  expect(current.version_count).toBe(1);
  await page.reload();
  if ((page.viewportSize()?.width ?? 1440) >= 1024) {
    await expect(page.getByRole("complementary", { name: "配置详情", exact: true }).getByRole("region", { name: "配置检查器", exact: true })).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  }
  await showRequirements(page);
  await expect(panel.getByText("¥6,500", { exact: true })).toBeVisible();

  const budgetRow = panel.getByRole("button", { name: "修改预算", exact: true }).locator("xpath=../../..");
  await budgetRow.getByRole("button", { name: "查看预算来源与操作", exact: true }).click();
  await budgetRow.getByRole("button", { name: "查看来源消息", exact: true }).click();
  await expect(page.locator("article:focus")).toContainText("6500");
  await send(page, sessionID, "预算改成6000");
  expect((await snapshot(page, sessionID)).requirement_state!.fields.budget_cny.value).toBe(6000);

  await send(page, sessionID, "如果换4K会怎样？先不改");
  current = await snapshot(page, sessionID);
  expect(current.requirement_state!.fields["use_case.resolution"].value).toBe("2K");
  expect(current.requirement_state!.alternatives).toEqual(expect.arrayContaining([expect.objectContaining({ field: "use_case.resolution", value: "4K" })]));
  await panel.getByText(/讨论中的备选/).click();
  await expect(panel.getByText("分辨率：4K", { exact: true })).toBeVisible();

  await send(page, sessionID, "取消显卡品牌偏好");
  current = await snapshot(page, sessionID);
  expect(current.requirement_state!.fields["brand_pref.gpu"].status).toBe("removed");
  await expect(panel.getByRole("button", { name: "修改显卡品牌", exact: true })).toHaveCount(0);
  await send(page, sessionID, "这次临时用AMD显卡");
  await expect(panel.getByText("临时例外", { exact: true })).toBeVisible();
  await send(page, sessionID, "恢复之前的显卡要求");
  expect((await snapshot(page, sessionID)).requirement_state!.fields["brand_pref.gpu"].status).toBe("removed");
  const unchangedBuildResponse = await page.request.get(`/api/v1/sessions/${sessionID}/builds/1`);
  expect(await unchangedBuildResponse.json()).toEqual(firstBuild);

  await expect(panel.getByText(/不会自动记为个人长期偏好/)).toBeVisible();
  // Audit the interactive state after the completed chat unlocks editing, not its fade transition.
  const readyEdit = panel.getByRole("button", { name: "修改预算", exact: true });
  await expect(readyEdit).toBeEnabled();
  await expect(readyEdit).toHaveCSS("opacity", "1");
  const violations = await new AxeBuilder({ page }).analyze();
  expect(violations.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await panel.getByRole("heading", { name: "当前需求", exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("requirements.png"), fullPage: true });

  const otherResponse = await page.request.post("/api/v1/sessions", { headers: { "Idempotency-Key": crypto.randomUUID() } });
  expect(otherResponse.ok()).toBeTruthy();
  const other: Session = await otherResponse.json();
  expect(Object.values(other.requirement_state!.fields).every((field) => field.status === "unknown")).toBeTruthy();
});

test("new conversations and session navigation stay reachable while chat remains the main workspace", async ({ page }, testInfo) => {
  await page.goto("/");
  await checkAccountEntry(page);
  await page.getByRole("button", { name: "新建对话", exact: true }).click();
  await page.waitForURL(/\/s\/[^/]+$/);
  const sessionID = new URL(page.url()).pathname.split("/").at(-1)!;
  const empty = await snapshot(page, sessionID);
  expect(empty.messages).toEqual([]);
  expect(empty.requirement_state!.revision).toBe(0);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByLabel("输入需求或改单内容")).toBeVisible();

  const initialMessage = "预算8000，玩游戏，要安静一点，尽量用N卡，帮朋友装机";
  await page.getByLabel("输入需求或改单内容").fill(initialMessage);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect.poll(async () => {
    const current = await snapshot(page, sessionID);
    return !current.active_run && current.requirement_state!.fields.budget_cny?.value === 8000;
  }).toBeTruthy();
  const chat = page.getByRole("region", { name: "会话", exact: true });
  await expect(chat.getByText(initialMessage, { exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  if ((page.viewportSize()?.width ?? 1440) >= 1024) {
    const chatBounds = await chat.boundingBox();
    const navigationBounds = await page.getByRole("complementary", { name: "会话导航", exact: true }).boundingBox();
    const buildBounds = await page.getByRole("complementary", { name: "配置详情", exact: true }).boundingBox();
    expect(chatBounds).not.toBeNull();
    expect(navigationBounds).not.toBeNull();
    expect(buildBounds).not.toBeNull();
    expect(navigationBounds!.x + navigationBounds!.width).toBeLessThanOrEqual(chatBounds!.x);
    expect(chatBounds!.x + chatBounds!.width).toBeLessThanOrEqual(buildBounds!.x);
    expect(chatBounds!.width).toBeGreaterThanOrEqual(buildBounds!.width);
    expect(buildBounds!.width).toBeGreaterThanOrEqual(360);
    expect(buildBounds!.width).toBeLessThanOrEqual(480);
  }
  await checkAccountEntry(page);
  await page.screenshot({ path: testInfo.outputPath("chat-with-navigation.png"), fullPage: true });

  const navigation = await showNavigation(page);
  await expect(navigation.getByRole("navigation", { name: "最近会话", exact: true })).toBeVisible();
  await expect(navigation.locator(`a[href="/s/${sessionID}"]`)).toHaveAttribute("aria-current", "page");
  await navigation.getByRole("button", { name: "新建对话", exact: true }).click();
  await page.waitForURL((url) => /^\/s\/[^/]+$/.test(url.pathname) && url.pathname !== `/s/${sessionID}`);
  const otherID = new URL(page.url()).pathname.split("/").at(-1)!;
  expect((await snapshot(page, otherID)).messages).toEqual([]);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByLabel("输入需求或改单内容")).toHaveValue("");

  const otherNavigation = await showNavigation(page);
  await otherNavigation.locator(`a[href="/s/${sessionID}"]`).click();
  await page.waitForURL(`/s/${sessionID}`);
  await expect(chat.getByText(initialMessage, { exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page.goBack();
  await page.waitForURL(`/s/${otherID}`);
  await expect(chat.getByText(initialMessage, { exact: true })).toHaveCount(0);
  await expect(chat.getByRole("heading", { name: "开始新的装机对话", exact: true })).toBeVisible();
  const backNavigation = await showNavigation(page);
  await expect(backNavigation.locator(`a[href="/s/${otherID}"]`)).toHaveAttribute("aria-current", "page");
  await expect(backNavigation.locator(`a[href="/s/${sessionID}"]`)).toBeVisible();
  await closeNavigation(page);

  await page.goForward();
  await page.waitForURL(`/s/${sessionID}`);
  await expect(chat.getByText(initialMessage, { exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const forwardNavigation = await showNavigation(page);
  await expect(forwardNavigation.locator(`a[href="/s/${sessionID}"]`)).toHaveAttribute("aria-current", "page");
  await expect(forwardNavigation.locator(`a[href="/s/${otherID}"]`)).toBeVisible();
  await closeNavigation(page);

  const detailsTrigger = page.getByRole("button", { name: "查看 / 修改", exact: true });
  await showRequirements(page);
  await expect(page.getByRole("region", { name: "当前需求", exact: true }).getByText("¥8,000", { exact: true })).toBeVisible();
  await closeDetails(page);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(detailsTrigger).toBeFocused();
  await expect(chat.getByText(initialMessage, { exact: true })).toBeVisible();
  await showRequirements(page);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(detailsTrigger).toBeFocused();

  await page.reload();
  await expect(chat.getByText(initialMessage, { exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect((await snapshot(page, sessionID)).requirement_state!.fields.budget_cny?.value).toBe(8000);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  const violations = await new AxeBuilder({ page }).analyze();
  expect(violations.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
});
