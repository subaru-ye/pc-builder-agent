import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import type { Session } from "../src/lib/api/types";

async function showRequirements(page: Page) {
  const mobile = page.getByRole("button", { name: "需求", exact: true });
  if ((page.viewportSize()?.width ?? 1440) < 1024) await mobile.click();
  await page.getByRole("tab", { name: "当前需求", exact: true }).click();
}

async function snapshot(page: Page, sessionID: string): Promise<Session> {
  const response = await page.request.get(`/api/v1/sessions/${sessionID}`);
  expect(response.ok()).toBeTruthy();
  return response.json();
}

async function send(page: Page, sessionID: string, text: string) {
  const before = await snapshot(page, sessionID);
  const mobile = page.getByRole("button", { name: "对话", exact: true });
  if ((page.viewportSize()?.width ?? 1440) < 1024) await mobile.click();
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
  await expect(panel.getByRole("button", { name: "确认并生成配置", exact: true })).toBeEnabled();
  await panel.getByRole("button", { name: "确认并生成配置", exact: true }).click();
  await expect.poll(async () => (await snapshot(page, sessionID)).version_count).toBe(1);
  current = await snapshot(page, sessionID);
  const firstBuildResponse = await page.request.get(`/api/v1/sessions/${sessionID}/builds/1`);
  const firstBuild = await firstBuildResponse.json();
  const frozenState = current.confirmed_requirement_state;
  const frozenRequirement = current.confirmed_requirement;
  expect(frozenRequirement!.budget_cny).toBe(8600);

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
