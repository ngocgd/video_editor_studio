import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { visualizer } from "rollup-plugin-visualizer";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [
    tanstackRouter({ target: "react", autoCodeSplitting: true, routesDirectory: "./src/routes" }),
    react(),
    tailwindcss(),
    // Written next to dist/, not inside it, so the Caddy image's
    // `COPY --from=build /src/dist /srv` never ships this report publicly
    // alongside the sourcemaps (review M1).
    visualizer({ filename: "bundle-stats.html", gzipSize: true, brotliSize: true }),
  ],
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    // Consumed by scripts/check-bundle-budget.mjs to measure what a route
    // actually loads (entry + every statically/modulepreloaded chunk), not
    // just the single largest file.
    manifest: true,
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/vitest-setup.ts"],
    // e2e/*.spec.ts are Playwright tests (a real browser, a real stack),
    // not vitest unit/component tests; excluded here so vitest does not
    // try to run them as its own test files.
    exclude: ["**/node_modules/**", "e2e/**"],
  },
});
