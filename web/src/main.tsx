import { QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { queryClient } from "./api/client";
import { installTrustedTypesPolicy } from "./lib/trusted-types";
import { routeTree } from "./routeTree.gen";
import "./styles/app.css";

// Deferred to idle: nothing calls an HTML sink during boot, and this keeps
// DOMPurify out of every route's first-paint bundle (see trusted-types.ts).
const scheduleIdle = window.requestIdleCallback ?? ((cb: () => void) => window.setTimeout(cb, 200));
scheduleIdle(() => void installTrustedTypesPolicy());

// A stale tab that still has an old, now-deleted chunk hash cached (after a
// deploy) gets a dynamic `import()` rejection instead of a broken UI; the
// only correct recovery is a full reload to pick up the new index.html
// (review M1).
window.addEventListener("vite:preloadError", () => {
  window.location.reload();
});

const router = createRouter({ routeTree, context: { queryClient } });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("root element not found");
}

createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
