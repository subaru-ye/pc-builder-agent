import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page, type Route } from "@playwright/test";

// Requirement v2 工作台离线套件。
// 合成部分:浏览器路由内嵌最小一致产物,确定性、零依赖、零模型。
// 真实部分:需要本机已启动 evaldesk(8086)+ 带代理的 Next;产物缺失时明确跳过。
test.beforeEach(async ({ page }) => {
  // 安全边界:evaldesk API 仅 GET;不向任何外部主机发起请求。
  const outbound: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (!["127.0.0.1", "localhost"].includes(url.hostname)) outbound.push(url.href);
    if (url.pathname.startsWith("/api/evaldesk")) expect(request.method()).toBe("GET");
  });
  await page.addInitScript(() => {
    const seen: string[] = [];
    (window as unknown as { __outbound: string[] }).__outbound = seen;
    new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        const url = new URL((entry as PerformanceResourceTiming).name);
        if (!["127.0.0.1", "localhost"].includes(url.hostname)) seen.push(url.href);
      }
    }).observe({ type: "resource", buffered: true });
  });
  (page as unknown as { __outboundLog: string[] }).__outboundLog = outbound;
});

async function expectZeroOutbound(page: Page) {
  const viaObserver = await page.evaluate(() => (window as unknown as { __outbound: string[] }).__outbound);
  expect(viaObserver).toEqual([]);
}

// ---- 合成产物(内嵌路由夹具)----

const liveRun = {
  id: "a".repeat(24), dir_name: "e2e-live-ok", label: "artifacts/reqv2/e2e-live-ok",
  created_at: "2026-09-24T00:00:00Z", mode: "live", zero_model: false, regrade: false,
  superseded: false, grader_version: "reqv2-grader-e2e", splits: ["development"], repeats: 2,
  code_commit: "0".repeat(40), code_dirty: false, models: [{ role: "screening", model: "offline-model", provider: "offline" }],
  gate_passed: true, conclusion: "冻结门槛全部通过(合成夹具)。", evidence: { status: "complete", notes: [] },
  manifest_sha256: "a1b2c3d4e5f6", gates_sha256: "b1b2c3d4e5f6", max_model_requests: 400,
};
const detail = {
  ...liveRun, duration_ms: 5,
  gate_verdicts: [
    { layer: "reducer", metric: "deterministic_pass", actual: "1/1", threshold: "100%", passed: true, evaluable: true },
    { layer: "extraction", metric: "operation_precision", actual: "0.95", threshold: "≥0.90", passed: true, evaluable: true },
  ],
  per_layer: {
    reducer: { cases: 1, passed: 1, skipped: 0, vetoes: 0, failure_classifications: {} },
    extraction: { cases: 1, passed: 1, skipped: 0, vetoes: 0, failure_classifications: {} },
  },
  model_quality: { extraction: { cases: 1, op_precision: 0.95 } },
  usage: { model_calls: 12, provider_errors: {}, tokens_known: 900, tokens_all_known: true, latency_p50_ms: 100, latency_p95_ms: 200 },
  limitations: ["合成夹具:仅验证工作台展示。"],
  cases: [{ layer: "reducer", id: "rd-1", split: "development", session: "rd-dev-1", repeats: [1], pass_k: true, skipped: false, vetoes: 0, failures: [] }],
  integrity_checks: [{ check: "grader_version", state: "ok" }, { check: "manifest.json", state: "ok" }],
  gate_thresholds: { version: "gates-e2e-v1" },
};
const caseDetail = {
  run: liveRun.id, layer: "reducer", case: "rd-1", split: "development", session: "rd-dev-1", pass_k: true,
  repeats: [{ repeat: 1, pass: true, vetoes: [], assertions: [{ name: "reducer:no_error", pass: true }, { name: "reducer:revision", pass: true, detail: "got delta=1", classification: "behavior_failure" }], turns: [{ index: 1, reply: "已记录预算 7500", operations: ['{"op":"set","field":"budget_cny","value":7500}'], turn_signals: [], screen_model_called: false }], observation_rest: { readiness: { status: "ready" } } }],
  frozen: { layer: "reducer", id: "rd-1", title: "预算改为 7500", split: "development", session: "rd-dev-1", rationale: "e2e", fields: { user_message: "预算改成7500", expected: { revision_delta: 1 } }, sha256: "ff".repeat(16) },
  integrity: { status: "complete", notes: [] },
};
const zeroRun = {
  ...liveRun, id: "b".repeat(24), dir_name: "e2e-zero-model", label: "artifacts/reqv2/e2e-zero-model",
  mode: "deterministic", zero_model: true, gate_passed: false, conclusion: "确定性层未通过(合成夹具)。",
};
const zeroDetail = {
  ...detail, ...zeroRun,
  gate_verdicts: [{ layer: "reducer", metric: "deterministic_pass", actual: "0/1", threshold: "100%", passed: false, evaluable: true }],
  per_layer: { reducer: { cases: 1, passed: 0, skipped: 0, vetoes: 0, failure_classifications: { behavior_failure: 1 } } },
  cases: [{ layer: "reducer", id: "rd-1", split: "development", session: "rd-dev-1", repeats: [1], pass_k: false, skipped: false, vetoes: 0, failures: ["behavior_failure"] }],
};
const archivedRun = { ...zeroRun, id: "c".repeat(24), dir_name: "superseded-e2e-archive", label: "artifacts/reqv2/superseded-e2e-archive", superseded: true };
const archivedDetail = { ...zeroDetail, ...archivedRun };

async function installFixtureRoutes(page: Page) {
  const json = (route: Route, value: unknown) => route.fulfill({ json: value });
  // 注意:先注册 run*(前缀更宽),后注册 runs 让精确目录优先。
  await page.route("**/api/evaldesk/requirement-v2/run*", (route) => {
    const id = new URL(route.request().url()).searchParams.get("id");
    if (id === zeroRun.id) return json(route, zeroDetail);
    if (id === archivedRun.id) return json(route, archivedDetail);
    return json(route, detail);
  });
  await page.route("**/api/evaldesk/requirement-v2/runs", (route) => json(route, { runs: [liveRun, zeroRun, archivedRun], warnings: [] }));
  await page.route("**/api/evaldesk/requirement-v2/case*", (route) => json(route, caseDetail));
  await page.route("**/api/evaldesk/requirement-v2/compare*", (route) => json(route, {
    strict: false,
    reasons: [
      "运行类型不同(live vs deterministic);live、零模型、replay/regrade 语义不同,只能并排查看各自证据",
      "live 与零模型运行对比不能得出模型质量改善或退步结论",
    ],
    baseline: liveRun, candidate: zeroRun,
    case_set: { baseline_cases: 1, candidate_cases: 1, common: 1 },
  }));
  await page.route("**/api/evaldesk/legacy-runs**", (route) => json(route, { runs: [], warnings: [] }));
  await page.route("**/api/evaldesk/runs", (route) => json(route, { runs: [], warnings: [], currentGrader: "independent-v2" }));
}

test.describe("Requirement v2 合成产物", () => {
  test("运行目录默认显示 v2,superseded 折叠,损坏产物不伪造结论", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval");
    await expect(page.getByRole("heading", { name: "Requirement v2 评估工作台" })).toBeVisible();
    await expect(page.getByTestId("reqv2-run-e2e-live-ok")).toBeVisible();
    await expect(page.getByTestId("reqv2-run-superseded-e2e-archive")).toBeHidden();
    await page.getByText(/superseded 历史归档/).click();
    await expect(page.getByTestId("reqv2-run-superseded-e2e-archive")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("reqv2-catalog.png"), fullPage: true });
    await expectZeroOutbound(page);
  });

  test("运行详情按顺序呈现身份、结论、门槛、六层,逐题与对比可进入", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval");
    await page.getByTestId("reqv2-run-e2e-live-ok").click();
    await expect(page.getByRole("heading", { name: "e2e-live-ok" })).toBeVisible();
    await expect(page.getByTestId("reqv2-conclusion")).toContainText("冻结门槛全部通过");
    await expect(page.getByTestId("gate-reducer-deterministic_pass")).toContainText("PASS");
    await expect(page.getByTestId("layer-reducer")).toContainText("Pass^k");
    await expect(page.getByText("模型层指标", { exact: false })).toBeVisible();
    await expect(page.getByText("调用与用量")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("reqv2-run-detail.png"), fullPage: true });
    await page.getByRole("button", { name: "全部层" }).click();
    await page.getByRole("button", { name: /rd-1/ }).click();
    await expect(page.getByText("reducer / rd-1")).toBeVisible();
    await expect(page.getByText("reducer:no_error")).toBeVisible();
    await expect(page.getByText("预算改为 7500")).toBeVisible();
    await page.getByRole("button", { name: "返回运行" }).click();
    await expect(page.getByRole("button", { name: "选择两次运行对比" })).toBeVisible();
    await page.getByRole("button", { name: "选择两次运行对比" }).click();
    await expect(page.getByRole("heading", { name: "同身份对比" })).toBeVisible();
  });

  test("零模型运行保持 UNEVALUABLE 与确定性层身份,不显示模型运行指标", async ({ page }) => {
    await installFixtureRoutes(page);
    await page.goto("/eval");
    await page.getByText(/superseded 历史归档/).click();
    await page.getByTestId("reqv2-run-e2e-zero-model").click();
    await expect(page.getByRole("heading", { name: "e2e-zero-model" })).toBeVisible();
    await expect(page.getByText(/冻结门槛未全过/)).toBeVisible();
    await expect(page.getByText("零模型运行:模型调用")).toBeVisible();
    await expect(page.getByTestId("layer-reducer")).toContainText("behavior_failure");
  });

  test("跨语义对比被拒绝并给出原因,不产出改善幅度", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval");
    await page.getByTestId("reqv2-run-e2e-live-ok").click();
    await page.getByRole("button", { name: "选择两次运行对比" }).click();
    await page.getByLabel("基线运行").selectOption(liveRun.id);
    await page.getByLabel("候选运行").selectOption(zeroRun.id);
    await expect(page.getByTestId("compare-incomparable")).toBeVisible();
    await expect(page.getByText(/live、零模型、replay\/regrade 语义不同/)).toBeVisible();
    await expect(page.getByText(/(提升|改善|进步)\s*\d+(\.\d+)?%/)).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath("reqv2-compare-incomparable.png") });
  });

  test("键盘完成运行选择与返回,1440/768/375 视口与 axe 通过,亮暗主题可用", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    for (const viewport of [{ width: 1440, height: 960 }, { width: 768, height: 1024 }, { width: 375, height: 812 }]) {
      await page.setViewportSize(viewport);
      await page.goto("/eval");
      await expect(page.getByTestId("reqv2-run-e2e-live-ok")).toBeVisible();
      await page.getByTestId("reqv2-run-e2e-live-ok").focus();
      await page.keyboard.press("Enter");
      await expect(page.getByRole("heading", { name: "e2e-live-ok" })).toBeVisible();
      await page.getByRole("button", { name: "返回运行目录" }).click();
      await expect(page.getByTestId("reqv2-run-e2e-live-ok")).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
      const violations = await new AxeBuilder({ page }).analyze();
      expect(violations.violations.filter((item) => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
    }
    await page.evaluate(() => localStorage.setItem("pcb-theme", "light"));
    await page.reload();
    await expect(page.locator("html")).toHaveClass(/\blight\b/);
    await page.screenshot({ path: testInfo.outputPath("reqv2-light-theme.png"), fullPage: true });
    await expectZeroOutbound(page);
  });

  test("历史评估入口保持可达", async ({ page }) => {
    await installFixtureRoutes(page);
    await page.route("**/api/evaldesk/runs", (route) => route.fulfill({ json: { runs: [], warnings: [], currentGrader: "independent-v2" } }));
    await page.goto("/eval?desk=legacy");
    await expect(page.getByRole("button", { name: "Requirement v2" })).toBeVisible();
    await expect(page.getByText(/尚未找到评估产物|历史运行/).first()).toBeVisible();
    await page.getByRole("button", { name: "Requirement v2" }).click();
    await expect(page.getByRole("heading", { name: "Requirement v2 评估工作台" })).toBeVisible();
  });
});

test.describe("Requirement v2 真实产物抽查(需本机 evaldesk 服务)", () => {
  test("spec3-final-live 的 16 条门槛、六层与结论可在浏览器逐项核对", async ({ page }) => {
    const probe = await page.request.get("/api/evaldesk/requirement-v2/runs");
    test.skip(!probe.ok(), "本机 evaldesk 服务未启动;真实产物抽查跳过,不影响合成用例");
    const { runs } = await probe.json();
    const target = runs.find((run: { dir_name: string }) => run.dir_name === "spec3-final-live-20260923");
    test.skip(!target, "本机无 spec3-final-live-20260923 产物");
    await page.goto(`/eval?run=${target.id}`);
    await expect(page.getByRole("heading", { name: "spec3-final-live-20260923" })).toBeVisible();
    // 该运行 gate_passed=false(确定性层当时红项),工作台不得误写为发布通过。
    await expect(page.getByTestId("reqv2-conclusion")).toContainText("冻结门槛未全过");
    await expect(page.getByText("冻结门槛(16 项", { exact: false })).toBeVisible();
    await expect(page.getByTestId("layer-extraction")).toContainText("25/26");
    const unevaluable = await page.getByText(/UNEVALUABLE · 未满足评估条件/).count();
    expect(unevaluable).toBeGreaterThanOrEqual(0);
    await page.screenshot({ path: "reqv2-real-spec3.png", fullPage: true });
  });

  test("零模型与 regrade 运行身份在浏览器正确呈现", async ({ page }) => {
    const probe = await page.request.get("/api/evaldesk/requirement-v2/runs");
    test.skip(!probe.ok(), "本机 evaldesk 服务未启动;真实产物抽查跳过");
    const { runs } = await probe.json();
    const zero = runs.find((run: { dir_name: string }) => run.dir_name.startsWith("spec4-"));
    test.skip(!zero, "本机无 spec4-* 产物");
    await page.goto(`/eval?run=${zero.id}`);
    await expect(page.getByRole("heading", { name: zero.dir_name })).toBeVisible();
    await expect(page.getByText(/零模型确定性|零模型重判/).first()).toBeVisible();
    const regrade = runs.find((run: { regrade: boolean; superseded: boolean }) => run.regrade && run.superseded);
    test.skip(!regrade, "本机无 superseded regrade 产物");
    await page.goto(`/eval?run=${regrade!.id}`);
    await expect(page.getByText(/零模型重判旧观测/)).toBeVisible();
    await expect(page.getByText(/不作为当前基线/)).toBeVisible();
  });
});
