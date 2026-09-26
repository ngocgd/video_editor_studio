import { Link } from "@tanstack/react-router";

import { InspectorSection } from "../../components/shared/inspector-panel";
import type { YouTubeChannel } from "../../api/gen/types.gen";
import { useYouTubeChannels } from "../settings-youtube/use-youtube-channels";
import { formatCount, formatDay, metricCell } from "./analytics-format";
import { analyticsChannels, windowTotals } from "./analytics-selectors";
import { useAnalyticsOverview, useAnalyticsSuggestions } from "./use-analytics";
import { YppProgressBars } from "./ypp-progress";

/** The dashboard shows at most this many channels; the Analytics page has the rest. */
const MAX_DASHBOARD_CHANNELS = 3;

function ChannelWidget({ channel }: { channel: YouTubeChannel }) {
  const overview = useAnalyticsOverview(channel.id);
  const suggestions = useAnalyticsSuggestions(channel.id);
  const data = overview.data;
  const views = metricCell(data ? windowTotals(data.days).views : undefined, "count", "views");
  const suggestionCount = suggestions.data?.items.length ?? 0;

  return (
    <div className="flex flex-col gap-3 rounded-md border border-border bg-card p-3">
      <div className="flex items-baseline justify-between gap-2">
        <Link to="/analytics" search={{ channel: channel.id }} className="truncate text-sm font-medium text-foreground hover:underline">
          {channel.title}
        </Link>
        <span className="shrink-0 text-2xs text-text-2">
          {data?.sync.analyticsThrough ? `Data through ${formatDay(data.sync.analyticsThrough)}` : "Not synced yet"}
        </span>
      </div>
      {overview.isError ? (
        <p className="text-sm text-text-2">Channel figures could not be loaded.</p>
      ) : !data ? (
        <div className="h-20 animate-pulse rounded-md bg-muted motion-reduce:animate-none" aria-hidden="true" />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-2 text-sm">
            <div className="flex flex-col" title={views.reason}>
              <span className="text-2xs uppercase tracking-[0.04em] text-text-2">Views, last 28 days</span>
              <span className={views.missing ? "text-xs text-text-2" : "font-mono tabular-nums"}>{views.text}</span>
            </div>
            <div className="flex flex-col">
              <span className="text-2xs uppercase tracking-[0.04em] text-text-2">Suggestions</span>
              <span className="font-mono tabular-nums">{formatCount(suggestionCount)}</span>
            </div>
          </div>
          <YppProgressBars ypp={data.ypp} />
        </>
      )}
    </div>
  );
}

/** Dashboard widgets: per-channel stats and YouTube Partner Program progress. Hidden when no channel is connected. */
export function DashboardChannelWidgets() {
  const channelsQuery = useYouTubeChannels();
  const channels = analyticsChannels(channelsQuery.data?.items ?? []).slice(0, MAX_DASHBOARD_CHANNELS);
  if (channels.length === 0) return null;
  return (
    <InspectorSection title="YouTube channels">
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {channels.map((channel) => (
          <ChannelWidget key={channel.id} channel={channel} />
        ))}
      </div>
    </InspectorSection>
  );
}
