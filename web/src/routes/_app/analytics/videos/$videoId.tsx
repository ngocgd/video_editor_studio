import { createFileRoute } from "@tanstack/react-router";

import { validateAnalyticsSearch } from "../../../../features/analytics/analytics-selectors";
import { VideoDetailView } from "../../../../features/analytics/video-detail-view";

function AnalyticsVideoRoute() {
  const { videoId } = Route.useParams();
  const { channel } = Route.useSearch();
  return <VideoDetailView key={videoId} videoId={videoId} channelId={channel} />;
}

export const Route = createFileRoute("/_app/analytics/videos/$videoId")({
  validateSearch: validateAnalyticsSearch,
  component: AnalyticsVideoRoute,
});
