import { defineConfig } from "@playwright/test";
import { OWNER_AUTH_FILE } from "./e2e/owner-session";

/**
 * Not run in CI (review decision: "runnable locally against a stack; don't
 * add it to CI unless it's cheap and stable"): it needs a real compose
 * stack (`docker compose -f deploy/compose.yml up`) and a seeded owner
 * user, neither of which CI provisions today. Run locally with:
 *   LT_E2E_BASE_URL=http://127.0.0.1:8080 LT_E2E_EMAIL=... LT_E2E_PASSWORD=... npx playwright test
 *
 * Sign-ins: the API limits logins per account (a burst of 5, then one
 * every 12 s) and a login revokes the account's other sessions. So
 * e2e/global-setup.ts signs the owner in once and every "signed-in" spec
 * reuses that session, however many spec files there are. smoke.spec.ts
 * tests the login form itself, and its login would end that shared
 * session, so it runs in its own project after all signed-in specs.
 */
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  globalSetup: "./e2e/global-setup.ts",
  use: {
    baseURL: process.env.LT_E2E_BASE_URL ?? "http://127.0.0.1:8080",
    viewport: { width: 1440, height: 900 },
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "signed-in", testIgnore: /smoke\.spec\.ts$/, use: { storageState: OWNER_AUTH_FILE } },
    { name: "login-form", testMatch: /smoke\.spec\.ts$/, dependencies: ["signed-in"] },
  ],
});
