import { expect, test } from "@playwright/test";

// Real API / empty sessions only: suggestions must never trigger a model call.
test("empty conversation suggestions and persistent sidebar resizing", async ({ page }, testInfo) => {
  const desktop = (page.viewportSize()?.width ?? 1440) >= 1024;
  await page.goto("/");
  if (!desktop) await page.getByRole("button", { name: "打开会话列表", exact: true }).click();
  const nav = desktop ? page.getByRole("complementary", { name: "会话导航", exact: true }) : page.getByRole("dialog", { name: "会话", exact: true });
  await nav.getByRole("button", { name: "新建对话", exact: true }).click();
  await page.waitForURL(/\/s\/[^/]+$/);
  const id = page.url().split("/").pop()!;
  try {
    const welcome = page.getByTestId("conversation-welcome");
    await expect(welcome).toBeVisible();
    const center = await welcome.evaluate((element) => {
      const rect = element.getBoundingClientRect();
      const scroll = element.parentElement!.parentElement!.getBoundingClientRect();
      return { x: Math.abs(rect.x + rect.width / 2 - scroll.x - scroll.width / 2), y: Math.abs(rect.y + rect.height / 2 - scroll.y - scroll.height / 2) };
    });
    expect(center.x).toBeLessThan(2);
    if (desktop) expect(center.y).toBeLessThan(2);
    const suggestions = page.locator('[aria-label="建议输入"] button');
    await expect(suggestions).toHaveCount(3);
    for (const button of await suggestions.all()) {
      await button.click();
      await expect(page.getByLabel("输入需求或改单内容", { exact: true })).toHaveValue((await button.innerText()).trim());
      await expect(page.getByLabel("输入需求或改单内容", { exact: true })).toBeFocused();
    }
    const untouched = await (await page.request.get(`/api/v1/sessions/${id}`)).json();
    expect(untouched).toMatchObject({ messages: [], version_count: 0, active_run: null });
    const left = page.getByRole("separator", { name: "调整会话列表宽度" });
    const right = page.getByRole("separator", { name: "调整右侧栏宽度" });
    if (desktop) {
      const drag = async (handle: typeof left, dx: number) => {
        const rect = (await handle.boundingBox())!;
        await page.mouse.move(rect.x + rect.width / 2, rect.y + rect.height / 2);
        await page.mouse.down();
        await page.mouse.move(rect.x + rect.width / 2 + dx, rect.y + rect.height / 2, { steps: 12 });
        await page.mouse.up();
      };
      await drag(left, 64);
      await expect(left).toHaveAttribute("aria-valuenow", "304");
      await drag(right, 80);
      await expect(right).toHaveAttribute("aria-valuenow", "400");
      await page.reload();
      await expect(left).toHaveAttribute("aria-valuenow", "304");
      await expect(right).toHaveAttribute("aria-valuenow", "400");
      await right.focus();
      await right.press("ArrowLeft");
      await expect(right).toHaveAttribute("aria-valuenow", "416");
      await page.setViewportSize({ width: 1024, height: 960 });
      await expect.poll(async () => (await page.locator("#conversation-workspace").boundingBox())!.width).toBeGreaterThanOrEqual(360);
      await drag(right, -300);
      expect((await page.locator("#conversation-workspace").boundingBox())!.width).toBeGreaterThanOrEqual(360);
      expect(await page.evaluate(() => document.body.classList.contains("workspace-resizing"))).toBe(false);
      await page.setViewportSize({ width: 1440, height: 960 });
      await left.press("Enter");
      await expect(left).toHaveAttribute("aria-valuenow", "240");
      await right.dblclick();
      await expect(right).toHaveAttribute("aria-valuenow", "480");
    } else {
      await expect(left).not.toBeVisible();
      await expect(right).not.toBeVisible();
      await expect(page.getByRole("button", { name: "打开会话列表", exact: true })).toBeVisible();
    }
    await expect(page.getByRole("button", { name: "发送", exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("workspace.png") });
  } finally {
    // Delete only the empty conversation created by this test.
    const current = await (await page.request.get(`/api/v1/sessions/${id}`)).json();
    if (current.messages?.length === 0 && current.version_count === 0) expect((await page.request.delete(`/api/v1/sessions/${id}`)).ok()).toBe(true);
  }
});
