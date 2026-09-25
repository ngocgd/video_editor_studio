import { defineConfig } from "@playwright/test";

/**
 * Not run in CI (review decision: "runnable locally against a stack; don't
 * add it to CI unless it's cheap and stable"): it needs a real compose
 * stack (`docker compose -f deploy/compose.yml up`) and a seeded owner
 * user, neither of which CI provisions today. Run locally with:
 *   LT_E2E_BASE_URL=http://127.0.0.1:8080 LT_E2E_EMAIL=... LT_E2E_PASSWORD=... npx playwright test
 */
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: {
    baseURL: process.env.LT_E2E_BASE_URL ?? "http://127.0.0.1:8080",
    viewport: { width: 1440, height: 900 },
    screenshot: "only-on-failure",
  },
});
