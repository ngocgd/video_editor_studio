#!/usr/bin/env node
// Enforces the phase 5 performance budgets against a production build by
// reading Vite's manifest (build.manifest: true in vite.config.ts) instead
// of just looking at the single largest chunk file: it walks the entry's
// and each route's *static* import closure (the files a browser actually
// requests/modulepreloads before that page can paint), lazy-loaded chunks
// (React.lazy, e.g. the command palette) are correctly excluded since they
// only show up as `dynamicImports`, not `imports`.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { gzipSync } from "node:zlib";

const DIST = join(import.meta.dirname, "..", "dist");
const HARD_CAP_BYTES = 200 * 1024;
const AUTHENTICATED_TARGET_BYTES = 160 * 1024;
const ROUTE_CHUNK_LIMIT_BYTES = 80 * 1024;
const CSS_LIMIT_BYTES = 30 * 1024;
// The writer route (phase 6) bundles TipTap/ProseMirror and has its own,
// larger documented budget (phase-06-story-writer-import.md: "<=180KB gzip
// and lazy-loaded"); every other route stays under the generic per-route cap.
// The phase doc only budgets the writer's *own* chunk, not its first-paint
// total once the shared authenticated-shell floor (~164KB, see the
// "Authenticated shell" check below) is added on top; this file's
// WRITER_FIRST_PAINT_HARD_CAP_BYTES is this script's own extension to cover
// that combined total, flagged in the phase report for the lead to confirm.
const WRITER_ROUTE_CHUNK_LIMIT_BYTES = 180 * 1024;
const WRITER_FIRST_PAINT_HARD_CAP_BYTES = 320 * 1024;
const isWriterRoute = (routeKey) => routeKey.includes("episodes/$episodeId");

const manifest = JSON.parse(readFileSync(join(DIST, ".vite", "manifest.json"), "utf8"));

function gzipSize(file) {
  return gzipSync(readFileSync(join(DIST, file))).length;
}

function formatKb(bytes) {
  return `${(bytes / 1024).toFixed(2)} KB`;
}

/** Every manifest key reachable through *static* `imports` edges only (never `dynamicImports`), starting from `key`. */
function staticClosure(key, seen = new Set()) {
  if (seen.has(key)) return seen;
  seen.add(key);
  const entry = manifest[key];
  if (!entry) return seen;
  for (const importKey of entry.imports ?? []) {
    staticClosure(importKey, seen);
  }
  return seen;
}

function closureBytes(key) {
  const files = new Set();
  for (const manifestKey of staticClosure(key)) {
    const file = manifest[manifestKey]?.file;
    if (file) files.add(file);
  }
  let total = 0;
  for (const file of files) total += gzipSize(file);
  return total;
}

const entryKey = "index.html";
if (!manifest[entryKey]) {
  console.error("No index.html entry in the Vite manifest; run `npm run build` first.");
  process.exit(1);
}

const routeKeys = Object.keys(manifest).filter(
  (key) => key.startsWith("src/routes/") && key.includes("?tsr-split=component"),
);

let failed = false;

function checkTotal(label, bytes, limit, warnLimit) {
  const over = bytes > limit;
  if (over) failed = true;
  const warn = !over && warnLimit != null && bytes > warnLimit;
  console.log(
    `${label}: ${formatKb(bytes)} (limit ${formatKb(limit)})${over ? " OVER BUDGET" : ""}${warn ? ` (above the ${formatKb(warnLimit)} target)` : ""}`,
  );
}

console.log("First-paint totals (entry + every statically-imported/modulepreloaded chunk):");
for (const routeKey of routeKeys) {
  const bytes = closureBytes(routeKey);
  const isAuthenticated = routeKey.startsWith("src/routes/_app");
  const hardCap = isWriterRoute(routeKey) ? WRITER_FIRST_PAINT_HARD_CAP_BYTES : HARD_CAP_BYTES;
  checkTotal(`  ${routeKey}`, bytes, hardCap, isAuthenticated ? AUTHENTICATED_TARGET_BYTES : null);
}

// The authenticated layout shell itself (the pathless `_app` route every
// authenticated page loads), independent of which leaf route is active.
const appShellKey = Object.keys(manifest).find((key) => manifest[key].name === "_app");
if (appShellKey) {
  checkTotal("Authenticated shell (_app layout only)", closureBytes(appShellKey), HARD_CAP_BYTES, AUTHENTICATED_TARGET_BYTES);
}

console.log("\nRoute chunk sizes (gzip, excluding what the shell already loaded):");
for (const routeKey of routeKeys) {
  const file = manifest[routeKey].file;
  const bytes = gzipSize(file);
  const limit = isWriterRoute(routeKey) ? WRITER_ROUTE_CHUNK_LIMIT_BYTES : ROUTE_CHUNK_LIMIT_BYTES;
  const over = bytes > limit;
  if (over) failed = true;
  console.log(`  ${file}: ${formatKb(bytes)} (limit ${formatKb(limit)})${over ? " OVER BUDGET" : ""}`);
}

const cssFiles = new Set();
for (const key of Object.keys(manifest)) {
  for (const css of manifest[key].css ?? []) cssFiles.add(css);
}
let cssTotal = 0;
for (const file of cssFiles) cssTotal += gzipSize(file);
checkTotal("\nCSS total", cssTotal, CSS_LIMIT_BYTES);

if (failed) {
  console.error("\nBundle budget check FAILED.");
  process.exit(1);
}
console.log("\nBundle budget check passed.");
