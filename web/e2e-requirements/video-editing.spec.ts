import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import type { Session } from "../src/lib/api/types";
import { closeDetails, showRequirements, snapshot } from "./helpers";

test("recorded video request builds after a soft budget edit and preserves explicit hard failures", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByLabel("输入需求或改单内容").fill("预算 6000，主要剪 4K 视频，尽量安静");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.waitForURL(/\/s\/[^/]+$/);
  const sessionID = new URL(page.url()).pathname.split("/").at(-1)!;
  const snapshotOf = () => snapshot(page, sessionID);
  // 离线录制分两句登记:先给预算/用途,再补购买范围后才可核定。
  await expect.poll(async () => (await snapshotOf()).active_run).toBeNull();
  await page.getByLabel("输入需求或改单内容").fill("配件全部新买，就用这套剪片子");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect.poll(async () => (await snapshotOf()).phase).toBe("requirement_ready");
  const panel = await showRequirements(page);
  await expect(panel.getByText("¥6,000", { exact: true })).toBeVisible();
  const original = await snapshotOf();
  expect(original.requirement_state!.fields.budget_flex.status).toBe("unknown");
  await panel.getByText(/未解决原话/).click();
  const observation = panel.getByText("主要剪 4K 视频", { exact: true }).locator("..");
  await expect(observation).toContainText("来自对话：主要剪 4K 视频");
  await observation.getByRole("button", { name: "查看来源消息", exact: true }).click();
  await expect(page.locator("article:focus")).toContainText("预算 6000，主要剪 4K 视频，尽量安静");
  await showRequirements(page);
  await panel.getByRole("button", { name: "修改预算", exact: true }).click();
  await panel.getByLabel("预算要求强度", { exact: true }).selectOption("prefer");
  await panel.getByRole("button", { name: "保存需求", exact: true }).click();
  await expect.poll(async () => (await snapshotOf()).requirement_state!.fields.budget_cny.strength).toBe("prefer");
  const edited = await snapshotOf();
  for (const [key, field] of Object.entries(original.requirement_state!.fields)) {
    if (key !== "budget_cny") expect(edited.requirement_state!.fields[key]).toEqual(field);
  }
  await page.screenshot({ path: testInfo.outputPath("video-requirements.png") });
  // 预算为软偏好,面板上限必须叫“预算参考上沿”,不得写成硬上限。
  await panel.getByTestId("requirement-primary").getByRole("button", { name: "核对当前需求", exact: true }).click();
  const review = page.getByRole("dialog", { name: "核定当前需求", exact: true });
  await expect(review).toBeVisible();
  await expect(review.getByText("预算参考上沿", { exact: true })).toBeVisible();
  await review.getByRole("button", { name: "确认并开始配置", exact: true }).click();
  await expect.poll(async () => (await snapshotOf()).version_count).toBe(1);
  await expect.poll(async () => (await snapshotOf()).phase).toBe("ready");
  const built = await snapshotOf();
  expect(built.requirement_confirmation).toMatchObject({ status: "confirmed" });
  const firstBuildResponse = await page.request.get(`/api/v1/sessions/${sessionID}/builds/1`);
  const firstBuild = await firstBuildResponse.json();
  const frozenSpec = firstBuild.requirement?.effective_constraints?.spec ?? firstBuild.requirement;
  expect(frozenSpec.requirement_observations).toEqual(expect.arrayContaining([expect.objectContaining({ text: "主要剪 4K 视频", source: expect.objectContaining({ quote: "主要剪 4K 视频", kind: "chat" }) })]));
  expect(frozenSpec.constraint_strengths!.budget_cny).toBe("prefer");
  await page.reload();
  await expect(page.getByRole("region", { name: "会话", exact: true })).toContainText("配置已保存为 v1");
  await page.screenshot({ path: testInfo.outputPath("video-generated.png") });

  await showRequirements(page);
  await panel.getByRole("button", { name: "修改静音", exact: true }).click();
  await panel.getByLabel("静音要求强度", { exact: true }).selectOption("must");
  await panel.getByRole("button", { name: "保存需求", exact: true }).click();
  await panel.getByTestId("requirement-primary").getByRole("button", { name: "重新核定并生成", exact: true }).click();
  await page.getByRole("dialog", { name: "核定当前需求", exact: true }).getByRole("button", { name: "确认修改并生成新版本", exact: true }).click();
  // v2 生成未交付合同:must 静音无法满足时运行成功但降级为待解决提案,
  // 不产生新版本、不报错;聊天持久化结构化安全说明。
  await expect.poll(async () => (await snapshotOf()).phase, { timeout: 30_000 }).toBe("requirement_ready");
  const failed = await snapshotOf();
  expect(failed.version_count).toBe(1);
  expect(failed.requirement_state!.fields.noise_pref.strength).toBe("must");
  expect(failed.requirement_confirmation).toMatchObject({ status: "confirmed" });
  // 降级提案的运行本身成功且关联当前确认快照:三轴仍为 current,版本保持 v1。
  expect(failed.build_relation).toMatchObject({ status: "current" });
  const message = failed.messages.at(-1)!;
  await expect(page.locator(`[id="message-${message.id}"]`)).toContainText("静音");
  await expect(page.locator(`[id="message-${message.id}"]`)).toContainText("已确认配置保持不变");
  await expect(page.getByLabel("输入需求或改单内容")).toBeEnabled();
  await page.reload();
  await expect(page.locator(`[id="message-${message.id}"]`)).toContainText("静音");
  await expect(page.locator(`[id="message-${message.id}"]`)).not.toContainText("改为尽量满足");
  expect(await (await page.request.get(`/api/v1/sessions/${sessionID}/builds/1`)).json()).toEqual(firstBuild);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  const issues = await new AxeBuilder({ page }).analyze();
  expect(issues.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("hard-condition-reason.png") });

  // 未交付提案可通过聊天继续;新预算信息不会清除硬性静音要求。
  await closeDetails(page);
  await page.getByLabel("输入需求或改单内容").fill("预算改成6000");
  const sent = page.waitForResponse((response) => response.url().endsWith(`/api/v1/sessions/${sessionID}/messages`) && response.request().method() === "POST");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  expect((await sent).status()).toBe(202);
  await expect.poll(async () => (await snapshotOf()).phase, { timeout: 15_000 }).toBe("requirement_ready");
  expect((await snapshotOf()).requirement_state!.fields.noise_pref.strength).toBe("must");
  await showRequirements(page);
  await panel.getByRole("button", { name: "修改补充说明", exact: true }).click();
  await panel.getByLabel("编辑补充说明", { exact: true }).fill("每天剪 4K 旅行纪录片");
  await panel.getByLabel("补充说明信息用途", { exact: true }).selectOption("context");
  await panel.getByRole("button", { name: "保存需求", exact: true }).click();
  await expect.poll(async () => (await snapshotOf()).requirement_state!.fields.notes.kind).toBe("context");
  const clarified = await snapshotOf();
  expect(clarified.requirement_state!.fields.notes.strength).toBe("must");
  expect(clarified.requirement_state!.fields.notes.source!.quote).toContain("补充说明");
  expect(clarified.requirement_state!.fields.notes.source!.quote).not.toContain("必须满足");
  expect(clarified.requirement_state!.observations!.every((item) => item.resolved)).toBeTruthy();
  expect(await (await page.request.get(`/api/v1/sessions/${sessionID}/builds/1`)).json()).toEqual(firstBuild);
  await page.screenshot({ path: testInfo.outputPath("context-edit.png") });
});
