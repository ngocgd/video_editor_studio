import { createFileRoute } from "@tanstack/react-router";

import { validateYouTubeSettingsSearch } from "../../../features/settings-youtube/connect-result";
import { YouTubeSettingsView } from "../../../features/settings-youtube/youtube-settings-view";

function YouTubeSettingsRoute() {
  return <YouTubeSettingsView search={Route.useSearch()} />;
}

export const Route = createFileRoute("/_app/settings/youtube")({
  validateSearch: validateYouTubeSettingsSearch,
  component: YouTubeSettingsRoute,
});
