import { createFileRoute } from "@tanstack/react-router";

import { StoryboardView } from "../../../../../features/storyboard/storyboard-view";

export const Route = createFileRoute("/_app/projects/$seriesId/storyboard/$episodeId")({
  component: RouteComponent,
});

function RouteComponent() {
  const { seriesId, episodeId } = Route.useParams();
  return <StoryboardView seriesId={seriesId} episodeId={episodeId} />;
}
