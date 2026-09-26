import { createFileRoute } from "@tanstack/react-router";

import { validateAnalyticsSearch } from "../../../features/analytics/analytics-selectors";
import { AnalyticsView } from "../../../features/analytics/analytics-view";

function AnalyticsRoute() {
  return <AnalyticsView search={Route.useSearch()} />;
}

export const Route = createFileRoute("/_app/analytics/")({
  validateSearch: validateAnalyticsSearch,
  component: AnalyticsRoute,
});
