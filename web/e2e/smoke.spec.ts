import { expect, test } from "@playwright/test";
import { OWNER_EMAIL as EMAIL, OWNER_PASSWORD as PASSWORD } from "./owner-session";

/**
 * A real end-to-end smoke test against a live compose stack (not mocks).
 * Not part of CI (see playwright.config.ts): create an owner first with
 *   docker compose -f ../deploy/compose.yml run --rm --entrypoint /usr/local/bin/loomtale api \
 *     create-owner -tenant "Smoke" -email owner@example.com
 * then run
 *   LT_E2E_EMAIL=owner@example.com LT_E2E_PASSWORD=... npx playwright test
 *
 * This covers the login -> app shell -> logout path and the CSP headers.
 * Enqueuing a real job and watching a live SSE update needs a registered
 * pipeline step-kind handler, which does not exist until a later phase
 * (see the phase report); that path was verified manually against a
 * seeded database row instead and is not re-tested here to keep this file
 * runnable against any stack, not just one with test fixtures pre-loaded.
 */
// Starts signed out (not from global-setup's session): this spec is the
// one that exercises the login form, and its logout ends only this session.
test.use({ storageState: { cookies: [], origins: [] } });

test("login, app shell, command palette, logout", async ({ page }) => {
  const response = await page.goto("/login");
  expect(response?.headers()["content-security-policy"]).toContain("script-src 'self'");
  expect(response?.headers()["content-security-policy"]).toContain("trusted-types default dompurify");

  const consoleErrors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });

  await page.getByLabel("Email").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/");

  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  await expect(page.getByText("GPU")).toBeVisible();

  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Render Queue" }).click();
  await expect(page.getByRole("heading", { name: "Render Queue" })).toBeVisible();

  await page.keyboard.press("Control+k");
  await expect(page.getByPlaceholder("Type a command or search...")).toBeVisible();
  await page.keyboard.press("Escape");

  await page.goto("/settings/account");
  await expect(page.getByRole("paragraph").getByText(EMAIL)).toBeVisible();

  await page.getByRole("button", { name: new RegExp(EMAIL) }).click();
  await page.getByRole("menuitem", { name: "Log out" }).click();
  await page.waitForURL("**/login*");

  const csp = consoleErrors.filter((message) => /content security policy|refused to/i.test(message));
  expect(csp).toEqual([]);
});
