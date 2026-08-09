import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("anonymous requirement confirmation reaches a validated build", async ({ page }, testInfo) => {
  let phase: "requirement_ready" | "ready" = "requirement_ready";
  let versionCount = 0;
  const requirement = { schema_version: 1, budget_cny: 8000, budget_flex: 0.1, use_case: { type: "gaming", titles: ["黑神话：悟空"], resolution: "2K", fps_target: 60 }, size_pref: "any", noise_pref: "normal", brand_pref: { cpu: "any", gpu: "any" }, existing_parts: [], priority: ["gpu"], notes: "" };
  const summary = { schema_version: 1, version: 1, parent_version: null, intent: "初始配置", total_cny: "7899.00", snapshot_date: "2026-08-09", overall_status: "pass", created_at: "2026-08-09T10:00:00Z" };
  const session = () => ({ schema_version: 1, id: "session-1", title: "8000 元 2K 玩黑神话", phase, created_at: "2026-08-09T10:00:00Z", updated_at: "2026-08-09T10:00:00Z", version_count: versionCount, messages: [{ schema_version: 1, id: "00000000-0000-4000-8000-000000000001", role: "user", content: "8000 元，2K 玩黑神话：悟空", created_at: "2026-08-09T10:00:00Z" }], pending_requirement: phase === "requirement_ready" ? requirement : null, active_run: null, last_error: null, recovery_phase: null, degraded: false });
  await page.route("**/readyz", (route) => route.fulfill({ json: { schema_version: 1, status: "ready", dependencies: { postgres: "ok", redis: "ok", buildsvc: "ok" } } }));
  await page.route("**/api/v1/sessions/session-1", (route) => route.fulfill({ json: session() }));
  await page.route("**/api/v1/sessions/session-1/builds", (route) => route.fulfill({ json: { schema_version: 1, builds: versionCount ? [summary] : [] } }));
  await page.route("**/api/v1/sessions/session-1/builds/1", (route) => route.fulfill({ json: { schema_version: 1, summary, requirement, parts: ["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"].map((category, index) => ({ category, sku: `sku-${index}`, name: `${category.toUpperCase()} 示例零件`, quantity: 1, unit_price_cny: "900.00", subtotal_cny: "900.00", rationale: "满足需求" })), quote: { snapshot_date: "2026-08-09", total_cny: "7899.00", budget_cny: "8000.00", budget_delta_cny: "101.00", missing_count: 0, missing_skus: [] }, validation: { overall_status: "pass", checks: ["SOCKET_MATCH", "CHIPSET_SUPPORT", "MEMORY_GENERATION", "MEMORY_SPEED", "GPU_CLEARANCE", "COOLER_CLEARANCE", "PSU_HEADROOM", "FORM_FACTOR_SUPPORT", "M2_SLOT_CAPACITY", "GPU_POWER_CONNECTORS", "DISPLAY_OUTPUT", "COOLER_THERMAL_CAPACITY"].map((rule_id) => ({ rule_id, outcome: "pass", severity: "none", observed: {}, missing_fields: [], detail: "检查通过" })) }, disclaimers: ["价格为快照参考。", "购买前核对接口与尺寸。", "实际性能受环境影响。"] } }));
  await page.route("**/api/v1/sessions/session-1/requirement/confirm", async (route) => {
    phase = "ready"; versionCount = 1;
    await route.fulfill({ status: 202, json: { schema_version: 1, id: "run-1", session_id: "session-1", kind: "build", status: "running", started_at: "2026-08-09T10:00:01Z", events_url: "/api/v1/runs/run-1/events" } });
  });
  await page.route("**/api/v1/runs/run-1/events", (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "id: 1-0\nevent: run.started\ndata: {\"schema_version\":1,\"run_id\":\"run-1\",\"timestamp\":\"2026-08-09T10:00:01Z\",\"payload\":{}}\n\nid: 2-0\nevent: build.saved\ndata: {\"schema_version\":1,\"run_id\":\"run-1\",\"timestamp\":\"2026-08-09T10:00:02Z\",\"payload\":{\"version\":1}}\n\nid: 3-0\nevent: run.completed\ndata: {\"schema_version\":1,\"run_id\":\"run-1\",\"timestamp\":\"2026-08-09T10:00:03Z\",\"payload\":{}}\n\n" }));

  await page.goto("/s/session-1");
  if (testInfo.project.name !== "desktop") await page.getByRole("button", { name: "配置" }).first().click();
  await expect(page.getByRole("heading", { name: "确认装机需求" })).toBeVisible();
  await page.getByRole("button", { name: "确认并生成配置" }).click();
  await expect(page.getByText("¥7899.00")).toBeVisible();
  await page.getByRole("tab", { name: "校验" }).click();
  await expect(page.getByText("处理器与主板接口")).toBeVisible();
  await expect(page.getByText("购买前说明")).toBeVisible();
  const violations = await new AxeBuilder({ page }).analyze();
  expect(violations.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("workspace.png"), fullPage: true });
});
