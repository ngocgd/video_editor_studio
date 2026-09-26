import { expect, type Page, test } from "@playwright/test";
import { mkdirSync } from "node:fs";

/**
 * Render page and Library, against a live stack with a seeded owner (same
 * manual-run contract as the other specs). The stack has no GPU worker,
 * so a freshly split episode has no images or voices: the render button
 * stays disabled and lists why, which is exactly what is checked here.
 * The rendering itself is covered by the api integration tests.
 */
const EMAIL = process.env.LT_E2E_EMAIL ?? "owner@loomtale.local";
const PASSWORD = process.env.LT_E2E_PASSWORD ?? "LoomtaleDemo!2026";
const SCREENSHOT_DIR = "../plans/260924-2244-loomtale-studio-mvp/reports/phase-08-screens";

test.setTimeout(120_000);

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/");
}

/** Calls the API from the page (same origin, session cookie, CSRF token). */
async function api<T>(page: Page, method: string, path: string, body?: unknown): Promise<T> {
  return page.evaluate(
    async ({ method, path, body }) => {
      const w = window as unknown as { __ltCsrf?: string };
      w.__ltCsrf ??= ((await (await fetch("/api/v1/auth/csrf")).json()) as { token: string }).token;
      const res = await fetch(`/api/v1${path}`, {
        method,
        headers: { "X-CSRF-Token": w.__ltCsrf, ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
      return res.status === 204 ? (undefined as T) : ((await res.json()) as T);
    },
    { method, path, body },
  );
}

const FILLER = "The mist rolled over the nine peaks while the bells of the sect rang. ";

test("render page explains why it cannot start, library previews a cleanup", async ({ page }) => {
  mkdirSync(SCREENSHOT_DIR, { recursive: true });
  await login(page);

  const series = await api<{ id: string }>(page, "POST", "/series", { title: `Render E2E ${Date.now()}`, targetLanguages: ["en"], targetEpisodeMinutes: 60, plannedEpisodeCount: 1 });
  const style = await api<{ id: string }>(page, "POST", "/settings/image-styles", { name: `Ink wash ${Date.now()}`, baseModel: "z-image-turbo", stylePrompt: "ink wash painting" });
  await api(page, "PUT", `/series/${series.id}/storyboard-settings`, { imageStyleId: style.id, cadenceMinS: 20, cadenceMaxS: 40, segmentGapMs: 150 });
  await api(page, "PUT", `/series/${series.id}/narrator-voices/en`, { engine: "chatterbox" });
  const episode = await api<{ id: string }>(page, "POST", `/episodes?seriesId=${series.id}`);
  await api(page, "PUT", `/episodes/${episode.id}/drafts/en`);
  const ops = [0, 1, 2].map((i) => ({ op: "upsert", paragraphId: `p${i}`, text: `Scene ${i + 1}. ${FILLER.repeat(6)}` }));
  await api(page, "PATCH", `/episodes/${episode.id}/drafts/en`, { expectedVersion: 0, ops });
  const split = await api<{ sceneCount: number }>(page, "POST", `/episodes/${episode.id}/scenes/split`, { lang: "en", mode: "paragraphs" });
  expect(split.sceneCount).toBeGreaterThanOrEqual(1);

  // Render page: stages, the disabled button with its reasons, settings.
  await page.goto(`/projects/${series.id}/render/${episode.id}`);
  const start = page.getByRole("button", { name: "Render episode" });
  await expect(start).toBeDisabled();
  await expect(page.getByRole("heading", { name: "Why the render cannot start yet" })).toBeVisible();
  await expect(page.getByText(/Scene \d+ has no selected image\./).first()).toBeVisible();
  await expect(page.getByText(/Scene \d+ has no ready voice take\./).first()).toBeVisible();
  await expect(page.getByText("No background music: narration only, by project decision.")).toBeVisible();
  await expect(page.getByRole("option", { name: "Parallax (depth model not installed)" })).toBeDisabled();
  await expect(page.getByRole("region", { name: "Estimate" })).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/01-render-blocked.png` });

  // The storyboard links to the render page and back.
  await page.getByRole("navigation", { name: "Episode" }).getByRole("link", { name: "Storyboard" }).click();
  await expect(page.getByRole("grid", { name: "Scenes" })).toBeVisible();

  // Library: usage, retention and the cleanup dry run in a dialog.
  await page.goto("/library");
  await expect(page.getByRole("region", { name: "Storage usage" })).toBeVisible();
  await expect(page.getByRole("form", { name: "Retention" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Assets" })).toBeVisible();
  await page.getByRole("button", { name: "Preview cleanup" }).click();
  const dialog = page.getByRole("dialog", { name: "Clean up the library?" });
  await expect(dialog).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/02-library-cleanup-preview.png` });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
});
