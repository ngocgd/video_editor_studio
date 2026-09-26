import { QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { queryClient } from "./api/client";
import { installTrustedTypesPolicy } from "./lib/trusted-types";
import { routeTree } from "./routeTree.gen";
import "./styles/app.css";

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

// The default Trusted Types policy must exist before React renders
// anything: third-party components write HTML sinks during render (Radix
// ScrollArea injects a <style> through dangerouslySetInnerHTML), and
// under `require-trusted-types-for 'script'` that throws until the policy
// is registered. Installing it at idle time lost that race on a hard
// reload of the writer. DOMPurify stays in its own chunk; only its fetch
// is awaited. A failed install still renders the app.
void installTrustedTypesPolicy()
  .catch(() => undefined)
  .finally(() => {
    createRoot(rootEl).render(
      <StrictMode>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </StrictMode>,
    );
  });
