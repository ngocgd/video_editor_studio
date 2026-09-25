import { createFileRoute } from "@tanstack/react-router";

import { BibleEditorView } from "../../../../features/bible/bible-editor-view";

export const Route = createFileRoute("/_app/projects/$seriesId/bible")({
  component: RouteComponent,
});

function RouteComponent() {
  const { seriesId } = Route.useParams();
  return <BibleEditorView seriesId={seriesId} />;
}
