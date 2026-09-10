import { defineConfig, devices } from "@playwright/test";

// Start the Go offline requirements harness and Next.js before this suite.
// All business state comes from the real API and PostgreSQL; no route mocks.
export default defineConfig({
  testDir: "./e2e-requirements",
  fullyParallel: false,
  workers: 1,
  timeout: 90_000,
  reporter: "list",
  use: {
    baseURL: process.env.REQUIREMENTS_WEB_URL ?? "http://127.0.0.1:3100",
    trace: "retain-on-failure",
  },
  projects: [
    { name: "desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 960 } } },
    { name: "tablet", use: { ...devices["Desktop Chrome"], viewport: { width: 768, height: 1024 } } },
    { name: "mobile", use: { ...devices["Pixel 5"], viewport: { width: 375, height: 812 } } },
  ],
});
