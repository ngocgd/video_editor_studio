import { createFileRoute } from "@tanstack/react-router";

import { CharactersView } from "../../../../../features/characters/characters-view";

export const Route = createFileRoute("/_app/projects/$seriesId/characters/")({
  component: RouteComponent,
});

function RouteComponent() {
  const { seriesId } = Route.useParams();
  return <CharactersView seriesId={seriesId} />;
}
