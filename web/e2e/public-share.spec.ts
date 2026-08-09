import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const token = "P".repeat(43);

test("public share is SSR-readable and renders a deterministic PNG", async ({ page, request }, testInfo) => {
  const response = await page.goto(`/share/${token}`);
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { name: "¥7499.00 的装机配置" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "配置清单" })).toBeVisible();
  await expect(page.getByText("处理器与主板接口")).toBeVisible();
  await expect(page.getByText("本文档不构成购买建议。")).toBeVisible();
  await expect(page.getByRole("link", { name: "下载 Markdown" })).toHaveAttribute("href", `/api/v1/public/shares/${token}/export.md`);
  const image = await request.get(`/share/${token}/image`);
  expect(image.status()).toBe(200);
  expect(image.headers()["content-type"]).toContain("image/png");
  const bytes = await image.body();
  expect(bytes.subarray(1, 4).toString()).toBe("PNG");
  expect(bytes.readUInt32BE(16)).toBe(1200);
  expect(bytes.readUInt32BE(20)).toBe(630);
  const violations = await new AxeBuilder({ page }).analyze();
  expect(violations.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("public-share.png"), fullPage: true });
});

test("invalid or revoked-looking token uses the same not-found surface", async ({ page }) => {
  const response = await page.goto(`/share/${"X".repeat(43)}`);
  expect(response?.status()).toBe(404);
  await expect(page.getByRole("heading", { name: "分享链接不可用" })).toBeVisible();
  await expect(page.getByText(/不存在、格式无效或已经被撤销/)).toBeVisible();
});
