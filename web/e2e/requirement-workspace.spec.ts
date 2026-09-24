import { expect, test, type Page } from "@playwright/test";
import type { RequirementOperation, Session } from "../src/lib/api/types";

// Spec 5 返工的离线浏览器回归(全路由 mock,不访问真实模型):
// ① 批量编辑只提交实际改动,预算未知可单独保存;
// ② Builder 运行中可编辑保存需求,但不能再次确认启动;
// ③ 有效系统默认显示为“系统默认”;
// ④ 同轮补齐最后条件并请求开始时,presentation.action 不因会话缓存
//    readiness 滞后而丢失;核定面板不重复标题、无重叠关闭按钮。

type RequirementFields = NonNullable<Session["requirement_state"]>["fields"];
const fields = (overrides: RequirementFields) => overrides;

function sessionFixture(overrides: Partial<Session> = {}): Session {
  return {
    schema_version: 1, id: "session-rw", title: "返工回归", phase: "collecting", created_at: "2026-09-24T01:00:00Z", updated_at: "2026-09-24T01:00:00Z", version_count: 0,
    messages: [], pending_requirement: null, active_run: null, last_error: null, recovery_phase: null, degraded: false,
    // 真实后端形状:所有已知字段都以 unknown 键存在(未知 ≠ 无键)。
    requirement_state: { schema_version: 2, revision: 1, fields: fields({
      "use_case.type": { status: "active", value: "gaming", strength: "must", scope: "session" },
      budget_cny: { status: "unknown" }, budget_flex: { status: "unknown" }, budget_basis: { status: "unknown" },
      "use_case.titles": { status: "unknown" }, "use_case.resolution": { status: "unknown" },
      "use_case.performance_goal": { status: "unknown" }, "use_case.fps_target": { status: "unknown" },
      existing_parts: { status: "unknown" }, owned_parts: { status: "unknown" },
      "brand_pref.cpu": { status: "unknown" }, "brand_pref.gpu": { status: "unknown" },
      noise_pref: { status: "unknown" }, size_pref: { status: "unknown" },
      appearance: { status: "unknown" }, notes: { status: "unknown" }, recipient: { status: "unknown" }, priority: { status: "unknown" },
    }), alternatives: [], changes: [], history: [] },
    requirement_readiness: { status: "incomplete", missing_fields: ["budget_cny"], blocking_conflicts: [], unsupported_capabilities: [], next_question: null, confirmation_eligible: false, effective_defaults: [
      { field: "budget_flex", value: 0.1, origin: "system_default" as const },
      { field: "size_pref", value: "any", origin: "system_default" as const },
      { field: "noise_pref", value: "any", origin: "system_default" as const },
      { field: "brand_pref.cpu", value: "any", origin: "system_default" as const },
      { field: "brand_pref.gpu", value: "any", origin: "system_default" as const },
      { field: "configuration_scope", value: ["tower"], origin: "system_default" as const },
    ] },
    review_spec: null, review_hash: null, effective_budget_ceiling_cny: null,
    requirement_confirmation: { status: "unconfirmed", confirmed_revision: null, confirmed_at: null, confirmed_review_hash: null, review_diff: null },
    build_relation: { status: "none", version: null, snapshot_id: null, review_hash: null, builder_input_hash: null, retry_run_id: null },
    ...overrides,
  };
}

const readyReadiness = (missing: string[] = []): Session["requirement_readiness"] => ({
  status: missing.length ? "incomplete" : "ready", missing_fields: missing, blocking_conflicts: [], unsupported_capabilities: [], next_question: null,
  confirmation_eligible: missing.length === 0,
  effective_defaults: [
    { field: "budget_flex", value: 0.1, origin: "system_default" as const },
    { field: "size_pref", value: "any", origin: "system_default" as const },
    { field: "noise_pref", value: "any", origin: "system_default" as const },
    { field: "brand_pref.cpu", value: "any", origin: "system_default" as const },
    { field: "brand_pref.gpu", value: "any", origin: "system_default" as const },
    { field: "configuration_scope", value: ["tower"], origin: "system_default" as const },
  ],
});

async function showRequirements(page: Page) {
  const region = page.getByRole("region", { name: "需求状态", exact: true });
  if (await region.isVisible()) return region;
  if ((page.viewportSize()?.width ?? 1440) >= 1024) {
    await page.getByRole("tab", { name: "需求状态" }).click();
  } else {
    await page.getByRole("button", { name: "打开需求状态" }).click();
    await expect(page.getByRole("dialog", { name: "需求状态", exact: true })).toBeVisible();
  }
  await expect(region).toBeVisible();
  return region;
}

test.beforeEach(async ({ page }) => {
  await page.route("**/readyz", (route) => route.fulfill({ json: { schema_version: 1, status: "ready", dependencies: { postgres: "ok", redis: "ok", buildsvc: "ok" } } }));
  await page.route("**/api/v1/sessions/session-rw/builds", (route) => route.fulfill({ json: { schema_version: 1, builds: [] } }));
});

test("batch editor submits only real changes and saves without a budget", async ({ page }) => {
  const patches: { expected_revision: number; operations: RequirementOperation[] }[] = [];
  // 加载即带活动 screening run:应用按合同进入 2 秒会话轮询;编辑器打开后
  // 轮询响应把 revision 翻到 2,模拟编辑期间缓存被并发刷新。
  const screeningRun = { schema_version: 1 as const, id: "run-screen-2", session_id: "session-rw", kind: "screening" as const, status: "running" as const, started_at: "2026-09-24T02:00:01Z", events_url: "/api/v1/runs/run-screen-2/events" };
  // active_run 从加载就存在:refetchInterval 轮询从加载即生效,编辑器打开后
  // 轮询响应把 revision 翻到 2,模拟编辑期间缓存被并发刷新。
  let editorOpened = false;
  let postOpenSessionGets = 0;
  await page.route("**/api/v1/sessions/session-rw", (route) => {
    if (editorOpened) postOpenSessionGets++;
    route.fulfill({ json: sessionFixture({ active_run: screeningRun, requirement_state: { ...sessionFixture().requirement_state!, revision: editorOpened ? 2 : 1 } }) });
  });
  await page.route("**/api/v1/runs/run-screen-2/events", (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: 'id: 1-0\nevent: run.started\ndata: {"schema_version":1,"run_id":"run-screen-2","timestamp":"2026-09-24T02:00:01Z","payload":{}}\n\n' }));
  await page.route("**/api/v1/sessions/session-rw/requirement-state", async (route) => {
    if (route.request().method() !== "PATCH") return route.fulfill({ status: 405 });
    const body = route.request().postDataJSON() as { expected_revision: number; operations: RequirementOperation[] };
    patches.push(body);
    await route.fulfill({ json: sessionFixture({ active_run: screeningRun, requirement_state: { ...sessionFixture().requirement_state!, revision: 2 } }) });
  });
  await page.goto("/s/session-rw");
  await showRequirements(page);
  await page.getByRole("button", { name: "编辑全部", exact: true }).click();
  const editor = page.getByRole("dialog", { name: "编辑全部需求", exact: true });
  await expect(editor).toBeVisible();
  editorOpened = true;
  // 预算未知(空)不阻塞表单;只把尺寸改成 ATX,其余系统默认预填不动。
  await expect(editor.getByLabel("预算（元）")).toHaveValue("");
  // 运行轮询把缓存刷到 revision 2 后,输入不丢;保存必须仍按打开时的
  // revision=1 提交,而不是缓存里的 2——否则并发修改会被新 revision
  // 静默覆盖而非以 409 暴露。
  await expect.poll(() => postOpenSessionGets, { timeout: 10_000 }).toBeGreaterThanOrEqual(1);
  await expect(editor.getByLabel("预算（元）")).toHaveValue("");
  await editor.getByLabel("尺寸").selectOption("atx");
  await editor.getByRole("button", { name: "保存修改", exact: true }).click();
  await expect(editor).toHaveCount(0);
  expect(patches).toHaveLength(1);
  expect(patches[0].expected_revision).toBe(1);
  expect(patches[0].operations).toEqual([{ op: "set", field: "size_pref", value: "atx", strength: "prefer", kind: "constraint" }]);
});

test("system defaults render as 系统默认 instead of 未指定", async ({ page }) => {
  await page.route("**/api/v1/sessions/session-rw", (route) => route.fulfill({ json: sessionFixture() }));
  await page.goto("/s/session-rw");
  const panel = await showRequirements(page);
  // 偏好组在全部未指定时折叠,先展开再断言。
  const prefsSummary = panel.getByText("可选偏好", { exact: false }).first();
  if (await prefsSummary.isVisible().catch(() => false)) await prefsSummary.click();
  for (const [label, key] of [["尺寸", "size_pref"], ["静音", "noise_pref"], ["CPU 品牌", "brand_pref.cpu"], ["显卡品牌", "brand_pref.gpu"]] as const) {
    const row = panel.locator(`[data-field-row="${key}"]`);
    await expect(row).toContainText(label);
    await expect(row).toContainText("系统默认");
    await expect(row).not.toContainText("未指定");
    await expect(row).toContainText("不限");
  }
  await expect(panel.getByText("configuration_scope")).toHaveCount(0);
});

test("editing stays available while the builder runs, but confirm does not", async ({ page }) => {
  const runningRun = { schema_version: 1 as const, id: "run-build", session_id: "session-rw", kind: "build" as const, status: "running" as const, started_at: "2026-09-24T01:00:01Z", events_url: "/api/v1/runs/run-build/events" };
  const patches: { expected_revision: number; operations: RequirementOperation[] }[] = [];
  let revision = 3;
  const state = () => sessionFixture({
    phase: "building",
    active_run: runningRun,
    requirement_state: {
      schema_version: 2, revision,
      fields: fields({
        budget_cny: { status: "active", value: 8000, strength: "must", scope: "session", source: { kind: "chat", message_id: "m1", quote: "8000 元" } },
        "use_case.type": { status: "active", value: "gaming", strength: "must", scope: "session" },
        "use_case.resolution": { status: "active", value: "2K", strength: "must", scope: "session" },
        existing_parts: { status: "active", value: [], strength: "must", scope: "session" },
      }),
      alternatives: [], changes: [], history: [],
    },
    requirement_readiness: readyReadiness(),
    review_spec: { schema_version: 2, budget_cny: 8000, budget_flex: 0.1, configuration_scope: ["tower"], use_case: { type: "gaming", titles: [], resolution: "2K", performance_goal: "balanced" }, size_pref: "any", noise_pref: "any", existing_parts: [], priority: [], notes: "" },
    review_hash: "hash-r",
    effective_budget_ceiling_cny: 8800,
    requirement_confirmation: { status: "confirmed", confirmed_revision: 2, confirmed_at: "2026-09-24T01:00:00Z", confirmed_review_hash: "hash-r", review_diff: [] },
    build_relation: { status: "running", version: null, snapshot_id: "snap-1", review_hash: "hash-r", builder_input_hash: "b-1", retry_run_id: null },
  });
  await page.route("**/api/v1/sessions/session-rw", (route) => route.fulfill({ json: state() }));
  await page.route("**/api/v1/sessions/session-rw/requirement-state", async (route) => {
    const body = route.request().postDataJSON() as { expected_revision: number; operations: RequirementOperation[] };
    patches.push(body);
    revision += 1;
    await route.fulfill({ json: state() });
  });
  await page.route("**/api/v1/runs/run-build/events", (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: 'id: 1-0\nevent: run.started\ndata: {"schema_version":1,"run_id":"run-build","timestamp":"2026-09-24T01:00:01Z","payload":{}}\n\n' }));
  await page.route("**/api/v1/sessions/session-rw/requirement/confirm", (route) => route.fulfill({ status: 409, contentType: "application/problem+json", json: { type: "/problems/session_busy", title: "会话正在运行", status: 409, code: "session_busy", detail: "已有任务在执行。", request_id: "req-1" } }));

  await page.goto("/s/session-rw");
  const panel = await showRequirements(page);
  await expect(panel.getByTestId("requirement-primary")).toContainText("正在生成并校验配置");
  await expect(panel.getByTestId("requirement-primary").getByRole("button", { name: /核对当前需求|重新生成|重新核定/ })).toHaveCount(0);
  // 行内编辑在运行中可用并可保存。
  const editBudget = panel.getByRole("button", { name: "修改预算", exact: true });
  await expect(editBudget).toBeEnabled();
  await editBudget.click();
  await panel.getByLabel("编辑预算", { exact: true }).fill("7500");
  await panel.getByRole("button", { name: "保存需求", exact: true }).click();
  await expect(patches).toHaveLength(1);
  // 完整需求抽屉同样可保存,且只提交实际改动。
  await page.getByRole("button", { name: "编辑全部", exact: true }).click();
  const editor = page.getByRole("dialog", { name: "编辑全部需求", exact: true });
  await editor.getByLabel("静音").selectOption("silent");
  await editor.getByRole("button", { name: "保存修改", exact: true }).click();
  await expect(editor).toHaveCount(0);
  expect(patches[1].operations).toEqual([{ op: "set", field: "noise_pref", value: "silent", strength: "prefer", kind: "constraint" }]);
});

test("open_requirement_review survives stale session cache from the same turn", async ({ page }) => {
  // 发送前缓存:缺预算 ineligible;同轮消息补齐预算并请求开始,SSE 按序发出
  // requirement.updated → presentation.action,而会话轮询仍短暂返回旧快照。
  let sessionGets = 0;
  const stale = sessionFixture();
  const fresh = sessionFixture({
    phase: "requirement_ready",
    messages: [
      { schema_version: 1, id: "m1", role: "user", content: "预算 8000，开始吧", created_at: "2026-09-24T01:00:01Z" },
      { schema_version: 1, id: "m2", role: "assistant", content: "需求已经齐备，现在可以核定需求；确认后再生成配置。", created_at: "2026-09-24T01:00:02Z" },
    ],
    requirement_state: {
      schema_version: 2, revision: 2,
      fields: fields({
        budget_cny: { status: "active", value: 8000, strength: "must", scope: "session", source: { kind: "chat", message_id: "m1", quote: "预算 8000" } },
        "use_case.type": { status: "active", value: "gaming", strength: "must", scope: "session" },
        "use_case.resolution": { status: "active", value: "2K", strength: "must", scope: "session" },
        existing_parts: { status: "active", value: [], strength: "must", scope: "session" },
      }),
      alternatives: [], changes: [], history: [],
    },
    requirement_readiness: readyReadiness(),
    review_spec: { schema_version: 2, budget_cny: 8000, budget_flex: 0.1, configuration_scope: ["tower"], use_case: { type: "gaming", titles: [], resolution: "2K", performance_goal: "balanced" }, size_pref: "any", noise_pref: "any", existing_parts: [], priority: [], notes: "" },
    review_hash: "hash-f",
    effective_budget_ceiling_cny: 8800,
  });
  await page.route("**/api/v1/sessions/session-rw", (route) => route.fulfill({ json: sessionGets++ < 3 ? stale : fresh }));
  const screeningRun = { schema_version: 1, id: "run-screen", session_id: "session-rw", kind: "screening" as const, status: "running" as const, started_at: "2026-09-24T01:00:01Z", events_url: "/api/v1/runs/run-screen/events" };
  await page.route("**/api/v1/sessions/session-rw/messages", (route) => route.fulfill({ status: 202, json: screeningRun }));
  await page.route("**/api/v1/runs/run-screen/events", (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: [
    'id: 1-0\nevent: run.started\ndata: {"schema_version":1,"run_id":"run-screen","timestamp":"2026-09-24T01:00:01Z","payload":{}}\n\n',
    'id: 2-0\nevent: requirement.updated\ndata: {"schema_version":1,"run_id":"run-screen","timestamp":"2026-09-24T01:00:02Z","payload":{"revision":2}}\n\n',
    'id: 3-0\nevent: assistant.completed\ndata: {"schema_version":1,"run_id":"run-screen","timestamp":"2026-09-24T01:00:02Z","payload":{}}\n\n',
    'id: 4-0\nevent: presentation.action\ndata: {"schema_version":1,"run_id":"run-screen","timestamp":"2026-09-24T01:00:02Z","payload":{"action":"open_requirement_review"}}\n\n',
    'id: 5-0\nevent: run.completed\ndata: {"schema_version":1,"run_id":"run-screen","timestamp":"2026-09-24T01:00:03Z","payload":{}}\n\n',
  ].join("") }));

  await page.goto("/s/session-rw");
  await page.getByLabel("输入需求或改单内容").fill("预算 8000，开始吧");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  // 核定面板必须被打开(动作不因缓存滞后丢失),并以最新快照渲染。
  const review = page.getByRole("dialog", { name: "核定当前需求", exact: true });
  await expect(review).toBeVisible();
  await expect(review.getByText("¥8,800", { exact: true })).toBeVisible();
  await expect(review.getByRole("button", { name: "确认并开始配置", exact: true })).toBeEnabled();
  // 顺带验收:标题只出现一次,默认右上角 X 关闭按钮不再与“返回修改”重叠。
  await expect(page.getByText("核定当前需求", { exact: true })).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Close" })).toHaveCount(0);
  // 头部 DialogClose 按钮带 aria-label;面板底部还有同名文本按钮,用 label 精确匹配头部那个。
  await expect(review.getByLabel("返回修改", { exact: true })).toBeVisible();
});
