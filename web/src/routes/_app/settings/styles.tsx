import { createFileRoute } from "@tanstack/react-router";

import { ImageStylesView } from "../../../features/presets/image-styles-view";

export const Route = createFileRoute("/_app/settings/styles")({
  component: ImageStylesView,
});
