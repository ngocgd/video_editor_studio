import { expect, test } from "@playwright/test";

/**
 * Analytics against a live compose stack with a seeded owner (same
 * manual-run contract as smoke.spec.ts). The default stack has no Google
 * OAuth app, so no channel is connected: the page must say how to get data
 * instead of showing zeros, and lead to Settings > YouTube.
 */

test("analytics without a connected youtube channel", async ({ page }) => {
  await page.goto("/");

  await page.getByRole("link", { name: "Analytics" }).first().click();
  await page.waitForURL("**/analytics");
  await expect(page.getByText("Connect a YouTube channel to see its views, watch time, CTR and retention here.")).toBeVisible();
  await expect(page.locator("main")).not.toContainText("NaN");

  await page.getByRole("button", { name: "Open YouTube channels" }).click();
  await page.waitForURL("**/settings/youtube");
  await expect(page.getByRole("heading", { name: "YouTube channels" })).toBeVisible();

  // A malformed channel parameter is ignored rather than breaking the page.
  await page.goto("/analytics?channel=%3Cscript%3E");
  await expect(page.getByText("Open YouTube channels")).toBeVisible();
  await expect(page.locator("body")).not.toContainText("<script>");
});
