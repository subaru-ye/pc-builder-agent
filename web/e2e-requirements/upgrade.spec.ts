import { expect, test } from "@playwright/test";
import type { Session } from "../src/lib/api/types";

test("chat upgrades directly and long conversations scroll only inside the workspace", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByLabel("输入需求或改单内容").fill("预算7000，剪1080p多轨视频，不要求静音");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.waitForURL(/\/s\/[^/]+$/);
  const id = new URL(page.url()).pathname.split("/").at(-1)!;
  const snapshot = async (): Promise<Session> => (await page.request.get(`/api/v1/sessions/${id}`)).json();
  await expect.poll(async () => (await snapshot()).phase).toBe("requirement_ready");
  expect((await snapshot()).version_count).toBe(0);
  await page.getByRole("button", { name: "查看 / 修改", exact: true }).click();
  await page.getByRole("button", { name: "确认并开始选配", exact: true }).click();
  await expect.poll(async () => (await snapshot()).version_count).toBe(1);
  await expect(page.getByLabel("输入需求或改单内容")).toBeEnabled();
  const first = await (await page.request.get(`/api/v1/sessions/${id}/builds/1`)).json();
  await page.getByLabel("输入需求或改单内容").fill("把处理器换更好的，预算还很充足啊，其他配件尽量不动");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  // No second confirmation, no extra answer: the same message reaches tools.
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
  if (testInfo.project.name !== "desktop") await page.getByRole("button", { name: "查看配置详情", exact: true }).click();
  await expect(page.getByText("Ryzen 7 5700X", { exact: true }).filter({ visible: true }).first()).toBeVisible();
  if (testInfo.project.name !== "desktop") await page.getByRole("button", { name: "关闭详情", exact: true }).click();
  for (let i = 0; i < 6; i++) {
    await page.getByLabel("输入需求或改单内容").fill("如果换更好的CPU会怎样，先别执行");
    await page.getByRole("button", { name: "发送", exact: true }).click();
    await expect.poll(async () => (await snapshot()).messages.length).toBe(upgraded.messages.length + 2 * (i + 1));
    await expect(page.getByLabel("输入需求或改单内容")).toBeEnabled();
  }
  expect((await snapshot()).version_count).toBe(2);
  const chat = page.locator("#conversation-workspace > .overflow-y-auto");
  expect(await chat.evaluate(e => e.scrollHeight > e.clientHeight)).toBeTruthy();
  await chat.evaluate(e => { e.scrollTop = 0; });
  await expect(page.getByText("预算7000，剪1080p多轨视频，不要求静音", { exact: true }).filter({ visible: true }).first()).toBeVisible();
  await expect.poll(async () => page.evaluate(() => document.documentElement.scrollHeight)).toBe(await page.evaluate(() => innerHeight));
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.mouse.move(100, 24);
  await page.mouse.wheel(0, 2000);
  expect(await page.evaluate(() => scrollY)).toBe(0);
  await chat.evaluate(e => { e.scrollTop = e.scrollHeight; });
  expect(await chat.evaluate(e => e.scrollTop > 0)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath("direct-upgrade-contained-scroll.png") });
});
