// Playwright E2E smoke testleri (Faz 02).
// Varsayılan: yerleşik mock API + vite preview (web build gerektirir).
// Gerçek Control Plane'e karşı: E2E_REAL=1 (API_PROXY_TARGET ile preview proxy
// gerçek sunucuya çevrilir; Makefile/CI bunu yapar).
import { defineConfig, devices } from "@playwright/test";

const realBackend = process.env.E2E_REAL === "1";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://127.0.0.1:4173",
    viewport: { width: 1280, height: 800 },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    locale: "tr-TR",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: realBackend
    ? [
        {
          command: "npm run preview -- --port 4173 --strictPort --host 127.0.0.1",
          url: "http://127.0.0.1:4173",
          reuseExistingServer: true,
          timeout: 60_000,
        },
      ]
    : [
        {
          command: "node e2e/mock-api.mjs",
          url: "http://127.0.0.1:8787/e2e/health",
          reuseExistingServer: true,
          timeout: 30_000,
        },
        {
          command: "npm run preview -- --port 4173 --strictPort --host 127.0.0.1",
          url: "http://127.0.0.1:4173",
          reuseExistingServer: true,
          timeout: 60_000,
        },
      ],
});
