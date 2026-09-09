import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

interface FrozenCase { id: string; title: string; input?: string; expect?: unknown; turns?: { input: string; expect: unknown }[] }
interface SavedUsage { model_calls: number; embedding_calls: number; usage_responses: number; input_tokens: number; output_tokens: number; total_tokens: number }
interface SavedTurn { text: string; model_text?: string; model_attempts?: string[] }
interface SavedRecord {
  case_id: string; seed: number; verdict: { passed: boolean; failures?: { id: string; detail: string }[] };
  usage?: SavedUsage; screening?: SavedTurn & { turns?: SavedTurn[] };
  result?: { decision?: { message: string } };
}
interface SavedMeta { requested_seeds?: number; suite_version?: string }
const root = resolve(__dirname, "../..");
const paths = {
  baseline: "eval/20260909-134447-3770035618",
  candidate: "evalchange/20260909-143405-run-1286854101/runs/20260909-143433-2017771388",
  previousSuite: "eval/20260909-105354-2212735039",
  legacy: "eval/20260906-235754",
  incomplete: "eval/20260908-225709-3047715439",
  builderAlternative: "eval/20260909-130258-3875612415",
};
function requireArtifacts(requirements: [string, string[]][]) {
  const missing = requirements.flatMap(([run, files]) => files.map(file => `${run}/${file}`))
    .filter(path => !existsSync(resolve(root, "artifacts", path)));
  test.skip(missing.length > 0, `本机缺少此项真实历史验收需要的产物：${missing.join("、")}。不会生成或替代历史样本。`);
}
function artifact<T>(run: string, file: string): T {
  return JSON.parse(readFileSync(resolve(root, "artifacts", run, file), "utf8")) as T;
}
function records(run: string): SavedRecord[] {
  return readFileSync(resolve(root, "artifacts", run, "results.jsonl"), "utf8")
    .split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line) as SavedRecord);
}
function savedRun(path: string) {
  return { path, rows: records(path), cases: artifact<{ cases: FrozenCase[] }>(path, "cases.json").cases, meta: artifact<SavedMeta>(path, "meta.json") };
}
function sum(rows: SavedRecord[], key: keyof SavedUsage) {
  if (rows.some(row => !row.usage)) throw new Error("This assertion requires recorded usage, without estimates.");
  return rows.reduce((total, row) => total + row.usage![key], 0);
}
const fmt = (value: number) => value.toLocaleString("zh-CN");
const diff = (value: number) => `${value > 0 ? "+" : ""}${fmt(value)}`;
const passed = (rows: SavedRecord[]) => rows.filter(row => row.verdict.passed).length;
function allPassed(run: ReturnType<typeof savedRun>) {
  return run.cases.filter(c => {
    const trials = run.rows.filter(row => row.case_id === c.id);
    return trials.length === run.meta.requested_seeds && trials.every(row => row.verdict.passed);
  }).length;
}
async function selectPair(page: Page, baseline: string, candidate: string) {
  await page.goto("/eval");
  for (const [name, path] of [["基线运行", baseline], ["候选运行", candidate]]) {
    const select = page.getByRole("combobox", { name, exact: true });
    const option = select.locator(`option[data-run-label="${path}"]`);
    await expect(option).toHaveCount(1);
    const value = await option.getAttribute("value");
    expect(value).toBeTruthy();
    await select.selectOption(value!);
  }
  await page.getByRole("button", { name: "对比运行", exact: true }).click();
  await expect(page.getByRole("region", { name: "条件变化", exact: true })).toBeVisible();
}
async function expectMetric(page: Page, label: string, baseline: string, candidate: string, delta?: string) {
  const row = page.getByRole("region", { name: "指标变化", exact: true }).getByText(label, { exact: true }).locator("..");
  await expect(row.locator(":scope > *").nth(1)).toHaveText(baseline);
  await expect(row.locator(":scope > *").nth(2)).toHaveText(candidate);
  if (delta !== undefined) await expect(row.locator(":scope > *").nth(3)).toHaveText(delta);
}
async function noHorizontalOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
}
async function accessible(page: Page) {
  const result = await new AxeBuilder({ page }).analyze();
  expect(result.violations.filter(v => v.impact === "serious" || v.impact === "critical")).toEqual([]);
}
async function runRow(page: Page, path: string): Promise<Locator> {
  await page.getByRole("textbox", { name: "搜索运行", exact: true }).fill(path.split("/").at(-1)!);
  const row = page.getByTestId(`run-${path}`);
  await expect(row).toBeVisible();
  return row;
}

const unexpectedRequests = new WeakMap<Page, string[]>();
test.beforeEach(async ({ page }) => {
  // Abort unexpected mutations or remote calls before they could trigger a model.
  const unexpected: string[] = [];
  unexpectedRequests.set(page, unexpected);
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url());
    if (url.protocol === "data:" || url.protocol === "blob:") return route.continue();
    if (!["127.0.0.1", "localhost", "[::1]"].includes(url.hostname) || request.method() !== "GET") {
      unexpected.push(`${request.method()} ${url.origin}${url.pathname}`);
      return route.abort("blockedbyclient");
    }
    if (url.pathname.startsWith("/api/") && !url.pathname.startsWith("/api/evaldesk/")) {
      unexpected.push(`${request.method()} ${url.pathname}`);
      return route.abort("blockedbyclient");
    }
    return route.continue();
  });
});
test.afterEach(async ({ page }) => {
  expect(unexpectedRequests.get(page)).toEqual([]);
});

test("saved regression opens the failed repeat and preserves visible versus original replies", async ({ page }, testInfo) => {
  requireArtifacts([paths.baseline, paths.candidate].map(path => [path, ["meta.json", "cases.json", "results.jsonl"]]));
  const a = savedRun(paths.baseline), b = savedRun(paths.candidate);
  await selectPair(page, a.path, b.path);
  await expect(page.getByText("严格对照条件通过 · 统一口径离线复核", { exact: true })).toBeVisible();
  for (const [label, key] of [["模型调用", "model_calls"], ["检索调用", "embedding_calls"], ["已知 token", "total_tokens"]] as const) {
    await expectMetric(page, label, fmt(sum(a.rows, key)), fmt(sum(b.rows, key)), diff(sum(b.rows, key) - sum(a.rows, key)));
  }
  const execution = (run: typeof a) => `${passed(run.rows)}/${run.cases.length * run.meta.requested_seeds!} · ${(passed(run.rows) / run.rows.length * 100).toFixed(1)}%`;
  const all = (run: typeof a) => `${allPassed(run)}/${run.cases.length} · ${(allPassed(run) / run.cases.length * 100).toFixed(1)}%`;
  await expectMetric(page, "执行通过", execution(a), execution(b));
  await expectMetric(page, `每题全部 ${a.meta.requested_seeds} 次通过`, all(a), all(b));
  await page.locator('[aria-label="题目分类"]').getByRole("button", { name: /^退步 / }).click();
  await page.getByRole("button", { name: /^查看 L5-301 / }).click();
  const detail = page.getByRole("region", { name: "单题详情", exact: true });
  const failed = b.rows.find(row => row.case_id === "L5-301" && !row.verdict.passed)!;
  expect(failed).toBeDefined();
  await expect(detail.getByRole("button", { name: `第 ${failed.seed} 次重复`, exact: true })).toHaveAttribute("aria-pressed", "true");
  const failure = failed.verdict.failures![0];
  const turnNumber = Number(failure.detail.match(/第 (\d+) 轮/)?.[1]);
  expect(turnNumber).toBeGreaterThan(0);
  const turn = detail.locator(":scope > section").filter({ has: page.getByRole("heading", { name: `第 ${turnNumber} 轮`, exact: true }) });
  await expect(turn).toBeVisible();
  const jump = detail.getByRole("button", { name: `定位第 ${turnNumber} 轮失败`, exact: true });
  await jump.click();
  await expect(turn).toBeFocused();
  const lastRepeat = Math.max(...b.rows.filter(row => row.case_id === failed.case_id).map(row => row.seed));
  await detail.getByRole("button", { name: `第 ${lastRepeat} 次重复`, exact: true }).focus();
  await page.keyboard.press("Tab");
  await expect(jump).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(turn).toBeFocused();
  await expect(turn).toBeInViewport();
  for (const [i, run] of [a, b].entries()) {
    const source = run.cases.find(c => c.id === failed.case_id)!.turns![turnNumber - 1];
    const row = run.rows.find(r => r.case_id === failed.case_id && r.seed === failed.seed)!;
    const output = row.screening!.turns![turnNumber - 1];
    const side = turn.locator(":scope > div > div").nth(i);
    await expect(side.locator("pre").nth(0)).toHaveText(source.input);
    expect(JSON.parse(await side.locator("pre").nth(1).innerText())).toEqual(source.expect);
    await expect(side.locator("pre").nth(2)).toHaveText(output.text);
    await side.locator("summary").filter({ hasText: "模型原回复 / 格式重试" }).click();
    const attempts = output.model_attempts ?? (output.model_text ? [output.model_text] : []);
    for (const [attempt, text] of attempts.entries()) await expect(side.locator("pre").nth(3 + attempt)).toHaveText(text);
  }
  await expect(turn.getByText(failure.detail, { exact: false })).toBeVisible();
  await noHorizontalOverflow(page);
  await accessible(page);
  await turn.screenshot({ path: testInfo.outputPath("regressed-turn.png") });
  await detail.getByRole("button", { name: "返回对比", exact: true }).click();
  await page.locator('[aria-label="题目分类"]').getByRole("button", { name: /^调用变化 / }).click();
  const ids = a.cases.map(c => c.id).filter(id => sum(a.rows.filter(r => r.case_id === id), "model_calls") !== sum(b.rows.filter(r => r.case_id === id), "model_calls"));
  const cases = page.getByRole("region", { name: "逐题变化", exact: true });
  await expect(cases.getByRole("button", { name: /^查看 / })).toHaveCount(ids.length);
  for (const id of ids) {
    const row = cases.getByRole("button", { name: new RegExp(`^查看 ${id} `) });
    const ac = sum(a.rows.filter(r => r.case_id === id), "model_calls"), bc = sum(b.rows.filter(r => r.case_id === id), "model_calls");
    await expect(row).toContainText(`${fmt(ac)} → ${fmt(bc)}`);
    await expect(row).toContainText(diff(bc - ac));
    await row.click();
    await expect(page.getByRole("region", { name: "单题详情", exact: true }).getByRole("heading", { name: new RegExp(`^${id} ·`) })).toBeVisible();
    await page.getByRole("button", { name: "返回对比", exact: true }).click();
  }
  await page.getByRole("combobox", { name: "外观", exact: true }).selectOption("light");
  await noHorizontalOverflow(page);
  await accessible(page);
  if (testInfo.project.name === "evaldesk-desktop") {
    await page.locator('[aria-label="题目分类"]').getByRole("button", { name: /^全部题目 / }).click();
    await page.getByRole("button", { name: /^查看 L4-201 / }).click();
    const details = page.getByRole("region", { name: "单题详情", exact: true });
    const delivery = details.locator(":scope > section").filter({ has: page.getByRole("heading", { name: "实际选件与交付结果", exact: true }) });
    await expect(delivery).toBeVisible();
    for (const [i, run] of [a, b].entries()) {
      const trial = run.rows.filter(row => row.case_id === "L4-201").sort((x, y) => x.seed - y.seed)[0];
      const message = trial.result?.decision?.message;
      expect(message).toBeTruthy();
      await expect(delivery.locator("pre").nth(i)).toContainText(message!);
      expect(JSON.parse(await delivery.locator("pre").nth(i).innerText()).decision.message).toBe(message);
    }
    await delivery.screenshot({ path: testInfo.outputPath("non-delivery-decision.png") });
  }
});

test("cross-suite comparison separates unchanged contracts and added cases", async ({ page }, testInfo) => {
  requireArtifacts([paths.previousSuite, paths.baseline].map(path => [path, ["meta.json", "cases.json", "results.jsonl"]]));
  const a = savedRun(paths.previousSuite), b = savedRun(paths.baseline);
  const common = a.cases.filter(c => b.cases.some(next => next.id === c.id && JSON.stringify(next) === JSON.stringify(c)));
  const added = b.cases.filter(c => !a.cases.some(previous => previous.id === c.id));
  await selectPair(page, a.path, b.path);
  await expect(page.getByRole("region", { name: "条件变化", exact: true })).toContainText(`共同题 ${common.length} 道`);
  await expect(page.getByText("条件对照 · 仅作观察比较", { exact: true })).toBeVisible();
  await page.locator('[aria-label="题目分类"]').getByRole("button", { name: `新增题 ${added.length}`, exact: true }).click();
  const section = page.getByRole("region", { name: "逐题变化", exact: true });
  await expect(section.getByRole("button", { name: /^查看 / })).toHaveCount(added.length);
  for (const c of added) await expect(section.getByRole("button", { name: `查看 ${c.id} ${c.title}`, exact: true })).toBeVisible();
  await section.getByRole("button", { name: /^查看 / }).first().click();
  await expect(page.getByRole("region", { name: "单题详情", exact: true })).toContainText("此侧未记录该次执行。");
  await noHorizontalOverflow(page);
  await accessible(page);
  await page.getByRole("region", { name: "条件变化", exact: true }).screenshot({ path: testInfo.outputPath("cross-suite-conditions.png") });
});

test("changed conditions explain actual model fields and keep hashes in expandable evidence", async ({ page }, testInfo) => {
  requireArtifacts([[paths.candidate, ["meta.json", "cases.json", "results.jsonl"]], [paths.builderAlternative, ["meta.json", "cases.json", "results.jsonl"]]]);
  type ConditionMeta = { code: { checkout_commit: string; binary_sha256: string; checkout_dirty: boolean }; configured_models: { builder: { model: string } } };
  const a = artifact<ConditionMeta>(paths.candidate, "meta.json"), b = artifact<ConditionMeta>(paths.builderAlternative, "meta.json");
  expect(a.configured_models.builder.model).not.toBe(b.configured_models.builder.model);
  await selectPair(page, paths.candidate, paths.builderAlternative);
  const readable = page.locator('[aria-label="可读变化说明"]');
  await expect(readable).toContainText(a.configured_models.builder.model);
  await expect(readable).toContainText(b.configured_models.builder.model);
  await expect(readable).toContainText("选配");
  await expect(readable).toContainText("未提交");
  await expect(readable).toContainText("源码清单");
  const primaryText = await readable.innerText();
  for (const meta of [a, b]) {
    expect(primaryText).not.toContain(meta.code.checkout_commit.slice(0, 12));
    expect(primaryText).not.toContain(meta.code.binary_sha256.slice(0, 12));
  }
  expect(primaryText).not.toMatch(/\b[a-f0-9]{32,64}\b/i);
  await noHorizontalOverflow(page);
  await accessible(page);
  await readable.screenshot({ path: testInfo.outputPath("readable-changes.png") });
  const evidence = page.getByRole("region", { name: "条件变化", exact: true }).locator("details").filter({ has: page.locator("summary").filter({ hasText: "版本证据与比较口径" }) });
  await evidence.locator(":scope > summary").click();
  await expect(evidence).toContainText(a.code.binary_sha256);
  await expect(evidence).toContainText(b.code.checkout_commit);
  await page.goBack();
  await expect(page.getByRole("textbox", { name: "搜索运行", exact: true })).toBeVisible();
  await page.goForward();
  await expect(page.locator('[data-testid="change-models"]')).toBeVisible();
});

test("legacy and interrupted runs retain unknown evidence and the planned denominator", async ({ page }, testInfo) => {
  requireArtifacts([[paths.legacy, ["meta.json", "results.jsonl"]], [paths.incomplete, ["meta.json", "cases.json"]]]);
  const oldRows = records(paths.legacy), oldMeta = artifact<SavedMeta>(paths.legacy, "meta.json");
  expect(oldMeta.requested_seeds).toBeUndefined();
  expect(oldRows.every(row => !row.usage)).toBe(true);
  await page.goto("/eval");
  const legacy = await runRow(page, paths.legacy);
  await expect(legacy).toContainText("旧格式");
  await expect(legacy).toContainText("题库未记录");
  await expect(legacy).toContainText("用量未记录");
  await expect(legacy).toContainText("全部重复通过比例：未记录");
  await expect(legacy).toContainText(`执行通过 ${passed(oldRows)}/${oldRows.length}`);
  await legacy.locator("summary").filter({ hasText: "版本证据与完整性" }).click();
  const promptVersion = legacy.locator("dl > div").filter({ has: page.getByText("提示词版本", { exact: true }) });
  await expect(promptVersion).toContainText("未记录");
  await noHorizontalOverflow(page);
  const frozen = artifact<{ cases: FrozenCase[] }>(paths.incomplete, "cases.json");
  const meta = artifact<SavedMeta>(paths.incomplete, "meta.json");
  const expected = frozen.cases.length * meta.requested_seeds!;
  const incompleteRows = existsSync(resolve(root, "artifacts", paths.incomplete, "results.jsonl")) ? records(paths.incomplete) : [];
  const incomplete = await runRow(page, paths.incomplete);
  await expect(incomplete).toContainText("未完成");
  await expect(incomplete).toContainText(`执行通过 ${passed(incompleteRows)}/${expected}`);
  await expect(incomplete).toContainText(`已记录 ${incompleteRows.length} 次，结果不完整`);
  await incomplete.locator("summary").filter({ hasText: "版本证据与完整性" }).click();
  await noHorizontalOverflow(page);
  await accessible(page);
  await incomplete.screenshot({ path: testInfo.outputPath("incomplete-run.png") });
  await selectPair(page, paths.incomplete, paths.incomplete);
  await expect(page.getByRole("region", { name: "条件变化", exact: true })).toContainText("未通过完整离线复验");
  await expect(page.getByRole("region", { name: "条件变化", exact: true })).toContainText("指标增减未知");
  await expect(page.getByRole("region", { name: "逐题变化", exact: true }).getByRole("button", { name: /^查看 / })).toHaveCount(frozen.cases.length);
  await expect(page.locator('[aria-label="题目分类"]').getByRole("button", { name: `证据不足 ${frozen.cases.length}`, exact: true })).toBeVisible();
});
