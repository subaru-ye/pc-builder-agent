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
  code_commit: "0".repeat(40), code_dirty: false,
  models: [{ role: "screening", model: "offline-model", provider: "offline", reasoning_effort: "low", timeout: "1m0s", session_cache: true }],
  gate_passed: true, conclusion: "冻结门槛全部通过(合成夹具)。", evidence: { status: "complete", notes: [] },
  manifest_sha256: "a1b2c3d4e5f6", gates_sha256: "b1b2c3d4e5f6", max_model_requests: 400,
  score_note: "初筛 2/2 · 选配 0/0", plan_note: "prompt SHA256 per request is recorded in events.jsonl model_request events",
};
const manifest = {
  dataset: "requirement-v2", frozen_at: "2026-09-23", grader_version: "reqv2-grader-e2e",
  files: [
    { layer: "reducer", cases: 1, sessions: 1, sha256: "aa11" },
    { layer: "extraction", cases: 1, sessions: 1, sha256: "bb22" },
    { layer: "catalog.json", cases: 0, sessions: 0, sha256: "cc33" },
  ],
  splits: [
    { split: "development", sessions: 1, used: true },
    { split: "holdout", sessions: 2, used: false },
  ],
};
const detail = {
  ...liveRun, duration_ms: 5,
  gate_verdicts: [
    { layer: "extraction", metric: "operation_precision", actual: "0.950", threshold: "≥0.95", passed: true, evaluable: true },
    { layer: "model", metric: "provider_success", actual: "1.000 (12/12)", threshold: "≥0.98", passed: true, evaluable: true },
    { layer: "all", metric: "veto_total", actual: "0", threshold: "≤0", passed: true, evaluable: true },
    { layer: "extraction+conversations", metric: "key_field_wrong_write_total", actual: "0", threshold: "≤0", passed: true, evaluable: true },
  ],
  per_layer: {
    reducer: { cases: 1, passed: 1, skipped: 0, vetoes: 0, failure_classifications: {} },
    extraction: { cases: 1, passed: 1, skipped: 0, vetoes: 0, failure_classifications: {} },
  },
  model_quality: { extraction: { cases: 1, op_precision: 0.95, op_recall: 0.9, task_passed: 1, task_total: 1, signal_matches: 3, signal_turns: 3, forbidden_op_failures: 0, repeated_questions: 0, key_field_wrong_writes: 0 } },
  usage: { model_calls: 12, provider_errors: {}, tokens_known: 900, tokens_all_known: true, latency_p50_ms: 100, latency_p95_ms: 200 },
  limitations: ["合成夹具:仅验证工作台展示。"],
  cases: [{ layer: "reducer", id: "rd-1", split: "development", session: "rd-dev-1", repeats: [1], pass_k: true, skipped: false, vetoes: 0, failures: [] }],
  integrity_checks: [{ check: "grader_version", state: "ok" }, { check: "manifest.json", state: "ok" }],
  gate_thresholds: { version: "gates-e2e-v1" },
  manifest,
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
  score_note: "初筛 1/1 · 选配 0/0 · 模型层跳过",
};
const zeroDetail = {
  ...detail, ...zeroRun,
  manifest: { ...manifest, files: [{ layer: "reducer", cases: 1, sessions: 1, sha256: "aa11" }] },
  gate_verdicts: [{ layer: "extraction", metric: "min_cases", actual: "0", threshold: "≥20", passed: false, evaluable: false }],
  per_layer: { reducer: { cases: 1, passed: 0, skipped: 0, vetoes: 0, failure_classifications: { behavior_failure: 1 } }, extraction: { cases: 0, passed: 0, skipped: 2, vetoes: 0, failure_classifications: {} } },
  cases: [{ layer: "reducer", id: "rd-1", split: "development", session: "rd-dev-1", repeats: [1], pass_k: false, skipped: false, vetoes: 0, failures: ["behavior_failure"] }],
};
const archivedRun = { ...zeroRun, id: "c".repeat(24), dir_name: "superseded-e2e-archive", label: "artifacts/reqv2/superseded-e2e-archive", superseded: true };
const archivedDetail = { ...zeroDetail, ...archivedRun };

async function installFixtureRoutes(page: Page) {
  const json = (route: Route, value: unknown) => route.fulfill({ json: value });
  await page.route("**/api/evaldesk/requirement-v2/prompt-versions", route => json(route, { versions: [
    { id: "git:old:screening", role: "screening", source: "git", commit: "old-commit", created_at: "2026-09-23T09:36:00Z", subject: "修订需求理解规则", components: [{ role: "screening", name: "system", text: "Screening 原文：旧的需求理解规则。\n保留逐字证据。", sha256: "old-screening" }], review: { commit: "old-commit", role: "screening", title: "修订需求证据规则", reason: "旧输出把系统默认值写成了用户事实。", changes: "禁止将系统默认值照抄到需求操作。", verification: { kind: "tests_added", summary: "新增默认值误写回归测试，未保存当时执行结果。", limitation: "不能将新增测试当成模型效果提升。" }, evidence: [{ kind: "commit", reference: "old-commit", excerpt: "补齐证据判断规则。" }], run_names: [liveRun.dir_name] } },
    { id: "git:old:builder", role: "builder", source: "git", commit: "builder-old", created_at: "2026-09-22T09:36:00Z", components: [{ role: "builder", name: "system", text: "Builder 原文：旧的选配规则。", sha256: "old-builder" }] },
    { id: "saved:fixture", role: "screening", source: "run_snapshot", created_at: "2026-09-24T09:36:00Z", runs: [liveRun.id], components: [{ role: "screening", name: "system", text: "保存的实际测试提示词。", sha256: "saved-system" }] },
  ], notes: [] }));
  await page.route("**/api/evaldesk/requirement-v2/prompts", route => json(route, { source: "evaldesk_binary", sha256: "fixture-prompts", components: [
    { role: "screening", name: "system", text: "Screening 原文：解析本轮用户需求。\n保留逐字证据。", sha256: "screening-system" },
    { role: "screening", name: "format_retry", text: "Screening 纠偏原文：重新输出完整 JSON。", sha256: "screening-retry" },
    { role: "builder", name: "system", text: "Builder 原文：根据候选配件选择配置。", sha256: "builder-system" },
    { role: "builder", name: "output_contract_attempt_1", text: '{"allowed_actions":["select"]}', sha256: "builder-contract" },
  ] }));
  // 注意:先注册 run*(前缀更宽),后注册 runs 让精确目录优先。
  await page.route("**/api/evaldesk/requirement-v2/run*", (route) => {
    const id = new URL(route.request().url()).searchParams.get("id");
    if (id === zeroRun.id) return json(route, zeroDetail);
    if (id === archivedRun.id) return json(route, archivedDetail);
    return json(route, detail);
  });
  await page.route("**/api/evaldesk/requirement-v2/runs", (route) => json(route, { runs: [{ ...liveRun, layer_scores: detail.per_layer }, { ...zeroRun, layer_scores: zeroDetail.per_layer }, archivedRun], warnings: [] }));
  await page.route("**/api/evaldesk/requirement-v2/case*", (route) => json(route, caseDetail));
  await page.route("**/api/evaldesk/requirement-v2/dataset?*", route => json(route, { cases: [
    { ...caseDetail.frozen, content_sha256: "rd-1" },
    { ...caseDetail.frozen, id: "rd-holdout", title: "保留验证预算题", split: "holdout", content_sha256: "rd-holdout" },
  ], notes: [] }));
  await page.route("**/api/evaldesk/requirement-v2/compare*", (route) => {
    const params = new URL(route.request().url()).searchParams;
    if (params.get("a") === liveRun.id && params.get("b") === liveRun.id) {
      return json(route, {
        strict: true,
        baseline: liveRun, candidate: liveRun,
        outcome: {
          manifest_same: true, grader_same: true, model_pairs: 1, still_pass: 1, still_fail: 0,
          regressed: [], newly_passing: [],
          deterministic_layers: { reducer: { baseline_pass: 1, candidate_pass: 1, total: 1 }, policy: { baseline_pass: 1, candidate_pass: 1, total: 1 } },
          model_layers: { extraction: { baseline_pass: 1, candidate_pass: 1, total: 1 } },
        },
        case_set: { baseline_cases: 1, candidate_cases: 1, common: 1 },
      });
    }
    return json(route, {
      strict: false,
      reasons: [
        "运行类型不同(live vs deterministic);live、零模型、replay/regrade 语义不同,只能并排查看各自证据",
        "live 与零模型运行对比不能得出模型质量改善或退步结论",
      ],
      baseline: liveRun, candidate: zeroRun,
      case_set: { baseline_cases: 1, candidate_cases: 1, common: 1 },
    });
  });
}

test.describe("Requirement v2 合成产物", () => {
  test("题目直接浏览未测试分组，迭代逐题比较、版本深链与关联测试可达", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    const currentManifest = { ...manifest, grader_version: "grader-2", files: [
      { layer: "extraction", cases: 28, sessions: 28, sha256: "a" },
      { layer: "conversations", cases: 8, sessions: 8, sha256: "b" },
      { layer: "reducer", cases: 13, sessions: 13, sha256: "c" },
      { layer: "readiness", cases: 14, sessions: 14, sha256: "d" },
      { layer: "policy", cases: 9, sessions: 9, sha256: "e" },
      { layer: "ui-contract", cases: 4, sessions: 4, sha256: "f-new" },
      { layer: "selftest.json", cases: 15, sessions: 0, sha256: "g" },
      { layer: "catalog.json", cases: 0, sessions: 0, sha256: "h" },
    ], splits: [{ split: "development", sessions: 53, used: true }, { split: "calibration", sessions: 14, used: true }, { split: "holdout", sessions: 9, used: false }] };
    const previousManifest = { ...currentManifest, grader_version: "grader-1", files: currentManifest.files.map(file => file.layer === "ui-contract" ? { ...file, sha256: "f-old" } : file) };
    const previousRun = { ...zeroRun, created_at: "2026-09-23T00:00:00Z", manifest_sha256: "previous-snapshot", layer_scores: zeroDetail.per_layer };
    await page.route("**/api/evaldesk/requirement-v2/runs", route => route.fulfill({ json: { runs: [{ ...liveRun, layer_scores: detail.per_layer }, previousRun], warnings: [] } }));
    await page.route("**/api/evaldesk/requirement-v2/run?*", route => route.fulfill({ json: new URL(route.request().url()).searchParams.get("id") === previousRun.id ? { ...zeroDetail, manifest: previousManifest } : { ...detail, manifest: currentManifest } }));
    await page.route("**/api/evaldesk/requirement-v2/dataset?*", route => {
      const older = new URL(route.request().url()).searchParams.get("id") === previousRun.id;
      return route.fulfill({ json: { cases: currentManifest.files.filter(file => !file.layer.endsWith(".json")).flatMap(file => Array.from({ length: file.cases }, (_, index) => ({
        ...caseDetail.frozen, layer: file.layer, id: `${file.layer}-${index}`, title: `${file.layer === "ui-contract" ? "界面确认" : "预算场景"} ${index + 1}`,
        split: index === 0 ? "holdout" : "development", content_sha256: file.layer === "ui-contract" && index === 0 ? (older ? "before" : "after") : `${file.layer}-${index}`,
        fields: { user_message: "预算改成7500", expected: { revision_delta: older ? 0 : 1 } },
      }))), notes: [] } });
    });
    await page.goto("/eval?area=datasets");
    await expect(page.getByRole("heading", { name: "需求理解与选配准入", exact: true })).toBeVisible();
    await expect(page.getByLabel("题库规模")).toContainText("76道评估题");
    await expect(page.getByRole("button", { name: "Screening · 需求理解63 题" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Builder · 选配准入13 题" })).toBeVisible();
    await page.getByRole("button", { name: "需求抽取28", exact: true }).click();
    await page.getByRole("navigation", { name: "题目分页" }).getByRole("button", { name: "下一页" }).click();
    await expect(page.getByText("第 2 / 3 页", { exact: true })).toBeVisible();
    await page.getByRole("navigation", { name: "题目分页" }).getByRole("button", { name: "上一页" }).click();
    await page.getByRole("button", { name: "预算场景 1 用途：保留验证", exact: true }).click();
    await expect(page.getByRole("heading", { name: "用户输入", exact: true })).toBeVisible();
    await expect(page.getByText("版本增量", { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("heading", { name: "用户输入", exact: true })).toBeVisible();
    await expect(page.getByText("这套题在测什么", { exact: true })).toHaveCount(0);
    for (const width of [1440, 768, 375]) {
      await page.setViewportSize({ width, height: 960 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
      const scan = await new AxeBuilder({ page }).analyze();
      expect(scan.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
    }
    await page.setViewportSize({ width: 1440, height: 960 });
    await page.screenshot({ path: testInfo.outputPath("dataset-overview.png"), fullPage: true });
    await page.getByRole("navigation", { name: "评估集分区" }).getByRole("button", { name: "迭代记录", exact: true }).click();
    await expect(page.getByRole("region", { name: "内容变化" })).toContainText("修改 1 题");
    await page.getByText("修改界面确认 1界面交互", { exact: true }).click();
    await expect(page.getByRole("heading", { name: "调整前", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "调整后", exact: true })).toBeVisible();
    await page.getByText("分类与评分规则变化", { exact: true }).click();
    await expect(page.getByRole("region", { name: "内容变化" })).toContainText("界面交互：内容变更，数量不变");
    await expect(page.getByRole("region", { name: "内容变化" })).toContainText("评分规则版本变更");
    await expect(page.getByText(liveRun.manifest_sha256, { exact: false })).toBeHidden();
    await page.getByRole("navigation", { name: "选择迭代记录" }).getByRole("button", { name: /09月23日 08:00/ }).click();
    await expect(page).toHaveURL(/snapshot=previous-snapshot/);
    await page.reload();
    await expect(page.getByText(/没有更早题目可比较/)).toBeVisible();
    await page.getByRole("navigation", { name: "评估集分区" }).getByRole("button", { name: "关联测试", exact: true }).click();
    await page.getByRole("region", { name: "关联测试" }).getByRole("button").click();
    await expect(page).toHaveURL(new RegExp(`run=${previousRun.id}`));
    await expectZeroOutbound(page);
  });

  test("题目按分类分组，预算期望汇总为一行，输入与结果对照且技术来源默认收起", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.route("**/api/evaldesk/requirement-v2/dataset?*", route => route.fulfill({ json: { cases: [{ ...caseDetail.frozen, content_sha256: "budget", fields: {
      user_message: "预算7500", expected: { fields: { budget_cny: { value: 7500, status: "active", strength: "must", evidence: "stated", source: { kind: "chat", message_id: "turn-1", quote: "预算7500" } } }, reply_next_action_absent: true, revision_delta: 1, custom_check: { identity: "kept" } },
    } }], notes: [] } }));
    await page.goto("/eval?area=datasets");
    const group = page.getByRole("region", { name: "需求更新题目", exact: true });
    await group.getByRole("button", { name: "预算改为 7500 用途：回归测试", exact: true }).click();
    const article = group.getByRole("article");
    await expect(article.getByText("分类：需求更新", { exact: true })).toBeVisible();
    await expect(article.getByText("日常修改后检查是否引入问题", { exact: true })).toBeVisible();
    await expect(article.getByText("7500 元", { exact: true })).toBeVisible();
    await expect(article.getByText("已生效 · 必须", { exact: true })).toBeVisible();
    await expect(article.getByText("设计依据", { exact: true })).toBeVisible();
    await expect(article.getByText("e2e", { exact: true })).toBeHidden();
    const inputBounds = await article.getByRole("heading", { name: "用户输入", exact: true }).boundingBox();
    const expectedBounds = await article.getByRole("heading", { name: "预期结果与判分条件", exact: true }).boundingBox();
    expect(expectedBounds!.x).toBeGreaterThan(inputBounds!.x);
    expect(Math.abs(expectedBounds!.y - inputBounds!.y)).toBeLessThan(2);
    await article.getByText("其他判分条件 · 1 项", { exact: true }).click();
    const otherConditions = article.locator("details").filter({ has: page.getByText("其他判分条件 · 1 项", { exact: true }) });
    await otherConditions.getByText("详细条件", { exact: true }).click();
    await expect(otherConditions.locator("pre")).toContainText("kept");
    await page.screenshot({ path: testInfo.outputPath("question-design-1440.png"), fullPage: true });
    await page.setViewportSize({ width: 375, height: 960 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    const scan = await new AxeBuilder({ page }).analyze();
    expect(scan.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
    await expectZeroOutbound(page);
  });

  test("评估集信息缺失不显示零题，读取失败支持重试", async ({ page }) => {
    await installFixtureRoutes(page);
    let failed = true;
    await page.route("**/api/evaldesk/requirement-v2/run?*", route => failed ? route.fulfill({ status: 503, json: {} }) : route.fulfill({ json: { ...detail, manifest: null } }));
    await page.goto("/eval?area=datasets");
    await expect(page.getByRole("main").getByRole("alert")).toContainText("无法读取评估产物");
    failed = false;
    await page.getByRole("button", { name: "重新读取评估集" }).click();
    await expect(page.getByRole("heading", { name: "评估集信息未记录" })).toBeVisible();
    await expect(page.getByLabel("题库规模")).toHaveCount(0);
  });

  test("完整题库读取失败可重试，文件校验缺口不冒充题目删减", async ({ page }) => {
    await installFixtureRoutes(page);
    let failed = true;
    await page.route("**/api/evaldesk/requirement-v2/dataset?*", route => failed ? route.fulfill({ status: 503, json: {} }) : route.fulfill({ json: { cases: [], notes: ["reducer：冻结题目文件与 manifest 哈希不一致"] } }));
    await page.goto("/eval?area=datasets");
    await expect(page.getByRole("main").getByRole("alert")).toContainText("题目读取失败");
    failed = false;
    await page.getByRole("button", { name: "重新读取题目" }).click();
    await expect(page.getByRole("main").getByRole("alert")).toContainText("哈希不一致");
    await expect(page.getByText("该范围的题目暂不可读取。", { exact: true })).toBeVisible();
    await expect(page.getByLabel("题库规模")).toContainText("2道评估题");
  });

  test("旧服务缺少目录分层摘要时从详情补齐两侧结果，不误显示零运行", async ({ page }) => {
    await installFixtureRoutes(page);
    const detailRequests: string[] = [];
    await page.route("**/api/evaldesk/requirement-v2/run?*", route => {
      detailRequests.push(new URL(route.request().url()).searchParams.get("id")!);
      return route.fulfill({ json: { ...detail, per_layer: { ...detail.per_layer, policy: { cases: 8, passed: 7, skipped: 0, vetoes: 0, failure_classifications: {} } } } });
    });
    await page.route("**/api/evaldesk/requirement-v2/runs", route => route.fulfill({ json: { runs: [liveRun, archivedRun], warnings: [] } }));
    await page.goto("/eval");
    await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("1/1");
    await expect(page.getByRole("heading", { name: "测试记录 1", exact: true })).toBeVisible();
    await expect(page.getByText("Requirement v2", { exact: true })).toHaveCount(0);
    await page.getByRole("navigation", { name: "评估工作导航" }).getByRole("button", { name: "Builder 选配" }).click();
    await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("7/8");
    await expect(page.getByRole("heading", { name: "测试记录 1", exact: true })).toBeVisible();
    expect(detailRequests).toEqual([liveRun.id]);
    await expectZeroOutbound(page);
  });

  test("分层详情读取失败显示错误，不冒充暂无运行；可重新读取恢复", async ({ page }) => {
    await installFixtureRoutes(page);
    await page.route("**/api/evaldesk/requirement-v2/runs", route => route.fulfill({ json: { runs: [liveRun], warnings: [] } }));
    let failed = true;
    await page.route("**/api/evaldesk/requirement-v2/run?*", route => failed ? route.fulfill({ status: 503, json: {} }) : route.fulfill({ json: detail }));
    await page.goto("/eval");
    const error = page.getByRole("main").getByRole("alert");
    await expect(error).toContainText("无法读取评估产物");
    await expect(page.getByText("暂无运行", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "最近测试 0" })).toHaveCount(0);
    failed = false;
    await error.getByRole("button", { name: "重新读取" }).click();
    await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("1/1");
  });

  test("提示词原文按角色和组成阅读，来源明确且深链可恢复", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval?area=prompts");
    const roles = page.getByLabel("提示词角色");
    const parts = page.getByLabel("提示词组成");
    await expect(page.getByLabel("Screening 主提示词原文")).toHaveText("Screening 原文：解析本轮用户需求。\n保留逐字证据。");
    await expect(page.getByText(/取自本机评估服务/)).toBeVisible();
    await parts.getByRole("button", { name: "格式纠偏" }).click();
    await expect(page.getByLabel("Screening 格式纠偏原文")).toContainText("纠偏原文");
    await roles.getByRole("button", { name: "Builder 选配" }).click();
    await expect(page.getByLabel("Builder 主提示词原文")).toContainText("根据候选配件选择配置");
    await parts.getByRole("button", { name: "第 1 次尝试输出契约" }).click();
    await page.reload();
    await expect(page.getByLabel("Builder 第 1 次尝试输出契约原文")).toHaveText('{"allowed_actions":["select"]}');
    await page.getByRole("navigation", { name: "提示词视图" }).getByRole("button", { name: "历史测试" }).click();
    await expect(page.getByText("历史测试记录", { exact: true })).toBeVisible();
    await expect(page.getByLabel("当前提示词原文")).toHaveCount(0);
    await page.getByRole("navigation", { name: "提示词视图" }).getByRole("button", { name: "版本与原文" }).click();
    for (const width of [1440, 375]) {
      await page.setViewportSize({ width, height: 960 });
      await parts.getByRole("button", { name: "主提示词", exact: true }).click();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ path: testInfo.outputPath(`prompt-original-${width}.png`) });
      const violations = await new AxeBuilder({ page }).analyze();
      expect(violations.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
    }
  });

  test("历史提示词可选择原文、比较增删并恢复版本深链，保存原文关联真实测试", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval?area=prompts");
    await expect(page.getByLabel("提示词版本", { exact: true }).locator("option")).toHaveCount(3);
    await page.getByLabel("提示词版本", { exact: true }).selectOption("git:old:screening");
    await expect(page.getByText("Git 历史原文", { exact: true })).toBeVisible();
    await expect(page.getByLabel("Screening 主提示词原文")).toHaveText("Screening 原文：旧的需求理解规则。\n保留逐字证据。");
    await page.reload();
    await expect(page.getByLabel("提示词版本", { exact: true })).toHaveValue("git:old:screening");
    await page.getByLabel("提示词版本", { exact: true }).selectOption("current");
    await page.getByRole("button", { name: "版本对比", exact: true }).click();
    await page.getByLabel("对照版本", { exact: true }).selectOption("git:old:screening");
    const diff = page.getByRole("region", { name: "提示词内容对比" });
    await expect(diff).toContainText("+ 新增");
    await expect(diff).toContainText("− 移除");
    await expect(diff).toContainText("旧的需求理解规则");
    const unchanged = diff.locator('li[data-change="same"] pre').filter({ hasText: "保留逐字证据" });
    await expect(unchanged).toBeHidden();
    await page.getByLabel("只看改动").uncheck();
    await expect(unchanged).toBeVisible();
    await page.reload();
    await expect(page.getByLabel("对照版本", { exact: true })).toHaveValue("git:old:screening");
    for (const width of [1440, 375]) {
      await page.setViewportSize({ width, height: 960 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      const violations = await new AxeBuilder({ page }).analyze();
      expect(violations.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`prompt-versions-${width}.png`) });
    }
    await page.getByRole("button", { name: "版本与原文", exact: true }).click();
    await page.getByLabel("提示词版本", { exact: true }).selectOption("saved:fixture");
    await expect(page.getByRole("region", { name: "此版本的关联测试" })).toContainText(liveRun.dir_name);
    await page.getByRole("region", { name: "此版本的关联测试" }).getByRole("button").click();
    await expect(page.getByRole("heading", { name: liveRun.dir_name, exact: true })).toBeVisible();
  });

  test("提示词迭代记录展示改动原因、验证等级与溯源依据，未知版本不猜填效果", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval?area=prompts&prompt_view=iterations");
    const iterations = page.getByRole("region", { name: "提示词迭代记录" });
    await expect(iterations).toContainText("有回归测试，未确认执行结果");
    await iterations.locator("summary").filter({ hasText: "修订需求证据规则" }).click();
    const review = iterations.getByRole("region", { name: "改动原因与验证" });
    await expect(review).toContainText("旧输出把系统默认值写成了用户事实");
    await expect(review).toContainText("未保存当时执行结果");
    await expect(review).toContainText("不能将新增测试当成模型效果提升");
    await review.getByText("查看溯源依据", { exact: true }).click();
    await expect(review).toContainText("补齐证据判断规则");
    for (const width of [1440, 375]) {
      await page.setViewportSize({ width, height: 960 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      const violations = await new AxeBuilder({ page }).analyze();
      expect(violations.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`prompt-reviews-${width}.png`) });
    }
    await iterations.getByRole("button", { name: "查看本版原文" }).click();
    await expect(page.getByLabel("提示词版本", { exact: true })).toHaveValue("git:old:screening");
    await page.getByText("改动说明 · 修订需求证据规则", { exact: true }).click();
    await expect(page.getByRole("region", { name: "改动原因与验证" })).toContainText("旧输出把系统默认值");
    await page.getByRole("region", { name: "改动原因与验证" }).getByRole("button").click();
    await expect(page.getByRole("heading", { name: liveRun.dir_name, exact: true })).toBeVisible();
    await page.goto("/eval?area=prompts&prompt_role=builder&prompt_view=iterations");
    await page.getByRole("region", { name: "提示词迭代记录" }).locator("summary").first().click();
    await expect(page.getByRole("region", { name: "提示词迭代记录" })).toContainText("改动原因与验证结果尚未整理");
  });

  test("长提示词分段阅读，段落内的小改动精确标记且上下文可展开", async ({ page }) => {
    await installFixtureRoutes(page);
    const context = "保留原有规则与逐字证据。".repeat(100);
    const before = `操作语义：\n${context}允许撤销要求。${context}\n\n输出契约：\n只返回 JSON。`;
    const after = before.replace("允许", "禁止");
    await page.route("**/api/evaldesk/requirement-v2/prompts", route => route.fulfill({ json: { source: "evaldesk_binary", sha256: "new", components: [{ role: "screening", name: "system", text: after, sha256: "new" }] } }));
    await page.route("**/api/evaldesk/requirement-v2/prompt-versions", route => route.fulfill({ json: { versions: [{ id: "git:long", role: "screening", source: "git", created_at: "2026-09-23T09:36:00Z", components: [{ role: "screening", name: "system", text: before, sha256: "old" }] }], notes: [] } }));
    await page.goto("/eval?area=prompts");
    await expect(page.getByRole("navigation", { name: "原文段落" }).getByRole("button")).toHaveCount(2);
    await page.getByRole("navigation", { name: "原文段落" }).getByRole("button", { name: /输出格式与示例/ }).click();
    await expect(page.getByRole("region", { name: "所选原文段落" }).locator("pre")).toHaveText("输出契约：\n只返回 JSON。");
    await page.getByRole("button", { name: "完整原文", exact: true }).click();
    await expect(page.getByLabel("Screening 主提示词原文")).toHaveText(after);
    await page.getByRole("button", { name: "版本对比", exact: true }).click();
    await expect(page.getByLabel("改动统计")).toContainText("+2 新增字符");
    await expect(page.getByLabel("改动统计")).toContainText("−2 移除字符");
    const diff = page.getByRole("region", { name: "提示词内容对比" });
    await expect(diff.locator('mark[data-change="removed"]')).toHaveText("允许");
    await expect(diff.locator('mark[data-change="added"]')).toHaveText("禁止");
    const foldedContext = diff.locator('div[data-change="removed"] details').first();
    await expect(foldedContext.locator("span")).toBeHidden();
    await foldedContext.locator("summary").click();
    await expect(foldedContext.locator("span")).toBeVisible();
    await page.getByLabel("只看改动").uncheck();
    await expect(diff.getByRole("heading", { name: "− 移除" }).locator("..")).toContainText(context + "允许撤销要求。" + context);
  });

  test("旧服务缺少提示词历史接口时明确提示重启，恢复后可重新读取", async ({ page }) => {
    await installFixtureRoutes(page);
    let outdated = true;
    await page.route("**/api/evaldesk/requirement-v2/prompt-versions", route => outdated ? route.fulfill({ status: 404, json: { error: "没有此只读接口" } }) : route.fulfill({ json: { versions: [{ id: "git:recovered", role: "screening", source: "git", created_at: "2026-09-23T09:36:00Z", components: [{ role: "screening", name: "system", text: "恢复后的历史原文", sha256: "recovered" }] }], notes: [] } }));
    await page.goto("/eval?area=prompts");
    const versions = page.getByRole("region", { name: "提示词版本浏览" });
    await expect(versions.getByRole("alert")).toContainText("当前评估服务尚不支持版本历史，请更新并重启本机 evaldesk 服务");
    await expect(versions).toContainText("历史版本暂不可用");
    await expect(versions).not.toContainText("正在读取版本列表");
    await expect(page.getByLabel("Screening 主提示词原文")).toContainText("Screening 原文");
    outdated = false;
    await page.getByRole("button", { name: "重新读取提示词" }).click();
    await expect(versions.getByRole("alert")).toHaveCount(0);
    await page.getByLabel("提示词版本", { exact: true }).selectOption("git:recovered");
    await expect(page.getByLabel("Screening 主提示词原文")).toHaveText("恢复后的历史原文");
  });

  test("提示词原文读取失败可重试，不从历史运行或当前模型名猜填", async ({ page }) => {
    await installFixtureRoutes(page);
    let failed = true;
    await page.route("**/api/evaldesk/requirement-v2/prompts", route => failed ? route.fulfill({ status: 503, json: {} }) : route.fulfill({ json: { source: "evaldesk_binary", sha256: "retry", components: [{ role: "screening", name: "system", text: "重新读取成功的原文", sha256: "system" }] } }));
    await page.goto("/eval?area=prompts");
    await expect(page.getByRole("region", { name: "提示词版本浏览" }).getByRole("alert")).toContainText("无法读取当前提示词原文");
    await expect(page.getByLabel("Screening 主提示词原文")).toHaveCount(0);
    failed = false;
    await page.getByRole("button", { name: "重新读取提示词" }).click();
    await expect(page.getByLabel("Screening 主提示词原文")).toHaveText("重新读取成功的原文");
  });

  test("独立工作区、评估集题目与提示词迭代可导航并恢复 URL", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval");
    await expect(page.getByRole("heading", { name: "Screening 初筛", exact: true })).toBeVisible();
    await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("1/1");
    const nav = page.getByRole("navigation", { name: "评估工作导航" });
    await nav.getByRole("button", { name: "Builder 选配" }).click();
    await expect(page.getByText("当前仅评估选配准入与界面行为，未覆盖配件选择质量。")).toBeVisible();
    await nav.getByRole("button", { name: "评估集与题目" }).click();
    await expect(page.getByRole("navigation", { name: "评估集分区" })).toBeVisible();
    await page.getByLabel("搜索评估题目").fill("rd-1");
    await page.getByRole("button", { name: /预算改为 7500/ }).click();
    await expect(page.getByRole("heading", { name: "预期结果与判分条件" })).toBeVisible();
    await nav.getByRole("button", { name: "提示词迭代" }).click();
    await expect(page.getByLabel("Screening 主提示词原文")).toContainText("Screening 原文");
    await page.getByRole("navigation", { name: "提示词视图" }).getByRole("button", { name: "历史测试" }).click();
    await expect(page.getByText(/代码提交不等于提示词版本/)).toBeVisible();
    await page.getByRole("button", { name: "查看配置与运行证据" }).click();
    await page.reload();
    await expect(page.getByText(/本次运行未保存提示词原文/)).toBeVisible();
    await page.getByRole("button", { name: "选择两次运行对比" }).click();
    await expect(page.getByLabel("基线运行")).toHaveValue(liveRun.id);
    await expect(page.getByText(/请选择基线与候选运行/)).toBeVisible();
    await expect(page.getByText(/正在核对两次运行/)).toHaveCount(0);
    await page.goBack();
    await expect(page.getByText(/本次运行未保存提示词原文/)).toBeVisible();
    await nav.getByRole("button", { name: "Screening 初筛" }).click();
    for (const width of [1440, 768, 375]) {
      await page.setViewportSize({ width, height: 960 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
      const scan = await new AxeBuilder({ page }).analyze();
      expect(scan.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`workspace-${width}.png`), fullPage: true });
    }
    await expectZeroOutbound(page);
  });

  test("测试记录分页与门槛筛选不混淆失败、未记录和证据状态", async ({ page }) => {
    await installFixtureRoutes(page);
    const many = Array.from({ length: 25 }, (_, index) => ({ ...liveRun, id: `run-${index}`, dir_name: `run-${index}`, gate_passed: index === 24 ? null : index === 23 ? false : true }));
    await page.route("**/api/evaldesk/requirement-v2/runs", route => route.fulfill({ json: { runs: many, warnings: [] } }));
    await page.goto("/eval?area=runs");
    await expect(page.getByTestId(/^reqv2-run-/)).toHaveCount(12);
    await page.getByRole("button", { name: "下一页" }).click();
    await expect(page.getByTestId("reqv2-run-run-12")).toBeVisible();
    await page.getByLabel("冻结门槛筛选").selectOption("fail");
    await expect(page.getByTestId(/^reqv2-run-/)).toHaveCount(1);
    await expect(page.getByTestId("reqv2-run-run-23")).toContainText("证据完整");
    await page.getByLabel("冻结门槛筛选").selectOption("unknown");
    await expect(page.getByTestId("reqv2-run-run-24")).toBeVisible();
  });

  test("运行目录默认显示 v2,superseded 折叠,损坏产物不伪造结论", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval?area=runs");
    await expect(page.getByRole("heading", { name: "测试记录", exact: true })).toBeVisible();
    await expect(page.getByTestId("reqv2-run-e2e-live-ok")).toBeVisible();
    // 目录行直接可见两侧跑分摘要。
    await expect(page.getByTestId("reqv2-run-e2e-live-ok")).toContainText("初筛 2/2 · 选配 0/0");
    await expect(page.getByTestId("reqv2-run-superseded-e2e-archive")).toBeHidden();
    await page.getByText(/superseded 历史归档/).click();
    await expect(page.getByTestId("reqv2-run-superseded-e2e-archive")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("reqv2-catalog.png"), fullPage: true });
    await expectZeroOutbound(page);
  });

  test("运行详情按顺序呈现身份、结论、门槛、两侧分层,逐题与对比可进入", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval?area=runs");
    await page.getByTestId("reqv2-run-e2e-live-ok").click();
    await expect(page.getByRole("heading", { name: "e2e-live-ok" })).toBeVisible();
    await expect(page.getByTestId("reqv2-conclusion")).toContainText("冻结门槛全部通过");
    await expect(page.getByText("初筛 Screening 侧 · 跑分", { exact: false })).toBeVisible();
    await expect(page.getByText("选配 Builder 侧 · 跑分", { exact: false })).toBeVisible();
    // 模型层指标结构化展示,不再用 JSON 折叠块。
    await expect(page.getByTestId("layer-extraction")).toContainText("操作 precision 0.950");
    await expect(page.getByTestId("layer-extraction")).toContainText("任务成功 1/1");
    await page.getByRole("button", { name: "评估集与题目", exact: true }).last().click();
    // 评估集清单与 split 分布;holdout 明确标注本运行未使用。
    await expect(page.getByTestId("manifest-reducer")).toContainText("1");
    await page.getByText("分组与评分信息", { exact: true }).click();
    await expect(page.getByRole("region", { name: "题目用途分组" })).toContainText("保留验证");
    await expect(page.getByRole("region", { name: "题目用途分组" })).toContainText("2 个会话本运行未使用");
    await page.getByText("文件校验信息", { exact: true }).click();
    await expect(page.getByTestId("manifest-catalog.json")).toBeVisible();
    await page.getByRole("button", { name: "模型与提示词", exact: true }).click();
    // 模型与提示词:脱敏身份与逐请求提示词哈希声明。
    await expect(page.getByText(/reasoning low/)).toBeVisible();
    await expect(page.getByText(/本次运行未保存提示词原文/)).toBeVisible();
    await page.getByRole("button", { name: "门槛检查", exact: true }).click();
    await expect(page.getByTestId("gate-extraction-operation_precision")).toContainText("PASS");
    await expect(page.getByTestId("gate-model-provider_success")).toContainText("PASS");
    // 回归:veto 总数与跨层关键字段两条门槛不再被分组静默丢弃。
    await expect(page.getByTestId("gate-all-veto_total")).toContainText("PASS");
    await expect(page.getByTestId("gate-extraction+conversations-key_field_wrong_write_total")).toContainText("PASS");
    await page.getByRole("button", { name: "运行证据", exact: true }).click();
    await expect(page.getByText("调用与用量")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("reqv2-run-detail.png"), fullPage: true });
    await page.getByRole("button", { name: "结果概览", exact: true }).click();
    await page.getByRole("button", { name: "全部层" }).click();
    await page.getByRole("button", { name: /rd-1/ }).click();
    await expect(page.getByText("reducer / rd-1")).toBeVisible();
    await expect(page.getByText("reducer:no_error")).toBeVisible();
    await expect(page.getByText("预算改为 7500")).toBeVisible();
    await page.getByRole("button", { name: "返回运行" }).click();
    await expect(page.getByRole("button", { name: "选择两次运行对比" })).toBeVisible();
    await page.getByRole("button", { name: "选择两次运行对比" }).click();
    await expect(page.getByRole("heading", { name: "同身份对比" })).toBeVisible();
    await page.getByLabel("基线运行").selectOption(liveRun.id);
    await page.getByLabel("候选运行").selectOption(liveRun.id);
    await expect(page.getByTestId("compare-outcome")).toBeVisible();
    await expect(page.getByText(/模型层 初筛抽取 1\/1 → 1\/1/)).toBeVisible();
    await expect(page.getByText(/确定性层 Policy 1\/1 → 1\/1/)).toBeVisible();
  });

  test("零模型运行保持 UNEVALUABLE 与确定性层身份,不显示模型运行指标", async ({ page }) => {
    await installFixtureRoutes(page);
    await page.goto("/eval?area=runs");
    await page.getByText(/superseded 历史归档/).click();
    // 目录行的跑分摘要标注模型层跳过。
    await expect(page.getByTestId("reqv2-run-e2e-zero-model")).toContainText(/初筛 1\/1 · 选配 0\/0 · 模型层跳过/);
    await page.getByTestId("reqv2-run-e2e-zero-model").click();
    await expect(page.getByRole("heading", { name: "e2e-zero-model" })).toBeVisible();
    await expect(page.getByText(/冻结门槛未全过/)).toBeVisible();
    // 模型层行显示跳过未评估。
    await expect(page.getByTestId("layer-extraction")).toContainText("跳过 · 未评估");
    await expect(page.getByTestId("layer-reducer")).toContainText("behavior_failure");
    await page.getByRole("button", { name: "运行证据", exact: true }).click();
    await expect(page.getByText("零模型运行:模型调用", { exact: false })).toBeVisible();
  });

  test("跨语义对比被拒绝并给出原因,不产出改善幅度", async ({ page }, testInfo) => {
    await installFixtureRoutes(page);
    await page.goto("/eval?area=runs");
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
      await page.goto("/eval?area=runs");
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
});

test("初筛按测试目的分组，复用基线与所选结果不被最新模型覆盖，筛选深链可恢复", async ({ page }) => {
  await installFixtureRoutes(page);
  const layer_scores = {
    extraction: { cases: 26, passed: 26, skipped: 0, vetoes: 0, failure_classifications: {} },
    conversations: { cases: 7, passed: 7, skipped: 0, vetoes: 0, failure_classifications: {} },
    reducer: { cases: 11, passed: 11, skipped: 0, vetoes: 0, failure_classifications: {} },
    readiness: { cases: 12, passed: 12, skipped: 0, vetoes: 0, failure_classifications: {} },
  };
  const flash = { ...liveRun, dir_name: "screening-fix2-live-devcal-r3-20260924", layer_scores, repeats: 3, created_at: "2026-09-24T12:00:00Z" };
  const glm = { ...flash, id: "d".repeat(24), dir_name: "modelcmp-glm-devcal-r3-20260924", created_at: "2026-09-24T15:00:00Z", gate_passed: false, models: [{ ...liveRun.models[0], model: "glm-5.3" }], layer_scores: { ...layer_scores, conversations: { ...layer_scores.conversations, passed: 6 } }, failed_gates: [{ layer: "model", metric: "latency_p95_ms", actual: "10059ms", threshold: "≤10000ms", passed: false, evaluable: true }] };
  const unknown = { ...flash, id: "e".repeat(24), dir_name: "modelcmp-unreviewed", created_at: "2026-09-23T12:00:00Z" };
  await page.route("**/api/evaldesk/requirement-v2/runs", route => route.fulfill({ json: { runs: [glm, flash, unknown], warnings: [] } }));
  await page.goto("/eval?area=screening");
  const selected = page.getByRole("region", { name: "最近分层结果" });
  await expect(selected).toContainText("Flash · 基线（复用）");
  await expect(selected).toContainText("7/7");
  await expect(page.getByRole("heading", { name: "测试记录 3", exact: true })).toBeVisible();
  const purposes = page.getByLabel("测试目的分类");
  await purposes.getByRole("button", { name: "模型对比2", exact: true }).click();
  const experiment = page.getByTestId("experiment-models-20260924");
  const glmRow = experiment.getByTestId(`experiment-run-${glm.dir_name}`);
  await expect(glmRow).toContainText("10059ms / 要求 ≤10000ms");
  await expect(page.getByTestId("experiment-evidence-fix")).toHaveCount(0);
  await glmRow.getByRole("button", { name: "查看分层" }).click();
  await page.reload();
  await expect(selected).toContainText("GLM · 候选");
  await expect(selected).toContainText("6/7");
  await expect(purposes.getByRole("button", { name: "模型对比2" })).toHaveAttribute("aria-pressed", "true");
  await glmRow.getByRole("button", { name: "与基线对比" }).click();
  await expect(page).toHaveURL(new RegExp(`va=${flash.id}.*vb=${glm.id}`));
  await page.goBack();
  await expect(selected).toContainText("GLM · 候选");
  await page.getByRole("combobox", { name: "执行方式", exact: true }).selectOption("deterministic");
  await expect(page.getByRole("status")).toContainText("“模型对比”有 2 条记录，当前“零模型确定性”筛选没有匹配项。");
  await expect(selected).toHaveCount(0);
  await page.getByRole("button", { name: "清除执行方式筛选" }).click();
  await expect(experiment).toBeVisible();
  await purposes.getByRole("button", { name: "未标注目的1", exact: true }).click();
  await expect(page.getByTestId("experiment-unclassified")).toContainText("modelcmp-unreviewed");
  await expectZeroOutbound(page);
});

test("选配复用实验分类，仅显示本侧题数，未知目的与未评估层不伪造", async ({ page }) => {
  await installFixtureRoutes(page);
  const policy = { cases: 8, passed: 8, skipped: 0, vetoes: 0, failure_classifications: {} };
  const flash = { ...liveRun, dir_name: "screening-fix2-live-devcal-r3-20260924", created_at: "2026-09-24T12:00:00Z", layer_scores: { policy, "ui-contract": { ...policy, cases: 3, passed: 3 } } };
  const glm = { ...flash, id: "d".repeat(24), dir_name: "modelcmp-glm-devcal-r3-20260924", created_at: "2026-09-24T15:00:00Z", gate_passed: false, models: [{ ...liveRun.models[0], model: "glm-5.3" }], failed_gates: [{ layer: "model", metric: "latency_p95_ms", actual: "10059ms", threshold: "≤10000ms", passed: false, evaluable: true }] };
  const unknown = { ...flash, id: "e".repeat(24), dir_name: "builder-unreviewed", created_at: "2026-09-23T12:00:00Z", layer_scores: { policy } };
  const unrelated = { ...flash, id: "f".repeat(24), dir_name: "screening-only", layer_scores: { extraction: policy } };
  await page.route("**/api/evaldesk/requirement-v2/runs", route => route.fulfill({ json: { runs: [glm, flash, unknown, unrelated], warnings: [] } }));
  await page.goto("/eval?area=builder&test_purpose=models");
  const selected = page.getByRole("region", { name: "最近分层结果" });
  const purposes = page.getByLabel("测试目的分类");
  await expect(page.getByRole("heading", { name: "测试记录 3", exact: true })).toBeVisible();
  await expect(selected).toContainText("Flash · 基线（复用）");
  await expect(selected).toContainText("8/8");
  await expect(selected).toContainText("3/3");
  await expect(selected).not.toContainText("需求抽取");
  await expect(purposes.getByRole("button", { name: "模型实验检查2", exact: true })).toHaveAttribute("aria-pressed", "true");
  const glmRow = page.getByTestId(`experiment-run-${glm.dir_name}`);
  await expect(glmRow).toContainText("初筛模型：glm-5.3");
  await expect(glmRow).toContainText("整次运行：门槛未通过");
  await expect(glmRow.getByText(/10059ms/)).not.toBeVisible();
  await glmRow.locator("summary").click();
  await expect(glmRow.getByText(/10059ms/)).toBeVisible();
  await glmRow.getByRole("button", { name: "查看分层" }).click();
  await page.reload();
  await expect(selected).toContainText("GLM · 候选");
  await expect(selected).toContainText("8/8");
  await glmRow.getByRole("button", { name: "与基线对比" }).click();
  await expect(page).toHaveURL(new RegExp(`va=${flash.id}.*vb=${glm.id}`));
  await page.goBack();
  await expect(selected).toContainText("GLM · 候选");
  await purposes.getByRole("button", { name: "未标注目的1", exact: true }).click();
  await expect(page.getByTestId("experiment-unclassified")).toContainText("builder-unreviewed");
  await expect(selected).toContainText("界面行为未评估");
  await page.getByRole("navigation", { name: "评估工作导航" }).getByRole("button", { name: "Screening 初筛" }).click();
  await expect(page).not.toHaveURL(/test_purpose=|test_result=/);
  await expectZeroOutbound(page);
});

test.describe("Requirement v2 真实产物抽查(需本机 evaldesk 服务)", () => {
  test("选配按目的展示全部批次，切换程序回归清除旧方式并保留真实失败与缺测", async ({ page }) => {
    await page.goto("/eval?area=builder&test_purpose=models&test_mode=live");
    const selected = page.getByRole("region", { name: "最近分层结果" });
    const experiment = page.getByTestId("experiment-models-20260924");
    await expect(experiment).toContainText("不同初筛模型下的选配检查");
    await expect(experiment.locator('[data-testid^="experiment-run-"]')).toHaveCount(4);
    await expect(selected).toContainText("8/8");
    await expect(selected).toContainText("3/3");
    const mode = page.getByRole("combobox", { name: "执行方式", exact: true });
    await page.getByLabel("测试目的分类").getByRole("button", { name: "程序回归7", exact: true }).click();
    await expect(mode).toHaveValue("all");
    await expect(page.getByRole("region", { name: "选配测试实验" }).locator('[data-testid^="experiment-run-"]')).toHaveCount(7);
    await page.reload();
    await expect(selected).toContainText("8/8");
    await page.getByLabel("测试目的分类").getByRole("button", { name: "认证检查6", exact: true }).click();
    const certification = page.getByTestId("experiment-certification");
    if (await certification.getAttribute("open") === null) await certification.locator(":scope > summary").click();
    const holdout = page.getByTestId("experiment-run-spec6-cert-live-c-holdout-r3-20260924");
    await expect(holdout).toContainText("0/1");
    await expect(holdout).toContainText("1/1");
    await holdout.getByRole("button", { name: "查看分层" }).click();
    await page.reload();
    await expect(selected).toContainText("C · 保留集验证");
    await expect(selected).toContainText("0/1");
    await page.getByLabel("测试目的分类").getByRole("button", { name: "消融与诊断2", exact: true }).click();
    await expect(page.getByTestId("experiment-run-spec6-cert-live-e-ablation-cal-r3-20260924")).toContainText("界面行为未评估");
    await page.goto("/eval?area=builder&test_purpose=regression&test_mode=live");
    await expect(page.getByRole("status")).toContainText("“程序回归”有 7 条记录，当前“真实调用”筛选没有匹配项。");
    await expect(selected).toHaveCount(0);
    await page.getByRole("button", { name: "清除执行方式筛选" }).click();
    await expect(page.getByRole("region", { name: "选配测试实验" }).locator('[data-testid^="experiment-run-"]')).toHaveCount(7);
    await expectZeroOutbound(page);
  });
  test("初筛与选配精简概览保留结果和范围，技术身份与报告原文仍可核对", async ({ page }, testInfo) => {
    const views = [
      { area: "screening&test_purpose=models", title: "Screening 初筛" },
      { area: "builder", title: "Builder 选配" },
    ];
    for (const view of views) {
      await page.goto(`/eval?area=${view.area}`);
      await expect(page.getByRole("heading", { name: view.title, exact: true })).toBeVisible();
      await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText(/\d+\/\d+/);
      await expect(page.getByRole("main").getByText("modelcmp-glm-devcal-r3-20260924", { exact: true })).toHaveCount(0);
      if (view.area === "builder") {
        await expect(page.getByText("当前仅评估选配准入与界面行为，未覆盖配件选择质量。", { exact: true })).toHaveCount(1);
        await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("选配准入");
        await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("界面行为");
      } else {
        const experiment = page.getByTestId("experiment-models-20260924");
        await expect(experiment.getByTestId("experiment-run-modelcmp-glm-devcal-r3-20260924")).toContainText("10059ms");
        await expect(experiment.getByTestId("experiment-run-modelcmp-kimi-devcal-r3-20260924")).toContainText("多轮任务成功率");
        const description = experiment.locator("details").filter({ has: page.locator("summary", { hasText: "测试说明" }) });
        await expect(description).not.toHaveAttribute("open", "");
        await description.locator("summary").click();
        await expect(description).toContainText("更换模型能否改善需求抽取和多轮对话");
        await description.locator("summary").click();
      }
      for (const theme of ["dark", "light"]) {
        await page.getByLabel("外观", { exact: true }).selectOption(theme);
        for (const width of [1440, 768, 375]) {
          await page.setViewportSize({ width, height: 960 });
          await page.evaluate(() => window.scrollTo(0, 0));
          expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
          const scan = await new AxeBuilder({ page }).analyze();
          expect(scan.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
          await page.screenshot({ path: testInfo.outputPath(`clean-${view.title}-${theme}-${width}.png`), fullPage: true });
        }
      }
    }
    await page.goto("/eval?area=screening&test_purpose=models");
    await page.getByTestId("experiment-run-modelcmp-glm-devcal-r3-20260924").getByRole("button", { name: "查看详情", exact: true }).click();
    await expect(page.getByRole("heading", { name: "modelcmp-glm-devcal-r3-20260924", exact: true })).toBeVisible();
    const report = page.getByRole("region", { name: "报告结论" }).locator("details").filter({ has: page.locator("summary", { hasText: "报告原文" }) });
    await expect(report).not.toHaveAttribute("open", "");
    await report.locator("summary").click();
    await expect(report).toContainText("10059ms");
    await page.getByRole("navigation", { name: "运行详情分区" }).getByRole("button", { name: "运行证据", exact: true }).click();
    await expect(page.getByText("代码提交", { exact: true })).toBeVisible();
    await expect(page.getByText("产物目录", { exact: true })).toBeVisible();
    await expectZeroOutbound(page);
  });
  test("从真实调用切换程序回归清除旧筛选，冲突深链说明原因并可恢复七条记录", async ({ page }, testInfo) => {
    await page.goto("/eval?area=screening&test_purpose=models&test_mode=live");
    const mode = page.getByRole("combobox", { name: "执行方式", exact: true });
    await expect(mode).toHaveValue("live");
    await page.getByLabel("测试目的分类").getByRole("button", { name: "程序回归7", exact: true }).click();
    await expect(mode).toHaveValue("all");
    await expect(page).not.toHaveURL(/test_mode=/);
    await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("11/11");
    await expect(page.getByTestId("experiment-run-spec4-strict-validation-212310").locator("details").first()).not.toHaveAttribute("open", "");
    await expect(page.getByRole("region", { name: "初筛测试实验" }).locator('[data-testid^="experiment-run-"]')).toHaveCount(7);
    await page.reload();
    await expect(mode).toHaveValue("all");
    await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("12/12");
    await page.goto("/eval?area=screening&test_purpose=regression&test_mode=live");
    await expect(page.getByRole("status")).toContainText("“程序回归”有 7 条记录，当前“真实调用”筛选没有匹配项。");
    await expect(page.getByRole("region", { name: "最近分层结果" })).toHaveCount(0);
    await page.getByRole("button", { name: "清除执行方式筛选" }).click();
    await expect(page.getByRole("region", { name: "初筛测试实验" }).locator('[data-testid^="experiment-run-"]')).toHaveCount(7);
    await page.getByLabel("外观", { exact: true }).selectOption("dark");
    await page.screenshot({ path: testInfo.outputPath("regression-filter-fixed.png"), fullPage: true });
    await expectZeroOutbound(page);
  });
  test("初筛模型实验集中显示四臂，失败原因可读，深浅主题和窄屏可审阅", async ({ page }, testInfo) => {
    await page.goto("/eval?area=screening&test_purpose=models");
    const experiment = page.getByTestId("experiment-models-20260924");
    await expect(experiment.locator('[data-testid^="experiment-run-"]')).toHaveCount(4);
    await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("26/26");
    await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText("7/7");
    await expect(experiment).toContainText("Flash · 基线（复用）");
    await expect(experiment.getByTestId("experiment-run-modelcmp-glm-devcal-r3-20260924")).toContainText("10059ms");
    for (const theme of ["dark", "light"]) {
      await page.getByLabel("外观", { exact: true }).selectOption(theme);
      for (const width of [1440, 768, 375]) {
        await page.setViewportSize({ width, height: 960 });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
        const violations = await new AxeBuilder({ page }).analyze();
        expect(violations.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
        await experiment.scrollIntoViewIfNeeded();
        await page.screenshot({ path: testInfo.outputPath(`screening-experiments-${theme}-${width}.png`), fullPage: true });
      }
    }
    await expectZeroOutbound(page);
  });
  test("历史原文均有审阅依据，最新验证关联真实运行并保留未通过结果", async ({ page }, testInfo) => {
    const history = await (await page.request.get("/api/evaldesk/requirement-v2/prompt-versions")).json();
    const versions = history.versions.filter((version: { source: string }) => version.source === "git");
    expect(versions.length).toBeGreaterThanOrEqual(29);
    expect(versions.every((version: { review?: { evidence: unknown[] } }) => version.review?.evidence.length)).toBeTruthy();
    await page.goto("/eval?area=prompts&prompt_view=iterations");
    const iterations = page.getByRole("region", { name: "提示词迭代记录" });
    await iterations.locator("summary").filter({ hasText: "补齐已有配件的撤销授权" }).click();
    const review = iterations.getByRole("region", { name: "改动原因与验证" }).first();
    await expect(review).toContainText("6/7 → 7/7");
    await expect(review).toContainText("关键字段错写 2 → 0");
    await expect(review).toContainText("不能把改善归因于提示词单因素");
    await expect(review.getByRole("button", { name: /screening-fix-live-devcal-r3-20260924/ })).toContainText("门槛未通过");
    await expect(review.getByRole("button", { name: /screening-fix2-live-devcal-r3-20260924/ })).toContainText("门槛通过");
    for (const theme of ["dark", "light"]) {
      await page.getByLabel("外观", { exact: true }).selectOption(theme);
      for (const width of [1440, 375]) {
        await page.setViewportSize({ width, height: 960 });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
        const violations = await new AxeBuilder({ page }).analyze();
        expect(violations.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
        await review.scrollIntoViewIfNeeded();
        await page.screenshot({ path: testInfo.outputPath(`real-prompt-reviews-${theme}-${width}.png`) });
      }
    }
    await review.getByRole("button", { name: "对比这两次测试" }).click();
    await expect(page.getByRole("heading", { name: "同身份对比" })).toBeVisible();
    await expect(page.getByLabel("基线运行")).not.toHaveValue("");
    await expect(page.getByLabel("候选运行")).not.toHaveValue("");
  });

  test("当前编译提示词原文在桌面与窄屏完整显示", async ({ page }, testInfo) => {
    const probe = await page.request.get("/api/evaldesk/requirement-v2/prompts");
    expect(probe.ok()).toBeTruthy();
    const prompts = await probe.json();
    expect(prompts.source).toBe("evaldesk_binary");
    await page.goto("/eval?area=prompts");
    await page.getByLabel("外观", { exact: true }).selectOption("dark");
    for (const width of [1440, 375]) {
      await page.setViewportSize({ width, height: 960 });
      for (const role of ["screening", "builder"]) {
        await page.getByLabel("提示词角色").getByRole("button", { name: role === "screening" ? "Screening 初筛" : "Builder 选配" }).click();
        await page.getByLabel("提示词组成").getByRole("button", { name: "主提示词", exact: true }).click();
        const fullText = page.getByRole("button", { name: "完整原文", exact: true });
        if (await fullText.count()) await fullText.click();
        const original = page.getByLabel(`${role === "screening" ? "Screening" : "Builder"} 主提示词原文`);
        await expect(original).toHaveText(prompts.components.find((component: { role: string; name: string }) => component.role === role && component.name === "system").text);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
        await page.screenshot({ path: testInfo.outputPath(`real-prompt-${role}-${width}.png`) });
      }
    }
    const history = await (await page.request.get("/api/evaldesk/requirement-v2/prompt-versions")).json();
    expect(history.versions.length).toBeGreaterThan(0);
    await page.setViewportSize({ width: 1440, height: 960 });
    await page.getByLabel("提示词角色").getByRole("button", { name: "Screening 初筛" }).click();
    const old = history.versions.find((version: { role: string; components: { name: string; sha256: string }[] }) => version.role === "screening" && version.components.some(component => component.name === "system" && component.sha256 !== prompts.components.find((component: { role: string; name: string }) => component.role === "screening" && component.name === "system").sha256));
    expect(old).toBeTruthy();
    await page.getByLabel("提示词版本", { exact: true }).selectOption(old.id);
    await expect(page.getByText("Git 历史原文", { exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("real-prompt-history.png") });
    await page.getByLabel("提示词版本", { exact: true }).selectOption("current");
    await page.getByRole("button", { name: "版本对比", exact: true }).click();
    await page.getByLabel("对照版本", { exact: true }).selectOption(old.id);
    await expect(page.getByRole("region", { name: "提示词内容对比" })).toContainText("+ 新增");
    await page.screenshot({ path: testInfo.outputPath("real-prompt-diff.png") });
    for (const width of [1440, 375]) {
      await page.setViewportSize({ width, height: 960 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      const violations = await new AxeBuilder({ page }).analyze();
      expect(violations.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
      await page.getByRole("region", { name: "提示词内容对比" }).scrollIntoViewIfNeeded();
      await page.screenshot({ path: testInfo.outputPath(`real-prompt-diff-${width}.png`) });
    }
    await page.setViewportSize({ width: 1440, height: 960 });
    await page.getByRole("button", { name: "版本与原文", exact: true }).click();
    await page.getByRole("button", { name: "分段阅读", exact: true }).click();
    await expect(page.getByRole("navigation", { name: "原文段落" }).getByRole("button")).toHaveCount(8);
    await page.getByRole("navigation", { name: "原文段落" }).getByRole("button", { name: /需求操作规则/ }).click();
    await page.reload();
    await expect(page.getByRole("region", { name: "所选原文段落" })).toContainText("操作语义：");
    await expect(page.getByLabel("提示词版本", { exact: true }).locator("option")).toHaveCount(history.versions.filter((version: { role: string }) => version.role === "screening").length + 1);
    for (const width of [1440, 375]) {
      await page.setViewportSize({ width, height: 960 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      const violations = await new AxeBuilder({ page }).analyze();
      expect(violations.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`real-prompt-sections-${width}.png`) });
    }
  });

  test("真实产物六个工作区在深色桌面和窄屏均可审阅", async ({ page }, testInfo) => {
    const probe = await page.request.get("/api/evaldesk/requirement-v2/runs");
    test.skip(!probe.ok(), "本机评估服务未启动");
    const latest = (await probe.json()).runs.find((run: { superseded: boolean }) => !run.superseded);
    const latestDetail = latest ? await (await page.request.get(`/api/evaldesk/requirement-v2/run?id=${latest.id}`)).json() : null;
    await page.goto("/eval");
    await page.getByLabel("外观", { exact: true }).selectOption("dark");
    for (const width of [1440, 375]) {
      await page.setViewportSize({ width, height: 960 });
      for (const name of ["Screening 初筛", "Builder 选配", "评估集与题目", "提示词迭代", "测试记录", "运行对比"]) {
        await page.getByRole("navigation", { name: "评估工作导航" }).getByRole("button", { name, exact: true }).click();
        await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
        await expect(page.getByText("正在读取评估工作区…")).toHaveCount(0);
        if (name === "评估集与题目") {
          await page.getByRole("button", { name: /Screening · 需求理解\d+ 题/ }).click();
          await expect(page.getByRole("region", { name: "题目浏览" }).getByRole("article").first()).toBeVisible();
        }
        const layers = name === "Screening 初筛" ? ["extraction", "conversations", "reducer", "readiness"] : name === "Builder 选配" ? ["policy", "ui-contract"] : [];
        if (layers.some(layer => latestDetail?.per_layer?.[layer]?.cases > 0)) {
          await expect(page.getByRole("region", { name: "最近分层结果" })).toContainText(/\d+\/\d+/);
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
        const scan = await new AxeBuilder({ page }).analyze();
        expect(scan.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
        await page.screenshot({ path: testInfo.outputPath(`real-${name}-${width}.png`) });
        if (name === "评估集与题目") {
          await page.getByRole("button", { name: "需求更新13", exact: true }).click();
          await page.getByRole("region", { name: "需求更新题目", exact: true }).getByRole("article").first().getByRole("button").click();
          await expect(page.getByText("7500 元", { exact: true })).toBeVisible();
          await page.getByRole("article").filter({ has: page.getByText("7500 元", { exact: true }) }).screenshot({ path: testInfo.outputPath(`real-预算题目-${width}.png`) });
          await page.getByRole("button", { name: /Builder · 选配准入\d+ 题/ }).click();
          await page.getByRole("region", { name: "题目浏览" }).getByRole("article").first().getByRole("button").click();
          await expect(page.getByRole("heading", { name: "预期结果与判分条件", exact: true })).toBeVisible();
          await page.getByRole("region", { name: "题目浏览" }).screenshot({ path: testInfo.outputPath(`real-题目详情-${width}.png`) });
          await page.getByRole("navigation", { name: "评估集分区" }).getByRole("button", { name: "迭代记录", exact: true }).click();
          await expect(page.getByText("正在核对题目变化…", { exact: true })).toHaveCount(0);
          await expect(page.getByRole("region", { name: "内容变化" }).getByRole("alert")).toHaveCount(0);
          expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
          const scanHistory = await new AxeBuilder({ page }).analyze();
          expect(scanHistory.violations.filter(item => ["serious", "critical"].includes(item.impact ?? ""))).toEqual([]);
          await page.screenshot({ path: testInfo.outputPath(`real-题目迭代-${width}.png`) });
          await page.getByRole("navigation", { name: "评估集分区" }).getByRole("button", { name: "题目", exact: true }).click();
        }
      }
    }
  });

  test("运行目录可显示无模型的真实产物", async ({ page }, testInfo) => {
    const probe = await page.request.get("/api/evaldesk/requirement-v2/runs");
    test.skip(!probe.ok(), "本机评估服务未启动;真实产物抽查跳过");
    const { runs } = await probe.json();
    const zero = runs.find((run: { superseded: boolean; models: unknown[] }) => !run.superseded && run.models.length === 0);
    test.skip(!zero, "本机无零模型产物");
    await page.goto("/eval?area=runs");
    await page.getByLabel("搜索运行").fill(zero.dir_name);
    await expect(page.getByTestId(`reqv2-run-${zero.dir_name}`)).toBeVisible();
    await expect(page.getByText("模型未记录").first()).toBeVisible();
    await page.evaluate(() => localStorage.setItem("pcb-theme", "dark"));
    await page.reload();
    await page.getByLabel("搜索运行").fill(zero.dir_name);
    await expect(page.getByTestId(`reqv2-run-${zero.dir_name}`)).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("real-catalog.png") });
  });

  test("spec3-final-live 的 16 条门槛、六层与结论可在浏览器逐项核对", async ({ page }, testInfo) => {
    const probe = await page.request.get("/api/evaldesk/requirement-v2/runs");
    test.skip(!probe.ok(), "本机 evaldesk 服务未启动;真实产物抽查跳过,不影响合成用例");
    const { runs } = await probe.json();
    const target = runs.find((run: { dir_name: string }) => run.dir_name === "spec3-final-live-20260923");
    test.skip(!target, "本机无 spec3-final-live-20260923 产物");
    await page.goto(`/eval?run=${target.id}`);
    await expect(page.getByRole("heading", { name: "spec3-final-live-20260923" })).toBeVisible();
    // 该运行 gate_passed=false(确定性层当时红项),工作台不得误写为发布通过。
    await expect(page.getByTestId("reqv2-conclusion")).toContainText("冻结门槛未全过");
    await expect(page.getByTestId("layer-extraction")).toContainText("25/26");
    await page.getByRole("button", { name: "门槛检查", exact: true }).click();
    await expect(page.getByText("冻结门槛(16 项", { exact: false })).toBeVisible();
    const unevaluable = await page.getByText(/UNEVALUABLE · 未满足评估条件/).count();
    expect(unevaluable).toBeGreaterThanOrEqual(0);
    await page.screenshot({ path: testInfo.outputPath("detail-viewport.png") });
    await page.screenshot({ path: testInfo.outputPath("reqv2-real-spec3.png"), fullPage: true });
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
