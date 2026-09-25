import { createFileRoute } from "@tanstack/react-router";

import { WriterView } from "../../../../../features/writer/writer-view";

export const Route = createFileRoute("/_app/projects/$seriesId/episodes/$episodeId")({
  component: RouteComponent,
});

function RouteComponent() {
  const { seriesId, episodeId } = Route.useParams();
  return <WriterView seriesId={seriesId} episodeId={episodeId} />;
}
