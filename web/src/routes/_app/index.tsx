import { createFileRoute } from "@tanstack/react-router";

import { DashboardView } from "../../features/dashboard/dashboard-view";

export const Route = createFileRoute("/_app/")({
  component: DashboardView,
});
