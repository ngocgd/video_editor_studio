#!/usr/bin/env node
// Enforces the phase 5 performance budgets against a production build:
// the largest JS chunk (the shell/vendor entry) <=200KB gzip, every other
// route chunk <=80KB gzip, and all CSS combined <=30KB gzip. size-limit's
// glob-sum semantics cannot express "each file individually", so this
// script checks per-file sizes directly with Node's built-in zlib.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { gzipSync } from "node:zlib";

const DIST_ASSETS = join(import.meta.dirname, "..", "dist", "assets");
const SHELL_LIMIT_BYTES = 200 * 1024;
const ROUTE_LIMIT_BYTES = 80 * 1024;
const CSS_LIMIT_BYTES = 30 * 1024;

function gzipSize(path) {
  return gzipSync(readFileSync(path)).length;
}

function formatKb(bytes) {
  return `${(bytes / 1024).toFixed(2)} KB`;
}

const entries = readdirSync(DIST_ASSETS).filter((name) => statSync(join(DIST_ASSETS, name)).isFile());
const jsFiles = entries.filter((name) => name.endsWith(".js"));
const cssFiles = entries.filter((name) => name.endsWith(".css"));

const jsSizes = jsFiles
  .map((name) => ({ name, bytes: gzipSize(join(DIST_ASSETS, name)) }))
  .sort((a, b) => b.bytes - a.bytes);

let failed = false;

console.log("JS chunks (gzip):");
for (const [index, { name, bytes }] of jsSizes.entries()) {
  const isShell = index === 0;
  const limit = isShell ? SHELL_LIMIT_BYTES : ROUTE_LIMIT_BYTES;
  const label = isShell ? "shell" : "route";
  const over = bytes > limit;
  if (over) failed = true;
  console.log(`  [${label}] ${name}: ${formatKb(bytes)} (limit ${formatKb(limit)})${over ? " OVER BUDGET" : ""}`);
}

const cssTotal = cssFiles.reduce((sum, name) => sum + gzipSize(join(DIST_ASSETS, name)), 0);
const cssOver = cssTotal > CSS_LIMIT_BYTES;
if (cssOver) failed = true;
console.log(`CSS total (gzip): ${formatKb(cssTotal)} (limit ${formatKb(CSS_LIMIT_BYTES)})${cssOver ? " OVER BUDGET" : ""}`);

if (failed) {
  console.error("\nBundle budget check FAILED.");
  process.exit(1);
}
console.log("\nBundle budget check passed.");
