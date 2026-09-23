import { expect, test } from "@playwright/test";
import type { Session } from "../src/lib/api/types";
import { closeDetails, isDesktop, showRequirements } from "./helpers";

test("a sourced session supplement resolves a saved proposal without rewriting the old version", async ({ page }, testInfo) => {
  await expect.poll(async () => (await page.request.get("/healthz")).status(), { timeout: 60_000 }).toBe(200);
  await page.goto("/");
  const send = async (text: string) => {
    // 窄屏抽屉覆盖 composer,发送前先返回对话。
    await closeDetails(page);
    await expect(page.getByLabel("输入需求或改单内容")).toBeEnabled();
    await page.getByLabel("输入需求或改单内容").fill(text);
    await page.getByRole("button", { name: "发送", exact: true }).click();
  };
  await send("预算7000，剪1080p多轨视频，不要求静音，配件全部新买");
  await page.waitForURL(/\/s\/[^/]+$/);
  const id = new URL(page.url()).pathname.split("/").at(-1)!;
  const snapshot = async (): Promise<Session> => (await page.request.get(`/api/v1/sessions/${id}`)).json();
  await expect.poll(async () => (await snapshot()).phase).toBe("requirement_ready");
  const panel = await showRequirements(page);
  await panel.getByTestId("requirement-primary").getByRole("button", { name: "核对当前需求", exact: true }).click();
  await page.getByRole("dialog", { name: "核定当前需求", exact: true }).getByRole("button", { name: "确认并开始配置", exact: true }).click();
  await expect.poll(async () => (await snapshot()).version_count).toBe(1);
  const original = await (await page.request.get(`/api/v1/sessions/${id}/builds/1`)).json();
  await send("预算调整为6500，其他要求保留并继续选配");
  // v2 合同:聊天更新草稿后,由核定面板显式确认生成新版本。
  await expect.poll(async () => (await snapshot()).requirement_confirmation?.status).toBe("modified");
  const reconfirmPanel = await showRequirements(page);
  await reconfirmPanel.getByTestId("requirement-primary").getByRole("button", { name: "重新核定并生成", exact: true }).click();
  await page.getByRole("dialog", { name: "核定当前需求", exact: true }).getByRole("button", { name: "确认修改并生成新版本", exact: true }).click();
  await expect.poll(async () => (await snapshot()).version_count).toBe(2);
  const budget = await snapshot();
  expect(budget.requirement_state?.fields.budget_cny.value).toBe(6500);
  expect(budget.requirement_state?.fields["free.workload_resolution"].value).toBe("1080p多轨视频剪辑");
  expect(budget.requirement_state?.fields.noise_pref.status).toBe("removed");
  expect(budget.requirement_confirmation).toMatchObject({ status: "confirmed" });
  // 旧版本保持字节不变;刷新后恢复服务端真值。
  expect(await (await page.request.get(`/api/v1/sessions/${id}/builds/1`)).json()).toEqual(original);
  await page.reload();
  await expect(page.getByLabel("输入需求或改单内容")).toBeEnabled();
  expect((await snapshot()).proposal).toEqual(budget.proposal);
  // 草稿无变化时继续校验不产生新版本(确认去重),旧版本保持字节不变。
  await send("按当前方案继续校验");
  await expect.poll(async () => (await snapshot()).active_run).toBeNull();
  expect((await snapshot()).version_count).toBe(2);
  expect(await (await page.request.get(`/api/v1/sessions/${id}/builds/1`)).json()).toEqual(original);
  // 历史版本只读视图。
  await page.goto(`/s/${id}?version=1`);
  await page.reload();
  if (isDesktop(page)) await page.getByRole("tab", { name: "配置详情" }).click();
  else {
    await page.getByRole("button", { name: "查看配置详情", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "配置详情", exact: true })).toBeVisible();
  }
  await expect(page.getByText("历史只读", { exact: true }).filter({ visible: true }).first()).toBeVisible();
  expect((await snapshot()).version_count).toBe(2);
  await testInfo.attach("session-supplement-evidence.json", { body: JSON.stringify({ budget, restored: await snapshot(), actual_model_requests: 0, actual_external_requests: 0 }, null, 2), contentType: "application/json" });
});
