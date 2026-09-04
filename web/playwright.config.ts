import { defineConfig, devices } from "@playwright/test";

const inCI = process.env.CI !== undefined;

export default defineConfig({
  testDir: "./e2e",
  outputDir: "./test-results",
  fullyParallel: false,
  forbidOnly: inCI,
  retries: inCI ? 2 : 0,
  workers: 1,
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],
  expect: {
    timeout: 5_000,
    toHaveScreenshot: {
      animations: "disabled",
      caret: "hide",
      maxDiffPixels: 0,
      scale: "css",
    },
  },
  use: {
    baseURL: "http://127.0.0.1:4173",
    colorScheme: "light",
    locale: "en-US",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        deviceScaleFactor: 1,
      },
    },
  ],
  // Two servers: Vite (the app under test, as before) and the Go fit
  // server the "real fit" spec drives at /api through vite.config.ts's
  // proxy. Both are always started -- Playwright has no per-project
  // webServer, so scoping the Go server to one spec is not an option here --
  // but scripts/playwright-go-server.sh guards the second entry so a missing
  // Go toolchain degrades to a spec-level skip rather than an aborted run.
  // See that script's own comment for the reasoning.
  webServer: [
    {
      command: "npm run dev -- --host 127.0.0.1 --port 4173 --strictPort",
      url: "http://127.0.0.1:4173",
      reuseExistingServer: !inCI,
      timeout: 30_000,
    },
    {
      command: "bash scripts/playwright-go-server.sh",
      url: "http://127.0.0.1:8080/api/version",
      reuseExistingServer: !inCI,
      timeout: 60_000,
    },
  ],
});
