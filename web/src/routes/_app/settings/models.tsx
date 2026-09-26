import { createFileRoute } from "@tanstack/react-router";

import { ModelManagerView } from "../../../features/models/model-manager-view";

export const Route = createFileRoute("/_app/settings/models")({
  component: ModelManagerView,
});
