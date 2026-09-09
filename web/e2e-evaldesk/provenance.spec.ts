import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { relative, resolve, sep } from "node:path";

interface FrozenCase {
  id: string;
  title: string;
  input?: string;
  expect?: unknown;
  turns?: { input: string; expect: unknown }[];
}
interface SavedMeta {
  suite_version?: string;
  suite_sha256?: string;
  generated_at?: string;
  requested_seeds?: number;
  code?: { checkout_commit?: string; checkout_dirty?: boolean };
}
interface FrozenSuite { cases: FrozenCase[] }

const root = resolve(__dirname, "../..");
const paths = {
  baseline: "eval/20260909-134447-3770035618",
  candidate: "evalchange/20260909-143405-run-1286854101/runs/20260909-143433-2017771388",
  legacy: "eval/20260906-235754",
  incomplete: "eval/20260908-225709-3047715439",
};

function artifactPath(run: string, file: string) {
  return resolve(root, "artifacts", run, file);
}
function requireArtifacts(requirements: [string, string[]][]) {
  const missing = requirements.flatMap(([run, files]) => files.map(file => [run, file]))
    .filter(([run, file]) => !existsSync(artifactPath(run, file)));
  test.skip(missing.length > 0, `缺少此项只读验收需要的真实历史产物：${missing.map(parts => parts.join("/")).join("、")}。不会生成或替代历史样本。`);
}
function artifact<T>(run: string, file: string): T {
  return JSON.parse(readFileSync(artifactPath(run, file), "utf8")) as T;
}
function savedRun(run: string) {
  return { path: run, meta: artifact<SavedMeta>(run, "meta.json"), cases: artifact<FrozenSuite>(run, "cases.json").cases };
}
function commitEvidence(meta: SavedMeta) {
  const commit = meta.code?.checkout_commit;
  expect(commit).toMatch(/^[a-f0-9]{40,64}$/i);
  // Read the recorded commit object, never infer the old runner's source from HEAD.
  const [subject, committedAt] = execFileSync("git", ["show", "-s", "--no-show-signature", "--format=%s%x00%cI", commit!], { cwd: root, encoding: "utf8" }).trimEnd().split("\0");
  expect(subject).toBeTruthy();
  expect(Number.isNaN(Date.parse(committedAt))).toBe(false);
  return { commit: commit!, subject, committedAt };
}
function query(page: Page) { return new URL(page.url()).searchParams; }

async function runID(page: Page, path: string) {
  const option = page.getByRole("combobox", { name: "基线运行", exact: true }).locator(`option[data-run-label="${path}"]`);
  await expect(option).toHaveCount(1);
  const id = await option.getAttribute("value");
  expect(id).toBeTruthy();
  return id!;
}
async function expectPair(page: Page, baseline: string, candidate: string) {
  const showSelectors = [null, "runs", "compare"].includes(query(page).get("view"));
  for (const [label, value] of [["基线运行", baseline], ["候选运行", candidate]]) {
    const selector = page.getByRole("combobox", { name: label, exact: true });
    if (showSelectors) await expect(selector).toHaveValue(value);
    else await expect(selector).not.toBeVisible();
  }
  expect(query(page).get("baseline") ?? "").toBe(baseline);
  expect(query(page).get("candidate") ?? "").toBe(candidate);
}
async function provenance(page: Page, id: string, expectedView = "suites") {
  const section = page.getByRole("region", { name: "题库与评估集", exact: true });
  await expect(section).toBeVisible();
  expect(query(page).get("view")).toBe(expectedView);
  expect(query(page).get("run")).toBe(id);
  await expect(section.getByRole("region", { name: "代码提交", exact: true })).toHaveCount(0);
  await expect(section.getByRole("region", { name: "运行版本条件", exact: true })).toHaveCount(0);
  return section;
}
async function openNavigation(page: Page) {
  const menu = page.getByRole("button", { name: "工作台菜单", exact: true });
  if (await menu.isVisible() && await menu.getAttribute("aria-expanded") !== "true") await menu.click();
  const navigation = page.getByRole("navigation", { name: "工作台导航", exact: true });
  await expect(navigation).toBeVisible();
  return navigation;
}
async function navigateSection(page: Page, name: string) {
  const navigation = await openNavigation(page);
  await navigation.getByRole("button", { name, exact: true }).click();
  const menu = page.getByRole("button", { name: "工作台菜单", exact: true });
  if (await menu.isVisible()) {
    await expect(menu).toHaveAttribute("aria-expanded", "false");
    await expect(navigation).not.toBeVisible();
  }
}
async function onlyScreen(page: Page, active: string) {
  for (const name of ["运行列表", "条件变化", "代码提交记录", "评估集版本目录", "题库与评估集", "溯源时间线"]) {
    const screen = page.getByRole("region", { name, exact: true });
    if (name === active) await expect(screen).toBeVisible();
    else await expect(screen).not.toBeVisible();
  }
}
function historicalMetadata() {
  const rows: { path: string; meta: SavedMeta }[] = [];
  const artifactRoot = resolve(root, "artifacts");
  function visit(dir: string) {
    if (!existsSync(dir)) return;
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.isSymbolicLink()) continue;
      const path = resolve(dir, entry.name);
      if (entry.isDirectory()) visit(path);
      else if (entry.name === "meta.json" && entry.isFile()) {
        try { rows.push({ path: relative(artifactRoot, dir).split(sep).join("/"), meta: JSON.parse(readFileSync(path, "utf8")) as SavedMeta }); }
        catch { /* Damaged evidence cannot provide expected commit associations. */ }
      }
    }
  }
  for (const dir of ["eval", "evalchange"]) visit(resolve(artifactRoot, dir));
  return rows;
}
function frozenSummaries(section: Locator) {
  return section.locator("summary").filter({ hasText: /^L\d+-\d+\b/ });
}
async function frozenCase(section: Locator, c: FrozenCase) {
  await section.getByRole("textbox", { name: "搜索冻结题目", exact: true }).fill(c.id);
  const summary = frozenSummaries(section).filter({ hasText: c.id });
  await expect(summary).toHaveCount(1);
  await expect(summary).toContainText(c.title);
  const detail = summary.locator("..");
  if (await detail.getAttribute("open") === null) await summary.click();
  return detail;
}
async function expectOriginalCase(detail: Locator, c: FrozenCase) {
  const turns = c.turns ?? [{ input: c.input!, expect: c.expect }];
  expect(turns.length).toBeGreaterThan(0);
  for (const [index, turn] of turns.entries()) {
    const frozenTurn = detail.getByRole("region", { name: `冻结第 ${index + 1} 轮`, exact: true });
    expect(turn.input).toBeTruthy();
    const input = frozenTurn.getByText(turn.input, { exact: true });
    await expect(input).toBeVisible();
    expect(await input.textContent()).toBe(turn.input);
    // Compare each turn with its own exact saved contract, not a current fixture.
    const summary = frozenTurn.locator("summary").filter({ hasText: "完整期望记录" });
    const original = summary.locator("..");
    if (await original.getAttribute("open") === null) await summary.click();
    expect(JSON.parse(await original.locator("pre").innerText())).toEqual(turn.expect);
  }
}
async function noHorizontalOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
}
async function accessible(page: Page) {
  const result = await new AxeBuilder({ page }).analyze();
  expect(result.violations.filter(v => v.impact === "serious" || v.impact === "critical")).toEqual([]);
}
async function expectTimestamp(section: Locator, iso: string) {
  const timestamps = await section.locator("time[datetime]").evaluateAll(nodes => nodes.map(node => node.getAttribute("datetime")));
  expect(timestamps.some(value => value !== null && Date.parse(value) === Date.parse(iso))).toBe(true);
}
async function expectHeadingBelowHeader(page: Page, heading: Locator) {
  await expect(heading).toBeVisible();
  await expect(heading).toBeInViewport({ ratio: 1 });
  await expect.poll(async () => {
    const [title, header] = await Promise.all([heading.boundingBox(), page.locator("header").first().boundingBox()]);
    if (!title || !header) return false;
    const viewportHeight = await page.evaluate(() => window.innerHeight);
    return title.y >= Math.max(0, header.y + header.height) - 1 && title.y + title.height <= viewportHeight + 1;
  }, { message: "标题应完整位于可视区域，并且处于实际固定顶栏下方" }).toBe(true);
}

const unexpectedRequests = new WeakMap<Page, string[]>();
test.beforeEach(async ({ page }) => {
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
test.afterEach(async ({ page }) => { expect(unexpectedRequests.get(page)).toEqual([]); });

test("timeline opens the full frozen exam in its own screen without choosing a comparison", async ({ page }, testInfo) => {
  requireArtifacts([[paths.baseline, ["meta.json", "cases.json", "results.jsonl"]]]);
  const run = savedRun(paths.baseline);
  const dialogue = run.cases.find(c => c.id === "L5-301")!;
  expect(dialogue?.turns?.length).toBeGreaterThan(1);
  await page.goto("/eval");
  const id = await runID(page, run.path);
  await expectPair(page, "", "");
  await navigateSection(page, "溯源时间线");
  await onlyScreen(page, "溯源时间线");
  const timeline = page.getByTestId(`timeline-${run.path}`);
  await expect(timeline).toBeVisible();
  await expect(timeline).toContainText(run.meta.suite_version!);
  await expectTimestamp(timeline, run.meta.generated_at!);
  await expectPair(page, "", "");
  await noHorizontalOverflow(page);
  await accessible(page);
  await timeline.screenshot({ path: testInfo.outputPath("timeline-recorded-run.png") });
  await timeline.getByRole("button", { name: "查看题库", exact: true }).click();
  const section = await provenance(page, id);
  await onlyScreen(page, "题库与评估集");
  await expectPair(page, "", "");
  const frozen = section.getByRole("region", { name: "冻结题库", exact: true });
  await expect(frozen).toContainText(`${run.cases.length} 道`);
  await expect(frozenSummaries(frozen)).toHaveCount(run.cases.length);
  for (const c of run.cases) await expect(frozenSummaries(frozen).filter({ hasText: c.id })).toHaveCount(1);
  const detail = await frozenCase(frozen, dialogue);
  expect(query(page).get("question")).toBe(dialogue.id);
  await expectOriginalCase(detail, dialogue);
  await noHorizontalOverflow(page);
  await accessible(page);
  await detail.screenshot({ path: testInfo.outputPath("frozen-dialogue-exam.png") });
  await detail.getByRole("button", { name: "查看本题运行结果", exact: true }).click();
  const result = page.getByRole("region", { name: "单题详情", exact: true });
  await expect(result).toBeVisible();
  await expect(result.getByRole("heading", { name: `${dialogue.id} · ${dialogue.title}`, exact: true })).toBeVisible();
  expect(query(page).get("case")).toBe(dialogue.id);
  expect(query(page).get("baseline")).toBe(id);
  expect(query(page).get("candidate")).toBe(id);
  await page.goBack();
  await provenance(page, id);
  await expectPair(page, "", "");
  expect(query(page).get("question")).toBe(dialogue.id);
  const restored = frozenSummaries(frozen).filter({ hasText: dialogue.id }).locator("..");
  await expect(restored).toHaveAttribute("open", "");
  await expectOriginalCase(restored, dialogue);
  await page.reload();
  await provenance(page, id);
  await expect(restored).toHaveAttribute("open", "");
  await expectOriginalCase(restored, dialogue);
  await page.getByRole("button", { name: "返回时间线", exact: true }).click();
  await expect(page.getByTestId(`timeline-${run.path}`)).toBeVisible();
  await expectPair(page, "", "");
});

test("run-list and both comparison provenance links preserve the selected baseline and candidate", async ({ page }, testInfo) => {
  requireArtifacts([paths.baseline, paths.candidate].map(path => [path, ["meta.json", "cases.json", "results.jsonl"]]));
  await page.goto("/eval");
  const baseline = await runID(page, paths.baseline), candidate = await runID(page, paths.candidate);
  await page.getByRole("combobox", { name: "基线运行", exact: true }).selectOption(baseline);
  await page.getByRole("combobox", { name: "候选运行", exact: true }).selectOption(candidate);
  await page.getByRole("textbox", { name: "搜索运行", exact: true }).fill(paths.baseline.split("/").at(-1)!);
  await page.getByTestId(`run-${paths.baseline}`).getByRole("button", { name: "查看题库", exact: true }).click();
  await provenance(page, baseline);
  await expectPair(page, baseline, candidate);
  await navigateSection(page, "运行对比");
  await expect(page.getByRole("region", { name: "条件变化", exact: true })).toBeVisible();
  for (const [label, id, path] of [["基线题库", baseline, paths.baseline], ["候选题库", candidate, paths.candidate]]) {
    await page.getByRole("button", { name: label, exact: true }).click();
    const section = await provenance(page, id);
    const saved = savedRun(path);
    await expect(section.getByRole("region", { name: "冻结题库", exact: true }).locator("summary").filter({ hasText: /^L\d+-\d+\b/ })).toHaveCount(saved.cases.length);
    await expectPair(page, baseline, candidate);
    await noHorizontalOverflow(page);
    await accessible(page);
    await page.goBack();
    await expect(page.getByRole("region", { name: "条件变化", exact: true })).toBeVisible();
    await expectPair(page, baseline, candidate);
  }
  await page.getByRole("button", { name: "候选题库", exact: true }).click();
  await provenance(page, candidate);
  await page.getByRole("region", { name: "题库与评估集", exact: true }).screenshot({ path: testInfo.outputPath("candidate-provenance.png") });
  const compatible = new URL(page.url());
  compatible.searchParams.set("view", "provenance");
  await page.goto(compatible.toString());
  await provenance(page, candidate, "provenance");
  await onlyScreen(page, "题库与评估集");
  await expectPair(page, baseline, candidate);
});

test("missing historical exams stay missing while interrupted runs retain their frozen questions", async ({ page }, testInfo) => {
  requireArtifacts([[paths.legacy, ["meta.json", "results.jsonl"]], [paths.incomplete, ["meta.json", "cases.json"]]]);
  expect(existsSync(artifactPath(paths.legacy, "cases.json"))).toBe(false);
  const unfinished = savedRun(paths.incomplete);
  const question = unfinished.cases.find(c => !!c.input || !!c.turns?.length)!;
  expect(question).toBeDefined();
  await page.goto("/eval");
  const legacyID = await runID(page, paths.legacy), incompleteID = await runID(page, paths.incomplete);
  await navigateSection(page, "溯源时间线");
  await page.getByTestId(`timeline-${paths.legacy}`).getByRole("button", { name: "查看题库", exact: true }).click();
  const legacy = await provenance(page, legacyID);
  const missingExam = legacy.getByRole("region", { name: "冻结题库", exact: true });
  await expect(missingExam).toContainText("未记录");
  await expect(frozenSummaries(missingExam)).toHaveCount(0);
  await expect(missingExam.getByRole("button", { name: "查看本题运行结果", exact: true })).toHaveCount(0);
  await noHorizontalOverflow(page);
  await accessible(page);
  await missingExam.screenshot({ path: testInfo.outputPath("legacy-frozen-exam-missing.png") });
  await page.getByRole("button", { name: "返回时间线", exact: true }).click();
  await page.getByTestId(`timeline-${paths.incomplete}`).getByRole("button", { name: "查看题库", exact: true }).click();
  const incomplete = await provenance(page, incompleteID);
  await expect(incomplete).toContainText("未完成");
  const frozen = incomplete.getByRole("region", { name: "冻结题库", exact: true });
  await expect(frozenSummaries(frozen)).toHaveCount(unfinished.cases.length);
  await expectOriginalCase(await frozenCase(frozen, question), question);
  await expectPair(page, "", "");
  await noHorizontalOverflow(page);
  await accessible(page);
  await frozen.screenshot({ path: testInfo.outputPath("incomplete-frozen-exam.png") });
});

test("sidebar separates commit records and version catalogs and stays keyboard reachable on narrow screens", async ({ page }, testInfo) => {
  requireArtifacts([paths.baseline, paths.candidate, paths.incomplete].map(path => [path, ["meta.json", "cases.json"]]));
  const run = savedRun(paths.baseline), otherVersion = savedRun(paths.incomplete), commit = commitEvidence(run.meta);
  expect(run.meta.suite_version).not.toBe(otherVersion.meta.suite_version);
  const associated = historicalMetadata().filter(row => row.meta.code?.checkout_commit?.toLowerCase() === commit.commit.toLowerCase());
  expect(associated.length).toBeGreaterThan(0);
  const files = execFileSync("git", ["diff-tree", "--no-commit-id", "--name-only", "-z", "-r", commit.commit], { cwd: root, encoding: "utf8" }).split("\0").filter(Boolean);
  expect(files.length).toBeGreaterThan(0);
  await page.goto("/eval");
  const baseline = await runID(page, paths.baseline), candidate = await runID(page, paths.candidate);
  const options = await page.getByRole("combobox", { name: "基线运行", exact: true }).locator("option[data-run-label]").evaluateAll(nodes => nodes.map(node => ({ label: node.getAttribute("data-run-label"), value: node.getAttribute("value") })));
  await page.getByRole("combobox", { name: "基线运行", exact: true }).selectOption(baseline);
  await page.getByRole("combobox", { name: "候选运行", exact: true }).selectOption(candidate);
  const navigation = page.getByRole("navigation", { name: "工作台导航", exact: true });
  const menu = page.getByRole("button", { name: "工作台菜单", exact: true });
  const narrow = page.viewportSize()!.width < 1024;
  // Start from an actually scrolled run list, as a user reading older runs would.
  await page.evaluate(() => window.scrollTo({ top: window.innerHeight, behavior: "instant" }));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
  if (narrow) {
    await expect(menu).toBeVisible();
    await expect(menu).toHaveAttribute("aria-expanded", "false");
    await expect(navigation).not.toBeVisible();
    await menu.focus();
    await page.keyboard.press("Enter");
    await expect(menu).toHaveAttribute("aria-expanded", "true");
    const commits = navigation.getByRole("button", { name: "代码提交记录", exact: true });
    await expect(commits).toBeVisible();
    // Walk the actual focus order, rather than directly focusing the target item.
    for (let tabs = 0; tabs < 12 && !(await commits.evaluate(node => node === document.activeElement)); tabs++) await page.keyboard.press("Tab");
    await expect(commits).toBeFocused();
    // The inline menu can itself extend below the fold. Keep the chosen item
    // visible below the sticky header while retaining a nonzero page scroll.
    await commits.evaluate(node => {
      const headerHeight = document.querySelector("header")?.getBoundingClientRect().height ?? 0;
      const position = node.getBoundingClientRect().top + window.scrollY;
      window.scrollTo({ top: Math.max(0, position - headerHeight - 8), behavior: "instant" });
    });
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
    await noHorizontalOverflow(page);
    await page.screenshot({ path: testInfo.outputPath("workbench-menu-open.png") });
    await page.keyboard.press("Enter");
    await expect(menu).toHaveAttribute("aria-expanded", "false");
    await expect(navigation).not.toBeVisible();
    await expect(page.locator("#eval-page-heading")).toBeFocused();
  } else {
    await expect(menu).not.toBeVisible();
    await expect(navigation).toBeVisible();
    await navigation.getByRole("button", { name: "代码提交记录", exact: true }).click();
  }
  await onlyScreen(page, "代码提交记录");
  await expectHeadingBelowHeader(page, page.locator("#eval-page-heading"));
  await expectPair(page, baseline, candidate);
  const codeScreen = page.getByRole("region", { name: "代码提交记录", exact: true });
  if (!narrow) {
    const sidebarBox = await navigation.boundingBox(), screenBox = await codeScreen.boundingBox();
    expect(sidebarBox).not.toBeNull();
    expect(screenBox).not.toBeNull();
    expect(sidebarBox!.x + sidebarBox!.width).toBeLessThanOrEqual(screenBox!.x + 1);
    await expect(navigation.getByRole("button", { name: "代码提交记录", exact: true })).toHaveAttribute("aria-current", "page");
  }
  const item = page.getByTestId(`commit-record-${commit.commit.toLowerCase()}`);
  await expect(item.getByRole("heading", { name: commit.subject, exact: true })).toBeVisible();
  await expectTimestamp(item, commit.committedAt);
  const sources = item.getByRole("combobox");
  await expect(sources.locator('option:not([value=""])')).toHaveCount(associated.length);
  for (const linked of associated) {
    const id = options.find(option => option.label === linked.path)?.value;
    expect(id).toBeTruthy();
    const option = sources.locator(`option[value="${id}"]`);
    await expect(option).toHaveCount(1);
    if (linked.meta.code?.checkout_dirty) await expect(option).toContainText("未提交");
  }
  await sources.selectOption(baseline);
  await noHorizontalOverflow(page);
  await accessible(page);
  await page.screenshot({ path: testInfo.outputPath("commit-records-separated-screen.png") });
  await item.getByRole("button", { name: "查看提交文件", exact: true }).click();
  expect(query(page).get("view")).toBe("commits");
  expect(query(page).get("run")).toBe(baseline);
  const detail = page.getByRole("region", { name: "提交详情", exact: true });
  await expect(detail).toBeVisible();
  await expectHeadingBelowHeader(page, detail.getByRole("heading", { name: "提交详情", exact: true }));
  await expect(detail).toContainText(commit.subject);
  await expectTimestamp(detail, commit.committedAt);
  if (run.meta.code?.checkout_dirty) await expect(detail).toContainText("未提交");
  const fileSummary = detail.locator("summary").filter({ hasText: "这次提交涉及的文件" });
  await fileSummary.click();
  for (const file of files) await expect(detail.getByText(file, { exact: true })).toBeVisible();
  await onlyScreen(page, "代码提交记录");
  await expectPair(page, baseline, candidate);
  await page.reload();
  await expect(detail).toBeVisible();
  await expectHeadingBelowHeader(page, detail.getByRole("heading", { name: "提交详情", exact: true }));
  await expect(detail).toContainText(commit.subject);
  await expectPair(page, baseline, candidate);
  await page.getByRole("button", { name: "返回提交记录", exact: true }).click();
  await item.getByRole("combobox").selectOption(baseline);
  await item.getByRole("button", { name: "查看该运行题库", exact: true }).click();
  const linkedSuite = await provenance(page, baseline);
  await expectHeadingBelowHeader(page, linkedSuite.getByRole("heading", { level: 2 }).first());
  await expectPair(page, baseline, candidate);
  await page.getByRole("button", { name: "返回题库版本", exact: true }).click();
  await onlyScreen(page, "评估集版本目录");
  await expectPair(page, baseline, candidate);

  const versionNavigation = await openNavigation(page);
  await versionNavigation.getByRole("group", { name: "题库版本", exact: true }).getByRole("button", { name: `题库版本 ${otherVersion.meta.suite_version}`, exact: true }).click();
  if (narrow) await expect(menu).toHaveAttribute("aria-expanded", "false");
  await expectHeadingBelowHeader(page, page.locator("#eval-page-heading"));
  expect(query(page).get("view")).toBe("suites");
  expect(query(page).get("version")).toBe(otherVersion.meta.suite_version);
  const catalog = page.getByRole("region", { name: "评估集版本目录", exact: true });
  await expect(catalog.getByRole("heading", { name: `评估集 · ${otherVersion.meta.suite_version}`, exact: true })).toBeVisible();
  await expectPair(page, baseline, candidate);
  await catalog.getByRole("button", { name: "返回版本目录", exact: true }).click();
  await catalog.getByRole("button", { name: `打开题库版本 ${run.meta.suite_version}`, exact: true }).click();
  expect(query(page).get("version")).toBe(run.meta.suite_version);
  const snapshot = catalog.locator(`li[data-suite-hash="${run.meta.suite_sha256}"]`);
  await expect(snapshot).toHaveCount(1);
  await snapshot.getByRole("combobox", { name: /^内容记录 \d+ 的来源运行$/ }).selectOption(baseline);
  await noHorizontalOverflow(page);
  await accessible(page);
  await catalog.screenshot({ path: testInfo.outputPath("version-catalog.png") });
  await snapshot.getByRole("button", { name: /^查看内容记录 \d+ 的完整题目$/ }).click();
  const selectedSuite = await provenance(page, baseline);
  await expectHeadingBelowHeader(page, selectedSuite.getByRole("heading", { level: 2 }).first());
  const frozen = selectedSuite.getByRole("region", { name: "冻结题库", exact: true });
  await expect(frozenSummaries(frozen)).toHaveCount(run.cases.length);
  await expectPair(page, baseline, candidate);
  await page.goBack();
  await onlyScreen(page, "评估集版本目录");
  expect(query(page).get("version")).toBe(run.meta.suite_version);
  await page.reload();
  await expect(catalog.getByRole("heading", { name: `评估集 · ${run.meta.suite_version}`, exact: true })).toBeVisible();
  await expectPair(page, baseline, candidate);
});
