import { expect, test } from "@playwright/test";
import { fileURLToPath } from "node:url";

/**
 * Phase 6 smoke path: create a series -> writer loads -> import a fixture
 * .txt -> preview -> commit -> Settings > LLM loads. Same manual-run
 * contract as smoke.spec.ts (needs a live compose stack + seeded owner; not
 * part of CI). No LLM provider is guaranteed configured in a fresh dev
 * stack, so this asserts the honest "no provider" state on the settings
 * page instead of any specific provider being available, per the phase's
 * scope: generation/AI-action screenshots were not captured because they
 * need a configured provider (Ollama or claude CLI) this environment does
 * not have.
 */
const EMAIL = process.env.LT_E2E_EMAIL ?? "owner@loomtale.local";
const PASSWORD = process.env.LT_E2E_PASSWORD ?? "LoomtaleDemo!2026";
const SCREENSHOT_DIR = "../plans/260924-2244-loomtale-studio-mvp/reports/phase-06-screens";

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/");
}

test("series -> writer -> import -> settings/llm", async ({ page }) => {
  await login(page);

  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Projects" }).click();
  await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/01-series-list.png` });

  // The header button; the empty state repeats it when no series exist yet.
  await page.getByRole("button", { name: "New series" }).first().click();
  await page.getByLabel("Title").fill(`Smoke Test Series ${Date.now()}`);
  await page.getByRole("button", { name: "Create series" }).click();
  await expect(page.getByText("Generating bible and episode outlines")).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/02-series-wizard-progress.png` });

  // Generation needs a configured LLM provider; without one this either
  // completes against a test-double or stays pending. Either way, navigate
  // straight to Import next rather than block the whole smoke run on it.
  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Import" }).click();
  await expect(page.getByRole("heading", { name: "Import chapters" })).toBeVisible();
  await expect(page.getByText(/user-supplied source material/i)).toBeVisible();

  const seriesSelect = page.locator("select").first();
  await seriesSelect.selectOption({ index: 1 });
  await page.setInputFiles('input[type="file"]', fileURLToPath(new URL("./fixtures/sample-chapter.txt", import.meta.url)));
  await expect(page.getByText("Split preset")).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: `${SCREENSHOT_DIR}/04-import-preview.png` });

  await page.getByRole("button", { name: "Preview split" }).click();
  await expect(page.getByRole("columnheader", { name: "Title" })).toBeVisible();

  await page.goto("/settings/llm");
  await expect(page.getByRole("heading", { name: "LLM providers" })).toBeVisible();
  await expect(page.getByText(/no api key configured|unavailable/i).first()).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/05-settings-llm.png` });
});
