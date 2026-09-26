import { defineConfig } from "@playwright/test";
import { OWNER_AUTH_FILE } from "./e2e/owner-session";

/**
 * Not run in CI (review decision: "runnable locally against a stack; don't
 * add it to CI unless it's cheap and stable"): it needs a real compose
 * stack (`docker compose -f deploy/compose.yml up`) and a seeded owner
 * user, neither of which CI provisions today. Run locally with:
 *   LT_E2E_BASE_URL=http://127.0.0.1:8080 LT_E2E_EMAIL=... LT_E2E_PASSWORD=... npx playwright test
 *
 * e2e/global-setup.ts signs the owner in once and every spec starts from
 * that session, so adding spec files never adds logins against the
 * per-account login limit. smoke.spec.ts opts out to test the login form.
 */
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  globalSetup: "./e2e/global-setup.ts",
  use: {
    baseURL: process.env.LT_E2E_BASE_URL ?? "http://127.0.0.1:8080",
    viewport: { width: 1440, height: 900 },
    screenshot: "only-on-failure",
    storageState: OWNER_AUTH_FILE,
  },
});
