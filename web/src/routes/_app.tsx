import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";

import { getCsrf, getMe } from "../api/gen/sdk.gen";
import { setCsrfToken } from "../api/client";
import { AppShell } from "../components/shared/app-shell";

export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ location }) => {
    const me = await getMe();
    if (!me.response?.ok) {
      throw redirect({ to: "/login", search: { redirect: location.pathname } });
    }
    // A hard reload clears the in-memory CSRF token; rehydrate it here so
    // the first mutation on this tab does not fail with a missing header.
    const csrf = await getCsrf();
    if (csrf.data) setCsrfToken(csrf.data.token);
  },
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});
