import { expect, type Page, test } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

/**
 * Storyboard and characters, against a live stack with a seeded owner
 * (same manual-run contract as the other specs). Seeds a 3-hour episode
 * (450 scenes) through the API, then checks the grid, keyboard moves and
 * timeline stay within the frame budget, edits a scene, and walks the
 * characters, voices and styles pages. Generation steps are queued but
 * not run: this stack has no GPU worker, so they honestly stay queued.
 */
const EMAIL = process.env.LT_E2E_EMAIL ?? "owner@loomtale.local";
const PASSWORD = process.env.LT_E2E_PASSWORD ?? "LoomtaleDemo!2026";
const SCREENSHOT_DIR = "../plans/260924-2244-loomtale-studio-mvp/reports/phase-07-screens";
const SCENES = 450;

test.setTimeout(240_000);

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
      // One CSRF fetch per page, not per call: the stack's per-IP request
      // budget is shared with every other spec of the run.
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

function paragraph(i: number): string {
  if (i % 3 === 1) return `"The furnace cracked again," Lin Mo said to the dawn. ${FILLER.repeat(3)}`;
  if (i % 3 === 2) return `Elder Qiu did not look up. "Then listen to the fire," Elder Qiu said. ${FILLER.repeat(3)}`;
  return `Scene ${i + 1}. ${FILLER.repeat(4)}`;
}

test("storyboard at 450 scenes, scene edit, characters, voices and styles", async ({ page }) => {
  mkdirSync(SCREENSHOT_DIR, { recursive: true });
  await login(page);

  const series = await api<{ id: string }>(page, "POST", "/series", { title: `Storyboard E2E ${Date.now()}`, targetLanguages: ["en"], targetEpisodeMinutes: 120, plannedEpisodeCount: 1 });
  const style = await api<{ id: string }>(page, "POST", "/settings/image-styles", { name: `Ink wash ${Date.now()}`, baseModel: "z-image-turbo", stylePrompt: "ink wash painting, xianxia" });
  await api(page, "PUT", `/series/${series.id}/storyboard-settings`, { imageStyleId: style.id, cadenceMinS: 20, cadenceMaxS: 40, segmentGapMs: 150 });
  for (const [en, orig] of [["Lin Mo", "林默"], ["Elder Qiu", "邱长老"]]) {
    const c = await api<{ id: string }>(page, "POST", `/series/${series.id}/characters`, {
      names: { orig, en, vi: "" },
      appearancePrompt: `${en}, xianxia cultivator`,
      profile: `${en} speaks little and distrusts authority he has not tested.`,
    });
    await api(page, "PUT", `/characters/${c.id}/voices/en`, { engine: "chatterbox", params: { exaggeration: "0.4" } });
  }
  await api(page, "PUT", `/series/${series.id}/narrator-voices/en`, { engine: "chatterbox" });
  const episode = await api<{ id: string }>(page, "POST", `/episodes?seriesId=${series.id}`);
  await api(page, "PUT", `/episodes/${episode.id}/drafts/en`);
  let version = 0;
  for (let start = 0; start < SCENES; start += 200) {
    const ops = Array.from({ length: Math.min(200, SCENES - start) }, (_, k) => ({ op: "upsert", paragraphId: `p${start + k}`, text: paragraph(start + k) }));
    version = (await api<{ version: number }>(page, "PATCH", `/episodes/${episode.id}/drafts/en`, { expectedVersion: version, ops })).version;
  }
  const split = await api<{ sceneCount: number }>(page, "POST", `/episodes/${episode.id}/scenes/split`, { lang: "en", mode: "paragraphs" });
  expect(split.sceneCount).toBeGreaterThanOrEqual(300);

  await page.goto(`/projects/${series.id}/storyboard/${episode.id}`);
  const grid = page.getByRole("grid", { name: "Scenes" });
  await expect(grid).toBeVisible();
  await expect(page.getByRole("button", { name: `All ${split.sceneCount}` })).toBeVisible();
  await expect(page.getByRole("button", { name: `Missing ${split.sceneCount}` })).toBeVisible();
  await expect(page.getByRole("button", { name: /Generate missing \(\d+\)/ })).toBeEnabled();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/01-storyboard-grid.png` });

  // Scene settings: the cadence the split used and the speaker gap.
  await page.getByText("Scene settings").click();
  await expect(page.getByLabel("Cadence minimum seconds")).toHaveValue("20");
  await expect(page.getByLabel("Cadence maximum seconds")).toHaveValue("40");
  await page.getByLabel("Segment gap milliseconds").fill("200");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByRole("button", { name: "Saved" })).toBeVisible();
  await page.getByText("Scene settings").click();

  // Grid: scroll through the whole grid, one step per frame.
  const scroll = await grid.evaluate(async (el) => {
    const deltas: number[] = [];
    let maxMounted = 0;
    let last = performance.now();
    for (let i = 0; i < 180; i += 1) {
      await new Promise((r) => requestAnimationFrame(r));
      const now = performance.now();
      deltas.push(now - last);
      last = now;
      el.scrollTop += 120;
      maxMounted = Math.max(maxMounted, el.querySelectorAll('[role="gridcell"]').length);
    }
    deltas.shift();
    deltas.sort((a, b) => a - b);
    return { avg: deltas.reduce((s, d) => s + d, 0) / deltas.length, p95: deltas[Math.floor(deltas.length * 0.95)], maxMounted, scrolled: el.scrollTop };
  });
  console.log(`grid scroll: avg ${scroll.avg.toFixed(2)}ms/frame (${(1000 / scroll.avg).toFixed(1)} fps), p95 ${scroll.p95.toFixed(2)}ms, max mounted tiles ${scroll.maxMounted}`);
  expect(scroll.maxMounted).toBeLessThanOrEqual(40);
  expect(scroll.avg).toBeLessThan(20);

  // Keyboard: an arrow move is handled and rendered in under a frame.
  await grid.evaluate((el) => (el.scrollTop = 0));
  await grid.focus();
  const moves = await grid.evaluate(async (el) => {
    const times: number[] = [];
    for (let i = 0; i < 30; i += 1) {
      const before = el.getAttribute("aria-activedescendant");
      const t0 = performance.now();
      el.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
      // React flushes a discrete event's update synchronously at the end
      // of the dispatch (or in the microtask right after), so microtask
      // polling measures the handler plus render and commit, not a timer.
      for (let k = 0; k < 1000 && el.getAttribute("aria-activedescendant") === before; k += 1) await Promise.resolve();
      times.push(performance.now() - t0);
      await new Promise((r) => requestAnimationFrame(r));
    }
    times.sort((a, b) => a - b);
    return { median: times[15], max: times[29] };
  });
  console.log(`keyboard move: median ${moves.median.toFixed(2)}ms, max ${moves.max.toFixed(2)}ms`);
  expect(moves.median).toBeLessThan(16);

  // Timeline: pan and zoom across a 3-hour episode.
  const canvas = page.getByTestId("timeline-canvas");
  await expect(canvas).toBeVisible();
  const timeline = await canvas.evaluate(async (el) => {
    const deltas: number[] = [];
    let last = performance.now();
    for (let i = 0; i < 120; i += 1) {
      await new Promise((r) => requestAnimationFrame(r));
      const now = performance.now();
      deltas.push(now - last);
      last = now;
      const zoom = i % 20 === 0;
      el.dispatchEvent(new WheelEvent("wheel", { deltaY: zoom ? (i % 40 === 0 ? -100 : 100) : 240, ctrlKey: zoom, clientX: 300, bubbles: true, cancelable: true }));
    }
    deltas.shift();
    deltas.sort((a, b) => a - b);
    return { avg: deltas.reduce((s, d) => s + d, 0) / deltas.length, p95: deltas[Math.floor(deltas.length * 0.95)] };
  });
  console.log(`timeline pan/zoom: avg ${timeline.avg.toFixed(2)}ms/frame (${(1000 / timeline.avg).toFixed(1)} fps), p95 ${timeline.p95.toFixed(2)}ms`);
  expect(timeline.avg).toBeLessThan(20);
  await page.screenshot({ path: `${SCREENSHOT_DIR}/02-storyboard-timeline.png` });

  // Scene edit from the inspector: E opens the narration, Save persists it.
  await grid.focus();
  await page.keyboard.press("Home");
  await page.keyboard.press("e");
  const narration = page.getByLabel("Narration", { exact: true });
  await expect(narration).toBeFocused();
  await narration.fill("Scene 1. The mist rolled over the peaks and did not lift.");
  await page.getByRole("button", { name: "Save narration" }).click();
  await expect(page.getByText("The mist rolled over the peaks and did not lift.").first()).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/03-scene-inspector-edit.png` });

  // Regenerate the image (I): one gpu step, honestly queued without a GPU worker.
  await page.getByRole("button", { name: /Regenerate image/ }).click();
  await expect(page.getByRole("button", { name: /^In queue [1-9]/ })).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: /^In queue/ }).click();
  await expect(grid.getByRole("gridcell")).toHaveCount(1);
  await page.screenshot({ path: `${SCREENSHOT_DIR}/04-storyboard-in-queue-filter.png` });

  // Characters: list, profile tokens, a reference upload.
  await page.goto(`/projects/${series.id}/characters`);
  await expect(page.getByRole("main").getByRole("heading", { name: "Characters" })).toBeVisible();
  await expect(page.getByRole("option", { name: /Lin Mo/ })).toBeVisible();
  await expect(page.getByText(/tok per request/)).toBeVisible();
  const png = join(tmpdir(), `ref-${Date.now()}.png`);
  writeFileSync(
    png,
    Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAgAAAAMCAIAAADQ/GvKAAAAEklEQVR4nGM4UWGDFTGMSqAjALmJjoFR1DfVAAAAAElFTkSuQmCC", "base64"),
  );
  await page.locator('input[type="file"][accept="image/png,image/jpeg,image/webp"]').setInputFiles(png);
  await expect(page.getByRole("checkbox", { name: /approved/ })).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: `${SCREENSHOT_DIR}/05-characters.png` });

  await page.goto("/settings/voices");
  await expect(page.getByRole("main").getByRole("heading", { name: "Voice presets" })).toBeVisible();
  await page.getByRole("button", { name: "New preset" }).click();
  await page.getByLabel("Name").fill(`Warm tenor ${Date.now()}`);
  await page.getByRole("button", { name: "Create preset" }).click();
  await expect(page.getByText("Built-in voice").first()).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/06-settings-voices.png` });

  await page.goto("/settings/styles");
  await expect(page.getByRole("main").getByRole("heading", { name: "Image styles" })).toBeVisible();
  await expect(page.getByText("z-image-turbo").first()).toBeVisible();
  await page.screenshot({ path: `${SCREENSHOT_DIR}/07-settings-styles.png` });
});
