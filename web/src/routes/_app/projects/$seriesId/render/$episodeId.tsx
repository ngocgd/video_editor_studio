import { createFileRoute } from "@tanstack/react-router";

import { RenderView } from "../../../../../features/render/render-view";

export const Route = createFileRoute("/_app/projects/$seriesId/render/$episodeId")({
  component: RouteComponent,
});

function RouteComponent() {
  const { seriesId, episodeId } = Route.useParams();
  return <RenderView seriesId={seriesId} episodeId={episodeId} />;
}
