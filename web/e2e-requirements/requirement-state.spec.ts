import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import type { Session } from "../src/lib/api/types";
import { closeDetails, closeNavigation, expectReviewClosedWithFocus, isDesktop, send, showBuild, showNavigation, showRequirements, snapshot } from "./helpers";

test.describe(() => {
  test.describe.configure({ mode: "serial" });
});

test("real session state survives chat, edits, confirmation and refresh", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByLabel("输入需求或改单内容").fill("预算8000，玩游戏，要安静一点，尽量用N卡，帮朋友装机，2K分辨率，配件全部新买");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.waitForURL(/\/s\/[^/]+$/);
  const sessionID = new URL(page.url()).pathname.split("/").at(-1)!;
  await expect.poll(async () => (await snapshot(page, sessionID)).requirement_state?.fields.budget_cny?.value).toBe(8000);
  // Collecting requirements must leave the conversation in view until the user opens details.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  // 新会话默认进入需求状态;配置 Tab 在生成配置前真实不可用并说明原因。
  const panel = await showRequirements(page);
  if (isDesktop(page)) {
    const buildTab = page.getByRole("tab", { name: "配置详情" });
    await expect(buildTab).toBeDisabled();
    await expect(page.locator("#build-tab-disabled-reason")).toBeVisible();
    await expect(page.getByRole("region", { name: "配置检查器", exact: true })).toHaveCount(0);
  }
  await expect(panel.getByText("¥8,000", { exact: true })).toBeVisible();
  await expect(panel.getByText("安静", { exact: true })).toBeVisible();
  await expect(panel.getByText("NVIDIA", { exact: true })).toBeVisible();
  const budgetHeader = panel.getByTestId("budget-block").getByRole("button", { name: "修改预算", exact: true });
  const budgetBounds = await budgetHeader.boundingBox();
  expect(budgetBounds).not.toBeNull();
  expect(budgetBounds!.height).toBeLessThanOrEqual(72);

  await send(page, sessionID, "预算改成6000");
  await expect(panel.getByText("¥6,000", { exact: true })).toBeVisible();
  let current = await snapshot(page, sessionID);
  expect(current.requirement_state!.fields.noise_pref).toMatchObject({ status: "active", value: "silent", strength: "prefer" });
  expect(current.requirement_state!.fields["brand_pref.gpu"]).toMatchObject({ status: "active", value: "nvidia", strength: "prefer" });
  expect(current.requirement_readiness!.missing_fields).not.toContain("budget_cny");
  expect(current.requirement_readiness!.missing_fields).not.toContain("noise_pref");

  await panel.getByRole("button", { name: "修改预算", exact: true }).click();
  await panel.getByLabel("编辑预算", { exact: true }).fill("8600");
  await panel.getByRole("button", { name: "保存需求", exact: true }).click();
  await expect(panel.getByText("¥8,600", { exact: true })).toBeVisible();
  // 最低条件齐全后 CTA 可用,但核定面板不自动弹出;确认必须显式发生。
  await expect(panel.getByTestId("requirement-primary").getByRole("button", { name: "核对当前需求", exact: true })).toBeEnabled();
  await expect(panel.getByRole("button", { name: "核对当前需求", exact: true })).toBeEnabled();
  await panel.getByRole("button", { name: "核对当前需求", exact: true }).click();
  const review = page.getByRole("dialog", { name: "核定当前需求", exact: true });
  await expect(review).toBeVisible();
  await expect(review.getByText(/主机（当前支持）/)).toBeVisible();
  await expect(review.getByText(/最高预算/)).toBeVisible();
  await expect(review.getByText("¥9,460", { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("review-panel.png") });
  await review.getByRole("button", { name: "确认并开始配置", exact: true }).click();
  await expectReviewClosedWithFocus(page);
  // 抽屉打开时对话区域对辅助技术是 aria-hidden;聊天断言前先返回对话。
  if (!isDesktop(page)) await closeDetails(page);
  await expect.poll(async () => (await snapshot(page, sessionID)).version_count).toBe(1);
  await expect.poll(async () => (await snapshot(page, sessionID)).messages.some((message) => !!message.display_content)).toBeTruthy();
  // Builder 完成只启用配置 Tab,不抢走当前焦点也不自动切换。
  if (isDesktop(page)) await expect(page.getByRole("tab", { name: "需求状态" })).toBeFocused();
  current = await snapshot(page, sessionID);
  const summaryMessage = current.messages.find((message) => !!message.display_content)!;
  const summaryArticle = page.locator(`[id="message-${summaryMessage.id}"]`);
  await expect(summaryArticle).toContainText("配置已保存为 v1");
  await expect(summaryArticle).not.toContainText("build_ref");
  await expect(summaryArticle.getByRole("button", { name: "不满意", exact: true })).toHaveText("");
  await expect(summaryArticle.getByRole("button", { name: "复制这条消息", exact: true })).toHaveText("");
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await summaryArticle.getByRole("button", { name: "复制这条消息", exact: true }).click();
  // Windows 剪贴板将 LF 规范化为 CRLF，文本内容与分段应保持一致。
  await expect.poll(() => page.evaluate(async () => (await navigator.clipboard.readText()).replace(/\r\n/g, "\n"))).toBe(summaryMessage.display_content);
  const firstBuildResponse = await page.request.get(`/api/v1/sessions/${sessionID}/builds/1`);
  const firstBuild = await firstBuildResponse.json();
  expect(firstBuild.requirement?.effective_constraints?.spec?.budget_cny ?? firstBuild.requirement?.budget_cny).toBe(8600);
  expect(current.requirement_confirmation).toMatchObject({ status: "confirmed" });
  expect(current.requirement_confirmation.confirmed_review_hash).toBeTruthy();

  const inspector = await showBuild(page);
  await expect(inspector.getByRole("tab", { name: "配置", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(inspector.getByText("价格快照", { exact: true })).toBeVisible();
  if (isDesktop(page)) {
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
  await expect(panel.getByText("已修改", { exact: true })).toBeVisible();
  await expect(panel.getByTestId("requirement-primary").getByRole("button", { name: "重新核定并生成", exact: true })).toBeEnabled();
  current = await snapshot(page, sessionID);
  expect(current.requirement_state!.fields.budget_cny).toMatchObject({ value: 6500, source: { kind: "edit" } });
  expect(current.requirement_state!.fields.noise_pref).toMatchObject({ value: "silent", strength: "prefer" });
  expect(current.requirement_confirmation).toMatchObject({ status: "modified" });
  expect(current.requirement_confirmation.review_diff ?? []).toEqual(expect.arrayContaining([expect.objectContaining({ field: "budget_cny", before: 8600, after: 6500 })]));
  expect(current.version_count).toBe(1);
  await page.reload();
  if (isDesktop(page)) {
    await expect(page.getByRole("complementary", { name: "工作台详情", exact: true }).getByRole("region", { name: "需求状态", exact: true })).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  }
  await showRequirements(page);
  await expect(panel.getByText("¥6,500", { exact: true })).toBeVisible();

  await send(page, sessionID, "预算改成6000");
  expect((await snapshot(page, sessionID)).requirement_state!.fields.budget_cny.value).toBe(6000);
  // 手动编辑来源不携带消息 ID;聊天来源可跳回原消息。
  const budgetRow = panel.getByTestId("budget-block");
  await budgetRow.getByRole("button", { name: "查看预算来源与操作", exact: true }).click();
  await budgetRow.getByRole("button", { name: "查看来源消息", exact: true }).click();
  await expect(page.locator("article:focus")).toContainText("6000");
  await showRequirements(page);

  await send(page, sessionID, "如果换4K会怎样？先不改");
  current = await snapshot(page, sessionID);
  expect(current.requirement_state!.fields["use_case.resolution"].value).toBe("2K");
  expect(current.requirement_state!.alternatives).toEqual(expect.arrayContaining([expect.objectContaining({ field: "use_case.resolution", value: "4K" })]));
  // 未解决原话是常驻 section,无需展开;缓存刷新后歧义备选必须可见。
  await expect(panel.getByText(/存在歧义，本次未采用：分辨率：4K/)).toBeVisible({ timeout: 30_000 });

  await send(page, sessionID, "取消显卡品牌偏好");
  current = await snapshot(page, sessionID);
  expect(current.requirement_state!.fields["brand_pref.gpu"].status).toBe("removed");
  await expect(panel.getByRole("button", { name: "修改显卡品牌", exact: true })).toHaveCount(0);
  await send(page, sessionID, "这次临时用AMD显卡");
  await expect(panel.getByText(/临时例外/)).toBeVisible();
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
  await panel.getByRole("heading", { name: "需求状态", exact: true }).scrollIntoViewIfNeeded();
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

  const initialMessage = "预算8000，玩游戏，要安静一点，尽量用N卡，帮朋友装机，2K分辨率，配件全部新买";
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
  if (isDesktop(page)) {
    const chatBounds = await chat.boundingBox();
    const navigationBounds = await page.getByRole("complementary", { name: "会话导航", exact: true }).boundingBox();
    const buildBounds = await page.getByRole("complementary", { name: "工作台详情", exact: true }).boundingBox();
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

  const detailsTrigger = page.getByRole("button", { name: "打开需求状态", exact: true });
  await showRequirements(page);
  await expect(page.getByRole("region", { name: "需求状态", exact: true }).getByText("¥8,000", { exact: true })).toBeVisible();
  if (!isDesktop(page)) {
    await closeDetails(page);
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(detailsTrigger).toBeFocused();
    await expect(chat.getByText(initialMessage, { exact: true })).toBeVisible();
    await showRequirements(page);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(detailsTrigger).toBeFocused();
  }

  await page.reload();
  await expect(chat.getByText(initialMessage, { exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect((await snapshot(page, sessionID)).requirement_state!.fields.budget_cny?.value).toBe(8000);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  const violations = await new AxeBuilder({ page }).analyze();
  expect(violations.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
});

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
