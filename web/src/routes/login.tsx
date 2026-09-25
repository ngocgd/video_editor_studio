import { createFileRoute, redirect } from "@tanstack/react-router";

import { getMeOptions } from "../api/gen/@tanstack/react-query.gen";
import { LoginForm } from "../features/auth/login-form";

export interface LoginSearch {
  redirect?: string;
}

/**
 * Hand-rolled instead of zod (review decision: "lazy-load zod-backed login
 * validation") so zod is not part of every route's eager bundle just
 * because `routeTree.gen.ts` statically imports this file for route
 * matching; `redirect` is also constrained to an internal path only
 * (review L1: never let a search param drive an external redirect target).
 */
function validateLoginSearch(search: Record<string, unknown>): LoginSearch {
  const redirectTo = search.redirect;
  if (typeof redirectTo === "string" && /^\/(?!\/|\\)/.test(redirectTo)) {
    return { redirect: redirectTo };
  }
  return {};
}

export const Route = createFileRoute("/login")({
  validateSearch: validateLoginSearch,
  beforeLoad: async ({ context }) => {
    // Already authenticated tabs skip the login screen entirely; primed
    // through the query cache so a redirect to "/" reuses this fetch
    // instead of the layout route fetching /auth/me again (review H6).
    const me = await context.queryClient.ensureQueryData(getMeOptions()).catch(() => null);
    if (me) {
      throw redirect({ to: "/" });
    }
  },
  component: LoginPage,
});

function LoginPage() {
  return (
    <main className="flex h-dvh flex-col items-center justify-center gap-8 bg-well px-4">
      <img src="/logo.svg" alt="Loomtale Studio" width={186} height={30} />
      <div className="flex w-full max-w-sm flex-col gap-6">
        <LoginForm />
      </div>
    </main>
  );
}
