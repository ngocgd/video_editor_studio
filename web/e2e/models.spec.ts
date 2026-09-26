import { expect, test } from "@playwright/test";

/**
 * Model manager against a live stack (see smoke.spec.ts for setup). The
 * base stack has no GPU worker, so an install stays queued: this covers
 * the manifest listing, the licence block, and install -> pause through
 * the real API and database, not the download itself (the GPU stack's
 * benchmark run covers that).
 */
const EMAIL = process.env.LT_E2E_EMAIL ?? "owner@loomtale.local";
const PASSWORD = process.env.LT_E2E_PASSWORD ?? "LoomtaleDemo!2026";

test("model manager lists models, blocks licences, installs and pauses", async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });

  await page.goto("/login");
  await page.getByLabel("Email").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/");

  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Models" }).click();
  await expect(page.getByRole("heading", { name: "Models & providers" })).toBeVisible();
  await expect(page.getByText("Commercial-use licences only. One GPU model loaded at a time.")).toBeVisible();

  const grid = page.getByRole("grid", { name: "Model manager" });
  const row = (name: string) => grid.getByRole("row").filter({ has: page.getByText(name, { exact: true }) });

  await expect(row("z-image-turbo")).toContainText("Apache-2.0");
  await expect(row("qwen-image-edit-2511")).toContainText("Character sheets");
  await expect(row("illustrious-xl-v1.1")).toContainText("Blocked: licence");
  await expect(row("illustrious-xl-v1.1").getByRole("button")).toHaveCount(0);

  await row("qwen-image").getByRole("button", { name: "Install qwen-image" }).click();
  await expect(row("qwen-image").getByLabel(/Downloading \d+%/)).toBeVisible();

  await row("qwen-image").getByRole("button", { name: "Pause qwen-image" }).click();
  await expect(row("qwen-image")).toContainText("Paused");
  await expect(row("qwen-image").getByRole("button", { name: "Resume qwen-image" })).toBeVisible();

  await page.screenshot({ path: "test-results/model-manager.png", fullPage: true });

  const csp = consoleErrors.filter((message) => /content security policy|refused to/i.test(message));
  expect(csp).toEqual([]);
});
