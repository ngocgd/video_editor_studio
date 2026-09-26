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
const SCREENSHOT_DIR = "../plans/260924-2244-loomtale-studio-mvp/reports/phase-06-screens";

test("series -> writer -> import -> settings/llm", async ({ page }) => {
  await page.goto("/");

  await page.getByRole("navigation", { name: "Primary" }).getByRole("link", { name: "Projects" }).click();
  await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/01-series-list.png` });

  // The header button; the empty state repeats it when no series exist yet.
  await page.getByRole("button", { name: "New series" }).first().click();
  const seriesTitle = `Smoke Test Series ${Date.now()}`;
  await page.getByLabel("Title").fill(seriesTitle);
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
  await seriesSelect.selectOption({ label: seriesTitle });
  const seriesId = await seriesSelect.inputValue();
  await page.setInputFiles('input[type="file"]', fileURLToPath(new URL("./fixtures/sample-chapter.txt", import.meta.url)));
  await expect(page.getByText("Split preset")).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: `${SCREENSHOT_DIR}/04-import-preview.png` });

  await page.getByRole("button", { name: "Preview split" }).click();
  await expect(page.getByRole("columnheader", { name: "Title" })).toBeVisible();
  await page.getByRole("button", { name: "Commit selected chapters" }).click();
  await expect(page.getByText(/Committed \d+ episode/)).toBeVisible({ timeout: 15_000 });

  // Opening an imported episode must show its text and must not autosave
  // over it: reload after the autosave debounce and the text is still there.
  const episodeId = await page.evaluate(async (id) => {
    const res = await fetch(`/api/v1/episodes?seriesId=${id}`);
    const body = (await res.json()) as { items: { id: string; title: string }[] };
    return body.items.find((e) => e.title === "Chapter 1 The Beginning")?.id;
  }, seriesId);
  expect(episodeId).toBeTruthy();
  await page.goto(`/projects/${seriesId}/episodes/${episodeId}`);
  const importedLine = page.getByText("Lin Mo climbed the forbidden peak at dusk", { exact: false });
  await expect(importedLine).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/03-writer-imported-episode.png` });
  await page.waitForTimeout(2_000);
  await page.reload();
  await expect(importedLine).toBeVisible();

  // Splitting a paragraph with Enter and typing in the new one is saved.
  await importedLine.click();
  await page.keyboard.press("End");
  await page.keyboard.press("Enter");
  await page.keyboard.type("A new paragraph after the split.");
  await expect(page.getByText("Unsaved changes")).toBeVisible();
  await expect(page.getByText("Saved", { exact: true })).toBeVisible({ timeout: 10_000 });
  await page.reload();
  await expect(page.getByText("A new paragraph after the split.")).toBeVisible();
  await expect(importedLine).toBeVisible();

  // A manually created episode has no draft until the writer creates one.
  const manualEpisodeId = await page.evaluate(async (id) => {
    const csrf = (await (await fetch("/api/v1/auth/csrf")).json()) as { token: string };
    const res = await fetch(`/api/v1/episodes?seriesId=${id}`, { method: "POST", headers: { "X-CSRF-Token": csrf.token } });
    return ((await res.json()) as { id: string }).id;
  }, seriesId);
  await page.goto(`/projects/${seriesId}/episodes/${manualEpisodeId}`);
  await page.getByRole("button", { name: "Create EN draft" }).click();
  await page.locator(".ProseMirror").click();
  await page.keyboard.type("The first line of a fresh draft.");
  await expect(page.getByText("Unsaved changes")).toBeVisible();
  await expect(page.getByText("Saved", { exact: true })).toBeVisible({ timeout: 10_000 });
  await page.reload();
  await expect(page.getByText("The first line of a fresh draft.")).toBeVisible();

  await page.goto("/settings/llm");
  await expect(page.getByRole("heading", { name: "LLM providers" })).toBeVisible();
  await expect(page.getByText(/no api key configured|unavailable/i).first()).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/05-settings-llm.png` });
});
