import { expect, test } from "@playwright/test";
import { closeDetails, isDesktop, showBuild, showRequirements, snapshot } from "./helpers";

test("chat upgrades directly and long conversations scroll only inside the workspace", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByLabel("输入需求或改单内容").fill("预算7000，剪1080p多轨视频，不要求静音，配件全部新买");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.waitForURL(/\/s\/[^/]+$/);
  const id = new URL(page.url()).pathname.split("/").at(-1)!;
  const snapshot = async () => (await page.request.get(`/api/v1/sessions/${id}`)).json();
  await expect.poll(async () => (await snapshot()).phase).toBe("requirement_ready");
  expect((await snapshot()).version_count).toBe(0);
  const panel = await showRequirements(page);
  await panel.getByTestId("requirement-primary").getByRole("button", { name: "核对当前需求", exact: true }).click();
  await page.getByRole("dialog", { name: "核定当前需求", exact: true }).getByRole("button", { name: "确认并开始配置", exact: true }).click();
  await expect.poll(async () => (await snapshot()).version_count).toBe(1);
  await expect(page.getByLabel("输入需求或改单内容")).toBeEnabled();
  const first = await (await page.request.get(`/api/v1/sessions/${id}/builds/1`)).json();
  // 窄屏抽屉覆盖 composer,发送前先返回对话。
  await closeDetails(page);
  await page.getByLabel("输入需求或改单内容").fill("把处理器换更好的，预算还很充足啊，其他配件尽量不动");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  // v2 合同:聊天只更新草稿,新版本必须经核定面板显式确认(唯一 Builder admission)。
  await expect.poll(async () => (await snapshot()).phase).toBe("requirement_ready");
  await expect.poll(async () => (await snapshot()).requirement_confirmation?.status).toBe("modified");
  const reconfirmPanel = await showRequirements(page);
  await reconfirmPanel.getByTestId("requirement-primary").getByRole("button", { name: "重新核定并生成", exact: true }).click();
  await page.getByRole("dialog", { name: "核定当前需求", exact: true }).getByRole("button", { name: "确认修改并生成新版本", exact: true }).click();
  await expect.poll(async () => (await snapshot()).version_count).toBe(2);
  const upgraded = await snapshot();
  expect(upgraded.proposal?.result.delivery?.status).toBe("delivered");
  expect(upgraded.proposal?.result.tool_calls).toBe(2);
  expect(upgraded.proposal?.result.quote?.total_cny).toBe("4929.90");
  expect(upgraded.requirement_state?.fields["free.preserve_other_parts"].strength).toBe("prefer");
  expect(upgraded.requirement_state?.fields.noise_pref.status).toBe("removed");
  await page.reload();
  await expect(page.getByLabel("输入需求或改单内容")).toBeEnabled();
  expect((await snapshot()).version_count).toBe(2);
  expect(await (await page.request.get(`/api/v1/sessions/${id}/builds/1`)).json()).toEqual(first);
  if (isDesktop(page)) await showBuild(page);
  else {
    await page.getByRole("button", { name: "查看配置详情", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "配置详情", exact: true })).toBeVisible();
  }
  await expect(page.getByText("Ryzen 7 5700X", { exact: true }).filter({ visible: true }).first()).toBeVisible();
  if (!isDesktop(page)) await page.getByRole("button", { name: "关闭详情", exact: true }).click();
  for (let i = 0; i < 6; i++) {
    await closeDetails(page);
    await page.getByLabel("输入需求或改单内容").fill("如果换更好的CPU会怎样，先别执行");
    await page.getByRole("button", { name: "发送", exact: true }).click();
    await expect.poll(async () => (await snapshot()).messages.length).toBe(upgraded.messages.length + 2 * (i + 1));
    await expect(page.getByLabel("输入需求或改单内容")).toBeEnabled();
  }
  expect((await snapshot()).version_count).toBe(2);
  const chat = page.locator("#conversation-workspace > .overflow-y-auto");
  expect(await chat.evaluate(e => e.scrollHeight > e.clientHeight)).toBeTruthy();
  await chat.evaluate(e => { e.scrollTop = 0; });
  await expect(page.getByText("预算7000，剪1080p多轨视频，不要求静音，配件全部新买", { exact: true }).filter({ visible: true }).first()).toBeVisible();
  await expect.poll(async () => page.evaluate(() => document.documentElement.scrollHeight)).toBe(await page.evaluate(() => innerHeight));
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.mouse.move(100, 24);
  await page.mouse.wheel(0, 2000);
  expect(await page.evaluate(() => scrollY)).toBe(0);
  await chat.evaluate(e => { e.scrollTop = e.scrollHeight; });
  expect(await chat.evaluate(e => e.scrollTop > 0)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath("direct-upgrade-contained-scroll.png") });
});
