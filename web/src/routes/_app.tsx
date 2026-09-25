import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";

import { getMeOptions } from "../api/gen/@tanstack/react-query.gen";
import { getCsrf } from "../api/gen/sdk.gen";
import { getCsrfToken, setCsrfToken } from "../api/client";
import { AppShell } from "../components/shared/app-shell";

export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ context, location }) => {
    // Every navigation into an authenticated route used to run two serial
    // round trips; both now go through the query cache (reused across
    // navigations, primed once by login's own beforeLoad) and in parallel
    // with each other (review H6).
    const [me] = await Promise.all([
      context.queryClient.ensureQueryData(getMeOptions()).catch(() => null),
      // A hard reload clears the in-memory CSRF token; rehydrate it here so
      // the first mutation on this tab does not fail with a missing header.
      // Only fetched when actually missing, not on every navigation.
      getCsrfToken()
        ? Promise.resolve()
        : getCsrf()
            .then((result) => {
              if (result.data) setCsrfToken(result.data.token);
            })
            .catch(() => {}),
    ]);
    if (!me) {
      throw redirect({ to: "/login", search: { redirect: location.pathname } });
    }
  },
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});
