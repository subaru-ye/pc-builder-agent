import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("finished reply feedback survives a failed save and can be edited", async ({ page }, testInfo) => {
  const runID = "00000000-0000-4000-8000-000000000001";
  const date = "2026-09-09T10:00:00Z";
  let feedback: Record<string, unknown> | null = null;
  let attempts = 0;
  const session = { schema_version: 1, id: "feedback-session", title: "初筛反馈", phase: "collecting", created_at: date, updated_at: date, version_count: 0,
    messages: [{ schema_version: 1, id: "00000000-0000-4000-8000-000000000002", role: "assistant", content: "请确认您的预算和显示器分辨率。", run_id: runID, created_at: date }],
    pending_requirement: null, active_run: null, last_error: null, recovery_phase: null, degraded: false };
  await page.route("**/readyz", (route) => route.fulfill({ json: { schema_version: 1, status: "ready", dependencies: { postgres: "ok", redis: "ok", buildsvc: "ok" } } }));
  await page.route("**/api/v1/sessions/feedback-session", (route) => route.fulfill({ json: session }));
  await page.route("**/api/v1/sessions/feedback-session/builds", (route) => route.fulfill({ json: { schema_version: 1, builds: [] } }));
  await page.route(`**/api/v1/runs/${runID}/feedback`, async (route) => {
    if (route.request().method() === "POST") {
      attempts++;
      if (attempts === 1) return route.fulfill({ status: 503, json: { code: "unavailable", title: "暂时无法保存", status: 503 } });
      feedback = { ...route.request().postDataJSON(), id: "00000000-0000-4000-8000-000000000003", run_id: runID, fingerprint: "a".repeat(64), created_at: date, updated_at: date };
    }
    return route.fulfill({ json: { schema_version: 1, feedback } });
  });
  await page.goto("/s/feedback-session");
  await page.getByRole("button", { name: "不满意" }).click();
  await page.getByLabel("补充说明", { exact: false }).fill("已经提供了分辨率，不应该重复追问。");
  await page.getByRole("button", { name: "提交反馈" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "说明仍保留" })).toBeVisible();
  await expect(page.getByLabel("补充说明", { exact: false })).toHaveValue("已经提供了分辨率，不应该重复追问。");
  await page.getByRole("button", { name: "提交反馈" }).click();
  await expect(page.getByText("反馈已保存")).toBeVisible();
  await page.getByRole("button", { name: "已反馈 · 修改" }).click();
  await expect(page.getByLabel("补充说明", { exact: false })).toHaveValue("已经提供了分辨率，不应该重复追问。");
  const result = await new AxeBuilder({ page }).analyze();
  expect(result.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("feedback.png"), fullPage: true });
  await page.reload();
  await page.getByRole("button", { name: "不满意" }).click();
  await page.getByRole("button", { name: "载入已保存内容" }).click();
  await expect(page.getByLabel("补充说明", { exact: false })).toHaveValue("已经提供了分辨率，不应该重复追问。");
  expect(attempts).toBe(2);
});
