import { defineConfig, devices } from "@playwright/test";

// This suite reads existing local artifacts. It never starts the model runner.
// The local workbench targets PC usage; routine validation runs on desktop only.
export default defineConfig({
  testDir: "./e2e-evaldesk",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: "list",
  timeout: 120_000,
  expect: { timeout: 30_000 },
  outputDir: "test-results/evaldesk",
  use: {
    baseURL: process.env.EVALDESK_WEB_BASE_URL ?? "http://127.0.0.1:3000",
    trace: "retain-on-failure",
  },
  projects: [
    { name: "evaldesk-desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 960 } } },
  ],
});
