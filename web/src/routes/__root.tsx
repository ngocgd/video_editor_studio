import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";
import { lazy, Suspense } from "react";

import type { RouterContext } from "../router-context";

// sonner's Toaster is only relevant once a background completion actually
// fires (guidelines: "Toasts only for background completions"); loading it
// lazily keeps it out of every route's first-paint bundle.
const Toaster = lazy(() => import("../components/ui/toaster").then((m) => ({ default: m.Toaster })));

export const Route = createRootRouteWithContext<RouterContext>()({
  component: () => (
    <>
      <Outlet />
      <Suspense fallback={null}>
        <Toaster />
      </Suspense>
    </>
  ),
});
