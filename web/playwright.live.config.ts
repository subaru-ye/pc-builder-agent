import { defineConfig, devices } from "@playwright/test";

if (process.env.P10_LIVE !== "1") {
  throw new Error("P10 Live E2E 会调用真实模型；只能在明确设置 P10_LIVE=1 后运行。");
}

export default defineConfig({
  testDir: "./e2e-live",
  testMatch: "stage1-live.spec.ts",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: "list",
  timeout: 45 * 60 * 1000,
  expect: { timeout: 10 * 60 * 1000 },
  use: {
    baseURL: process.env.P10_WEB_BASE_URL ?? "http://127.0.0.1:3000",
    trace: "retain-on-failure",
    video: "retain-on-failure",
  },
  webServer: {
    command: "node e2e-live/orchestrator.mjs",
    url: "http://127.0.0.1:18083/healthz",
    reuseExistingServer: false,
    timeout: 180_000,
  },
  projects: [{ name: "live-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 960 } } }],
});
