import { createWriteStream, existsSync, mkdirSync, readFileSync } from "node:fs";
import { createServer } from "node:http";
import { randomBytes } from "node:crypto";
import { spawn, spawnSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { isChildRunning } from "./process-state.mjs";
import { assertComposeServices, parseComposePS } from "./compose-state.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const web = join(root, "web");
const runID = new Date().toISOString().replaceAll(":", "-").replaceAll(".", "-");
const artifactDir = join(root, "artifacts", "p10", runID);
const metricsDir = join(artifactDir, "metrics");
const reportPath = join(artifactDir, "live-results.json");
mkdirSync(metricsDir, { recursive: true, mode: 0o700 });
const webBaseURL = process.env.P10_WEB_BASE_URL ?? "http://127.0.0.1:3000";

const childEnv = {
  ...process.env,
  P10_METRICS_DIR: metricsDir,
  PUBLIC_WEB_BASE_URL: webBaseURL,
  WEB_ALLOWED_ORIGIN: new URL(webBaseURL).origin,
  SHARE_TOKEN_SECRET: randomBytes(32).toString("base64url"),
};
const goCommand = process.platform === "win32" ? "go.exe" : "go";
const executableSuffix = process.platform === "win32" ? ".exe" : "";
const children = new Map();

function checkComposeDependencies() {
  const result = spawnSync("docker", ["compose", "ps", "--format", "json", "postgres", "redis"], {
    cwd: root,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    throw new Error(`docker compose ps postgres redis 失败: ${String(result.stderr).trim()}`);
  }
  assertComposeServices(parseComposePS(result.stdout));
}

function buildGoBinary(name, packagePath) {
  const binary = join(artifactDir, `${name}${executableSuffix}`);
  const result = spawnSync(goCommand, ["build", "-o", binary, packagePath], {
    cwd: root,
    env: childEnv,
    stdio: "inherit",
  });
  if (result.status !== 0) throw new Error(`${name} 构建失败: exit ${result.status}`);
  return binary;
}

function start(name, command, args, cwd) {
  if (isChildRunning(children.get(name))) throw new Error(`${name} 已在运行`);
  const output = createWriteStream(join(artifactDir, `${name}.log`), { flags: "a", mode: 0o600 });
  const child = spawn(command, args, { cwd, env: childEnv, detached: process.platform !== "win32", stdio: ["ignore", "pipe", "pipe"] });
  child.stdout.pipe(output);
  child.stderr.pipe(output);
  child.on("exit", (code, signal) => output.write(`\n[p10] process exit code=${code} signal=${signal}\n`));
  children.set(name, child);
  return child;
}

function stop(name) {
  const child = children.get(name);
  if (!isChildRunning(child)) return;
  if (process.platform === "win32") {
    spawnSync("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore" });
  } else {
    try { process.kill(-child.pid, "SIGTERM"); } catch { child.kill("SIGTERM"); }
  }
}

async function waitFor(url, timeoutMS = 120_000) {
  const deadline = Date.now() + timeoutMS;
  let last = "";
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url, { cache: "no-store" });
      if (response.ok) return;
      last = `${response.status} ${await response.text()}`;
    } catch (error) { last = String(error); }
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
  }
  throw new Error(`等待 ${url} 超时: ${last};日志位于 ${artifactDir}`);
}

function readMetrics() {
  const events = [];
  for (const component of ["api", "buildsvc"]) {
    const path = join(metricsDir, `${component}.jsonl`);
    if (!existsSync(path)) continue;
    for (const line of readFileSync(path, "utf8").split("\n")) {
      if (line.trim()) events.push(JSON.parse(line));
    }
  }
  return events.sort((a, b) => String(a.timestamp).localeCompare(String(b.timestamp)));
}

async function restartAPI() {
  stop("api");
  const deadline = Date.now() + 15_000;
  while (isChildRunning(children.get("api")) && Date.now() < deadline) {
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 100));
  }
  start("api", apiBinary, [], root);
  await waitFor("http://127.0.0.1:8082/readyz");
}

checkComposeDependencies();
const buildsvcBinary = buildGoBinary("buildsvc", "./cmd/buildsvc");
const apiBinary = buildGoBinary("api", "./cmd/api");
const nextEntrypoint = join(web, "node_modules", "next", "dist", "bin", "next");

start("buildsvc", buildsvcBinary, [], root);
await waitFor("http://127.0.0.1:8081/.well-known/agent-card.json");
start("api", apiBinary, [], root);
await waitFor("http://127.0.0.1:8082/readyz");
start("web", process.execPath, [nextEntrypoint, "dev", "--hostname", "127.0.0.1"], web);
await waitFor("http://127.0.0.1:3000");

const server = createServer(async (request, response) => {
  response.setHeader("Content-Type", "application/json; charset=utf-8");
  response.setHeader("Cache-Control", "no-store");
  if (request.method === "GET" && request.url === "/healthz") {
    const healthy = ["buildsvc", "api", "web"].every((name) => isChildRunning(children.get(name)));
    response.statusCode = healthy ? 200 : 503;
    response.end(JSON.stringify({ status: healthy ? "ready" : "failed" }));
    return;
  }
  if (request.method === "GET" && request.url === "/config") {
    response.end(JSON.stringify({ artifact_dir: artifactDir, metrics_dir: metricsDir, report_path: reportPath }));
    return;
  }
  if (request.method === "GET" && request.url === "/metrics") {
    response.end(JSON.stringify({ events: readMetrics() }));
    return;
  }
  if (request.method === "POST" && request.url === "/restart-api") {
    try {
      await restartAPI();
      response.end(JSON.stringify({ status: "ready" }));
    } catch (error) {
      response.statusCode = 500;
      response.end(JSON.stringify({ error: String(error) }));
    }
    return;
  }
  response.statusCode = 404;
  response.end(JSON.stringify({ error: "not_found" }));
});

server.listen(18083, "127.0.0.1");

function shutdown() {
  server.close();
  for (const name of ["web", "api", "buildsvc"]) stop(name);
}
process.once("SIGINT", () => { shutdown(); process.exit(130); });
process.once("SIGTERM", () => { shutdown(); process.exit(143); });
process.once("exit", shutdown);
