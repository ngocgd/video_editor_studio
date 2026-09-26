import { useNavigate } from "@tanstack/react-router";

import { EmptyState } from "../../components/shared/empty-state";
import { InlineError } from "../../components/shared/inline-error";
import { InspectorSection } from "../../components/shared/inspector-panel";
import type { YouTubeChannel } from "../../api/gen/types.gen";
import { useYouTubeChannels } from "../settings-youtube/use-youtube-channels";
import { analyticsChannels, pickChannel, type AnalyticsSearch } from "./analytics-selectors";
import { ChannelOverview } from "./channel-overview";
import { ExplainPanel } from "./explain-panel";
import { SuggestionsPanel } from "./suggestions-panel";
import { TrackVideoForm } from "./track-video-form";
import { useCanEditAnalytics } from "./use-analytics";
import { VideoTable } from "./video-table";

export function ChannelPicker({ channels, selected, onSelect }: { channels: YouTubeChannel[]; selected: string; onSelect: (id: string) => void }) {
  return (
    <div className="flex items-center gap-2 text-sm">
      <label htmlFor="analytics-channel" className="text-text-2">
        Channel
      </label>
      <select
        id="analytics-channel"
        value={selected}
        onChange={(event) => onSelect(event.target.value)}
        className="h-8 rounded-md border border-border bg-background px-2 text-sm"
      >
        {channels.map((c) => (
          <option key={c.id} value={c.id}>
            {c.title}
            {c.status === "reconnect_needed" ? " (reconnect needed)" : ""}
          </option>
        ))}
      </select>
    </div>
  );
}

/** Analytics page: channel overview, tracked video table, suggestions and the Explain action. */
export function AnalyticsView({ search }: { search: AnalyticsSearch }) {
  const channelsQuery = useYouTubeChannels();
  const navigate = useNavigate();
  const canEdit = useCanEditAnalytics();

  if (channelsQuery.isError) {
    return <InlineError cause="Could not load the YouTube channels." onRetry={() => void channelsQuery.refetch()} />;
  }
  if (!channelsQuery.data) {
    return <div className="h-64 animate-pulse rounded-md bg-muted motion-reduce:animate-none" aria-hidden="true" />;
  }

  const channels = analyticsChannels(channelsQuery.data.items);
  const channel = pickChannel(channels, search.channel);
  if (!channel) {
    return (
      <EmptyState
        message="Connect a YouTube channel to see its views, watch time, CTR and retention here."
        actionLabel="Open YouTube channels"
        onAction={() => void navigate({ to: "/settings/youtube" })}
      />
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <ChannelPicker
        channels={channels}
        selected={channel.id}
        onSelect={(id) => void navigate({ to: "/analytics", search: { channel: id } })}
      />
      {channel.status === "reconnect_needed" && (
        <p role="status" className="rounded-md border border-warning/40 bg-warning-muted px-3 py-2 text-sm text-warning">
          Google revoked access to this channel. Reconnect it in Settings to resume the daily sync; the figures below stop at the last sync.
        </p>
      )}
      <ChannelOverview key={channel.id} channelId={channel.id} />
      <InspectorSection title="Videos">
        <div className="flex flex-col gap-4">
          {canEdit && <TrackVideoForm channelId={channel.id} />}
          <VideoTable key={channel.id} channelId={channel.id} />
        </div>
      </InspectorSection>
      <InspectorSection title="Suggestions">
        <SuggestionsPanel channelId={channel.id} />
      </InspectorSection>
      <InspectorSection title="Explain">
        <ExplainPanel key={channel.id} channelId={channel.id} />
      </InspectorSection>
    </div>
  );
}
