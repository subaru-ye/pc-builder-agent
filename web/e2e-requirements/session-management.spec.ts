import { expect, test } from "@playwright/test";

// 可对普通 API 运行：仅新建空会话和管理元数据，0 次模型调用，不使用前端 route mock。
test("manage a session through rename, archive, restore and confirmed deletion", async ({ page }, testInfo) => {
  const mobile = (page.viewportSize()?.width ?? 1440) < 1024;
  const navigation = async () => {
    if (!mobile) return page.getByRole("complementary", { name: "会话导航", exact: true });
    const drawer = page.getByRole("dialog", { name: "会话", exact: true });
    const trigger = page.getByRole("button", { name: "打开会话列表", exact: true });
    // 嵌套确认框关闭动画期间，Radix 暂时隐藏底层会话抽屉的可访问树。
    await expect(drawer.or(trigger).first()).toBeVisible();
    if (!await drawer.isVisible()) await trigger.click();
    return drawer;
  };
  const menu = async (title: string) => {
    await (await navigation()).getByRole("button", { name: `${title}的对话菜单`, exact: true }).click();
  };
  await page.goto("/");
  await (await navigation()).getByRole("button", { name: "新建对话", exact: true }).click();
  await page.waitForURL(/\/s\/[^/]+$/);
  const id = page.url().split("/").pop()!;
  const original = await (await page.request.get(`/api/v1/sessions/${id}`)).json();
  const title = `会话管理验证 · ${testInfo.project.name}`;
  await menu(original.title);
  await page.getByRole("menuitem", { name: "重命名", exact: true }).click();
  await page.getByLabel("对话标题", { exact: true }).fill(title);
  await page.getByRole("button", { name: "保存标题", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "重命名对话", exact: true })).toHaveCount(0);
  await page.reload();
  await menu(title);
  await page.getByRole("menuitem", { name: "归档对话", exact: true }).click();
  await page.waitForURL("/");
  let nav = await navigation();
  await expect(nav.getByRole("link", { name: new RegExp(title) })).toHaveCount(0);
  await nav.getByRole("button", { name: "已归档", exact: true }).click();
  await nav.getByRole("button", { name: `${title}的对话菜单`, exact: true }).click();
  await page.getByRole("menuitem", { name: "取消归档", exact: true }).click();
  await nav.getByRole("button", { name: "返回最近", exact: true }).click();
  await nav.getByRole("link", { name: new RegExp(title) }).click();
  await page.waitForURL(`/s/${id}`);
  const restored = await (await page.request.get(`/api/v1/sessions/${id}`)).json();
  expect(restored).toMatchObject({ title, archived: false, messages: original.messages, version_count: original.version_count, requirement_state: original.requirement_state, active_run: null });
  await menu(title);
  await page.getByRole("menuitem", { name: "删除对话", exact: true }).click();
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "删除对话？", exact: true })).toHaveCount(0);
  expect((await page.request.get(`/api/v1/sessions/${id}`)).ok()).toBeTruthy();
  await menu(title);
  await page.getByRole("menuitem", { name: "删除对话", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "删除对话？", exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("delete-confirmation.png") });
  await page.getByRole("button", { name: "确认删除", exact: true }).click();
  await page.waitForURL("/");
  expect((await page.request.get(`/api/v1/sessions/${id}`)).status()).toBe(404);
  await page.reload();
  nav = await navigation();
  await expect(nav.getByRole("link", { name: new RegExp(title) })).toHaveCount(0);
  await nav.getByRole("button", { name: "已归档", exact: true }).click();
  await expect(nav.getByRole("link", { name: new RegExp(title) })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
});
