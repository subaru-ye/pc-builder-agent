import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { closeDetails } from "../e2e-requirements/helpers";

test("anonymous requirement confirmation reaches a validated build", async ({ page }, testInfo) => {
  let phase: "requirement_ready" | "ready" = "requirement_ready";
  let versionCount = 0;
  const reviewSpec = { schema_version: 2, budget_cny: 8000, budget_flex: 0.1, configuration_scope: ["tower"], use_case: { type: "gaming", titles: ["黑神话：悟空"], resolution: "2K", performance_goal: "balanced", fps_target: 60 }, size_pref: "any", noise_pref: "normal", brand_pref: { cpu: "any", gpu: "any" }, existing_parts: [], priority: ["gpu"], notes: "" };
  const requirementState = {
    schema_version: 2, revision: 3,
    fields: {
      budget_cny: { status: "active", value: 8000, strength: "must", scope: "session", source: { kind: "chat", message_id: "00000000-0000-4000-8000-000000000001", quote: "8000 元" } },
      "use_case.type": { status: "active", value: "gaming", strength: "must", scope: "session" },
      "use_case.resolution": { status: "active", value: "2K", strength: "must", scope: "session" },
      existing_parts: { status: "active", value: [], strength: "must", scope: "session" },
    },
    alternatives: [], changes: [], history: [],
  };
  const summary = { schema_version: 1, version: 1, parent_version: null, intent: "初始配置", total_cny: "7899.00", snapshot_date: "2026-08-09", overall_status: "pass", created_at: "2026-08-09T10:00:00Z" };
  const session = () => ({
    schema_version: 1, id: "session-1", title: "8000 元 2K 玩黑神话", phase, created_at: "2026-08-09T10:00:00Z", updated_at: "2026-08-09T10:00:00Z", version_count: versionCount,
    messages: [{ schema_version: 1, id: "00000000-0000-4000-8000-000000000001", role: "user", content: "8000 元，2K 玩黑神话：悟空", created_at: "2026-08-09T10:00:00Z" }],
    pending_requirement: null, active_run: null, last_error: null, recovery_phase: null, degraded: false,
    requirement_state: requirementState,
    requirement_readiness: { status: "ready", missing_fields: [], blocking_conflicts: [], unsupported_capabilities: [], next_question: null, confirmation_eligible: true, effective_defaults: [{ field: "size_pref", value: "any", origin: "system_default" }] },
    review_spec: reviewSpec,
    review_hash: "hash-1",
    effective_budget_ceiling_cny: 8800,
    requirement_confirmation: { status: "unconfirmed", confirmed_revision: null, confirmed_at: null, confirmed_review_hash: null, review_diff: null },
    build_relation: { status: "none", version: null, snapshot_id: null, review_hash: null, builder_input_hash: null, retry_run_id: null },
  });
  await page.route("**/readyz", (route) => route.fulfill({ json: { schema_version: 1, status: "ready", dependencies: { postgres: "ok", redis: "ok", buildsvc: "ok" } } }));
  await page.route("**/api/v1/sessions/session-1", (route) => route.fulfill({ json: session() }));
  await page.route("**/api/v1/sessions/session-1/builds", (route) => route.fulfill({ json: { schema_version: 1, builds: versionCount ? [summary] : [] } }));
  await page.route("**/api/v1/sessions/session-1/builds/1", (route) => route.fulfill({ json: { schema_version: 1, summary, requirement: { schema_version: 2, requirement_state: requirementState, effective_constraints: { spec: reviewSpec, defaults: [] } }, parts: ["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"].map((category, index) => ({ category, sku: `sku-${index}`, name: `${category.toUpperCase()} 示例零件`, quantity: 1, unit_price_cny: "900.00", subtotal_cny: "900.00", rationale: "满足需求" })), quote: { snapshot_date: "2026-08-09", total_cny: "7899.00", budget_cny: "8000.00", budget_delta_cny: "101.00", missing_count: 0, missing_skus: [] }, validation: { overall_status: "pass", checks: ["SOCKET_MATCH", "CHIPSET_SUPPORT", "MEMORY_GENERATION", "MEMORY_SPEED", "GPU_CLEARANCE", "COOLER_CLEARANCE", "PSU_HEADROOM", "FORM_FACTOR_SUPPORT", "M2_SLOT_CAPACITY", "GPU_POWER_CONNECTORS", "DISPLAY_OUTPUT", "COOLER_THERMAL_CAPACITY"].map((rule_id) => ({ rule_id, outcome: "pass", severity: "none", observed: {}, missing_fields: [], detail: "检查通过" })) }, disclaimers: ["价格为快照参考。", "购买前核对接口与尺寸。", "实际性能受环境影响。"] } }));
  await page.route("**/api/v1/sessions/session-1/builds/1/shares", (route) => {
    if (route.request().method() === "POST") return route.fulfill({ status: 201, json: { schema_version: 1, id: "00000000-0000-4000-8000-000000000099", version: 1, token: "P".repeat(43), url: `http://localhost:3000/share/${"P".repeat(43)}`, created_at: "2026-08-09T12:30:00Z", revoked_at: null } });
    return route.fulfill({ json: { schema_version: 1, shares: [] } });
  });
  let confirmBody: Record<string, unknown> | null = null;
  let confirmKey = "";
  await page.route("**/api/v1/sessions/session-1/requirement/confirm", async (route) => {
    confirmBody = route.request().postDataJSON() as Record<string, unknown>;
    confirmKey = route.request().headers()["idempotency-key"] ?? "";
    phase = "ready"; versionCount = 1;
    await route.fulfill({ status: 202, json: { schema_version: 1, id: "run-1", session_id: "session-1", kind: "build", status: "running", started_at: "2026-08-09T10:00:01Z", events_url: "/api/v1/runs/run-1/events" } });
  });
  await page.route("**/api/v1/runs/run-1/events", (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "id: 1-0\nevent: run.started\ndata: {\"schema_version\":1,\"run_id\":\"run-1\",\"timestamp\":\"2026-08-09T10:00:01Z\",\"payload\":{}}\n\nid: 2-0\nevent: requirement.confirmed\ndata: {\"schema_version\":1,\"run_id\":\"run-1\",\"timestamp\":\"2026-08-09T10:00:01Z\",\"payload\":{\"snapshot_id\":\"snapshot-1\",\"review_hash\":\"hash-1\",\"builder_input_hash\":\"builder-1\"}}\n\nid: 3-0\nevent: build.saved\ndata: {\"schema_version\":1,\"run_id\":\"run-1\",\"timestamp\":\"2026-08-09T10:00:02Z\",\"payload\":{\"version\":1}}\n\nid: 4-0\nevent: run.completed\ndata: {\"schema_version\":1,\"run_id\":\"run-1\",\"timestamp\":\"2026-08-09T10:00:03Z\",\"payload\":{}}\n\n" }));

  await page.goto("/s/session-1");
  // 需求状态是默认 Tab;确认必须显式经核定面板发生,携带服务端预览 hash。
  if (testInfo.project.name !== "desktop") await page.getByRole("button", { name: "打开需求状态" }).click();
  await expect(page.getByRole("region", { name: "需求状态", exact: true })).toBeVisible();
  await page.getByTestId("requirement-primary").getByRole("button", { name: "核对当前需求", exact: true }).click();
  const review = page.getByRole("dialog", { name: "核定当前需求", exact: true });
  await expect(review).toBeVisible();
  await expect(review.getByText("¥8,800", { exact: true })).toBeVisible();
  await review.getByRole("button", { name: "确认并开始配置", exact: true }).click();
  await expect(review).toHaveCount(0);
  expect(confirmBody).toMatchObject({ schema_version: 2, expected_revision: 3, expected_review_hash: "hash-1" });
  expect(confirmKey).not.toBe("");
  // Builder 完成只启用配置 Tab,不自动切换;用户进入后可见新配置。
  // 分享动作在顶栏;窄屏需先关闭详情抽屉,不被弹层遮挡。
  await closeDetails(page);
  await page.getByRole("button", { name: "分享配置 v1" }).click();
  await page.getByRole("button", { name: "创建当前版本分享" }).click();
  await expect(page.getByText(/只在本机/)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).not.toBeVisible();
  // 再进入配置详情核对价格与校验。
  await closeDetails(page);
  if (testInfo.project.name === "desktop") await page.getByRole("tab", { name: "配置详情" }).click();
  else await page.getByRole("button", { name: "查看配置详情", exact: true }).click();
  await expect(page.getByText("¥7899.00")).toBeVisible();
  await page.getByRole("tab", { name: "校验" }).click();
  await expect(page.getByText("处理器与主板接口")).toBeVisible();
  await expect(page.getByText("购买前说明")).toBeVisible();
  await closeDetails(page);
  const violations = await new AxeBuilder({ page }).analyze();
  expect(violations.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("workspace.png"), fullPage: true });
});
