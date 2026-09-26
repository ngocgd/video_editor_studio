import { createFileRoute } from "@tanstack/react-router";

import { SeriesDetailView } from "../../../../features/series/series-detail-view";

export const Route = createFileRoute("/_app/projects/$seriesId/")({
  component: RouteComponent,
});

function RouteComponent() {
  const { seriesId } = Route.useParams();
  return <SeriesDetailView seriesId={seriesId} />;
}
