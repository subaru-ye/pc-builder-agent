import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import { request as newRequest, expect, test, type BrowserContext, type Page, type Response } from "@playwright/test";
import type { BuildDiff, BuildView, Session } from "../src/lib/api/types";

const controlURL = process.env.P10_CONTROL_URL ?? "http://127.0.0.1:18083";
const webURL = process.env.P10_WEB_BASE_URL ?? "http://127.0.0.1:3000";

type MetricEvent = { component: string; name: string; fields?: Record<string, unknown> };
type RunCapture = { runID: string; firstProgressMS: number; totalMS: number; maxGapMS: number };
type RunCaptureOutcome = { capture?: RunCapture; error?: Error };
type Trial = {
  scenario: string; repetition: number; session_fingerprint: string; run_fingerprints: string[];
  final_phase: string; versions: number[]; first_progress_ms: number; total_ms: number; max_sse_gap_ms: number;
  screening_calls: number; builder_calls: number; validation_rounds: number; embedding_cache: string;
  total_cny?: string; budget_delta_cny?: string; overall_status?: string;
  diff_summary?: Record<string, unknown>; assertions: Record<string, boolean>;
};
type LiveReport = {
  schema_version: 1; generated_at: string;
  models: { screening: string; builder: string; embedding: string };
  trials: Trial[]; route_latency_ms: number[];
};

test.describe.configure({ mode: "serial" });

test("L1-L6 complete live matrix passes three consecutive times", async ({ browser }) => {
  const config = await controlJSON<{ report_path: string }>("/config");
  const report: LiveReport = {
    schema_version: 1,
    generated_at: new Date().toISOString(),
    models: { screening: "", builder: "", embedding: "" },
    trials: [],
    route_latency_ms: [],
  };
  const save = () => writeFileSync(config.report_path, `${JSON.stringify(report, null, 2)}\n`, "utf8");

  try {
    for (let repetition = 1; repetition <= 3; repetition++) {
      const context = await browser.newContext();
      const page = await context.newPage();
      const observer = observePage(page, report.route_latency_ms);
      let sessionID = "";

      const l1 = await measure("L1", repetition, observer, 2, async () => {
        sessionID = await startSession(page, "8000 元，2K 玩黑神话：悟空");
        await waitPhase(page, sessionID, "requirement_ready");
        await page.getByRole("button", { name: "确认并生成配置" }).click();
        await waitPhase(page, sessionID, "ready", 1);
        const build = await apiJSON<BuildView>(page, `/api/v1/sessions/${sessionID}/builds/1`);
        const budget = build.requirement.budget_cny;
        const budgetFlex = build.requirement.budget_flex ?? 0.1;
        const total = Number(build.quote.total_cny);
        return trialResult(sessionID, "ready", [1], build, {
          requirement_confirmed: true,
          version_v1: build.summary.version === 1 && build.summary.parent_version == null,
          validation_pass: build.validation.overall_status === "pass",
          budget_within_flex: Math.abs(total - budget) <= budget * budgetFlex,
        });
      });
      recordTrial(report, l1, save);

      const l3 = await measure("L3", repetition, observer, 1, async () => {
        await sendComposer(page, "降 500");
        await waitPhase(page, sessionID, "ready", 2);
        const build = await apiJSON<BuildView>(page, `/api/v1/sessions/${sessionID}/builds/2`);
        const diff = await apiJSON<BuildDiff>(page, `/api/v1/sessions/${sessionID}/diff?from=1&to=2`);
        const changed = diff.lines.filter((line) => line.changed).map((line) => line.category);
        return trialResult(sessionID, "ready", [1, 2], build, {
          version_v2: build.summary.version === 2 && build.summary.parent_version === 1,
          budget_7500: build.requirement.budget_cny === 7500,
          validation_pass: build.validation.overall_status === "pass",
          minimal_change: changed.length > 0 && changed.length <= 2,
        }, { changed_categories: changed, total_delta_cny: diff.total_delta_cny });
      });
      recordTrial(report, l3, save);

      const restart = await fetch(`${controlURL}/restart-api`, { method: "POST" });
      expect(restart.ok, `API restart failed: ${await restart.text()}`).toBeTruthy();
      await expect.poll(async () => (await apiJSON<Session>(page, `/api/v1/sessions/${sessionID}`)).phase).toBe("ready");

      const l4 = await measure("L4", repetition, observer, 1, async () => {
        await page.reload();
        await waitPhase(page, sessionID, "ready", 2);
        await sendComposer(page, "换成 A 卡");
        await waitPhase(page, sessionID, "ready", 3);
        const build = await apiJSON<BuildView>(page, `/api/v1/sessions/${sessionID}/builds/3`);
        const diff = await apiJSON<BuildDiff>(page, `/api/v1/sessions/${sessionID}/diff?from=2&to=3`);
        const changed = diff.lines.filter((line) => line.changed).map((line) => line.category);
        const gpu = build.parts.find((part) => part.category === "gpu");
        const delivery = await verifyDeliveryAndRevocation(page, context, sessionID);
        return trialResult(sessionID, "ready", [1, 2, 3], build, {
          version_v3: build.summary.version === 3 && build.summary.parent_version === 2,
          amd_gpu: /AMD|Radeon|RX\s?\d/i.test(`${gpu?.name ?? ""} ${gpu?.sku ?? ""}`),
          only_gpu_changed: changed.length === 1 && changed[0] === "gpu",
          validation_pass: build.validation.overall_status === "pass",
          ...delivery,
        }, { changed_categories: changed, total_delta_cny: diff.total_delta_cny });
      });
      recordTrial(report, l4, save);
      await context.close();

      const l2Context = await browser.newContext();
      const l2Page = await l2Context.newPage();
      const l2Observer = observePage(l2Page, report.route_latency_ms);
      const l2 = await measure("L2", repetition, l2Observer, 2, async () => {
        const id = await startSession(l2Page, "8000 元，2K 游戏，想要安静的显卡和白色海景房机箱，品牌不限");
        await waitPhase(l2Page, id, "requirement_ready");
        const pending = await apiJSON<Session>(l2Page, `/api/v1/sessions/${id}`);
        await l2Page.getByRole("button", { name: "确认并生成配置" }).click();
        await waitPhase(l2Page, id, "ready", 1);
        const build = await apiJSON<BuildView>(l2Page, `/api/v1/sessions/${id}/builds/1`);
        const rationale = build.parts.filter((part) => part.category === "gpu" || part.category === "case").map((part) => part.rationale).join(" ");
        return trialResult(id, "ready", [1], build, {
          no_inferred_gpu_brand: pending.pending_requirement?.brand_pref?.gpu === "any" || pending.pending_requirement?.brand_pref?.gpu == null,
          semantic_reason_visible: /安静|低噪|白色|海景|风格/.test(rationale),
          validation_pass: build.validation.overall_status === "pass",
        });
      });
      recordTrial(report, l2, save);
      await l2Context.close();

      const l5Context = await browser.newContext();
      const l5Page = await l5Context.newPage();
      const l5Observer = observePage(l5Page, report.route_latency_ms);
      const l5 = await measure("L5", repetition, l5Observer, 1, async () => {
        const id = await startSession(l5Page, "我想配台电脑");
        await expect.poll(async () => {
          const current = await apiJSON<Session>(l5Page, `/api/v1/sessions/${id}`);
          const hasQuestion = current.messages.some((message) => message.role === "assistant" && message.content.length > 0);
          return `${current.phase}:${current.active_run == null}:${hasQuestion}`;
        }).toBe("collecting:true:true");
        const session = await apiJSON<Session>(l5Page, `/api/v1/sessions/${id}`);
        const builds = await apiJSON<{ builds: unknown[] }>(l5Page, `/api/v1/sessions/${id}/builds`);
        return {
          sessionID: id, finalPhase: session.phase, versions: [], assertions: {
            asks_question: session.messages.some((message) => message.role === "assistant" && message.content.length > 0),
            no_build: builds.builds.length === 0,
          },
        };
      });
      recordTrial(report, l5, save);
      await l5Context.close();

      const l6Context = await browser.newContext();
      const l6Page = await l6Context.newPage();
      const l6Observer = observePage(l6Page, report.route_latency_ms);
      const l6 = await measure("L6", repetition, l6Observer, 2, async () => {
        const id = await startSession(l6Page, "8000 元，2K 玩黑神话，常规噪音即可");
        await waitPhase(l6Page, id, "requirement_ready");
        await l6Page.getByLabel("预算（元）").fill("8500");
        await l6Page.getByLabel("噪音偏好").selectOption("silent");
        await l6Page.getByRole("button", { name: "确认并生成配置" }).click();
        await waitPhase(l6Page, id, "ready", 1);
        const build = await apiJSON<BuildView>(l6Page, `/api/v1/sessions/${id}/builds/1`);
        return trialResult(id, "ready", [1], build, {
          edited_budget_used: build.requirement.budget_cny === 8500,
          edited_noise_used: build.requirement.noise_pref === "silent",
          validation_pass: build.validation.overall_status === "pass",
        });
      });
      recordTrial(report, l6, save);
      await l6Context.close();
    }

    const metrics = await metricEvents();
    report.models.screening = modelName(metrics, "api", "model.call");
    report.models.builder = modelName(metrics, "buildsvc", "model.call");
    report.models.embedding = String(metrics.find((event) => event.name === "embedding.cache")?.fields?.model ?? "");
    report.generated_at = new Date().toISOString();
    save();

    expect(report.trials).toHaveLength(18);
    expect(report.models.screening).not.toBe("");
    expect(report.models.builder).not.toBe("");
    expect(report.models.embedding).not.toBe("");
    for (const trial of report.trials) {
      expect(Object.values(trial.assertions).every(Boolean), `${trial.scenario}/${trial.repetition} assertions`).toBeTruthy();
      expect(trial.first_progress_ms).toBeLessThanOrEqual(1000);
      expect(trial.max_sse_gap_ms).toBeLessThanOrEqual(30_000);
      expect(trial.total_ms).toBeLessThanOrEqual(600_000);
    }
  } finally {
    save();
  }
});

function observePage(page: Page, routeLatency: number[]) {
  const captures: Promise<RunCaptureOutcome>[] = [];
  page.on("response", (response) => {
    const path = new URL(response.url()).pathname;
    if (response.request().method() === "POST" && (/\/messages$/.test(path) || /\/requirement\/confirm$/.test(path)) && response.ok()) {
      void response.json().then((body: { id?: string }) => {
        if (body.id) captures.push(captureRun(page.context(), body.id).then(
          (capture) => ({ capture }),
          (error: unknown) => ({ error: error instanceof Error ? error : new Error(String(error)) }),
        ));
      });
    }
    if (response.request().method() === "GET" && (/^\/api\/v1\/sessions/.test(path) || path === "/readyz") && !path.endsWith("/events")) {
      void recordLatency(response, routeLatency);
    }
  });
  return { captures };
}

async function recordLatency(response: Response, target: number[]) {
  await response.finished();
  const duration = response.request().timing().responseEnd;
  if (duration >= 0 && duration < 60_000) target.push(Math.round(duration));
}

async function measure(
  scenario: string,
  repetition: number,
  observer: ReturnType<typeof observePage>,
  expectedRuns: number,
  action: () => Promise<{ sessionID: string; finalPhase: string; versions: number[]; build?: BuildView; diffSummary?: Record<string, unknown>; assertions: Record<string, boolean> }>,
): Promise<Trial> {
  const beforeMetrics = await metricEvents();
  const beforeRuns = observer.captures.length;
  const started = Date.now();
  const result = await action();
  await waitUntil(() => observer.captures.length >= beforeRuns + expectedRuns, 10_000, "未捕获预期 run 响应");
  const outcomes = await Promise.all(observer.captures.slice(beforeRuns, beforeRuns + expectedRuns));
  const captureError = outcomes.find((outcome) => outcome.error)?.error;
  if (captureError) throw captureError;
  const runs = outcomes.map((outcome) => outcome.capture as RunCapture);
  const afterMetrics = await metricEvents();
  const delta = afterMetrics.slice(beforeMetrics.length);
  const cache = delta.filter((event) => event.name === "embedding.cache").map((event) => String(event.fields?.status ?? ""));
  return {
    scenario, repetition,
    session_fingerprint: fingerprint(result.sessionID),
    run_fingerprints: runs.map((run) => fingerprint(run.runID)),
    final_phase: result.finalPhase,
    versions: result.versions,
    first_progress_ms: Math.max(...runs.map((run) => run.firstProgressMS)),
    total_ms: Date.now() - started,
    max_sse_gap_ms: Math.max(...runs.map((run) => run.maxGapMS)),
    screening_calls: delta.filter((event) => event.component === "api" && event.name === "model.call").length,
    builder_calls: delta.filter((event) => event.component === "buildsvc" && event.name === "model.call").length,
    validation_rounds: delta.filter((event) => event.name === "validation.round").length,
    embedding_cache: [...new Set(cache)].join(",") || "not_used",
    total_cny: result.build?.quote.total_cny,
    budget_delta_cny: result.build?.quote.budget_delta_cny,
    overall_status: result.build?.validation.overall_status,
    diff_summary: result.diffSummary,
    assertions: result.assertions,
  };
}

function trialResult(sessionID: string, finalPhase: string, versions: number[], build: BuildView, assertions: Record<string, boolean>, diffSummary?: Record<string, unknown>) {
  return { sessionID, finalPhase, versions, build, assertions, diffSummary };
}

function recordTrial(report: LiveReport, trial: Trial, save: () => void) {
  report.trials.push(trial);
  save();
  const failedAssertions = Object.entries(trial.assertions).filter(([, passed]) => !passed).map(([name]) => name);
  expect(failedAssertions, `${trial.scenario}/${trial.repetition} failed assertions`).toEqual([]);
  expect(trial.first_progress_ms, `${trial.scenario}/${trial.repetition} first progress`).toBeLessThanOrEqual(1000);
  expect(trial.max_sse_gap_ms, `${trial.scenario}/${trial.repetition} SSE gap`).toBeLessThanOrEqual(30_000);
  expect(trial.total_ms, `${trial.scenario}/${trial.repetition} total`).toBeLessThanOrEqual(600_000);
}

async function startSession(page: Page, prompt: string) {
  await page.goto("/");
  await page.locator("#message-composer").fill(prompt);
  const createResponse = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v1/sessions", { timeout: 30_000 });
  const messageResponse = page.waitForResponse((response) => response.request().method() === "POST" && /\/api\/v1\/sessions\/[^/]+\/messages$/.test(new URL(response.url()).pathname), { timeout: 30_000 });
  await page.getByRole("button", { name: "发送" }).click();
  for (const response of await Promise.all([createResponse, messageResponse])) {
    if (!response.ok()) throw new Error(`${response.request().method()} ${new URL(response.url()).pathname} -> ${response.status()} ${await response.text()}`);
  }
  await page.waitForURL(/\/s\/[^/?]+/, { timeout: 30_000 });
  return decodeURIComponent(new URL(page.url()).pathname.split("/").at(-1) ?? "");
}

async function sendComposer(page: Page, text: string) {
  await page.locator("#message-composer").fill(text);
  await page.getByRole("button", { name: "发送" }).click();
}

async function waitPhase(page: Page, sessionID: string, phase: string, versionCount?: number) {
  await expect.poll(async () => {
    const session = await apiJSON<Session>(page, `/api/v1/sessions/${sessionID}`);
    return `${session.phase}:${session.version_count}`;
  }).toBe(`${phase}:${versionCount ?? 0}`);
}

async function apiJSON<T>(page: Page, path: string): Promise<T> {
  const response = await page.context().request.get(path);
  if (!response.ok()) throw new Error(`${path} -> ${response.status()} ${await response.text()}`);
  return response.json() as Promise<T>;
}

async function captureRun(context: BrowserContext, runID: string): Promise<RunCapture> {
  const cookies = await context.cookies(webURL);
  const cookie = cookies.map((item) => `${item.name}=${item.value}`).join("; ");
  const deadline = Date.now() + 600_000;
  const events: { event: string; data: { timestamp: string } }[] = [];
  const seen = new Set<string>();
  const arrivals: number[] = [];
  let lastEventID = "";
  let backoffMS = 1000;
  let completed = false;
  let lastError = "connection closed before run.completed";

  while (!completed && Date.now() < deadline) {
    try {
      const headers: Record<string, string> = { Accept: "text/event-stream", Cookie: cookie };
      if (lastEventID) headers["Last-Event-ID"] = lastEventID;
      const response = await fetch(`${webURL}/api/v1/runs/${runID}/events`, { headers });
      if (!response.ok || !response.body) throw new Error(`SSE ${runID} -> ${response.status}`);
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (!completed) {
        const chunk = await reader.read();
        if (chunk.done) break;
        arrivals.push(Date.now());
        buffer += decoder.decode(chunk.value, { stream: true }).replaceAll("\r\n", "\n");
        let boundary = buffer.indexOf("\n\n");
        while (boundary >= 0) {
          const frame = buffer.slice(0, boundary);
          buffer = buffer.slice(boundary + 2);
          const lines = frame.split("\n");
          const id = lines.find((line) => line.startsWith("id:"))?.slice(3).trim() ?? "";
          const event = lines.find((line) => line.startsWith("event:"))?.slice(6).trim();
          const data = lines.filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trim()).join("\n");
          if (id) lastEventID = id;
          if (event && data && (!id || !seen.has(id))) {
            if (id) seen.add(id);
            events.push({ event, data: JSON.parse(data) as { timestamp: string } });
            if (event === "run.completed") completed = true;
          }
          boundary = buffer.indexOf("\n\n");
        }
      }
      if (completed) await reader.cancel();
      backoffMS = 1000;
    } catch (error) {
      lastError = String(error);
    }
    if (!completed) {
      await new Promise((resolveDelay) => setTimeout(resolveDelay, backoffMS));
      backoffMS = Math.min(backoffMS * 2, 15_000);
    }
  }
  if (!completed) throw new Error(`SSE ${runID} 在 10 分钟内未完成: ${lastError}`);
  const start = events.find((event) => event.event === "run.started");
  const progress = events.find((event) => event.event === "run.progress");
  const completedEvent = [...events].reverse().find((event) => event.event === "run.completed");
  if (!start || !progress || !completedEvent) throw new Error(`run ${runID} 缺少必需 SSE 事件`);
  const gaps = arrivals.slice(1).map((value, index) => value - arrivals[index]);
  return {
    runID,
    firstProgressMS: Math.max(0, Date.parse(progress.data.timestamp) - Date.parse(start.data.timestamp)),
    totalMS: Math.max(1, Date.parse(completedEvent.data.timestamp) - Date.parse(start.data.timestamp)),
    maxGapMS: gaps.length ? Math.max(...gaps) : 0,
  };
}

async function verifyDeliveryAndRevocation(page: Page, ownerContext: BrowserContext, sessionID: string) {
  const ownerMarkdown = await ownerContext.request.get(`/api/v1/sessions/${sessionID}/builds/3/export.md`);
  await page.getByRole("button", { name: "分享配置 v3" }).click();
  await page.getByRole("button", { name: "创建当前版本分享" }).click();
  const input = page.getByLabel("分享链接");
  await expect(input).toBeVisible();
  const shareURL = await input.inputValue();
  const token = new URL(shareURL).pathname.split("/").at(-1) ?? "";
  const publicJSON = await ownerContext.request.get(`/api/v1/public/shares/${token}`);
  const publicPage = await ownerContext.request.get(`/share/${token}`);
  const publicMarkdown = await ownerContext.request.get(`/api/v1/public/shares/${token}/export.md`);
  const image = await ownerContext.request.get(`/share/${token}/image`);
  const outsider = await newRequest.newContext({ baseURL: webURL });
  const outsiderRevoke = await outsider.delete(`/api/v1/shares/${token}`, { headers: { "Idempotency-Key": crypto.randomUUID() } });
  await outsider.dispose();
  const share = (await publicJSON.json()) as { summary: { version: number } };
  const ownerRevoke = await ownerContext.request.delete(`/api/v1/shares/${token}`, { headers: { "Idempotency-Key": crypto.randomUUID() } });
  const after = await Promise.all([
    ownerContext.request.get(`/api/v1/public/shares/${token}`),
    ownerContext.request.get(`/share/${token}`),
    ownerContext.request.get(`/api/v1/public/shares/${token}/export.md`),
    ownerContext.request.get(`/share/${token}/image`),
  ]);
  await page.keyboard.press("Escape");
  return {
    markdown_ok: ownerMarkdown.ok() && publicMarkdown.ok(),
    share_version_v3: publicJSON.ok() && share.summary.version === 3,
    public_ssr_ok: publicPage.ok(),
    png_ok: image.ok() && image.headers()["content-type"] === "image/png",
    outsider_revoke_404: outsiderRevoke.status() === 404,
    owner_revoke_204: ownerRevoke.status() === 204,
    revoked_all_404: after.every((response) => response.status() === 404),
  };
}

async function metricEvents(): Promise<MetricEvent[]> {
  return (await controlJSON<{ events: MetricEvent[] }>("/metrics")).events;
}

async function controlJSON<T>(path: string): Promise<T> {
  const response = await fetch(`${controlURL}${path}`, { cache: "no-store" });
  if (!response.ok) throw new Error(`control ${path} -> ${response.status} ${await response.text()}`);
  return response.json() as Promise<T>;
}

function modelName(events: MetricEvent[], component: string, name: string) {
  return String(events.find((event) => event.component === component && event.name === name)?.fields?.model ?? "");
}

function fingerprint(value: string) {
  return createHash("sha256").update(value).digest("hex").slice(0, 12);
}

async function waitUntil(predicate: () => boolean, timeoutMS: number, message: string) {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 50));
  }
  throw new Error(message);
}
