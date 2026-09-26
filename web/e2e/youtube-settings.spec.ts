import { expect, test } from "@playwright/test";

/**
 * Settings > YouTube against a live compose stack with a seeded owner (same
 * manual-run contract as smoke.spec.ts). The default compose stack has no
 * Google OAuth app configured, so the page must say so honestly, keep the
 * Connect button disabled, still show the daily quota meter, and turn a
 * callback error redirect into a readable alert without reflecting anything
 * but the fixed reason codes.
 */
const EMAIL = process.env.LT_E2E_EMAIL ?? "owner@loomtale.local";
const PASSWORD = process.env.LT_E2E_PASSWORD ?? "LoomtaleDemo!2026";

test("settings/youtube without a configured google oauth app", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/");

  await page.goto("/settings/youtube");
  await expect(page.getByRole("heading", { name: "YouTube channels" })).toBeVisible();
  await expect(page.getByRole("note")).toContainText("Google OAuth is not configured");
  await expect(page.getByRole("button", { name: "Connect a YouTube channel" })).toBeDisabled();
  await expect(page.getByText(/units used today/)).toBeVisible();

  await page.goto("/settings/youtube?connect=error&reason=%3Cscript%3E");
  await expect(page.getByRole("heading", { name: "YouTube channels" })).toBeVisible();
  await expect(page.locator("body")).not.toContainText("<script>");
});
