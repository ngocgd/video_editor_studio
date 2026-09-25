import { createFileRoute } from "@tanstack/react-router";

import { RenderQueueView } from "../../features/jobs/render-queue-view";

export const Route = createFileRoute("/_app/jobs")({
  component: RenderQueueView,
});
