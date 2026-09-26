import { createFileRoute } from "@tanstack/react-router";

import { SeriesListView } from "../../../features/series/series-list-view";

export const Route = createFileRoute("/_app/projects/")({
  component: SeriesListView,
});
